// Package crawler define a interface comum dos crawlers de vagas e o
// Orchestrator que os executa de forma coordenada.
package crawler

import (
	"log/slog"

	"job-scout/internal/models"
)

// Crawler é implementado por cada fonte de vagas suportada pelo job-scout.
type Crawler interface {
	// Name identifica a fonte (ex: "gupy", "indeed").
	Name() string
	// Fetch busca vagas para o termo informado, percorrendo até maxPages
	// páginas de resultado.
	Fetch(term string, maxPages int) ([]models.Job, error)
}

// Entry representa um crawler habilitado junto com sua configuração de execução.
type Entry struct {
	Crawler     Crawler
	SearchTerms []string
	MaxPages    int
	// Companies é usado só pelo crawler do Lever, que busca vagas de uma
	// lista fixa de empresas em vez de por termo de busca.
	Companies []string
}

// searchTermsIgnorer é implementado por crawlers que buscam vagas de todas
// as fontes de uma vez (feeds RSS, boards de ATS, threads de fórum) e fazem
// a própria filtragem por search_terms internamente. Pra esses, o
// Orchestrator chama Fetch uma única vez (com term vazio) em vez de iterar
// sobre cada search_term configurado.
type searchTermsIgnorer interface {
	IgnoresSearchTerms() bool
}

// Orchestrator coordena a execução de múltiplos crawlers, respeitando um
// delay entre requisições e deduplicando os resultados por URL.
type Orchestrator struct {
	entries []Entry
}

// NewOrchestrator cria um Orchestrator para a lista de crawlers habilitados.
func NewOrchestrator(entries []Entry) *Orchestrator {
	return &Orchestrator{entries: entries}
}

// Run executa todos os crawlers configurados, um termo de busca por vez,
// aplicando um delay aleatório entre cada requisição para evitar sobrecarregar
// os sites de origem. Retorna todas as vagas encontradas, deduplicadas por URL.
func (o *Orchestrator) Run() ([]models.Job, error) {
	seen := make(map[string]struct{})
	var results []models.Job

	addJobs := func(jobs []models.Job) int {
		added := 0
		for _, j := range jobs {
			if j.URL == "" {
				continue
			}
			if _, dup := seen[j.URL]; dup {
				continue
			}
			seen[j.URL] = struct{}{}
			results = append(results, j)
			added++
		}
		return added
	}

	first := true
	for _, entry := range o.entries {
		name := entry.Crawler.Name()
		var foundForCrawler int

		if ignorer, ok := entry.Crawler.(searchTermsIgnorer); ok && ignorer.IgnoresSearchTerms() {
			if !first {
				randomDelay()
			}
			first = false

			slog.Info("crawler: buscando vagas", "source", name)

			jobs, err := entry.Crawler.Fetch("", entry.MaxPages)
			if err != nil {
				slog.Error("crawler: erro ao buscar vagas", "source", name, "error", err)
			}
			foundForCrawler += addJobs(jobs)

			slog.Info("crawler: concluído", "source", name, "vagas_encontradas", foundForCrawler)
			continue
		}

		for _, term := range entry.SearchTerms {
			if !first {
				randomDelay()
			}
			first = false

			slog.Info("crawler: buscando vagas", "source", name, "term", term, "max_pages", entry.MaxPages)

			jobs, err := entry.Crawler.Fetch(term, entry.MaxPages)
			if err != nil {
				slog.Error("crawler: erro ao buscar vagas", "source", name, "term", term, "error", err)
			}
			foundForCrawler += addJobs(jobs)
		}

		slog.Info("crawler: concluído", "source", name, "vagas_encontradas", foundForCrawler)
	}

	return results, nil
}
