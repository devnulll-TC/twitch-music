package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Роли, под которыми мы ходим в Twitch. Токенов ровно два и они не взаимозаменяемы:
//
//	bot         — пишет в чат от имени devnulll_music;
//	broadcaster — Азим; ТОЛЬКО его токен даёт создать награду за баллы и, главное,
//	              вернуть баллы за неудавшийся заказ.
const (
	RoleBot         = "bot"
	RoleBroadcaster = "broadcaster"
)

var roleScopes = map[string][]string{
	// Панель: прав не просим вовсе — Twitch нужен только чтобы подтвердить, кто человек.
	RolePanel: {},
	RoleBot: {"chat:read", "chat:edit"},
	RoleBroadcaster: {
		"channel:manage:redemptions", // создать награду и менять статус активаций (возврат баллов)
		"channel:read:redemptions",   // читать активации через EventSub
	},
}

// Token — то, что храним на диске. Refresh-токен здесь ценнее access: access живёт
// 4 часа, refresh — пока пользователь не отзовёт доступ.
type Token struct {
	Login        string    `json:"login"`
	UserID       string    `json:"user_id"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	Scopes       []string  `json:"scopes"`
	ExpiresAt    time.Time `json:"expires_at"`
}

func (t *Token) expiringSoon() bool { return time.Now().After(t.ExpiresAt.Add(-5 * time.Minute)) }

// TokenStore — хранилище токенов на диске (secrets/tokens.json, права 600).
type TokenStore struct {
	path         string
	clientID     string
	clientSecret string

	mu     sync.Mutex
	tokens map[string]*Token
}

func NewTokenStore(path, clientID, clientSecret string) *TokenStore {
	s := &TokenStore{path: path, clientID: clientID, clientSecret: clientSecret, tokens: map[string]*Token{}}
	s.load()
	return s
}

func (s *TokenStore) load() {
	b, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	_ = json.Unmarshal(b, &s.tokens)
}

func (s *TokenStore) save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s.tokens, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, b, 0o600)
}

func (s *TokenStore) Put(role string, t *Token) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens[role] = t
	return s.save()
}

// Get отдаёт живой access-токен, освежая его при необходимости. Все вызовы к Twitch
// должны ходить через него, а не через сохранённое поле — иначе через 4 часа эфира
// бот молча перестанет отвечать.
func (s *TokenStore) Get(role string) (*Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	t, ok := s.tokens[role]
	if !ok {
		return nil, fmt.Errorf("нет авторизации для роли %q", role)
	}
	if !t.expiringSoon() {
		return t, nil
	}
	refreshed, err := s.refresh(t)
	if err != nil {
		return nil, fmt.Errorf("не удалось обновить токен %q: %w", role, err)
	}
	s.tokens[role] = refreshed
	_ = s.save()
	return refreshed, nil
}

func (s *TokenStore) Roles() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]string{}
	for role, t := range s.tokens {
		out[role] = t.Login
	}
	return out
}

func (s *TokenStore) refresh(t *Token) (*Token, error) {
	form := url.Values{
		"client_id":     {s.clientID},
		"client_secret": {s.clientSecret},
		"grant_type":    {"refresh_token"},
		"refresh_token": {t.RefreshToken},
	}
	var resp struct {
		AccessToken  string   `json:"access_token"`
		RefreshToken string   `json:"refresh_token"`
		ExpiresIn    int      `json:"expires_in"`
		Scope        []string `json:"scope"`
		Message      string   `json:"message"`
	}
	if err := postJSON("https://id.twitch.tv/oauth2/token", form, &resp); err != nil {
		return nil, err
	}
	if resp.AccessToken == "" {
		return nil, fmt.Errorf("twitch: %s", resp.Message)
	}
	return &Token{
		Login:        t.Login,
		UserID:       t.UserID,
		AccessToken:  resp.AccessToken,
		RefreshToken: firstNonEmpty(resp.RefreshToken, t.RefreshToken),
		Scopes:       resp.Scope,
		ExpiresAt:    time.Now().Add(time.Duration(resp.ExpiresIn) * time.Second),
	}, nil
}

// --- HTTP: страница авторизации ---

type authState struct {
	role      string
	createdAt time.Time
}

func (a *App) handleAuthPage(w http.ResponseWriter, r *http.Request) {
	roles := a.tokens.Roles()
	status := func(role, human string) string {
		if login, ok := roles[role]; ok {
			return fmt.Sprintf(`<p class="ok">✅ %s: <b>%s</b> — авторизован. <a href="login?role=%s">переподключить</a></p>`, human, login, role)
		}
		return fmt.Sprintf(`<p class="no">⛔ %s: не авторизован — <a class="btn" href="login?role=%s">подключить</a></p>`, human, role)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>dev&gt;nulll music — подключение Twitch</title>
<style>body{font:16px/1.6 system-ui;max-width:640px;margin:60px auto;padding:0 20px;background:#0e0e10;color:#efeff1}
a.btn{display:inline-block;background:#9147ff;color:#fff;padding:8px 16px;border-radius:6px;text-decoration:none}
.ok{color:#00f593}.no{color:#ff8b8b}code{background:#1f1f23;padding:2px 6px;border-radius:4px}</style>
<h1>dev&gt;nulll music</h1>
<p>Нужны два подключения — они делают разные вещи и заменить друг друга не могут.</p>
%s%s
<hr><p><small>Аккаунт бота пишет в чат. Аккаунт стримера нужен, чтобы создать награду за баллы
и <b>возвращать баллы</b>, когда заказ не удался — без него Twitch возврат не разрешит.</small></p>`,
		status(RoleBot, "Аккаунт бота (devnulll_music)"),
		status(RoleBroadcaster, "Аккаунт стримера (f1pah)"),
	)
}

