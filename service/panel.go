package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// RolePanel — вход в панель. Отличается от bot/broadcaster тем, что токен мы НЕ храним:
// Twitch нужен здесь только чтобы доказать, кто человек, дальше решает список доступа.
const RolePanel = "panel"

const sessionCookie = "tm_panel"

// allowedInPanel — стример и те, кого он назвал (модеры). Список явный, а не «все моды
// канала»: чтобы читать модеров из API, пришлось бы просить у Азима лишнее право.
func (a *App) allowedInPanel(login string) bool {
	return a.inList(a.cfg.PanelAllow, login)
}

// allowedSR — кому позволен !sr помимо модеров канала. Нужен разработчику: чтобы
// пользоваться ботом в чате, иначе пришлось бы выдавать модерку в канале Азима —
// а это права на банхаммер ради одной команды.
func (a *App) allowedSR(login string) bool {
	return a.inList(a.cfg.SRAllow, login)
}

// inList — общая проверка «логин есть в списке через запятую». Стример проходит всегда.
func (a *App) inList(list, login string) bool {
	login = strings.ToLower(strings.TrimSpace(login))
	if login == "" {
		return false
	}
	if login == strings.ToLower(a.cfg.BroadcasterLogin) {
		return true
	}
	for _, l := range strings.Split(list, ",") {
		if l = strings.TrimSpace(strings.ToLower(l)); l != "" && l == login {
			return true
		}
	}
	return false
}

func (a *App) signSession(login string, exp int64) string {
	payload := fmt.Sprintf("%s|%d", strings.ToLower(login), exp)
	mac := hmac.New(sha256.New, []byte(a.cfg.PanelSecret))
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." +
		base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (a *App) sessionLogin(r *http.Request) string {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return ""
	}
	rawPayload, sig, ok := strings.Cut(c.Value, ".")
	if !ok {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(rawPayload)
	if err != nil {
		return ""
	}
	mac := hmac.New(sha256.New, []byte(a.cfg.PanelSecret))
	mac.Write(payload)
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	// hmac.Equal, а не ==: сравнение строк утекает по времени.
	if !hmac.Equal([]byte(want), []byte(sig)) {
		return ""
	}
	login, expStr, ok := strings.Cut(string(payload), "|")
	if !ok {
		return ""
	}
	exp, _ := strconv.ParseInt(expStr, 10, 64)
	if time.Now().Unix() > exp {
		return ""
	}
	if !a.allowedInPanel(login) { // доступ мог быть отозван уже после выдачи куки
		return ""
	}
	return login
}

// dockKey — постоянный ключ для дока в OBS. Внутри OBS логиниться неудобно и незачем:
// док открывается в его встроенном браузере, а ссылка с ключом лежит только у Азима.
// Ключ выводится из PANEL_SECRET, так что смена секрета отзывает все старые ссылки.
func (a *App) dockKey() string {
	mac := hmac.New(sha256.New, []byte(a.cfg.PanelSecret))
	mac.Write([]byte("obs-dock-v1"))
	return hex.EncodeToString(mac.Sum(nil))[:32]
}

func (a *App) dockAuthorized(r *http.Request) bool {
	k := r.FormValue("k")
	return k != "" && hmac.Equal([]byte(k), []byte(a.dockKey()))
}

func (a *App) requirePanel(w http.ResponseWriter, r *http.Request) (string, bool) {
	if a.dockAuthorized(r) {
		return "obs-dock", true
	}
	login := a.sessionLogin(r)
	if login == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "нужен вход через Twitch"})
		return "", false
	}
	return login, true
}

// --- страницы ---

