#!/usr/bin/env bash
# Чистка кэша заказанных треков.
#
# Кэш существует не просто так: повторный заказ той же песни не перекачивается, что
# экономит время в эфире и лимиты YouTube (за них уже прилетало HTTP 429). Поэтому
# здесь именно ПРЕДЕЛ по давности, а не удаление всего подряд.
#
# Считаем по времени доступа (atime), а не скачивания: трек, который заказывают
# каждую неделю, должен жить, даже если скачан год назад. Корневая ФС смонтирована
# с relatime — atime обновляется не чаще раза в сутки, и для окна в 30 дней этого
# ровно достаточно.
#
# Удалять файл, который прямо сейчас играет, безопасно: liquidsoap держит его
# открытым, и в Linux данные живут до закрытия дескриптора. Эфир не прервётся.
set -euo pipefail

CACHE="${CACHE_DIR:-/root/twitch-music/cache}"
DAYS="${CACHE_KEEP_DAYS:-30}"

if [[ ! -d "$CACHE" ]]; then
    echo "кэш не найден: $CACHE" >&2
    exit 1
fi

before_size=$(du -sm "$CACHE" | cut -f1)
before_count=$(find "$CACHE" -maxdepth 1 -type f ! -name '*.gain' | wc -l)

removed=0
freed=0

# Основной проход: аудио, к которому не обращались дольше срока. Файл замера
# громкости (.gain) уходит вместе со своим треком — сам по себе он бесполезен.
while IFS= read -r -d '' f; do
    size=$(stat -c %s "$f" 2>/dev/null || echo 0)
    rm -f -- "$f" "$f.gain"
    removed=$((removed + 1))
    freed=$((freed + size))
    echo "удалён: $(basename "$f") ($((size / 1024 / 1024)) МБ, не трогали >${DAYS}д)"
done < <(find "$CACHE" -maxdepth 1 -type f ! -name '*.gain' -atime "+${DAYS}" -print0)

# Второй проход: осиротевшие замеры. Появляются, если аудио удалили руками —
# сами по себе они крошечные, но копились бы вечно.
orphans=0
while IFS= read -r -d '' g; do
    if [[ ! -e "${g%.gain}" ]]; then
        rm -f -- "$g"
        orphans=$((orphans + 1))
    fi
done < <(find "$CACHE" -maxdepth 1 -type f -name '*.gain' -print0)

after_size=$(du -sm "$CACHE" | cut -f1)
echo "итог: удалено ${removed} треков и ${orphans} осиротевших замеров, освобождено $((freed / 1024 / 1024)) МБ"
echo "кэш: было ${before_count} файлов / ${before_size} МБ → стало $((before_count - removed)) / ${after_size} МБ"
