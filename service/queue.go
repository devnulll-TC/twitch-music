package main

import (
	"strconv"
	"strings"
	"sync"
	"time"
)

// Queue — очередь заказов, которой владеем МЫ, а не liquidsoap.
//
// Плеер умеет только «в конец» и «пропустить текущий»: вынуть третий трек или поменять
// местами второй с четвёртым в нём нельзя. Поэтому liquidsoap получает ровно один трек
// перед самым воспроизведением, а всё остальное ждёт здесь — и потому поддаётся правке
// из панели модератора.
type Queue struct {
	mu    sync.Mutex
	items []Item
	seq   int64
}

func (q *Queue) Add(it Item) Item {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.seq++
	it.QueueID = strconv.FormatInt(q.seq, 10)
	q.items = append(q.items, it)
	return it
}

func (q *Queue) List() []Item {
	q.mu.Lock()
	defer q.mu.Unlock()
	// Пустой срез, а не nil: nil уезжает в JSON как null, и клиенты падают на len().
	out := make([]Item, 0, len(q.items))
	return append(out, q.items...)
}

func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

// Pop достаёт следующий трек для отправки в плеер.
func (q *Queue) Pop() (Item, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return Item{}, false
	}
	it := q.items[0]
	q.items = q.items[1:]
	return it, true
}

func (q *Queue) Remove(id string) (Item, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i, it := range q.items {
		if it.QueueID == id {
			q.items = append(q.items[:i], q.items[i+1:]...)
			return it, true
		}
	}
	return Item{}, false
}

// Move двигает трек на одну позицию вверх (-1) или вниз (+1).
func (q *Queue) Move(id string, delta int) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i, it := range q.items {
		if it.QueueID != id {
			continue
		}
		j := i + delta
		if j < 0 || j >= len(q.items) {
			return false
		}
		q.items[i], q.items[j] = q.items[j], q.items[i]
		return true
	}
	return false
}

func (q *Queue) Clear() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	n := len(q.items)
	q.items = nil
	return n
}

// CountBy — сколько заказов в очереди у одного зрителя (для лимита «не больше N подряд»).
func (q *Queue) CountBy(login string) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	n := 0
	for _, it := range q.items {
		if strings.EqualFold(it.RequestedBy, login) {
			n++
		}
	}
	return n
}

// pump — единственное место, где треки попадают в плеер.
//
// Отдаём следующий трек ТОЛЬКО когда в эфире тишина, а не заранее. Подача впрок
// экономила бы долю секунды на стыке, но ломала две важные вещи:
//   - «пропустить» и «пауза» перескакивали на уже поданный трек вместо остановки;
//   - «сейчас играет» показывал трек, который начнётся лишь через несколько секунд.
//
// Цена — пауза до секунды между треками, и она того стоит.
func (a *App) pump() {
	for range time.Tick(time.Second) {
		a.pumpOnce()
	}
}

// pumpSoon подаёт следующий трек, не дожидаясь тика. Вызывается после «пропустить»:
// liquidsoap обрывает трек мгновенно, а тикер мог сработать только что — и в эфире
// повисала лишняя секунда тишины ровно там, где стример ждёт быстрой реакции.
// Пауза перед вызовом нужна, чтобы skip успел довести источник до тишины.
func (a *App) pumpSoon() {
	time.Sleep(200 * time.Millisecond)
	a.pumpOnce()
}

// pumpOnce — одна попытка подачи. Под своим мьютексом: тикер и «пропустить» могут
// прийти одновременно, а отдать два трека сразу нельзя — второй бы играл в никуда.
func (a *App) pumpOnce() {
	a.pumpMu.Lock()
	defer a.pumpMu.Unlock()

	if a.Paused() {
		return // на паузе следующий трек не подаём
	}
	if a.player.QueueLen() > 0 {
		return // плеер ещё не забрал предыдущую заявку
	}
	// remaining >= 0 означает, что трек играет; ждём его конца.
	if a.player.Remaining() >= 0 {
		return
	}

	it, ok := a.queue.Pop()
	if !ok {
		return
	}
	meta := map[string]string{
		"title":        it.Title,
		"artist":       it.Uploader,
		"source":       it.Source,
		"requested_by": it.RequestedBy,
		"web_url":      it.WebURL,
	}
	// Поправка громкости. Применяет её ступень normalize в radio.liq — та, что
	// БЕЗ override=null(); ступени громкости из панели помечены override=null()
	// именно чтобы это поле их не перебивало.
	if it.Gain != "" {
		meta["liq_amplify"] = it.Gain
	}
	if err := a.player.Push(a.playerPath(it.File), meta); err != nil {
		logf("⚠️ не смог отдать трек плееру (%s): %v", it.Title, err)
		return
	}
	a.setNowPlaying(it)
	logf("в эфир: %s — %s (заказал %s)", it.Uploader, it.Title, it.RequestedBy)
}

// Pause — настоящая пауза, а не мьют: запоминаем позицию, обрываем воспроизведение
// (в эфир идёт тишина), а при снятии паузы возвращаем трек ровно с того же места.
// Мьютом обойтись нельзя — трек продолжал бы идти, и зритель терял бы полминуты песни.
func (a *App) Pause() error {
	a.mu.Lock()
	if a.paused {
		a.mu.Unlock()
		return nil
	}
	cur := a.nowPlaying
	a.paused = true
	a.mu.Unlock()

	remaining := a.player.Remaining()
	if cur == nil || remaining <= 0 {
		return nil // играла тишина: достаточно остановить подачу новых треков
	}

	pos := float64(cur.Duration) - remaining
	if pos < 0 {
		pos = 0
	}
	a.mu.Lock()
	a.pausedAt = pos
	a.mu.Unlock()

	logf("пауза на %.0f сек трека «%s»", pos, cur.Title)
	return a.player.Skip()
}

// Resume возвращает трек с сохранённой секунды. Старт с середины liquidsoap 2.3
// понимает через метаданное liq_cue_in прямо в заявке.
func (a *App) Resume() error {
	a.mu.Lock()
	if !a.paused {
		a.mu.Unlock()
		return nil
	}
	a.paused = false
	cur, pos := a.nowPlaying, a.pausedAt
	a.pausedAt = 0
	a.mu.Unlock()

	if cur == nil || pos <= 0 {
		return nil // продолжит pump() со следующего трека
	}
	meta := map[string]string{
		"title":        cur.Title,
		"artist":       cur.Uploader,
		"source":       cur.Source,
		"requested_by": cur.RequestedBy,
		"web_url":      cur.WebURL,
		"liq_cue_in":   strconv.FormatFloat(pos, 'f', 1, 64),
	}
	// Без этого трек после снятия с паузы возвращался бы в эфир на исходной громкости
	// и прыгал относительно того, как звучал минуту назад.
	if cur.Gain != "" {
		meta["liq_amplify"] = cur.Gain
	}
	logf("продолжаю «%s» с %.0f сек", cur.Title, pos)
	return a.player.Push(a.playerPath(cur.File), meta)
}

func (a *App) Paused() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.paused
}

func (a *App) setNowPlaying(it Item) {
	a.mu.Lock()
	a.nowPlaying = &it
	a.mu.Unlock()
}

func (a *App) NowPlaying() *Item {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.nowPlaying == nil {
		return nil
	}
	cp := *a.nowPlaying
	return &cp
}