func (a *App) handlePanel(w http.ResponseWriter, r *http.Request) {
	login := a.sessionLogin(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if login == "" {
		_, _ = w.Write([]byte(strings.NewReplacer(
			"{{BRAND}}", brandCSS,
			"{{LOGIN}}", a.base()+"/panel/login",
		).Replace(loginPageHTML)))
		return
	}
	_, _ = w.Write([]byte(strings.NewReplacer(
		"{{BRAND}}", brandCSS,
		"{{ICONS}}", iconsSVG,
		"{{WHO}}", login,
		"{{BASE}}", a.base(),
	).Replace(panelHTML)))
}

func (a *App) base() string { return "/" + a.cfg.WebPrefix }

// handleDock — та же панель, но в раскладке под узкую колонку внутри OBS.
func (a *App) handleDock(w http.ResponseWriter, r *http.Request) {
	if !a.dockAuthorized(r) && a.sessionLogin(r) == "" {
		http.Error(w, "нужна ссылка с ключом или вход через Twitch", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(strings.NewReplacer(
		"{{BRAND}}", brandCSS,
		"{{ICONS}}", iconsSVG,
		"{{BASE}}", a.base(),
		"{{KEY}}", r.FormValue("k"),
	).Replace(dockHTML)))
}

// dockURL — то, что Азим вставляет в OBS → Docks → Custom Browser Docks.
func (a *App) dockURL() string {
	return strings.TrimSuffix(a.cfg.PublicBase, "/") + a.base() + "/dock?k=" + a.dockKey()
}

func (a *App) handlePanelLogin(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	q.Set("role", RolePanel)
	r.URL.RawQuery = q.Encode()
	a.handleAuthLogin(w, r)
}

func (a *App) handlePanelLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1})
	http.Redirect(w, r, a.base()+"/panel", http.StatusFound)
}

// --- API панели ---

func (a *App) handlePanelState(w http.ResponseWriter, r *http.Request) {
	login, ok := a.requirePanel(w, r)
	if !ok {
		return
	}
	meta := a.player.Metadata()
	set := a.settings.Snapshot()
	writeJSON(w, http.StatusOK, map[string]any{
		"me":             login,
		"settings":       set,
		"is_owner":       strings.EqualFold(login, a.cfg.BroadcasterLogin),
		"alive":          a.player.Alive(),
		"now_playing":    meta,
		"remaining":      a.player.Remaining(),
		"queue":          a.queue.List(),
		"stream_url":     a.streamURL(),
		"dock_url":       a.dockURL(),
		"listeners":      a.listeners.Count(a.streamPort()),
		"volume":         a.player.Volume(),
		"monitor_url":    a.monitorURL(),
		"monitor_volume": a.player.MonitorVolume(),
		"paused":         a.Paused(),
		"reward":         map[string]any{"title": a.cfg.RewardTitle, "cost": set.RewardCost, "enabled": set.RewardOn},
		"channel":        a.cfg.BroadcasterLogin,
	})
}

// streamPort — порт, по которому раздаётся звук; из него же считаем слушателей.
func (a *App) streamPort() int {
	_, port, ok := strings.Cut(a.cfg.StreamHost, ":")
	if !ok {
		return 8000
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		return 8000
	}
	return n
}

// streamURL — то, что Азим вставляет в OBS. Логин и пароль зашиты прямо в адрес:
// OBS форму авторизации показать не умеет.
func (a *App) streamURL() string {
	return fmt.Sprintf("http://%s:%s@%s%s",
		a.cfg.StreamUser, a.cfg.StreamPass, a.cfg.StreamHost, a.cfg.StreamMount)
}

// monitorURL — второй источник в OBS, который слышит только сам стример.
func (a *App) monitorURL() string {
	if a.cfg.MonitorMount == "" {
		return ""
	}
	return fmt.Sprintf("http://%s:%s@%s%s",
		a.cfg.StreamUser, a.cfg.StreamPass, a.cfg.StreamHost, a.cfg.MonitorMount)
}

