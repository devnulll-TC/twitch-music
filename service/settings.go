package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// SettingsData — сами настройки эфира, которые Азим и модер крутят из панели.
//
// Отделены от замка намеренно: структуру с sync.Mutex внутри нельзя копировать
// (копия мьютекса не защищает оригинал), а снимок настроек мы копируем постоянно.
type SettingsData struct {
	MaxDuration int  `json:"max_duration"`   // секунд; длиннее — отказ и возврат баллов
	RewardCost  int  `json:"reward_cost"`    // баллов за заказ
	RewardOn    bool `json:"reward_enabled"` // видна ли награда зрителям
}

// Settings хранит настройки на диске: .env правится руками при деплое, а это —
// то, что меняют посреди стрима, и оно обязано переживать перезапуск.
type Settings struct {
	path string
	mu   sync.Mutex
	data SettingsData
}

func LoadSettings(path string, def SettingsData) *Settings {
	s := &Settings{path: path, data: def}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &s.data)
	}
	return s
}

func (s *Settings) Snapshot() SettingsData {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data
}

func (s *Settings) Update(fn func(*SettingsData)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.data)

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, b, 0o644)
}
