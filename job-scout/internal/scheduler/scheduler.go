// Package scheduler agenda e executa o pipeline completo do job-scout
// (crawl -> análise -> geração de currículo) em um horário configurável.
package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"job-scout/internal/analyzer"
	"job-scout/internal/config"
	"job-scout/internal/crawler"
	"job-scout/internal/models"
	"job-scout/internal/resume"
	"job-scout/internal/storage"
)

// Scheduler agenda a execução periódica do pipeline de acordo com a
// expressão cron configurada.
type Scheduler struct {
	cfg          *config.Config
	db           *storage.DB
	orchestrator *crawler.Orchestrator
	analyzer     *analyzer.Analyzer
	resumeGen    *resume.Generator

	cron *cron.Cron
	wg   sync.WaitGroup

	mu      sync.Mutex
	running bool
	lastRun time.Time
}

// NewScheduler cria um Scheduler com as dependências do pipeline já
// resolvidas.
func NewScheduler(cfg *config.Config, db *storage.DB, orchestrator *crawler.Orchestrator, az *analyzer.Analyzer, resumeGen *resume.Generator) *Scheduler {
	return &Scheduler{
		cfg:          cfg,
		db:           db,
		orchestrator: orchestrator,
		analyzer:     az,
		resumeGen:    resumeGen,
		cron:         cron.New(),
	}
}

// Run registra a expressão cron do config e inicia o agendador em segundo
// plano. Não bloqueia.
func (s *Scheduler) Run() error {
	if _, err := s.cron.AddFunc(s.cfg.Schedule, s.runPipeline); err != nil {
		return fmt.Errorf("erro ao registrar expressão cron %q: %w", s.cfg.Schedule, err)
	}
	s.cron.Start()
	return nil
}

// Stop impede novos disparos agendados pelo cron. Não espera por ciclos em
// execução — use Wait para isso. O context retornado é encerrado quando o
// próprio cron termina de parar.
func (s *Scheduler) Stop() context.Context {
	return s.cron.Stop()
}

// Wait bloqueia até que qualquer ciclo do pipeline em execução (disparado
// pelo cron ou via RunNow) termine. Deve ser chamado durante o graceful
// shutdown, antes de fechar o banco de dados, para garantir que nenhuma
// goroutine de pipeline continue rodando (e escrevendo no banco) depois que
// o processo começa a encerrar.
func (s *Scheduler) Wait() {
	s.wg.Wait()
}

// RunNow dispara o pipeline imediatamente, fora do horário agendado — usado
// pelo disparo manual via API. Se um ciclo já estiver em execução (agendado
// ou manual), o disparo é ignorado.
func (s *Scheduler) RunNow() {
	s.runPipeline()
}

// NextRun calcula o próximo horário em que o pipeline será executado de
// acordo com a expressão cron configurada.
func (s *Scheduler) NextRun() (time.Time, error) {
	schedule, err := cron.ParseStandard(s.cfg.Schedule)
	if err != nil {
		return time.Time{}, fmt.Errorf("erro ao parsear expressão cron %q: %w", s.cfg.Schedule, err)
	}
	return schedule.Next(time.Now()), nil
}

// LastRun retorna o horário de início do último ciclo concluído do pipeline.
// O segundo valor é false se o pipeline ainda não rodou nenhuma vez.
func (s *Scheduler) LastRun() (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastRun, !s.lastRun.IsZero()
}

// runPipeline executa um ciclo completo: crawl de todas as fontes
// habilitadas, dedup e persistência das vagas novas, análise de fit via LLM
// e geração de currículo para as vagas com fit_score acima do mínimo
// configurado.
func (s *Scheduler) runPipeline() {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		slog.Warn("pipeline: ciclo já em execução, ignorando novo disparo")
		return
	}
	s.running = true
	s.mu.Unlock()

	s.wg.Add(1)
	defer func() {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
		s.wg.Done()
	}()

	start := time.Now()
	slog.Info("pipeline: iniciando ciclo")

	jobs, err := s.orchestrator.Run()
	if err != nil {
		slog.Error("pipeline: falha ao executar crawlers", "error", err)
		return
	}

	bySource := make(map[string]int)
	var newJobs []models.Job
	for _, j := range jobs {
		bySource[j.Source]++
		if j.URL == "" || s.db.JobExistsByURL(j.URL) {
			continue
		}
		if err := s.db.InsertJob(j); err != nil {
			slog.Warn("pipeline: falha ao salvar vaga", "url", j.URL, "error", err)
			continue
		}
		newJobs = append(newJobs, j)
	}
	slog.Info("pipeline: crawlers concluídos", "total_vagas", len(jobs), "novas_vagas", len(newJobs), "por_fonte", bySource)

	var analisadas, comFitAlto, curriculosGerados int

	if s.cfg.AnthropicAPIKey == "" {
		slog.Warn("pipeline: anthropic_api_key não configurada — pulando análise e geração de currículos")
	} else {
		var analyses []models.Analysis
		for _, j := range newJobs {
			analysis, err := s.analyzer.AnalyzeJob("profile.md", j)
			if err != nil {
				slog.Warn("pipeline: falha ao analisar vaga", "title", j.Title, "error", err)
				continue
			}
			id, err := s.db.InsertAnalysis(analysis)
			if err != nil {
				slog.Warn("pipeline: falha ao salvar análise", "title", j.Title, "error", err)
				continue
			}
			analysis.ID = id
			analyses = append(analyses, analysis)

			analisadas++
			if analysis.FitScore >= s.cfg.MinFitScore {
				comFitAlto++
			}
		}

		profileBytes, err := os.ReadFile("profile.md")
		if err != nil {
			slog.Warn("pipeline: falha ao ler perfil para geração de currículos, pulando etapa", "error", err)
		} else {
			results := s.resumeGen.GenerateForHighScoreJobs(string(profileBytes), newJobs, analyses, s.cfg.MinFitScore)
			for _, r := range results {
				if r.Err != nil {
					slog.Warn("pipeline: falha ao gerar currículo", "title", r.Job.Title, "error", r.Err)
					continue
				}
				if err := s.db.UpdateResumePath(r.AnalysisID, r.Path); err != nil {
					slog.Warn("pipeline: falha ao salvar caminho do currículo", "title", r.Job.Title, "error", err)
					continue
				}
				curriculosGerados++
				slog.Info("pipeline: currículo gerado", "title", r.Job.Title, "path", r.Path)
			}
		}
	}

	s.mu.Lock()
	s.lastRun = start
	s.mu.Unlock()

	slog.Info("pipeline: ciclo concluído",
		"duracao", time.Since(start),
		"vagas_encontradas", len(jobs),
		"vagas_novas", len(newJobs),
		"vagas_analisadas", analisadas,
		"vagas_com_fit_alto", comFitAlto,
		"curriculos_gerados", curriculosGerados,
	)
}
