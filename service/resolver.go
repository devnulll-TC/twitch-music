package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Track — то, что заказали, после опознания источником.
type Track struct {
	ID       string `json:"id"`       // идентификатор внутри источника
	Title    string `json:"title"`    //
	Uploader string `json:"uploader"` //
	Duration int    `json:"duration"` // секунды
	WebURL   string `json:"web_url"`  // ссылка для чата и оверлея
	Source   string `json:"source"`   // youtube / soundcloud
	Thumb    string `json:"thumb"`    // обложка для дока в OBS
	File     string `json:"file"`     // путь к скачанному файлу (заполняется в Download)
	Gain     string `json:"gain"`     // поправка громкости для liquidsoap, вида "-3.2 dB"
}

// Resolver дёргает yt-dlp. Вынесен отдельно, потому что именно здесь живут все
// грабли YouTube, и менять их придётся чаще всего остального кода.
type Resolver struct {
	Bin          string // путь к yt-dlp
	CookiesFile  string // куки одноразового Google-аккаунта, без них YouTube не отдаёт ничего
	CacheDir     string
	MaxDuration  int    // секунд; всё длиннее отклоняем ДО скачивания
	Normalize    bool   // мерить ли громкость; см. NORMALIZE_LOUDNESS в main
	PlayerClient string // клиенты YouTube через запятую; пусто = web_embedded,mweb — см. baseArgs
	Spotify      *Spotify
}

// playerClient — вынесен в настройку, потому что YouTube отключает клиентам выдачу
// аудио-дорожек без предупреждения, и следующий раз чиниться придётся сменой значения,
// а не пересборкой. Проверять кандидатов так:
//
//	yt-dlp --extractor-args "youtube:player_client=X" -f bestaudio --simulate <url>
func (r *Resolver) playerClient() string {
	if r.PlayerClient != "" {
		return r.PlayerClient
	}
	return "web_embedded,mweb"
}

// Обязательные флаги для YouTube, добытые замерами (см. память проекта):
//   - без --cookies       → "Sign in to confirm you're not a bot", 0 из 6 треков
//   - без ejs-компонента  → "Signature solving failed" и форматов нет
//   - без player_client → 11.08.26 YouTube перестал отдавать аудио-дорожки:
//     в выдаче остаются только склеенные m3u8-рендиции, bestaudio не находит
//     кандидатов и падает с "Requested format is not available".
//
// Клиенты перечислены списком не для красоты — они ломаются ПО-РАЗНОМУ, и поодиночке
// каждый оставляет часть каталога недоступной:
//   - mweb        отдаёт форматы, но на части роликов YouTube привязывает GVS PO-токен
//     к id видео ("Detected experiment to bind GVS PO Token to video ID"),
//     и скачивание падает уже на байтах — 403 Forbidden при живом токене;
//   - web_embedded качает такие ролики, поэтому идёт первым;
//   - web / ios / android / tv_simply / web_safari аудио-дорожек не отдают вовсе,
//     tv отвечает "The page needs to be reloaded" (проверено 11.08.26).
//
// Симптомы у двух поломок разные («формат недоступен» против «403»), но лечатся
// они одним — сменой клиента, поэтому и то и другое чинится через YT_PLAYER_CLIENT.
//
// PO-token провайдер (контейнер tm-pot) подхватывается плагином сам, флага не требует.
func (r *Resolver) baseArgs() []string {
	args := []string{"--no-warnings", "--no-progress", "--remote-components", "ejs:github",
		"--extractor-args", "youtube:player_client=" + r.playerClient(),
		// Заказ — это один трек. Кнопка «Слушать радио» на YouTube даёт ссылку вида
		// watch?v=ID&list=RDID, и без этого флага yt-dlp идёт разбирать бесконечный
		// автоплейлист экстрактором youtube:tab, спотыкается о проверку авторизации
		// и роняет заказ целиком. С флагом берётся ровно то видео, что в ссылке.
		// На поиск (ytsearchN:) флаг не влияет — проверено, пять результатов остаются пятью.
		"--no-playlist"}
	if r.CookiesFile != "" {
		args = append(args, "--cookies", r.CookiesFile)
	}
	return args
}

