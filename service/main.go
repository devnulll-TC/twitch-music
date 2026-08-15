package main

import (
	"bufio"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Config struct {
	WebPort      string
	WebPrefix    string // секретный префикс пути, см. локацию в nginx
	PlayerAddr   string
	CacheDir     string
	CookiesFile  string
	YtDlpBin     string
	MaxDuration  int
	PlayerClient string // клиент YouTube для yt-dlp; пусто = mweb

	// liquidsoap живёт в контейнере и видит кэш по другому пути, чем мы с хоста.
	// Без трансляции он принимает заявку и молча её выбрасывает, не найдя файл, —
	// в эфире при этом просто тишина, и никакой ошибки нигде нет.
	PlayerCacheDir string

	TwitchClientID     string
	TwitchClientSecret string
	PublicBase         string // внешний адрес, с которого Twitch вернёт пользователя

	BroadcasterLogin string // канал, в котором работает бот
	BotLogin         string
	RewardTitle      string
	RewardCost       int
	RewardCooldown   int // секунд между активациями награды, 0 = без кулдауна

	PanelAllow   string // логины модеров через запятую (кроме стримера)
	SRAllow      string // кому можно !sr помимо модеров канала; логины через запятую
	Normalize    bool   // выравнивать ли громкость заказов; включать ТОЛЬКО вместе с новым radio.liq
	PanelSecret  string // ключ подписи сессионной куки панели
	StreamUser   string
	StreamPass   string
	StreamMount  string
	MonitorMount string // отдельный поток для наушников стримера
	StreamHost   string // адрес:порт, по которому OBS Азима достаёт звук
}

// Item — заказ в нашей очереди. Своя очередь нужна помимо liquidsoap-овской, потому что
// liquidsoap помнит только пути к файлам: ни кто заказал, ни как показать это в оверлее.
type Item struct {
	Track
	QueueID     string    `json:"queue_id"` // короткий id для кнопок в панели
	RequestedBy string    `json:"requested_by"`
	RequestedAt time.Time `json:"requested_at"`
}

type App struct {
	cfg    Config
	player *Player
	res    *Resolver

	tokens   *TokenStore
	chat     *Chat
	rewardID string

	queue     *Queue
	settings  *Settings
	listeners *ListenerCounter
	leaders   *Leaderboard

	pumpMu     sync.Mutex // одна подача за раз: тикер и «пропустить» ходят параллельно
	mu         sync.Mutex
	nowPlaying *Item
	paused     bool
	pausedAt   float64 // секунда, на которой остановили трек
	authStates map[string]authState
}

func main() {
	log.SetFlags(log.LstdFlags)

	env := loadEnv("/root/twitch-music/.env")
	cfg := Config{
		WebPort:      envOr(env, "WEB_PORT", "8030"),
		WebPrefix:    env["WEB_PREFIX"],
		PlayerAddr:   envOr(env, "PLAYER_ADDR", "127.0.0.1:1234"),
		CacheDir:     envOr(env, "CACHE_DIR", "/root/twitch-music/cache"),
		CookiesFile:  envOr(env, "YT_COOKIES", "/root/twitch-music/secrets/youtube-cookies.txt"),
		YtDlpBin:     envOr(env, "YTDLP_BIN", "/usr/local/bin/yt-dlp"),
		MaxDuration:  atoiOr(envOr(env, "MAX_DURATION", "600"), 600),
		PlayerClient: env["YT_PLAYER_CLIENT"],

		PlayerCacheDir: envOr(env, "PLAYER_CACHE_DIR", "/cache"),

		TwitchClientID:     env["TWITCH_CLIENT_ID"],
		TwitchClientSecret: env["TWITCH_CLIENT_SECRET"],
		PublicBase:         envOr(env, "PUBLIC_BASE", "https://get.dev-nulll.com"),

		BroadcasterLogin: envOr(env, "BROADCASTER_LOGIN", "f1pah"),
		BotLogin:         envOr(env, "BOT_LOGIN", "devnulll_music"),
		RewardTitle:      envOr(env, "REWARD_TITLE", "Заказать трек"),
		RewardCost:       atoiOr(envOr(env, "REWARD_COST", "500"), 500),
		RewardCooldown:   atoiOr(envOr(env, "REWARD_COOLDOWN", "30"), 30),

		PanelAllow:   envOr(env, "PANEL_ALLOW", ""),
		SRAllow:      envOr(env, "SR_ALLOW", ""),
		Normalize:    envOr(env, "NORMALIZE_LOUDNESS", "") == "1",
		PanelSecret:  env["PANEL_SECRET"],
		StreamUser:   env["STREAM_USER"],
		StreamPass:   env["STREAM_PASS"],
		StreamMount:  env["STREAM_MOUNT"],
		MonitorMount: env["MONITOR_MOUNT"],
		StreamHost:   envOr(env, "STREAM_HOST", "187.124.187.72:8000"),
	}
	if cfg.PanelSecret == "" {
		log.Fatal("PANEL_SECRET пуст: без него сессионную куку панели можно подделать")
	}
	if cfg.WebPrefix == "" {
		log.Fatal("WEB_PREFIX пуст: без него сервис торчал бы наружу по угадываемому пути")
	}

	app := &App{
		cfg:        cfg,
		player:     NewPlayer(cfg.PlayerAddr),
		authStates: map[string]authState{},
		queue:      &Queue{},
		listeners:  NewListenerCounter("tm-liquidsoap"),
		leaders:    LoadLeaderboard("/root/twitch-music/leaderboard.json"),
		settings: LoadSettings("/root/twitch-music/settings.json", SettingsData{
			MaxDuration: cfg.MaxDuration,
			RewardCost:  cfg.RewardCost,
			RewardOn:    false, // после перезапуска награда не включается сама
		}),
		tokens: NewTokenStore("/root/twitch-music/secrets/tokens.json",
			cfg.TwitchClientID, cfg.TwitchClientSecret),
		res: &Resolver{
			Bin:          cfg.YtDlpBin,
			CookiesFile:  cfg.CookiesFile,
			CacheDir:     cfg.CacheDir,
			MaxDuration:  cfg.MaxDuration,
			PlayerClient: cfg.PlayerClient,
			Normalize:    cfg.Normalize,
			Spotify:      NewSpotify(env["SPOTIFY_CLIENT_ID"], env["SPOTIFY_CLIENT_SECRET"]),
		},
	}
	app.res.MaxDuration = app.settings.Snapshot().MaxDuration

	go app.pump()
	app.warmTokens()

	// Награда, чат и приём активаций поднимаются только если авторизация на месте:
	// без токенов сервис всё равно полезен (панель, ручной заказ), но молчит в Twitch.
	if _, err := app.tokens.Get(RoleBroadcaster); err == nil {
		if reward, err := app.EnsureReward(); err != nil {
			logf("⚠️ награда за баллы недоступна: %v", err)
		} else {
			app.rewardID = reward.ID
			// Состояние берём у Twitch: награду могли включить или выключить руками
			// в его панели, и кнопка у нас обязана показывать правду.
			_ = app.settings.Update(func(s *SettingsData) {
				s.RewardOn = reward.IsEnabled && !reward.IsPaused
				s.RewardCost = reward.Cost
			})
			logf("награда «%s» готова (%d баллов, включена=%v)", reward.Title, reward.Cost, reward.IsEnabled && !reward.IsPaused)
			go app.RunEventSub()
		}
	}
	if _, err := app.tokens.Get(RoleBot); err == nil {
		app.chat = NewChat(app, cfg.BotLogin, cfg.BroadcasterLogin)
		go app.chat.Run()
	}

	mux := http.NewServeMux()
	p := "/" + cfg.WebPrefix
	mux.HandleFunc(p+"/health", app.handleHealth)
	mux.HandleFunc(p+"/api/state", app.handleState)
	mux.HandleFunc(p+"/api/request", app.handleRequest)
	mux.HandleFunc(p+"/panel", app.handlePanel)
	mux.HandleFunc(p+"/dock", app.handleDock)
	mux.HandleFunc(p+"/panel/login", app.handlePanelLogin)
	mux.HandleFunc(p+"/panel/logout", app.handlePanelLogout)
	mux.HandleFunc(p+"/api/panel/state", app.handlePanelState)
	mux.HandleFunc(p+"/api/panel/", app.handlePanelAction)
	mux.HandleFunc(p+"/twitch/", app.handleAuthPage)
	mux.HandleFunc(p+"/twitch/login", app.handleAuthLogin)
	mux.HandleFunc(p+"/twitch/callback", app.handleAuthCallback)

	addr := "127.0.0.1:" + cfg.WebPort
	log.Printf("twitch-music слушает %s, префикс /%s", addr, cfg.WebPrefix)
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Fatal(srv.ListenAndServe())
}

func (a *App) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     a.player.Alive(),
		"player": a.cfg.PlayerAddr,
	})
}

