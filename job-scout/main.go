package main

import (
	"log/slog"
	"os"

	"job-scout/internal/config"
	"job-scout/internal/crawler"
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

	entries := buildCrawlerEntries(cfg.Crawlers)
	orchestrator := crawler.NewOrchestrator(entries)

	jobs, err := orchestrator.Run()
	if err != nil {
		slog.Error("falha ao executar crawlers", "error", err)
		os.Exit(1)
	}

	bySource := make(map[string]int)
	for _, j := range jobs {
		bySource[j.Source]++
		if err := db.InsertJob(j); err != nil {
			slog.Warn("falha ao salvar vaga", "url", j.URL, "error", err)
		}
	}

	slog.Info("crawlers concluídos", "total_vagas", len(jobs), "por_fonte", bySource)
}

// buildCrawlerEntries instancia os crawlers correspondentes às entradas
// habilitadas em config.yaml.
func buildCrawlerEntries(crawlers []config.Crawler) []crawler.Entry {
	factories := map[string]func() crawler.Crawler{
		"gupy":         func() crawler.Crawler { return crawler.NewGupyCrawler() },
		"indeed":       func() crawler.Crawler { return crawler.NewIndeedCrawler() },
		"remoteok":     func() crawler.Crawler { return crawler.NewRemoteOKCrawler() },
		"programathor": func() crawler.Crawler { return crawler.NewProgramaThorCrawler() },
		"trampos":      func() crawler.Crawler { return crawler.NewTramposCrawler() },
		"linkedin":     func() crawler.Crawler { return crawler.NewLinkedInCrawler() },
	}

	var entries []crawler.Entry
	for _, c := range crawlers {
		if !c.Enabled {
			continue
		}
		factory, ok := factories[c.Name]
		if !ok {
			slog.Warn("crawler desconhecido na configuração, ignorando", "name", c.Name)
			continue
		}
		entries = append(entries, crawler.Entry{
			Crawler:     factory(),
			SearchTerms: c.SearchTerms,
			MaxPages:    c.MaxPages,
		})
	}
	return entries
}