var errNotFound = errors.New("ничего не нашлось")

// searchTarget решает, что делать с заказом: ссылку отдать yt-dlp как есть, а свободный
// текст — искать. По умолчанию ищем на YouTube, там каталог шире всего; SoundCloud
// вызывается явно префиксом, потому что нужен он в основном под ремиксы и андеграунд,
// которых на YouTube нет.
func searchTarget(query string) string {
	if strings.HasPrefix(query, "http://") || strings.HasPrefix(query, "https://") {
		return query
	}
	for _, p := range []string{"sc:", "sc ", "soundcloud:", "soundcloud "} {
		if rest, ok := cutPrefixFold(query, p); ok {
			return "scsearch1:" + strings.TrimSpace(rest)
		}
	}
	return "ytsearch1:" + query
}

func cutPrefixFold(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix) {
		return s[len(prefix):], true
	}
	return "", false
}

// Resolve опознаёт заказ: ссылку берёт как есть, свободный текст ищет на YouTube.
// Скачивания здесь НЕТ — сначала надо узнать длительность и отсеять часовые стримы.
func (r *Resolver) Resolve(ctx context.Context, query string) (*Track, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errNotFound
	}

	// Spotify: играть его звук нельзя, но каталог у него точный — берём оттуда
	// исполнителя, название и длительность, а запись ищем на YouTube.
	if ref, ok := parseSpotify(query); ok {
		return r.resolveViaSpotify(ctx, ref)
	}

	target := searchTarget(query)

	const fmtLine = "%(id)s\x1f%(title)s\x1f%(duration)s\x1f%(uploader)s\x1f%(webpage_url)s\x1f%(extractor_key)s\x1f%(is_live)s\x1f%(thumbnail)s"
	args := append(r.baseArgs(), "--flat-playlist", "--print", fmtLine, target)

	out, err := r.run(ctx, 45*time.Second, args)
	if err != nil {
		return nil, err
	}
	line := strings.TrimSpace(firstLine(out))
	if line == "" {
		return nil, errNotFound
	}

	parts := strings.Split(line, "\x1f")
	if len(parts) < 8 {
		return nil, fmt.Errorf("не разобрал ответ yt-dlp: %q", line)
	}
	// Живой эфир качать нельзя: yt-dlp будет тянуть его, пока стрим не кончится,
	// а заказ так и не доедет до очереди.
	if parts[6] == "True" {
		return nil, &RejectError{Reason: "это прямой эфир, а не трек"}
	}
	dur := parseDuration(parts[2])
	t := &Track{
		ID:       parts[0],
		Title:    naOrValue(parts[1]),
		Duration: dur,
		Uploader: naOrValue(parts[3]),
		WebURL:   naOrValue(parts[4]),
		Source:   strings.ToLower(parts[5]),
		Thumb:    naOrValue(parts[7]),
	}
	if t.WebURL == "" {
		t.WebURL = target
	}

	// В плоском поиске yt-dlp не отдаёт обложку ни для YouTube, ни для SoundCloud
	// (всегда "NA") — она есть только при полном разборе, который мы намеренно не
	// делаем ради скорости. У YouTube адрес картинки выводится из id, это дешевле
	// любого запроса.
	if t.Thumb == "" && strings.Contains(t.Source, "youtube") && t.ID != "" {
		t.Thumb = "https://i.ytimg.com/vi/" + t.ID + "/mqdefault.jpg"
	}

	// SoundCloud в плоском поиске длительность не отдаёт вовсе. Без неё отсев по длине
	// не работает, и в эфир уезжает часовой микс — поэтому добираем метаданные отдельно.
	// Пустая обложка — второй повод для дозапроса: у YouTube её видно из id, а у
	// остальных источников взять больше неоткуда.
	if t.Duration == 0 || t.Thumb == "" {
		if err := r.fillDuration(ctx, t); err != nil {
			return nil, err
		}
	}
	return t, nil
}