func (a *App) handleState(w http.ResponseWriter, r *http.Request) {
	meta := a.player.Metadata()
	writeJSON(w, http.StatusOK, map[string]any{
		"now_playing": meta,
		"queue_len":   a.queue.Len(),
		"remaining":   a.player.Remaining(),
		"queue":       a.queue.List(),
		"alive":       a.player.Alive(),
	})
}

// handleRequest — заказ трека. Пока дёргается руками для проверки; в Ф1 сюда придут
// награда за баллы канала и чат-команды.
func (a *App) handleRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "нужен POST"})
		return
	}
	query := strings.TrimSpace(r.FormValue("q"))
	user := strings.TrimSpace(r.FormValue("user"))
	if user == "" {
		user = "panel"
	}
	if query == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "пустой заказ"})
		return
	}

	item, err := a.Enqueue(r.Context(), query, user)
	if err != nil {
		// Текст ошибки уйдёт в чат как причина отказа и как повод вернуть баллы.
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, item)
}

// Enqueue — весь путь заказа: опознать → отсеять по длительности → скачать → в эфир.
func (a *App) Enqueue(ctx context.Context, query, user string) (*Item, error) {
	track, err := a.res.Resolve(ctx, query)
	if err != nil {
		return nil, err
	}
	if a.res.TooLong(track) {
		// Лимит берём из настроек, а не из cfg: cfg — это то, что прочитано из .env при
		// старте, а панель меняет лимит на лету. Разъехавшись, они дают худший вид ошибки —
		// отказ по одному числу с объяснением про другое.
		return nil, &RejectError{Reason: "трек длиннее " + humanLimit(a.settings.Snapshot().MaxDuration)}
	}
	if err := a.res.Download(ctx, track); err != nil {
		return nil, err
	}
	// В плеер трек попадёт из pump() — см. queue.go: очередь должна оставаться у нас,
	// иначе её нельзя будет править из панели.
	item := a.queue.Add(Item{Track: *track, RequestedBy: user, RequestedAt: time.Now()})

	log.Printf("заказ принят: %s (%s, %d сек) от %s", track.Title, track.Source, track.Duration, user)
	return &item, nil
}