func (a *App) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	role := r.URL.Query().Get("role")
	scopes, ok := roleScopes[role]
	if !ok {
		http.Error(w, "неизвестная роль", http.StatusBadRequest)
		return
	}

	state := randomHex(16)
	a.mu.Lock()
	a.authStates[state] = authState{role: role, createdAt: time.Now()}
	a.mu.Unlock()

	q := url.Values{
		"client_id":     {a.cfg.TwitchClientID},
		"redirect_uri":  {a.redirectURI()},
		"response_type": {"code"},
		"scope":         {strings.Join(scopes, " ")},
		"state":         {state},
		// Каждый раз показываем экран согласия: иначе, если в браузере залогинен не тот
		// аккаунт, Twitch молча подключит его — и бот начнёт писать от имени Азима.
		"force_verify": {"true"},
	}
	http.Redirect(w, r, "https://id.twitch.tv/oauth2/authorize?"+q.Encode(), http.StatusFound)
}

func (a *App) handleAuthCallback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	if e := r.URL.Query().Get("error"); e != "" {
		fmt.Fprintf(w, "<p>Twitch отказал: %s — %s</p>", e, r.URL.Query().Get("error_description"))
		return
	}
	code, state := r.URL.Query().Get("code"), r.URL.Query().Get("state")

	a.mu.Lock()
	st, ok := a.authStates[state]
	delete(a.authStates, state)
	a.mu.Unlock()
	if !ok || time.Since(st.createdAt) > 10*time.Minute {
		http.Error(w, "просроченная или чужая ссылка авторизации, начните заново", http.StatusBadRequest)
		return
	}

	form := url.Values{
		"client_id":     {a.cfg.TwitchClientID},
		"client_secret": {a.cfg.TwitchClientSecret},
		"code":          {code},
		"grant_type":    {"authorization_code"},
		"redirect_uri":  {a.redirectURI()},
	}
	var tr struct {
		AccessToken  string   `json:"access_token"`
		RefreshToken string   `json:"refresh_token"`
		ExpiresIn    int      `json:"expires_in"`
		Scope        []string `json:"scope"`
		Message      string   `json:"message"`
	}
	if err := postJSON("https://id.twitch.tv/oauth2/token", form, &tr); err != nil || tr.AccessToken == "" {
		http.Error(w, "обмен кода не удался: "+tr.Message, http.StatusBadGateway)
		return
	}

	login, userID, err := whoami(a.cfg.TwitchClientID, tr.AccessToken)
	if err != nil {
		http.Error(w, "не удалось опознать аккаунт: "+err.Error(), http.StatusBadGateway)
		return
	}

	// Вход в панель токен не сохраняет: личность подтверждена, дальше решает список
	// доступа, а лишний токен на диске — лишний риск.
	if st.role == RolePanel {
		if !a.allowedInPanel(login) {
			http.Error(w, "Аккаунт "+login+" не в списке доступа к панели", http.StatusForbidden)
			return
		}
		exp := time.Now().Add(30 * 24 * time.Hour).Unix()
		http.SetCookie(w, &http.Cookie{
			Name: sessionCookie, Value: a.signSession(login, exp), Path: "/",
			Expires: time.Unix(exp, 0), HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
		})
		http.Redirect(w, r, a.base()+"/panel", http.StatusFound)
		return
	}

	tok := &Token{
		Login: login, UserID: userID,
		AccessToken: tr.AccessToken, RefreshToken: tr.RefreshToken,
		Scopes: tr.Scope, ExpiresAt: time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second),
	}
	if err := a.tokens.Put(st.role, tok); err != nil {
		http.Error(w, "не сохранил токен: "+err.Error(), http.StatusInternalServerError)
		return
	}

	fmt.Fprintf(w, `<meta charset="utf-8"><style>body{font:16px/1.6 system-ui;background:#0e0e10;color:#efeff1;max-width:640px;margin:60px auto;padding:0 20px}</style>
<h2>✅ Готово</h2><p>Роль <b>%s</b> подключена как <b>%s</b>.</p><p><a style="color:#9147ff" href="../twitch/">вернуться</a></p>`, st.role, login)
}

func (a *App) redirectURI() string {
	return strings.TrimSuffix(a.cfg.PublicBase, "/") + "/" + a.cfg.WebPrefix + "/twitch/callback"
}

// --- мелкие помощники ---

func postJSON(endpoint string, form url.Values, out any) error {
	resp, err := http.PostForm(endpoint, form)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

func whoami(clientID, accessToken string) (login, userID string, err error) {
	req, _ := http.NewRequest("GET", "https://api.twitch.tv/helix/users", nil)
	req.Header.Set("Client-Id", clientID)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	var body struct {
		Data []struct {
			ID    string `json:"id"`
			Login string `json:"login"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return "", "", err
	}
	if len(body.Data) == 0 {
		return "", "", fmt.Errorf("twitch вернул пустой ответ (код %d)", resp.StatusCode)
	}
	return body.Data[0].Login, body.Data[0].ID, nil
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
