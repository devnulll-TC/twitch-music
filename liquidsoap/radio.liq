# Тракт звука для music-бота Азима.
# Очередь заказов наполняется извне через telnet (порт 1234, слушает только localhost):
#   echo "requests.push /cache/<файл>" | nc 127.0.0.1 1234
# Наружу отдаётся один MP3-поток, который OBS берёт как Media Source.

settings.log.level := 3
settings.log.stdout := true

# Управляющий интерфейс. Наружу НЕ выставлен (см. проброс портов в compose).
settings.server.telnet := true
settings.server.telnet.bind_addr := "0.0.0.0"
settings.server.telnet.port := 1234

# По умолчанию liquidsoap выпускает на выход только стандартные теги и молча режет
# все остальные — из-за этого оверлей не видел ни источника, ни имени заказчика.
settings.encoder.metadata.export := [
  "artist", "title", "album", "source", "requested_by", "web_url"
]

settings.harbor.bind_addrs := ["0.0.0.0"]

# Очередь заказов. Пустая очередь — это норма, а не ошибка.
requests = request.queue(id="requests")

# Spotify играет НЕ файлом: go-librespot держит живое соединение и льёт сырой PCM
# в именованный канал. Поэтому это отдельный источник, а не элемент очереди.
spotify = input.external.rawaudio(
  id="spotify", restart=true, channels=2, samplerate=44100,
  "cat /run/tm/spotify.fifo"
)

# Флагом рулит наш сервис по telnet: var.set spotify_on = true|false.
# Без явного флага switch не отличил бы «Spotify молчит между треками» от «Spotify не нужен».
spotify_on = interactive.bool("spotify_on", false)

# Пока очередь пуста, в эфир идёт тишина: поток обязан быть непрерывным,
# иначе OBS отвалится по таймауту и Азиму придётся дёргать источник руками.
# track_sensitive=false — чтобы заказ подхватывался сразу, не дожидаясь конца тишины.
files = fallback(track_sensitive=false, [requests, blank()])

# Spotify имеет приоритет, пока флаг поднят; всё остальное время играют файлы.
radio = switch(track_sensitive=false, [({spotify_on()}, spotify), ({true}, files)])
radio = mksafe(radio)

# Громкость, управляемая из панели: var.set tm_volume = 0.75
# Нужна потому, что у модера нет доступа к OBS стримера, а убавить музыку
# посреди эфира иногда надо срочно.
# Две независимые громкости, потому что задачи разные:
#   tm_volume         — то, что слышат зрители (крутит панель и модер);
#   tm_monitor_volume — то, что слышит сам стример в наушниках.
# В OBS одним источником так не сделать: там один фейдер и на эфир, и на мониторинг,
# поэтому отдаём два отдельных потока, каждый со своим усилением.
tm_volume = interactive.float("tm_volume", 1.0)
tm_monitor_volume = interactive.float("tm_monitor_volume", 1.0)

# Выравнивание громкости заказов. Поправку в дБ считает сервис (ffmpeg loudnorm при
# скачивании) и передаёт с треком в поле liq_amplify — здесь она и применяется.
#
# ⚠️ Ступень ОБЯЗАНА быть отдельной. amplify по умолчанию ЗАМЕНЯЕТ свой множитель
# значением из liq_amplify, а не умножает на него. Поставь мы поле на ступени
# громкости — заказ молча перебивал бы регулятор из панели, и убавить музыку
# посреди эфира стало бы невозможно. Поэтому ниже у обеих ступеней громкости
# стоит override=null(), а забирает поле только эта.
radio = amplify(id="normalize", 1.0, radio)

# Считаем слушателей сам плеер: подсчёт сокетов снаружи врал (докерный NAT прячет
# реальные соединения), а harbor знает своих клиентов точно.
clients = ref(0)
def on_client_connect(~headers=_, ~uri=_, ~protocol=_, _) =
  clients := !clients + 1
  log("listener connected, now #{!clients}")
end
def on_client_disconnect(_) =
  clients := max(0, !clients - 1)
  log("listener disconnected, now #{!clients}")
end
server.register(namespace="tm", "listeners", fun (_) -> string_of(!clients))

output.harbor(
  on_connect=on_client_connect,
  on_disconnect=on_client_disconnect,
  %mp3(bitrate=192, samplerate=44100, stereo=true),
  id="stream",
  port=8000,
  # Дилей в OBS складывается из нашего стартового выброса и буфера самого OBS.
  # По умолчанию harbor отдаёт клиенту 65534 байта сразу — на 192 kbps это 2.7 секунды
  # музыки, которые сразу становятся задержкой. 8 КБ = ~0.34 сек: хватает, чтобы плеер
  # не голодал, и не копит отставание.
  burst=8192,
  chunk=2048,
  buffer=131072,
  mount=environment.get("STREAM_MOUNT"),
  user=environment.get("STREAM_USER"),
  password=environment.get("STREAM_PASS"),
  amplify({tm_volume()}, override=null(), radio)
)

# Поток для наушников стримера: тот же звук, своя громкость. В OBS добавляется вторым
# источником с Audio Monitoring = "Monitor Only (mute output)" — тогда его слышит
# только Азим, а на зрителей он не влияет вообще.
output.harbor(
  %mp3(bitrate=192, samplerate=44100, stereo=true),
  id="monitor",
  port=8000,
  burst=8192,
  chunk=2048,
  buffer=131072,
  mount=environment.get("MONITOR_MOUNT"),
  user=environment.get("STREAM_USER"),
  password=environment.get("STREAM_PASS"),
  amplify({tm_monitor_volume()}, override=null(), radio)
)