// resolveViaSpotify превращает ссылку Spotify в тот же трек на YouTube.
func (r *Resolver) resolveViaSpotify(ctx context.Context, ref spotifyRef) (*Track, error) {
	if ref.kind != "track" {
		// Альбом или плейлист одной активацией награды — это десятки треков за одну
		// цену и забитая на час очередь. Просим ссылку на конкретный трек.
		return nil, &RejectError{Reason: "нужна ссылка на трек, а не на " +
			map[string]string{"album": "альбом", "playlist": "плейлист", "artist": "исполнителя"}[ref.kind]}
	}
	if r.Spotify == nil {
		return nil, &RejectError{Reason: "поиск по Spotify не настроен"}
	}

	title, artist, dur, err := r.Spotify.TrackInfo(ctx, ref.id)
	if err != nil {
		return nil, err
	}

	t, err := r.searchBest(ctx, artist+" - "+title, dur)
	if err != nil {
		return nil, &RejectError{Reason: fmt.Sprintf("«%s — %s» не нашёлся на доступных площадках", artist, title)}
	}
	return t, nil
}

// searchBest ищет запись, ближайшую по длительности к эталону. Это заметно точнее
// «взять первый результат»: по одному названию наверх часто вылезают часовые
// расширенные версии, склейки и реакции.
func (r *Resolver) searchBest(ctx context.Context, query string, wantDur int) (*Track, error) {
	const fmtLine = "%(id)s\x1f%(title)s\x1f%(duration)s\x1f%(uploader)s\x1f%(webpage_url)s\x1f%(extractor_key)s"
	args := append(r.baseArgs(), "--flat-playlist", "--print", fmtLine, "ytsearch5:"+query)

	out, err := r.run(ctx, 60*time.Second, args)
	if err != nil {
		return nil, err
	}

	var best *Track
	bestDiff := 1 << 30
	for _, line := range strings.Split(out, "\n") {
		parts := strings.Split(strings.TrimSpace(line), "\x1f")
		if len(parts) < 6 {
			continue
		}
		dur := parseDuration(parts[2])
		if dur == 0 {
			continue
		}
		diff := dur - wantDur
		if diff < 0 {
			diff = -diff
		}
		if diff > 20 { // это уже другая запись, а не та же песня
			continue
		}
		if diff < bestDiff {
			bestDiff = diff
			best = &Track{
				ID: parts[0], Title: naOrValue(parts[1]), Duration: dur,
				Uploader: naOrValue(parts[3]), WebURL: naOrValue(parts[4]),
				Source: strings.ToLower(parts[5]),
				Thumb:  "https://i.ytimg.com/vi/" + parts[0] + "/mqdefault.jpg",
			}
		}
	}
	if best == nil {
		return nil, errNotFound
	}
	return best, nil
}

// fillDuration — точечный дозапрос длительности (без скачивания).
func (r *Resolver) fillDuration(ctx context.Context, t *Track) error {
	args := append(r.baseArgs(), "--skip-download",
		"--print", "%(duration)s\x1f%(title)s\x1f%(uploader)s\x1f%(thumbnail)s", t.WebURL)
	out, err := r.run(ctx, 60*time.Second, args)
	if err != nil {
		return err
	}
	parts := strings.Split(strings.TrimSpace(firstLine(out)), "\x1f")
	if len(parts) > 0 {
		t.Duration = parseDuration(parts[0])
	}
	// Заодно подтягиваем нормальные название, автора и обложку: в плоском поиске
	// их либо нет, либо они беднее.
	if len(parts) > 2 {
		if v := naOrValue(parts[1]); v != "" {
			t.Title = v
		}
		if v := naOrValue(parts[2]); v != "" {
			t.Uploader = v
		}
	}
	if len(parts) > 3 && t.Thumb == "" {
		t.Thumb = naOrValue(parts[3])
	}
	return nil
}

// loudnessTarget — целевая громкость в LUFS. −14 держат у себя стриминговые площадки;
// на этом уровне заказы звучат ровно между собой и не перебивают голос стримера.
const loudnessTarget = -14.0