func (a *App) handlePanelAction(w http.ResponseWriter, r *http.Request) {
	login, ok := a.requirePanel(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "нужен POST"})
		return
	}

	action := strings.TrimPrefix(r.URL.Path, a.base()+"/api/panel/")
	id := r.FormValue("id")

	switch action {
	case "skip":
		if err := a.player.Skip(); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		go a.pumpSoon() // не ждать тика: между треками и так набегает задержка тракта
		logf("панель (%s): пропущен текущий трек", login)

	case "remove":
		it, ok := a.queue.Remove(id)
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "трек уже не в очереди"})
			return
		}
		logf("панель (%s): убран из очереди %s", login, it.Title)

	case "up", "down":
		delta := -1
		if action == "down" {
			delta = 1
		}
		if !a.queue.Move(id, delta) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "нельзя подвинуть"})
			return
		}

	case "pause":
		if err := a.Pause(); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		logf("панель (%s): пауза", login)

	case "resume":
		if err := a.Resume(); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		go a.pumpSoon() // если пауза застала тишину, следующий трек не должен ждать тика
		logf("панель (%s): продолжаем", login)

	case "volume", "volume_monitor":
		v, err := strconv.ParseFloat(r.FormValue("v"), 64)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "громкость должна быть числом"})
			return
		}
		set := a.player.SetVolume
		if action == "volume_monitor" {
			set = a.player.SetMonitorVolume
		}
		if err := set(v); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}

	case "clear":
		n := a.queue.Clear()
		logf("панель (%s): очередь очищена (%d треков)", login, n)

	case "add":
		q := strings.TrimSpace(r.FormValue("q"))
		if q == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "пустой запрос"})
			return
		}
		item, err := a.Enqueue(contextTODO(), q, login)
		if err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, item)
		return

	case "settings":
		if err := a.applySettings(login, r.FormValue("reward_enabled"),
			r.FormValue("reward_cost"), r.FormValue("max_duration")); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}

	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "неизвестное действие"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// applySettings меняет настройки эфира на лету. Пустое поле означает «не трогать»,
// поэтому панель может слать только то, что реально поменяли.
func (a *App) applySettings(login, rewardEnabled, rewardCost, maxDuration string) error {
	cur := a.settings.Snapshot()
	twitchPatch := map[string]any{}

	if rewardEnabled != "" {
		on := rewardEnabled == "true"
		// Выключаем именно is_enabled, а не паузу: на паузе награда всё равно висит
		// у зрителей серой плашкой, а выключенную не видно вовсе.
		twitchPatch["is_enabled"] = on
		if on {
			twitchPatch["is_paused"] = false
		}
		cur.RewardOn = on
	}
	if rewardCost != "" {
		n, err := strconv.Atoi(rewardCost)
		if err != nil || n < 1 {
			return fmt.Errorf("цена должна быть целым числом больше нуля")
		}
		twitchPatch["cost"] = n
		cur.RewardCost = n
	}
	if maxDuration != "" {
		n, err := strconv.Atoi(maxDuration)
		if err != nil || n < 30 {
			return fmt.Errorf("лимит длины — не меньше 30 секунд")
		}
		cur.MaxDuration = n
	}

	if len(twitchPatch) > 0 {
		if a.rewardID == "" {
			return fmt.Errorf("награда ещё не создана")
		}
		bc, err := a.tokens.Get(RoleBroadcaster)
		if err != nil {
			return err
		}
		q := map[string][]string{"broadcaster_id": {bc.UserID}, "id": {a.rewardID}}
		if _, err := a.helix(RoleBroadcaster, "PATCH", "/channel_points/custom_rewards", q, twitchPatch); err != nil {
			return err
		}
	}

	if err := a.settings.Update(func(s *SettingsData) {
		s.RewardOn, s.RewardCost, s.MaxDuration = cur.RewardOn, cur.RewardCost, cur.MaxDuration
	}); err != nil {
		return err
	}
	// Резолвер держит лимит у себя — без этого новая настройка начала бы действовать
	// только после перезапуска сервиса.
	a.res.MaxDuration = cur.MaxDuration

	logf("панель (%s): награда=%v, цена=%d, лимит=%dс", login, cur.RewardOn, cur.RewardCost, cur.MaxDuration)
	return nil
}
