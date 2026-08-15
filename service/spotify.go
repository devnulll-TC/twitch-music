package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Spotify используется ТОЛЬКО как каталог: узнать, что за трек имел в виду зритель.
// Играть их звук невозможно — треки закрыты собственным DRM (PlayPlay), и ключи
// сторонним клиентам Spotify не выдаёт (проверено 10.08.2026 на Premium-аккаунте,
// с двух стран). Поэтому по названию из Spotify мы находим ту же запись на YouTube.
type Spotify struct {
	clientID     string
	clientSecret string

	mu    sync.Mutex
	token string
	exp   time.Time
}

func NewSpotify(id, secret string) *Spotify {
	if id == "" || secret == "" {
		return nil
	}
	return &Spotify{clientID: id, clientSecret: secret}
}

func (s *Spotify) accessToken(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token != "" && time.Now().Before(s.exp.Add(-time.Minute)) {
		return s.token, nil
	}

	form := url.Values{"grant_type": {"client_credentials"}}
	req, err := http.NewRequestWithContext(ctx, "POST", "https://accounts.spotify.com/api/token",
		strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString(
		[]byte(s.clientID+":"+s.clientSecret)))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var body struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return "", err
	}
	if body.AccessToken == "" {
		return "", fmt.Errorf("spotify не выдал токен")
	}
	s.token = body.AccessToken
	s.exp = time.Now().Add(time.Duration(body.ExpiresIn) * time.Second)
	return s.token, nil
}

// spotifyLink разбирает и ссылку, и URI: open.spotify.com/track/<id>, spotify:track:<id>,
// в том числе со страновым префиксом (/intl-ru/track/...) и параметрами после «?».
var spotifyLink = regexp.MustCompile(`(?:open\.spotify\.com/(?:intl-[a-z]{2}/)?|spotify:)(track|album|playlist|artist)[:/]([A-Za-z0-9]+)`)

type spotifyRef struct{ kind, id string }

func parseSpotify(q string) (spotifyRef, bool) {
	m := spotifyLink.FindStringSubmatch(q)
	if m == nil {
		return spotifyRef{}, false
	}
	return spotifyRef{kind: m[1], id: m[2]}, true
}

// TrackInfo — название, исполнитель и длительность трека из каталога Spotify.
func (s *Spotify) TrackInfo(ctx context.Context, id string) (title, artist string, dur int, err error) {
	tok, err := s.accessToken(ctx)
	if err != nil {
		return "", "", 0, err
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://api.spotify.com/v1/tracks/"+id, nil)
	req.Header.Set("Authorization", "Bearer "+tok)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", "", 0, fmt.Errorf("spotify не знает такой трек")
	}

	var body struct {
		Name       string `json:"name"`
		DurationMs int    `json:"duration_ms"`
		Artists    []struct {
			Name string `json:"name"`
		} `json:"artists"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return "", "", 0, err
	}
	if len(body.Artists) > 0 {
		artist = body.Artists[0].Name
	}
	return body.Name, artist, body.DurationMs / 1000, nil
}
