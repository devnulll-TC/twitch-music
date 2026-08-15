package main

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
)

// Счётчик слушателей пережил две неудачные реализации, обе врали:
//
//  1. Сокеты на ХОСТЕ — докерный NAT прячет реальные соединения, панель показывала
//     «OBS не подключен», когда OBS прекрасно играл.
//  2. Счётчик событий в самом liquidsoap (on_connect/on_disconnect) — обработчик
//     разрыва не вызывается, когда клиент отваливается аварийно, а OBS отваливается
//     именно так. Счётчик уползал вверх и навсегда застревал на «подключен».
//
// Работает третий вариант: читаем таблицу сокетов в сетевом пространстве контейнера
// (/proc/<pid контейнера>/net/tcp). Это ровно то, что видит сам harbor, и стоит
// дешевле любого docker exec.
type ListenerCounter struct {
	container string
	mu        sync.Mutex
	pid       int
}

func NewListenerCounter(container string) *ListenerCounter {
	return &ListenerCounter{container: container}
}

// Count возвращает число слушателей или -1, если посчитать не удалось.
// Минус один важен: «не знаю» нельзя показывать как «никого нет».
func (l *ListenerCounter) Count(port int) int {
	pid := l.resolvePid()
	if pid == 0 {
		return -1
	}
	n, ok := countInNetns(pid, port)
	if !ok {
		// Контейнер могли пересоздать — pid устарел, пробуем ещё раз.
		l.mu.Lock()
		l.pid = 0
		l.mu.Unlock()
		if pid = l.resolvePid(); pid == 0 {
			return -1
		}
		if n, ok = countInNetns(pid, port); !ok {
			return -1
		}
	}
	return n
}

func (l *ListenerCounter) resolvePid() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.pid != 0 {
		return l.pid
	}
	out, err := exec.Command("docker", "inspect", "-f", "{{.State.Pid}}", l.container).Output()
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || pid <= 0 {
		return 0
	}
	l.pid = pid
	return pid
}

func countInNetns(pid, port int) (int, bool) {
	target := ":" + strings.ToUpper(strconv.FormatInt(int64(port), 16))
	found := false
	n := 0
	for _, name := range []string{"tcp", "tcp6"} {
		b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/net/" + name)
		if err != nil {
			continue
		}
		found = true
		for i, line := range strings.Split(string(b), "\n") {
			if i == 0 {
				continue
			}
			f := strings.Fields(line)
			// f[1] — локальный адрес, f[3] — состояние (01 = ESTABLISHED)
			if len(f) > 3 && strings.HasSuffix(f[1], target) && f[3] == "01" {
				n++
			}
		}
	}
	return n, found
}
