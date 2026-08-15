package main

import (
	"encoding/json"
	"os"
	"sort"
	"sync"
	"time"
)

// Leaderboard — «DJ трансляции»: кто сколько баллов канала спустил на заказы.
//
// Считаем ТОЛЬКО состоявшиеся заказы. Неудачные сюда не доходят по построению:
// запись идёт после успешного Enqueue, а на провале баллы возвращаются зрителю —
// засчитывать их было бы враньём в обе стороны.
//
// Заказы модеров через !sr в зачёт не идут: они бесплатные, и модер с этой командой
// мгновенно занял бы весь пьедестал, ничего не потратив.
type Leaderboard struct {
	path string
	mu   sync.Mutex
	data map[string]*DJStats
}

// DJStats — строка зачёта. Логин ключом, отображаемое имя отдельно: ключ должен быть
// стабильным, а имя зритель может сменить в любой момент, и в чате приятнее видеть
// именно его, с заглавными буквами.
type DJStats struct {
	Login  string    `json:"login"`
	Name   string    `json:"name"`
	Points int       `json:"points"`
	Tracks int       `json:"tracks"`
	LastAt time.Time `json:"last_at"`
}

func LoadLeaderboard(path string) *Leaderboard {
	lb := &Leaderboard{path: path, data: map[string]*DJStats{}}
	b, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			logf("⚠️ лидерборд не прочитался (начинаю с пустого): %v", err)
		}
		return lb
	}
	var rows []DJStats
	if err := json.Unmarshal(b, &rows); err != nil {
		logf("⚠️ лидерборд битый, начинаю с пустого: %v", err)
		return lb
	}
	for i := range rows {
		r := rows[i]
		lb.data[r.Login] = &r
	}
	return lb
}

// Add записывает состоявшийся заказ. Ошибку сохранения не возвращаем: заказ уже уехал
// в эфир, и ронять его из-за неудачной записи статистики было бы неравноценным обменом.
func (l *Leaderboard) Add(login, name string, cost int) {
	if login == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	s, ok := l.data[login]
	if !ok {
		s = &DJStats{Login: login}
		l.data[login] = s
	}
	if name != "" {
		s.Name = name
	}
	s.Points += cost
	s.Tracks++
	s.LastAt = time.Now()

	l.saveLocked()
}

// Top отдаёт первые n мест.
func (l *Leaderboard) Top(n int) []DJStats {
	l.mu.Lock()
	defer l.mu.Unlock()

	out := make([]DJStats, 0, len(l.data))
	for _, s := range l.data {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Points != out[j].Points {
			return out[i].Points > out[j].Points
		}
		if out[i].Tracks != out[j].Tracks {
			return out[i].Tracks > out[j].Tracks
		}
		// При полном равенстве — по логину: иначе порядок мест плясал бы от запуска
		// к запуску, потому что обход карты в Go намеренно случаен.
		return out[i].Login < out[j].Login
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// saveLocked пишет через временный файл: обрыв на середине записи оставил бы
// обрезанный JSON, и весь зачёт пропал бы при следующем чтении.
func (l *Leaderboard) saveLocked() {
	rows := make([]DJStats, 0, len(l.data))
	for _, s := range l.data {
		rows = append(rows, *s)
	}
	b, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		logf("⚠️ лидерборд не сериализовался: %v", err)
		return
	}
	tmp := l.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		logf("⚠️ лидерборд не сохранился: %v", err)
		return
	}
	if err := os.Rename(tmp, l.path); err != nil {
		logf("⚠️ лидерборд не переименовался: %v", err)
	}
}

// pluralRu — три формы русского числительного: 1 балл, 2 балла, 5 баллов.
// Двухформенного plural() здесь мало: он написан под родительный падеж после
// «длиннее», а тут именительный со счётом.
func pluralRu(n int, one, few, many string) string {
	mod10, mod100 := n%10, n%100
	switch {
	case mod10 == 1 && mod100 != 11:
		return one
	case mod10 >= 2 && mod10 <= 4 && (mod100 < 12 || mod100 > 14):
		return few
	default:
		return many
	}
}
