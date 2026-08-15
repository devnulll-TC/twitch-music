package main

import (
	"bufio"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Player — тонкий клиент к управляющему интерфейсу liquidsoap (telnet на 127.0.0.1:1234).
//
// Протокол простой: пишем команду, читаем строки до "END", соединение закрываем.
// Соединение НЕ держим постоянным: liquidsoap молча роняет висящие сессии, а команды
// у нас редкие (заказ раз в несколько минут), так что дешевле открывать каждый раз.
type Player struct {
	addr string
	mu   sync.Mutex // liquidsoap однопоточный по управлению, не шлём команды внахлёст
}

func NewPlayer(addr string) *Player { return &Player{addr: addr} }

func (p *Player) cmd(command string) ([]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	conn, err := net.DialTimeout("tcp", p.addr, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("liquidsoap недоступен: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))

	if _, err := fmt.Fprintf(conn, "%s\nquit\n", command); err != nil {
		return nil, err
	}

	var out []string
	sc := bufio.NewScanner(conn)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if line == "END" || line == "Bye!" {
			break
		}
		out = append(out, line)
	}
	return out, sc.Err()
}

// Push ставит файл в конец очереди воспроизведения.
//
// meta прокидывается через annotate: у скачанных с YouTube m4a тегов нет вообще,
// поэтому без этого оверлей «сейчас играет» показывал бы пустоту.
func (p *Player) Push(path string, meta map[string]string) error {
	uri := path
	if len(meta) > 0 {
		pairs := make([]string, 0, len(meta))
		// Ключи сортируем, чтобы команда была воспроизводимой в логах.
		keys := make([]string, 0, len(meta))
		for k := range meta {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(meta[k])
			pairs = append(pairs, fmt.Sprintf(`%s="%s"`, k, v))
		}
		uri = "annotate:" + strings.Join(pairs, ",") + ":" + path
	}

	out, err := p.cmd("requests.push " + uri)
	if err != nil {
		return err
	}
	// В ответ прилетает rid — номер заявки. Пустой ответ значит, что заявку не приняли.
	if len(out) == 0 || strings.TrimSpace(out[0]) == "" {
		return fmt.Errorf("liquidsoap не принял заявку: %v", out)
	}
	return nil
}

// Skip обрывает то, что играет сейчас, и переходит к следующему заказу.
func (p *Player) Skip() error { _, err := p.cmd("stream.skip"); return err }

// FlushAndSkip выбрасывает всю очередь и обрывает текущий трек — «стоп всё».
func (p *Player) FlushAndSkip() error { _, err := p.cmd("requests.flush_and_skip"); return err }

// Remaining — сколько секунд осталось до конца текущего трека.
// Возвращает -1, когда играет тишина (у неё бесконечная длительность).
func (p *Player) Remaining() float64 {
	out, err := p.cmd("stream.remaining")
	if err != nil || len(out) == 0 {
		return -1
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(out[0]), 64)
	if err != nil {
		return -1
	}
	return v
}

// Metadata — метаданные того, что звучит прямо сейчас.
func (p *Player) Metadata() map[string]string {
	out, err := p.cmd("stream.metadata")
	if err != nil {
		return nil
	}
	m := map[string]string{}
	for _, line := range out {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		m[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"`)
	}
	return m
}

// QueueLen — сколько заявок уже отдано плееру и ждут своей очереди.
// Нам важно держать там не больше одной: всё остальное должно оставаться в нашей
// очереди, иначе панель модератора не сможет её править.
func (p *Player) QueueLen() int {
	out, err := p.cmd("requests.queue")
	if err != nil || len(out) == 0 {
		return 0
	}
	return len(strings.Fields(strings.Join(out, " ")))
}

// Volume — текущая громкость эфира (1.0 = как есть).
func (p *Player) Volume() float64 {
	out, err := p.cmd("var.get tm_volume")
	if err != nil || len(out) == 0 {
		return 1
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(out[0]), 64)
	if err != nil {
		return 1
	}
	return v
}

// SetVolume — громкость всего потока. Полезна модератору, у которого нет доступа
// к OBS стримера, а убавить музыку надо прямо сейчас.
func (p *Player) SetVolume(v float64) error {
	if v < 0 {
		v = 0
	}
	if v > 1.5 { // выше 1.5 начинается клиппинг, а спасать эфир от него нечем
		v = 1.5
	}
	_, err := p.cmd(fmt.Sprintf("var.set tm_volume = %.3f", v))
	return err
}

// MonitorVolume / SetMonitorVolume — громкость отдельного потока для наушников
// стримера. Отдельная именно потому, что в OBS один фейдер управляет и эфиром, и
// мониторингом: сделать «тише у себя, так же у зрителей» одним источником нельзя.
func (p *Player) MonitorVolume() float64 {
	out, err := p.cmd("var.get tm_monitor_volume")
	if err != nil || len(out) == 0 {
		return 1
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(out[0]), 64)
	if err != nil {
		return 1
	}
	return v
}

func (p *Player) SetMonitorVolume(v float64) error {
	if v < 0 {
		v = 0
	}
	if v > 1.5 {
		v = 1.5
	}
	_, err := p.cmd(fmt.Sprintf("var.set tm_monitor_volume = %.3f", v))
	return err
}

// Listeners — сколько клиентов сейчас слушают поток, по данным самого harbor.
// Подсчёт сокетов снаружи оказался неверным: докерный NAT прячет реальные соединения,
// и панель уверяла, что OBS не подключен, когда он прекрасно играл.
func (p *Player) Listeners() int {
	out, err := p.cmd("tm.listeners")
	if err != nil || len(out) == 0 {
		return -1 // -1 = неизвестно, чтобы не врать «никого нет»
	}
	n, err := strconv.Atoi(strings.TrimSpace(out[0]))
	if err != nil {
		return -1
	}
	return n
}

// Alive — жив ли вообще тракт звука. Нужен для health-check и для панели Азима.
func (p *Player) Alive() bool {
	out, err := p.cmd("uptime")
	return err == nil && len(out) > 0
}
