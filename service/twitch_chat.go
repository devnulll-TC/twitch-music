package main

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"
)

// Chat — клиент чата Twitch по IRC.
//
// IRC, а не EventSub-чат: боту нужны ровно две вещи — читать команды и отвечать, а
// токен у него именно с chat:read/chat:edit. Плюс IRC переживает разрывы тривиально.
type Chat struct {
	app     *App
	channel string // без решётки
	nick    string

	mu   sync.Mutex
	conn net.Conn

	echoMu   sync.Mutex
	lastEcho map[string]time.Time // ник → когда боту отвечали в последний раз
}

func NewChat(app *App, nick, channel string) *Chat {
	return &Chat{
		app:      app,
		nick:     strings.ToLower(nick),
		channel:  strings.ToLower(channel),
		lastEcho: map[string]time.Time{},
	}
}

// Run держит соединение живым: Twitch рвёт IRC регулярно, это норма, а не авария.
func (c *Chat) Run() {
	for {
		if err := c.session(); err != nil {
			logf("чат отвалился: %v (переподключаюсь через 5с)", err)
		}
		time.Sleep(5 * time.Second)
	}
}

func (c *Chat) session() error {
	tok, err := c.app.tokens.Get(RoleBot)
	if err != nil {
		return err
	}

	conn, err := tls.Dial("tcp", "irc.chat.twitch.tv:6697", &tls.Config{})
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()
	defer conn.Close()

	// tags нужны, чтобы отличать модератора от обычного зрителя по бейджам.
	fmt.Fprintf(conn, "CAP REQ :twitch.tv/tags twitch.tv/commands\r\n")
	fmt.Fprintf(conn, "PASS oauth:%s\r\n", tok.AccessToken)
	fmt.Fprintf(conn, "NICK %s\r\n", c.nick)
	fmt.Fprintf(conn, "JOIN #%s\r\n", c.channel)
	logf("чат: подключаюсь к #%s как %s", c.channel, c.nick)

	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Text()

		// PING/PONG: не ответить — значит быть отключённым через минуту.
		if strings.HasPrefix(line, "PING") {
			fmt.Fprintf(conn, "PONG :tmi.twitch.tv\r\n")
			continue
		}
		if strings.Contains(line, " NOTICE ") && strings.Contains(line, "Login authentication failed") {
			return fmt.Errorf("twitch не принял токен бота")
		}
		if strings.Contains(line, " 001 ") {
			logf("чат: подключён")
		}
		if user, text, tags, ok := parsePrivmsg(line); ok {
			c.handle(user, text, tags)
		}
	}
	return sc.Err()
}

// Say пишет в чат. Сообщения короткие: длинное Twitch обрежет на 500 символах.
func (c *Chat) Say(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if len(msg) > 450 {
		msg = msg[:450] + "…"
	}
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		logf("чат недоступен, сообщение потеряно: %s", msg)
		return
	}
	fmt.Fprintf(conn, "PRIVMSG #%s :%s\r\n", c.channel, msg)
}

// parsePrivmsg разбирает строку IRC вида
// @badges=...;display-name=X :nick!nick@nick.tmi.twitch.tv PRIVMSG #chan :текст
func parsePrivmsg(line string) (user, text string, tags map[string]string, ok bool) {
	tags = map[string]string{}
	if strings.HasPrefix(line, "@") {
		tagPart, rest, found := strings.Cut(line[1:], " ")
		if !found {
			return "", "", nil, false
		}
		for _, kv := range strings.Split(tagPart, ";") {
			k, v, _ := strings.Cut(kv, "=")
			tags[k] = v
		}
		line = rest
	}
	if !strings.HasPrefix(line, ":") {
		return "", "", nil, false
	}
	prefix, rest, found := strings.Cut(line[1:], " ")
	if !found || !strings.HasPrefix(rest, "PRIVMSG ") {
		return "", "", nil, false
	}
	user, _, _ = strings.Cut(prefix, "!")
	_, msg, found := strings.Cut(rest, " :")
	if !found {
		return "", "", nil, false
	}
	return user, strings.TrimSpace(msg), tags, true
}

// handleMention — «@бот hi» → «@ник hi». Просьба Хмоши: бот должен здороваться в ответ.
//
// Отвечаем ТОЛЬКО на hi и ничем, кроме hi. Это не ограничение на будущее, а суть:
// бот, повторяющий произвольный текст, — это чужие слова из его уст, а значит и
// команды модератора («/ban зритель» бот выполнит, а не напишет), и пинги по списку,
// и обход собственного таймаута. С фиксированным ответом всего этого просто нет.
func (c *Chat) handleMention(user, text string) {
	rest, ok := cutMention(text, c.nick)
	if !ok {
		return
	}
	// Бот, отвечающий боту, — это бесконечный пинг-понг на двоих.
	if strings.EqualFold(user, c.nick) {
		return
	}
	reply, ok := echoReplies[strings.ToLower(strings.TrimSpace(rest))]
	if !ok {
		return
	}

	// Кулдаун на человека: без него один зритель заваливает чат ботом, а Twitch
	// за такое выдаёт боту глобальный таймаут, и молчит уже вся музыка.
	c.echoMu.Lock()
	now := time.Now()
	last, seen := c.lastEcho[strings.ToLower(user)]
	if seen && now.Sub(last) < echoCooldown {
		c.echoMu.Unlock()
		return
	}
	c.lastEcho[strings.ToLower(user)] = now
	// Карта живёт всё время работы сервиса, поэтому изредка выкидываем протухшее:
	// иначе она растёт на каждого зрителя, который когда-либо звал бота.
	if len(c.lastEcho) > 500 {
		for nick, t := range c.lastEcho {
			if now.Sub(t) > 10*time.Minute {
				delete(c.lastEcho, nick)
			}
		}
	}
	c.echoMu.Unlock()

	c.Say("@%s %s", user, reply)
}

