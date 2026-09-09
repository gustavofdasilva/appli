package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Crawler representa a configuração de um crawler individual.
type Crawler struct {
	Name        string   `yaml:"name"`
	Enabled     bool     `yaml:"enabled"`
	SearchTerms []string `yaml:"search_terms"`
	MaxPages    int      `yaml:"max_pages"`
}

// Config representa a configuração completa do job-scout.
type Config struct {
	OmniRouteBaseURL string    `yaml:"omniroute_base_url"`
	OmniRouteAPIKey  string    `yaml:"omniroute_api_key"`
	OmniRouteModel   string    `yaml:"omniroute_model"`
	Schedule         string    `yaml:"schedule"`
	MinFitScore      int       `yaml:"min_fit_score"`
	ServerPort       int       `yaml:"server_port"`
	Crawlers         []Crawler `yaml:"crawlers"`

	TelegramBotToken string `yaml:"telegram_bot_token"`
	TelegramChatID   string `yaml:"telegram_chat_id"`
	// TelegramMinScore é o fit_score mínimo (0-100) pra disparar uma
	// notificação no Telegram. Se omitido (zero), usa o mesmo valor de
	// MinFitScore.
	TelegramMinScore int `yaml:"telegram_min_score"`
}

const (
	defaultOmniRouteBaseURL = "http://localhost:20128/v1"
	defaultOmniRouteModel   = "claude-haiku-4-5"
)

// Load lê e faz o parse do arquivo de configuração YAML no caminho informado.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("erro ao ler arquivo de config %q: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("erro ao fazer parse do config %q: %w", path, err)
	}

	if cfg.OmniRouteBaseURL == "" {
		cfg.OmniRouteBaseURL = defaultOmniRouteBaseURL
	}
	if cfg.OmniRouteModel == "" {
		cfg.OmniRouteModel = defaultOmniRouteModel
	}
	if cfg.TelegramMinScore == 0 {
		cfg.TelegramMinScore = cfg.MinFitScore
	}

	return &cfg, nil
}
