package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"job-scout/internal/analyzer"
	"job-scout/internal/config"
	"job-scout/internal/crawler"
	"job-scout/internal/resume"
	"job-scout/internal/scheduler"
	"job-scout/internal/server"
	"job-scout/internal/storage"
)

const resumeOutputDir = "data/resumes"

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
	defer func() {
		if err := db.Close(); err != nil {
			slog.Error("erro ao fechar banco de dados", "error", err)
		}
	}()

	slog.Info("job-scout inicializado com sucesso",
		"server_port", cfg.ServerPort,
		"min_fit_score", cfg.MinFitScore,
		"crawlers", len(cfg.Crawlers),
	)

	entries := buildCrawlerEntries(cfg.Crawlers)
	orchestrator := crawler.NewOrchestrator(entries)
	az := analyzer.NewAnalyzer(cfg.AnthropicAPIKey)
	resumeGen := resume.NewGenerator(cfg.AnthropicAPIKey, resumeOutputDir)

	sched := scheduler.NewScheduler(cfg, db, orchestrator, az, resumeGen)
	if err := sched.Run(); err != nil {
		slog.Error("falha ao iniciar scheduler", "error", err)
		os.Exit(1)
	}
	if next, err := sched.NextRun(); err != nil {
		slog.Warn("não foi possível calcular o próximo horário agendado", "error", err)
	} else {
		slog.Info("scheduler iniciado", "schedule", cfg.Schedule, "proxima_execucao", next)
	}

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.ServerPort),
		Handler: server.New(db, sched).Handler(),
	}

	go func() {
		slog.Info("servidor http iniciado", "port", cfg.ServerPort)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("erro no servidor http", "error", err)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh
	slog.Info("sinal de encerramento recebido, iniciando graceful shutdown")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("erro ao encerrar servidor http", "error", err)
	}

	<-sched.Stop().Done()

	slog.Info("aguardando eventuais ciclos do pipeline em execução terminarem")
	sched.Wait()

	slog.Info("job-scout encerrado")
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
