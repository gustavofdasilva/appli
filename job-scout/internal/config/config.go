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
	AnthropicAPIKey string    `yaml:"anthropic_api_key"`
	Schedule        string    `yaml:"schedule"`
	MinFitScore     int       `yaml:"min_fit_score"`
	ServerPort      int       `yaml:"server_port"`
	Crawlers        []Crawler `yaml:"crawlers"`
}

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

	return &cfg, nil
}
