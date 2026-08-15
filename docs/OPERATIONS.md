# Эксплуатация

Хост: **Frankfurt-стенд `187.124.187.72`**, каталог `/root/twitch-music`, вход `ssh root@…`.
Сборка Go — там же (`/usr/local/go/bin/go`, 1.26.3), локальная машина её не тянет.

---

## Что где лежит на сервере

```
/root/twitch-music/
├── tm-service              собранный бинарь (systemd его и запускает)
├── .env                    600 — секреты; читают и сервис, и docker compose
├── settings.json           настройки эфира, их пишет панель
├── secrets/tokens.json     600 — токены bot + broadcaster
├── secrets/youtube-cookies.txt  600 — куки одноразового Google-аккаунта
├── cache/                  скачанные треки (~230 МБ, чистки нет)
├── logs/service.log        весь лог сервиса (append из systemd)
├── liquidsoap/radio.liq    конфиг плеера (монтируется в контейнер ro)
├── docker-compose.yml      tm-liquidsoap
└── run/spotify.fifo        наследие Spotify-звука, не используется
```

Внешнее: юнит `/etc/systemd/system/tm-service.service`, location в
`/etc/nginx/sites-enabled/dev-nulll`, `map $connection_upgrade` в
`/etc/nginx/conf.d/ws-upgrade.conf`, контейнер `tm-pot` (PO-token провайдер).

---

## Деплой кода

Исходники правим **локально** (`~/Desktop/twitch-music`), на сервере только собираем.

```bash
# 1. синк (из корня локальной копии)
rsync -az --delete \
  --exclude '.git' --exclude 'docs' --exclude 'deploy' \
  service/ root@187.124.187.72:/root/twitch-music/service/

# 2. сборка и перезапуск
ssh root@187.124.187.72 '
  cd /root/twitch-music/service &&
  /usr/local/go/bin/go vet ./... &&
  /usr/local/go/bin/go build -o /root/twitch-music/tm-service.new . ; echo "build rc=$?"
'

# 3. подмена бинаря (работающий файл перезаписать нельзя — ETXTBSY)
ssh root@187.124.187.72 '
  systemctl stop tm-service &&
  mv /root/twitch-music/tm-service.new /root/twitch-music/tm-service &&
  systemctl start tm-service &&
  sleep 2 && systemctl is-active tm-service
'
```

- `go vet` не для красоты: он уже дважды ловил реальные баги (шаблон через `Fprintf`,
  копирование структуры с мьютексом).
- **Не проверять сборку через пайп** (`… && echo OK | head`) — код возврата теряется.
- Если на сервере ставился новый пакет (`go get`), **сразу забрать `go.mod`/`go.sum`
  обратно к себе**, иначе следующий синк их затрёт.

## Деплой конфига плеера

```bash
scp deploy/liquidsoap/radio.liq root@187.124.187.72:/root/twitch-music/liquidsoap/
ssh root@187.124.187.72 'cd /root/twitch-music && docker compose restart liquidsoap'
```

⚠️ **Рестарт плеера рвёт соединение с OBS, и оно не всегда восстанавливается само.**
Во время эфира — только по согласованию со стримером.

---

## Ежедневные команды

```bash
systemctl status tm-service                       # жив ли сервис
tail -f /root/twitch-music/logs/service.log       # что происходит
docker ps --filter name=tm-                       # плеер и pot-провайдер
docker logs --tail 50 tm-liquidsoap               # звуковой тракт
curl -s localhost:8030/<WEB_PREFIX>/health        # {"ok":true} = плеер отвечает

# состояние плеера напрямую
echo -e 'stream.remaining\nquit' | nc 127.0.0.1 1234   # <0 → в эфире тишина
echo -e 'stream.metadata\nquit'  | nc 127.0.0.1 1234
echo -e 'var.get tm_volume\nquit' | nc 127.0.0.1 1234
```

Проверить, что поток реально звучит (снаружи, с локальной машины):