const echoCooldown = 15 * time.Second

// echoReplies — исчерпывающий список того, на что бот отзывается: ключ ищется по
// сообщению целиком (уже в нижнем регистре), значение уходит в чат как есть.
//
// Это не «пока только два слова», а намеренная граница: ответ должен оставаться
// нашим текстом, а не зрительским. Как только сюда попадёт что-то, собранное из
// сообщения, вернутся и команды модератора из чужих уст, и пинги через бота.
var echoReplies = map[string]string{
	"hi":  "hi",
	"bye": "bye",
}

// cutMention отрезает обращение к боту в начале сообщения и возвращает хвост.
// Форму «@ник» и «ник» принимаем обе, регистр Twitch не различает.
func cutMention(text, nick string) (string, bool) {
	t := strings.TrimSpace(text)
	for _, form := range []string{"@" + nick, nick} {
		if len(t) >= len(form) && strings.EqualFold(t[:len(form)], form) {
			rest := t[len(form):]
			// Требуем разделитель, иначе «@devnulll_musicbot» тоже сойдёт за обращение.
			if rest == "" || rest[0] == ' ' || rest[0] == ',' || rest[0] == ':' {
				return strings.TrimSpace(strings.TrimLeft(rest, " ,:")), true
			}
		}
	}
	return "", false
}

func isPrivileged(user string, tags map[string]string, broadcaster string) bool {
	if strings.EqualFold(user, broadcaster) {
		return true
	}
	if tags["mod"] == "1" {
		return true
	}
	return strings.Contains(tags["badges"], "broadcaster") || strings.Contains(tags["badges"], "moderator")
}

func (c *Chat) handle(user, text string, tags map[string]string) {
	if !strings.HasPrefix(text, "!") {
		c.handleMention(user, text)
		return
	}
	cmd, arg, _ := strings.Cut(strings.TrimPrefix(text, "!"), " ")
	cmd = strings.ToLower(cmd)
	arg = strings.TrimSpace(arg)
	priv := isPrivileged(user, tags, c.app.cfg.BroadcasterLogin)

	switch cmd {
	case "help", "музыка", "commands":
		c.Say("Заказ трека — за баллы канала (кнопка под чатом): ссылка на YouTube, " +
			"SoundCloud или Spotify, либо просто название. Команды: !song — что играет, " +
			"!queue — очередь, !dj — топ заказчиков.")

	case "song", "np", "трек":
		meta := c.app.player.Metadata()
		if meta["title"] == "" {
			c.Say("Сейчас ничего не играет.")
			return
		}
		by := meta["requested_by"]
		if by == "" {
			by = "—"
		}
		c.Say("♪ %s — %s (заказал: %s)", meta["artist"], meta["title"], by)

	case "queue", "очередь":
		q := c.app.queue.List()
		if len(q) == 0 {
			c.Say("Очередь пуста — можно заказывать.")
			return
		}
		parts := make([]string, 0, 3)
		for i, it := range q {
			if i == 3 {
				break
			}
			parts = append(parts, fmt.Sprintf("%d) %s", i+1, it.Title))
		}
		c.Say("В очереди %d: %s", len(q), strings.Join(parts, " | "))

	case "skip", "скип":
		if !priv {
			return // молча: иначе зрители будут спамить командой ради ответа бота
		}
		if err := c.app.player.Skip(); err != nil {
			c.Say("Не смог переключить: %v", err)
			return
		}
		go c.app.pumpSoon()
		c.Say("Трек пропущен.")

	case "dj", "top", "топ", "диджеи":
		top := c.app.leaders.Top(3)
		if len(top) == 0 {
			c.Say("Пока никто не заказывал треки за баллы — место свободно.")
			return
		}
		parts := make([]string, 0, len(top))
		for i, s := range top {
			who := s.Name
			if who == "" {
				who = s.Login
			}
			parts = append(parts, fmt.Sprintf("%d) %s — %d %s (%d %s)",
				i+1, who,
				s.Points, pluralRu(s.Points, "балл", "балла", "баллов"),
				s.Tracks, pluralRu(s.Tracks, "трек", "трека", "треков")))
		}
		c.Say("🎧 DJ трансляции: %s", strings.Join(parts, " · "))

	case "sr", "songrequest":
		// Чат-заказ доступен модерам, стримеру и поимённо названным в SR_ALLOW:
		// для зрителей вход через награду за баллы, иначе баллы обесцениваются.
		if !priv && !c.app.allowedSR(user) {
			c.Say("@%s заказ трека — за баллы канала, кнопкой под чатом.", user)
			return
		}
		if arg == "" {
			c.Say("@%s после !sr нужна ссылка или название.", user)
			return
		}
		go c.requestAndReport(user, arg)
	}
}

func (c *Chat) requestAndReport(user, query string) {
	item, err := c.app.Enqueue(contextTODO(), query, user)
	if err != nil {
		c.Say("@%s не вышло: %s", user, err.Error())
		return
	}
	c.Say("@%s добавил: %s (%s)", user, item.Title, fmtDuration(item.Duration))
}

func fmtDuration(sec int) string {
	return fmt.Sprintf("%d:%02d", sec/60, sec%60)
}

func logf(format string, args ...any) { log.Printf(format, args...) }