// gainSuffix — расширение файла-спутника с замером. Вынесено в константу, потому что
// его приходится ИСКЛЮЧАТЬ при поиске самого аудио: glob по stem.* иначе однажды
// вернёт .gain вместо трека, и в эфир уйдёт тишина.
const gainSuffix = ".gain"

// cachedAudio ищет в кэше именно аудиофайл, пропуская файлы-спутники.
func cachedAudio(stem string) string {
	got, _ := filepath.Glob(stem + ".*")
	for _, f := range got {
		if !strings.HasSuffix(f, gainSuffix) {
			return f
		}
	}
	return ""
}

// measureGain считает, на сколько децибел трек тише или громче цели.
//
// Меряем, но НЕ перекодируем: усиление применит liquidsoap на лету, получив число
// через annotate. Перекодирование стоило бы качества (lossy→lossy) и процессорного
// времени на каждом заказе, а выигрыш был бы тот же.
//
// Замер — это полный проход декодером, поэтому результат кладём рядом с треком
// в файл-спутник: повторный заказ той же песни считать заново незачем.
//
// Ошибки здесь не фатальны: пустая строка означает «играть как есть», и это
// заведомо лучше, чем отклонить заказ из-за неудавшегося замера громкости.
func (r *Resolver) measureGain(ctx context.Context, file string) string {
	// Выключатель нужен из-за порядка выкатки: пока в radio.liq нет отдельной ступени
	// нормализации с override=null() на ступенях громкости, поле liq_amplify перебивало
	// бы регулятор из панели. Поэтому замер молчит, пока плеер к нему не готов.
	if !r.Normalize {
		return ""
	}
	sidecar := file + gainSuffix
	if b, err := os.ReadFile(sidecar); err == nil {
		if s := strings.TrimSpace(string(b)); s != "" {
			return s
		}
	}

	cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	// print_format=json пишет отчёт в stderr, поэтому берём объединённый вывод.
	cmd := exec.CommandContext(cctx, "ffmpeg", "-nostdin", "-hide_banner", "-nostats",
		"-i", file,
		"-af", fmt.Sprintf("loudnorm=I=%.1f:print_format=json", loudnessTarget),
		"-f", "null", "-")
	out, err := cmd.CombinedOutput()
	if err != nil {
		logf("⚠️ не смог замерить громкость %s: %v", filepath.Base(file), err)
		return ""
	}

	i, j := bytes.LastIndexByte(out, '{'), bytes.LastIndexByte(out, '}')
	if i < 0 || j <= i {
		return ""
	}
	var report struct {
		InputI string `json:"input_i"`
	}
	if err := json.Unmarshal(out[i:j+1], &report); err != nil {
		return ""
	}
	// У тишины input_i приходит как "-inf" и в число не разбирается — это не ошибка.
	measured, err := strconv.ParseFloat(report.InputI, 64)
	if err != nil {
		return ""
	}

	gain := loudnessTarget - measured
	// Предел нужен против записей с очень тихим или битым звуком: без него поправка
	// в +30 дБ вытащила бы наверх шум вместе с музыкой и ударила бы по ушам зрителей.
	if gain > 12 {
		gain = 12
	}
	if gain < -12 {
		gain = -12
	}

	s := fmt.Sprintf("%.1f dB", gain)
	if err := os.WriteFile(sidecar, []byte(s), 0o644); err != nil {
		logf("⚠️ замер громкости не сохранился (посчитаем заново в следующий раз): %v", err)
	}
	return s
}

// TooLong — отдельный метод, чтобы бот мог вернуть баллы с внятной причиной.
func (r *Resolver) TooLong(t *Track) bool {
	return r.MaxDuration > 0 && t.Duration > r.MaxDuration
}