```bash
ffmpeg -i 'http://<user>:<pass>@187.124.187.72:8000/<mount>' -t 10 -af volumedetect -f null - 2>&1 | grep -E 'mean_volume|max_volume'
```

`mean_volume` около −20 дБ — играет музыка; около −91 дБ — тишина.

---

## Диагностика: симптом → причина

| Симптом | Куда смотреть |
|---|---|
| **В эфире тишина, в логах чисто** | классика: путь к файлу не транслирован в контейнерный. Проверить `PLAYER_CACHE_DIR` и что файл виден как `/cache/…` |
| **Все заказы с YouTube падают** | протухли куки (`Sign in to confirm you're not a bot`) → переэкспортировать. Если ошибка про формат/подпись — обновить yt-dlp |
| **Заказы падают только на поиске по названию** | то же самое: поиск идёт в YouTube |
| **Бот молчит в чате** | токен роли `bot` (лог `⚠️ роль bot не готова`), или IRC не переподключился → `systemctl restart tm-service` |
| **Баллы не возвращаются** | лог `⚠️ активация не закрыта` с полными id. 404 здесь = награда создана не нашим приложением или права `broadcaster` отозваны |
| **Награда не появилась у зрителей** | она выключена (`reward_enabled` в панели), или роль `broadcaster` не авторизована |
| **Панель показывает «OBS не подключен», а звук идёт** | счётчик не смог прочитать netns контейнера (пересоздали контейнер) — не смертельно; см. `listeners.go` |
| **Панель не пускает** | логин не в `PANEL_ALLOW` (сверить через Helix!), или сменился `PANEL_SECRET` — тогда перелогиниться |
| **«Сейчас играет» показывает прошлый трек** | `stream.metadata` так и работает; правда — в `stream.remaining < 0` |
| **Задержка звука выросла** | проверить `burst` в `radio.liq` (должен быть 8192) и буфер медиа-источника в OBS |

---

## Переавторизация Twitch

Открыть `https://get.dev-nulll.com/<WEB_PREFIX>/twitch/` — страница показывает статус обеих
ролей. Подключение роли `broadcaster` требует **личного участия стримера** (это его аккаунт).

После переподключения `broadcaster` сервис при следующем старте заново найдёт награду
(`EnsureReward` ищет по названию среди наград, созданных нашим client_id) и переподпишет
EventSub.

## Обновление кук YouTube

1. Залогиниться на **одноразовом** Google-аккаунте, экспортировать куки **только с
   `.youtube.com`**.
2. `scp` в `/root/twitch-music/secrets/youtube-cookies.txt`, права `600`.
3. Проверить живым заказом (или `yt-dlp --cookies … --remote-components ejs:github -F <url>`).
4. Этим аккаунтом больше нигде не логиниться — иначе сессия ротируется и куки умрут.

## Обновление yt-dlp

```bash
/usr/local/bin/yt-dlp -U && /usr/local/bin/yt-dlp --version
```
Только вне эфира, и сразу после — прогнать реальный заказ: обновление ломает не реже,
чем чинит.

---

## Гигиена, которую стоит завести

Ничего из этого сейчас нет:

- **чистка кэша** — `find /root/twitch-music/cache -type f -mtime +30 -delete` в cron;
- **бэкап `secrets/tokens.json`** (664 байта; потеря = переавторизация со стримером);
- **health-check кук YouTube** с алертом в `@devnulll_bot` — падение тихое и полное;
- **мониторинг `/health`** — на стенде уже крутится `ops/devnulll-monitor.sh`, можно
  подцепить туда;
- **git-репозиторий** — проект не под контролем версий ни локально, ни на сервере.

## Чего не трогать на этом сервере

`wg-obfs-server` (UDP 47821) и AmneziaWG (UDP 51821) — **живые личные VPN**. Порт `8000`
занят нашим потоком, `8010`/`8012` — rp-app и TTS, `8020` — шахматы, `443` — mtg.
Всё это соседи по стенду, не наши.