// playerPath переводит хостовый путь файла в путь, по которому его видит liquidsoap.
func (a *App) playerPath(hostPath string) string {
	if a.cfg.PlayerCacheDir == "" || !strings.HasPrefix(hostPath, a.cfg.CacheDir) {
		return hostPath
	}
	return a.cfg.PlayerCacheDir + strings.TrimPrefix(hostPath, a.cfg.CacheDir)
}

// RejectError — отказ по нашим правилам, а не техническая поломка. Разделены потому,
// что за отказ баллы возвращаем всегда, а за поломку — тоже, но в лог она идёт иначе.
type RejectError struct{ Reason string }

func (e *RejectError) Error() string { return e.Reason }

// contextWithTimeout — общий контекст для фоновых заказов: скачивание не должно
// висеть вечно, но и обрываться раньше, чем yt-dlp дотянет длинный трек.
func contextWithTimeout(d time.Duration) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	_ = cancel // контекст живёт до таймаута; отменять раньше нам нечем и незачем
	return ctx
}

func contextTODO() context.Context { return contextWithTimeout(6 * time.Minute) }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// loadEnv читает тот же .env, что и docker compose: один источник правды на секреты.
func loadEnv(path string) map[string]string {
	env := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		log.Printf("предупреждение: не читается %s: %v", path, err)
		return env
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		env[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
	}
	return env
}

func envOr(env map[string]string, key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	if v, ok := env[key]; ok && v != "" {
		return v
	}
	return def
}

func atoiOr(s string, def int) int {
	if v, err := strconv.Atoi(s); err == nil {
		return v
	}
	return def
}

// humanLimit превращает лимит в секундах в кусок фразы после «трек длиннее …».
// Лимит в панели задаётся секундами и не обязан быть кратным минуте, поэтому
// 330 секунд честнее показать как «5 минут 30 секунд», а не округлить до пяти:
// зритель сверяет отказ с длительностью своего трека.
func humanLimit(sec int) string {
	if sec <= 0 {
		return "0 секунд"
	}
	m, s := sec/60, sec%60
	switch {
	case m == 0:
		return strconv.Itoa(s) + " " + plural(s, "секунды", "секунд")
	case s == 0:
		return strconv.Itoa(m) + " " + plural(m, "минуты", "минут")
	default:
		return strconv.Itoa(m) + " " + plural(m, "минуты", "минут") + " " +
			strconv.Itoa(s) + " " + plural(s, "секунды", "секунд")
	}
}

// plural выбирает форму после «длиннее» — это родительный падеж, где от остальных
// отличается только единица: «длиннее одной минуты», но «длиннее двух/пяти минут».
func plural(n int, one, many string) string {
	if n%10 == 1 && n%100 != 11 {
		return one
	}
	return many
}