// Download кладёт аудио в кэш. Повторный заказ того же трека файл не перекачивает —
// на популярных треках это экономит и время в эфире, и лимиты YouTube.
func (r *Resolver) Download(ctx context.Context, t *Track) error {
	if err := os.MkdirAll(r.CacheDir, 0o755); err != nil {
		return err
	}
	safeSource := strings.NewReplacer("/", "_", " ", "_").Replace(t.Source)
	stem := filepath.Join(r.CacheDir, safeSource+"-"+t.ID)

	if existing := cachedAudio(stem); existing != "" {
		t.File = existing
		t.Gain = r.measureGain(ctx, existing)
		return nil
	}

	var lastErr error
	for i, format := range downloadFormats {
		args := append(r.baseArgs(),
			"-f", format,
			// Прогрессивные форматы идут со склеенным видео — вытаскиваем звук.
			// "best" вместо конкретного контейнера, чтобы не перекодировать: с m4a
			// это просто снятие видеодорожки, а перекодирование стоило бы CPU на каждом заказе.
			"-x", "--audio-format", "best",
			"-o", stem+".%(ext)s",
			// ⚠️ --print включает симуляцию и НИЧЕГО не качает; --no-simulate обязателен.
			"--no-simulate", "--print", "after_move:%(filepath)s",
			t.WebURL,
		)
		out, err := r.run(ctx, 5*time.Minute, args)
		if err != nil {
			lastErr = err
			// Мусор от неудачной попытки: .part и куски дорожек. Не убрать — и следующая
			// попытка либо подберёт огрызок как готовый файл, либо споткнётся о него.
			if leftovers, _ := filepath.Glob(stem + ".*"); len(leftovers) > 0 {
				for _, f := range leftovers {
					_ = os.Remove(f)
				}
			}
			continue
		}
		path := strings.TrimSpace(firstLine(out))
		if path == "" {
			if got := cachedAudio(stem); got != "" {
				path = got
			} else {
				lastErr = errors.New("yt-dlp отработал, но файла нет")
				continue
			}
		}
		if i > 0 {
			logf("трек %s взят запасным форматом %q (основной не дался: %v)", t.ID, format, lastErr)
		}
		t.File = path
		t.Gain = r.measureGain(ctx, path)
		return nil
	}
	return lastErr
}

// downloadFormats — порядок попыток скачивания, от лучшего к живучему.
//
// Список нужен потому, что fallback внутри самого -f тут НЕ работает: «a/b» выбирает b,
// только если a отсутствует в выдаче, а у нас типичная поломка другая — формат есть,
// отдаётся в списке, и падает уже на байтах с 403 Forbidden (YouTube привязывает
// GVS PO-токен к id видео). yt-dlp в этот момент не возвращается к разбору форматов,
// поэтому перебирать приходится снаружи, отдельными запусками.
//
//   - bestaudio — чистая аудио-дорожка: лучшее качество и меньше всего трафика;
//   - 18/best   — старый прогрессивный mp4 (360p, звук внутри). Раздаётся иначе,
//     чем адаптивные дорожки, и переживает 403 там, где падает bestaudio.
//     Звук из него вытаскивает ffmpeg, на выходе тот же m4a.
var downloadFormats = []string{
	"bestaudio[ext=m4a]/bestaudio",
	"18/best",
}

func (r *Resolver) run(ctx context.Context, timeout time.Duration, args []string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, r.Bin, args...)
	stdout, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			msg := lastMeaningfulLine(string(ee.Stderr))
			return "", fmt.Errorf("yt-dlp: %s", msg)
		}
		return "", err
	}
	return string(stdout), nil
}

func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			return l
		}
	}
	return ""
}

// lastMeaningfulLine достаёт из простыни stderr ту строку, которую не стыдно показать
// зрителю в чате: обычно это ERROR в самом конце.
func lastMeaningfulLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if l != "" && !strings.HasPrefix(l, "WARNING:") {
			return strings.TrimPrefix(l, "ERROR: ")
		}
	}
	return "неизвестная ошибка"
}

// parseDuration: yt-dlp печатает длительность дробной ("250.473"), а у части источников
// её нет вовсе ("NA"). Целочисленный разбор молча давал ноль — и отсев по длине
// переставал работать именно там, где он нужнее всего (часовые миксы на SoundCloud).
func parseDuration(s string) int {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	return int(f)
}

// yt-dlp печатает "NA" вместо пустого значения.
func naOrValue(s string) string {
	if s == "NA" {
		return ""
	}
	return s
}
