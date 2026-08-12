package main

import (
	"log/slog"
	"os"

	"job-scout/internal/config"
	"job-scout/internal/storage"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	cfg, err := config.Load("config.yaml")
	if err != nil {
		slog.Error("falha ao carregar configuração", "error", err)
		os.Exit(1)
	}

	db, err := storage.Open("data/job-scout.db")
	if err != nil {
		slog.Error("falha ao abrir banco de dados", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	slog.Info("job-scout inicializado com sucesso",
		"server_port", cfg.ServerPort,
		"min_fit_score", cfg.MinFitScore,
		"crawlers", len(cfg.Crawlers),
	)
}
