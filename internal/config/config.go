package config

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

type Config struct {
	Domain      string        `json:"domain"`
	SessionTTL  Duration      `json:"session_ttl"`
	SessionIdle Duration      `json:"session_idle"`
	CertCache   string        `json:"cert_cache"`
	Gemini      GeminiConfig  `json:"gemini"`
	TTS         TTSConfig     `json:"tts"`
	Tokens      []Token       `json:"tokens"`
}

type GeminiConfig struct {
	Model string `json:"model"`
}

type TTSConfig struct {
	Voice        string  `json:"voice"`
	Language     string  `json:"language"`
	SpeakingRate float64 `json:"speaking_rate"`
}

type Token struct {
	Token string `json:"token"`
	Label string `json:"label"`
}

// Duration wraps time.Duration for JSON unmarshaling from strings like "2h".
type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	dur, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	d.Duration = dur
	return nil
}

func Load(path string) (*Config, error) {
	cfg := &Config{
		SessionTTL:  Duration{2 * time.Hour},
		SessionIdle: Duration{30 * time.Minute},
		CertCache:   "/var/cache/visionary/certs",
	}
	cfg.Gemini.Model = "gemini-2.5-pro"
	cfg.TTS.Voice = "en-US-Wavenet-F"
	cfg.TTS.Language = "en-US"
	cfg.TTS.SpeakingRate = 2.0

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, fmt.Errorf("reading config: %w", err)
	}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	return cfg, nil
}
