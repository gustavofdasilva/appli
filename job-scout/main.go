package main

import (
	"log/slog"
	"os"

	"job-scout/internal/analyzer"
	"job-scout/internal/config"
	"job-scout/internal/crawler"
	"job-scout/internal/models"
	"job-scout/internal/resume"
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
	var newJobs []models.Job
	for _, j := range jobs {
		bySource[j.Source]++
		if j.URL == "" || db.JobExistsByURL(j.URL) {
			continue
		}
		if err := db.InsertJob(j); err != nil {
			slog.Warn("falha ao salvar vaga", "url", j.URL, "error", err)
			continue
		}
		newJobs = append(newJobs, j)
	}

	slog.Info("crawlers concluídos", "total_vagas", len(jobs), "novas_vagas", len(newJobs), "por_fonte", bySource)

	if cfg.AnthropicAPIKey == "" {
		slog.Warn("anthropic_api_key não configurada — pulando análise de vagas")
		return
	}

	az := analyzer.NewAnalyzer(cfg.AnthropicAPIKey)

	var analisadas, comFitAlto int
	var analyses []models.Analysis
	for _, j := range newJobs {
		analysis, err := az.AnalyzeJob("profile.md", j)
		if err != nil {
			slog.Warn("falha ao analisar vaga", "title", j.Title, "error", err)
			continue
		}
		id, err := db.InsertAnalysis(analysis)
		if err != nil {
			slog.Warn("falha ao salvar análise", "title", j.Title, "error", err)
			continue
		}
		analysis.ID = id
		analyses = append(analyses, analysis)

		analisadas++
		if analysis.FitScore >= cfg.MinFitScore {
			comFitAlto++
		}
	}

	slog.Info("análise concluída",
		"vagas_analisadas", analisadas,
		"vagas_com_fit_alto", comFitAlto,
		"min_fit_score", cfg.MinFitScore,
	)

	profileBytes, err := os.ReadFile("profile.md")
	if err != nil {
		slog.Warn("falha ao ler perfil para geração de currículos, pulando etapa", "error", err)
		return
	}

	gen := resume.NewGenerator(cfg.AnthropicAPIKey, resumeOutputDir)
	results := gen.GenerateForHighScoreJobs(string(profileBytes), newJobs, analyses, cfg.MinFitScore)

	var gerados int
	for _, r := range results {
		if r.Err != nil {
			slog.Warn("falha ao gerar currículo", "title", r.Job.Title, "error", r.Err)
			continue
		}
		if err := db.UpdateResumePath(r.AnalysisID, r.Path); err != nil {
			slog.Warn("falha ao salvar caminho do currículo", "title", r.Job.Title, "error", err)
			continue
		}
		gerados++
		slog.Info("currículo gerado", "title", r.Job.Title, "path", r.Path)
	}

	slog.Info("geração de currículos concluída", "gerados", gerados, "elegiveis", len(results))
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
