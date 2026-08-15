#!/usr/bin/env python3
"""Замер покрытия: сколько треков из Spotify реально находятся на YouTube/SoundCloud.

Смысл в том, чтобы не гадать. Выборка нарочно перекошена в сторону непопулярного:
половина треков берётся с popularity <= 25, потому что мейнстрим на YouTube есть всегда
и мерить на нём — обманывать себя.

Совпадением считаем не похожесть названия, а СОВПАДЕНИЕ ДЛИТЕЛЬНОСТИ (±7 сек) при
пересечении слов исполнителя/названия: одноимённых треков много, а вот «то же название
и та же длительность» почти наверняка та самая запись.
"""
import json
import re
import subprocess
import sys
import urllib.parse
import urllib.request

YTDLP = "/usr/local/bin/yt-dlp"
COOKIES = "/root/twitch-music/secrets/youtube-cookies.txt"

# Запросы подобраны так, чтобы задеть разные сцены и языки, а не только англоязычный поп.
QUERIES = [
    ("mainstream", "year:2024 pop"),
    ("mainstream", "hip hop classic"),
    ("русская сцена", "русский рэп"),
    ("русская сцена", "русский рок"),
    ("нишевое", "phonk"),
    ("нишевое", "vaporwave"),
    ("нишевое", "dungeon synth"),
    ("нишевое", "hyperpop"),
    ("нишевое", "city pop"),
    ("нишевое", "atmospheric black metal"),
]


def spotify_token(cid, secret):
    data = urllib.parse.urlencode({"grant_type": "client_credentials"}).encode()
    req = urllib.request.Request("https://accounts.spotify.com/api/token", data=data)
    import base64
    basic = base64.b64encode(f"{cid}:{secret}".encode()).decode()
    req.add_header("Authorization", "Basic " + basic)
    return json.load(urllib.request.urlopen(req, timeout=20))["access_token"]


# ⚠️ Spotify отдаёт "Invalid limit" на limit > 10 в поиске (проверено 10.08.2026,
# в их же документации до сих пор написано «до 50»).
def spotify_search(token, q, limit=10, offset=0):
    # quote_via=quote обязателен: urlencode по умолчанию кодирует пробел как "+",
    # и поиск Spotify на такой запрос отвечает 400.
    url = "https://api.spotify.com/v1/search?" + urllib.parse.urlencode(
        {"q": q, "type": "track", "limit": limit, "offset": offset},
        quote_via=urllib.parse.quote
    )
    req = urllib.request.Request(url, headers={"Authorization": "Bearer " + token})
    return json.load(urllib.request.urlopen(req, timeout=20))["tracks"]["items"]


def norm(s):
    s = s.lower()
    s = re.sub(r"\(.*?\)|\[.*?\]", " ", s)            # (Official Video), [HD] и прочий шум
    s = re.sub(r"[^\w\s]", " ", s, flags=re.UNICODE)
    return set(w for w in s.split() if len(w) > 2)


def search_source(prefix, query, n=4):
    """Плоский поиск: id, название, длительность."""
    fmt = "%(title)s\x1f%(duration)s"
    cmd = [YTDLP, "--no-warnings", "--flat-playlist", "--print", fmt,
           "--cookies", COOKIES, f"{prefix}{n}:{query}"]
    try:
        out = subprocess.run(cmd, capture_output=True, text=True, timeout=90).stdout
    except subprocess.TimeoutExpired:
        return []
    res = []
    for line in out.strip().split("\n"):
        if not line.strip():
            continue
        parts = line.split("\x1f")
        try:
            dur = int(float(parts[1]))
        except (ValueError, IndexError):
            dur = 0
        res.append((parts[0], dur))
    return res


def main():
    env = {}
    for line in open("/root/twitch-music/.env"):
        k, _, v = line.strip().partition("=")
        env[k] = v
    token = spotify_token(env["SPOTIFY_CLIENT_ID"], env["SPOTIFY_CLIENT_SECRET"])

    # ⚠️ Spotify перестал отдавать popularity в поиске (10.08.2026: ноль даже у мировых
    # хитов), поэтому «непопулярность» берём иначе — глубиной выдачи. Что лежит на 150-й
    # позиции по запросу, заведомо не мейнстрим, и это не зависит от сломанного поля.
    sample = []
    seen = set()
    for kind, q in QUERIES:
        items = []
        for offset in (0, 150, 400):
            got = spotify_search(token, q, limit=10, offset=offset)
            items += [t for t in got if t and t.get("artists") and t.get("name")][:2]
        for t in items:
            key = (t["artists"][0]["name"].lower(), t["name"].lower())
            if key in seen:      # без этого один трек считался бы дважды
                continue
            seen.add(key)
            sample.append({
                "kind": kind,
                "deep": True,
                "title": t["name"],
                "artist": t["artists"][0]["name"],
                "dur": t["duration_ms"] // 1000,
                "pop": t.get("popularity", 0),
            })

    print(f"выборка: {len(sample)} треков из Spotify\n")
    stats = {"yt": 0, "sc": 0, "нет": 0}
    misses = []

    for i, t in enumerate(sample, 1):
        query = f"{t['artist']} - {t['title']}"
        want = norm(query)
        found = None

        for prefix, tag in (("ytsearch", "yt"), ("scsearch", "sc")):
            for cand_title, cand_dur in search_source(prefix, query):
                # Длительность — главный признак: ±7 сек это та же запись, а не кавер.
                dur_ok = cand_dur and abs(cand_dur - t["dur"]) <= 7
                words_ok = len(want & norm(cand_title)) >= max(1, len(want) // 3)
                if dur_ok and words_ok:
                    found = (tag, cand_title, cand_dur)
                    break
            if found:
                break

        if found:
            stats[found[0]] += 1
            mark = "✅" if found[0] == "yt" else "🟡"
            print(f"{i:2}. {mark} [{found[0]}] {query[:52]:52} pop={t['pop']:3}")
        else:
            stats["нет"] += 1
            misses.append(t)
            print(f"{i:2}. ❌ НЕ НАЙДЕН    {query[:52]:52} pop={t['pop']:3}")

    total = len(sample)
    print(f"\n{'='*70}")
    print(f"YouTube:          {stats['yt']:3} из {total} ({stats['yt']*100//total}%)")
    print(f"SoundCloud (доп): {stats['sc']:3} из {total} ({stats['sc']*100//total}%)")
    print(f"НЕ НАЙДЕНО:       {stats['нет']:3} из {total} ({stats['нет']*100//total}%)")
    if misses:
        print("\nчто не нашлось:")
        for m in misses:
            print(f"  - {m['artist']} — {m['title']} (популярность {m['pop']}, {m['kind']})")


if __name__ == "__main__":
    sys.exit(main())
