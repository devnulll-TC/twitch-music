package main

import (
	"encoding/json"
	"time"

	"github.com/gorilla/websocket"
)

// Активация награды за баллы — то, ради чего всё затевалось.
type Redemption struct {
	ID        string `json:"id"`
	UserLogin string `json:"user_login"`
	UserName  string `json:"user_name"`
	UserInput string `json:"user_input"`
	Reward    struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		Cost  int    `json:"cost"`
	} `json:"reward"`
}

type esMessage struct {
	Metadata struct {
		MessageType string `json:"message_type"`
	} `json:"metadata"`
	Payload struct {
		Session struct {
			ID           string `json:"id"`
			ReconnectURL string `json:"reconnect_url"`
		} `json:"session"`
		Event json.RawMessage `json:"event"`
	} `json:"payload"`
}

// RunEventSub держит websocket-соединение с Twitch и обрабатывает активации награды.
// Twitch сам просит переподключаться (session_reconnect) — это штатное поведение,
// а не сбой, и игнорировать его нельзя, иначе соединение просто умрёт.
func (a *App) RunEventSub() {
	url := "wss://eventsub.wss.twitch.tv/ws?keepalive_timeout_seconds=30"
	for {
		next, err := a.eventSubSession(url)
		if err != nil {
			logf("eventsub: %v (переподключаюсь через 5с)", err)
			time.Sleep(5 * time.Second)
		}
		if next != "" {
			url = next // Twitch выдал адрес для бесшовного переподключения
		} else {
			url = "wss://eventsub.wss.twitch.tv/ws?keepalive_timeout_seconds=30"
		}
	}
}

func (a *App) eventSubSession(url string) (reconnectTo string, err error) {
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	for {
		// Пауза больше keepalive означает мёртвое соединение — рвём и переподключаемся.
		_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		_, data, err := conn.ReadMessage()
		if err != nil {
			return "", err
		}

		var msg esMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}

		switch msg.Metadata.MessageType {
		case "session_welcome":
			if err := a.subscribeEventSub(msg.Payload.Session.ID, a.rewardID); err != nil {
				return "", err
			}
			logf("eventsub: подписка на активации награды оформлена")

		case "session_reconnect":
			logf("eventsub: Twitch просит переподключиться")
			return msg.Payload.Session.ReconnectURL, nil

		case "notification":
			var r Redemption
			if err := json.Unmarshal(msg.Payload.Event, &r); err != nil {
				logf("eventsub: не разобрал активацию: %v", err)
				continue
			}
			go a.handleRedemption(r)

		case "revocation":
			logf("⚠️ eventsub: Twitch отозвал подписку (сменились права или награда удалена)")
		}
	}
}

// handleRedemption — весь смысл проекта в одном месте: заказ либо доезжает до эфира,
// либо зритель получает баллы обратно и внятную причину в чат.
func (a *App) handleRedemption(r Redemption) {
	who := r.UserName
	if who == "" {
		who = r.UserLogin
	}
	logf("активация от %s: %q (reward=%s redemption=%s)", who, r.UserInput, r.Reward.ID, r.ID)

	item, err := a.Enqueue(contextWithTimeout(6*time.Minute), r.UserInput, r.UserLogin)
	if err != nil {
		if e := a.FinishRedemption(r.Reward.ID, r.ID, false); e != nil {
			logf("⚠️ не смог вернуть баллы %s: %v", who, e)
		}
		if a.chat != nil {
			a.chat.Say("@%s не получилось: %s. Баллы (%d) вернул.", r.UserLogin, err.Error(), r.Reward.Cost)
		}
		return
	}

	// Зачёт «DJ трансляции». Пишем ПОСЛЕ успешного заказа и берём цену из самой
	// активации, а не из настроек: цену награды могли поменять в панели уже после
	// того, как зритель нажал кнопку, и списалось бы с него всё равно старое.
	a.leaders.Add(r.UserLogin, r.UserName, r.Reward.Cost)

	if e := a.FinishRedemption(r.Reward.ID, r.ID, true); e != nil {
		logf("⚠️ не смог закрыть активацию %s: %v", who, e)
	}
	if a.chat != nil {
		a.chat.Say("@%s добавил в очередь: %s (%s)", r.UserLogin, item.Title, fmtDuration(item.Duration))
	}
}
