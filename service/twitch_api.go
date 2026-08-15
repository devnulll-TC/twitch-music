package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// helix — обёртка над Twitch Helix API. Каждый запрос ходит под токеном конкретной роли,
// потому что права у бота и у стримера разные и подменять их нельзя.
func (a *App) helix(role, method, path string, query url.Values, body any) ([]byte, error) {
	tok, err := a.tokens.Get(role)
	if err != nil {
		return nil, err
	}

	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(b)
	}

	u := "https://api.twitch.tv/helix" + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequest(method, u, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Client-Id", a.cfg.TwitchClientID)
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode >= 300 {
		return raw, fmt.Errorf("twitch %s %s → %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return raw, nil
}

type Reward struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Cost     int    `json:"cost"`
	IsPaused  bool `json:"is_paused"`
	IsEnabled bool `json:"is_enabled"`
}

// EnsureReward создаёт награду за баллы или находит уже созданную нами.
//
// ⚠️ Награду обязан создать НАШ client_id: Twitch разрешает менять статус активаций
// (то есть подтверждать заказ и ВОЗВРАЩАТЬ баллы) только приложению-создателю. Если
// стример сделает награду руками в панели Twitch, мы сможем её только видеть.
func (a *App) EnsureReward() (*Reward, error) {
	bc, err := a.tokens.Get(RoleBroadcaster)
	if err != nil {
		return nil, err
	}
	q := url.Values{
		"broadcaster_id": {bc.UserID},
		// only_manageable_rewards=true оставляет только наши награды — чужие нам бесполезны.
		"only_manageable_rewards": {"true"},
	}

	raw, err := a.helix(RoleBroadcaster, "GET", "/channel_points/custom_rewards", q, nil)
	if err == nil {
		var list struct {
			Data []Reward `json:"data"`
		}
		if json.Unmarshal(raw, &list) == nil {
			for _, r := range list.Data {
				if strings.EqualFold(r.Title, a.cfg.RewardTitle) {
					return &r, nil
				}
			}
		}
	}

	body := map[string]any{
		"title":                             a.cfg.RewardTitle,
		"cost":                              a.cfg.RewardCost,
		"prompt":                            "Напиши название трека или ссылку (YouTube, SoundCloud, Spotify). Если трек не найдётся — баллы вернутся автоматически.",
		"is_user_input_required":            true,
		"is_global_cooldown_enabled":        a.cfg.RewardCooldown > 0,
		"global_cooldown_seconds":           a.cfg.RewardCooldown,
		"background_color":                  "#9147FF",
		// false обязателен: иначе активации сразу помечаются выполненными и вернуть
		// баллы уже нельзя — а возврат и есть главная причина всей затеи.
		"should_redemptions_skip_request_queue": false,
	}
	raw, err = a.helix(RoleBroadcaster, "POST", "/channel_points/custom_rewards",
		url.Values{"broadcaster_id": {bc.UserID}}, body)
	if err != nil {
		return nil, err
	}
	var created struct {
		Data []Reward `json:"data"`
	}
	if err := json.Unmarshal(raw, &created); err != nil || len(created.Data) == 0 {
		return nil, fmt.Errorf("награда создана, но ответ не разобран: %s", string(raw))
	}
	return &created.Data[0], nil
}

// FinishRedemption закрывает активацию: FULFILLED — заказ принят, CANCELED — баллы
// вернулись зрителю. Возврат делаем всегда, когда заказ не доехал до эфира.
func (a *App) FinishRedemption(rewardID, redemptionID string, ok bool) error {
	bc, err := a.tokens.Get(RoleBroadcaster)
	if err != nil {
		return err
	}
	status := "CANCELED"
	if ok {
		status = "FULFILLED"
	}
	q := url.Values{
		"id":             {redemptionID},
		"broadcaster_id": {bc.UserID},
		"reward_id":      {rewardID},
	}
	raw, err := a.helix(RoleBroadcaster, "PATCH", "/channel_points/custom_reward_redemptions", q,
		map[string]string{"status": status})
	if err != nil {
		// Подробности нужны в логе целиком: 404 здесь означает не «нет такого»,
		// а расхождение между наградой, активацией и приложением, и без точных
		// идентификаторов это не отладить.
		logf("⚠️ активация не закрыта: status=%s reward=%s redemption=%s ответ=%s",
			status, rewardID, redemptionID, strings.TrimSpace(string(raw)))
	}
	return err
}

// subscribeEventSub подписывает websocket-сессию на активации нашей награды.
func (a *App) subscribeEventSub(sessionID, rewardID string) error {
	bc, err := a.tokens.Get(RoleBroadcaster)
	if err != nil {
		return err
	}
	cond := map[string]string{"broadcaster_user_id": bc.UserID}
	if rewardID != "" {
		// Фильтр по награде: иначе прилетали бы активации всех наград канала подряд.
		cond["reward_id"] = rewardID
	}
	body := map[string]any{
		"type":      "channel.channel_points_custom_reward_redemption.add",
		"version":   "1",
		"condition": cond,
		"transport": map[string]string{"method": "websocket", "session_id": sessionID},
	}
	_, err = a.helix(RoleBroadcaster, "POST", "/eventsub/subscriptions", nil, body)
	return err
}

// warmTokens прогревает оба токена на старте: лучше узнать о протухшей авторизации
// сразу, чем в момент первого заказа в прямом эфире.
func (a *App) warmTokens() {
	for _, role := range []string{RoleBot, RoleBroadcaster} {
		if t, err := a.tokens.Get(role); err != nil {
			logf("⚠️ роль %s не готова: %v", role, err)
		} else {
			logf("роль %s: %s (токен живёт ещё %s)", role, t.Login,
				time.Until(t.ExpiresAt).Round(time.Minute))
		}
	}
}
