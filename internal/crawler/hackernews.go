package crawler

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"job-scout/internal/models"
	"job-scout/internal/storage"
)

const (
	// hnAlgoliaSearchURL usa search_by_date (não o endpoint "search" comum):
	// o "search" do Algolia ordena por relevância, não por data, e retorna o
	// thread mais comentado/citado historicamente (ex: um de 2020) em vez do
	// mais recente.
	hnAlgoliaSearchURL   = "https://hn.algolia.com/api/v1/search_by_date?query=Ask+HN:+Who+is+hiring?&tags=story,author_whoishiring&hitsPerPage=1"
	hnItemURLTemplate    = "https://hacker-news.firebaseio.com/v0/item/%d.json"
	hnCommentURLTemplate = "https://news.ycombinator.com/item?id=%d"

	hnMaxComments    = 200
	hnWorkerCount    = 5
	hnCommentDelay   = 100 * time.Millisecond
	hnMinRunInterval = 7 * 24 * time.Hour
)

type hnAlgoliaResponse struct {
	Hits []struct {
		ObjectID string `json:"objectID"`
	} `json:"hits"`
}

// hnItem representa tanto o item do thread "who is hiring" (usado só pelo
// campo Kids) quanto um comentário individual (uma oferta de vaga).
type hnItem struct {
	ID      int64   `json:"id"`
	By      string  `json:"by"`
	Text    string  `json:"text"`
	Time    int64   `json:"time"`
	Type    string  `json:"type"`
	Dead    bool    `json:"dead"`
	Deleted bool    `json:"deleted"`
	Kids    []int64 `json:"kids"`
}

var (
	hnLocationKeywordRe = regexp.MustCompile(`(?i)\b(remote|on-?site|hybrid)\b`)
	hnCityRe            = regexp.MustCompile(`\b[A-Z][a-zA-Z.]+(?:\s[A-Z][a-zA-Z.]+)?,\s*[A-Z]{2,}\b`)
	hnSalaryRe          = regexp.MustCompile(`(?i)(USD\s*)?\$\s?\d{2,3}(,\d{3}|[kK])(\s?[-–]\s?\$?\d{2,3}(,\d{3}|[kK]))?`)
	hnURLRe             = regexp.MustCompile(`https?://[^\s)>\]]+`)
	hnAllCapsRe         = regexp.MustCompile(`^[A-Z][A-Z0-9&.,'-]{1,30}\b`)
)

// HackerNewsCrawler busca vagas no thread mensal "Ask HN: Who is hiring?"
// via API pública do Algolia (pra achar o thread atual) e API oficial do HN
// (pra ler os comentários). O thread só é republicado uma vez por mês, então
// o crawler consulta o banco pra evitar rodar mais de uma vez por semana.
type HackerNewsCrawler struct {
	db          *storage.DB
	searchTerms []string
}

// NewHackerNewsCrawler cria um novo crawler pro thread "who is hiring" do
// Hacker News, filtrando os comentários pelos termos informados. db é usado
// só pra checar a última vez que esse crawler rodou (ver Fetch).
func NewHackerNewsCrawler(db *storage.DB, searchTerms []string) *HackerNewsCrawler {
	return &HackerNewsCrawler{db: db, searchTerms: searchTerms}
}

func (c *HackerNewsCrawler) Name() string {
	return "hackernews"
}

// IgnoresSearchTerms indica ao Orchestrator que os search_terms já são
// aplicados internamente (ver Fetch) e que ele deve chamar Fetch uma única
// vez — não há paginação nem busca por termo na API do HN.
func (c *HackerNewsCrawler) IgnoresSearchTerms() bool {
	return true
}

// Fetch localiza o thread "who is hiring" mais recente e extrai vagas dos
// seus comentários top-level. term e maxPages são ignorados. Se o crawler já
// rodou nos últimos 7 dias (checado via o found_at mais recente salvo pra
// essa fonte), pula a execução — o thread é mensal, não vale a pena bater
// nele todo dia.
func (c *HackerNewsCrawler) Fetch(_ string, _ int) ([]models.Job, error) {
	if c.db != nil {
		last, ok, err := c.db.LatestJobFoundAt(c.Name())
		if err != nil {
			slog.Warn("hackernews: erro ao checar última execução, seguindo mesmo assim", "error", err)
		} else if ok && time.Since(last) < hnMinRunInterval {
			slog.Info("hackernews: já rodou nos últimos 7 dias, pulando", "ultima_execucao", last)
			return nil, nil
		}
	}

	threadID, err := c.findCurrentThread()
	if err != nil {
		return nil, fmt.Errorf("erro ao localizar thread atual no hackernews: %w", err)
	}

	kids, err := c.fetchKids(threadID)
	if err != nil {
		return nil, fmt.Errorf("erro ao buscar comentários do thread %d no hackernews: %w", threadID, err)
	}
	if len(kids) > hnMaxComments {
		kids = kids[:hnMaxComments]
	}

	jobs := c.fetchComments(kids)
	slog.Info("hackernews: thread processado", "thread_id", threadID, "comentarios", len(kids), "vagas_encontradas", len(jobs))

	return jobs, nil
}

func (c *HackerNewsCrawler) findCurrentThread() (int64, error) {
	body, err := httpGet(hnAlgoliaSearchURL)
	if err != nil {
		return 0, err
	}

	var parsed hnAlgoliaResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return 0, fmt.Errorf("erro ao parsear resposta do algolia: %w", err)
	}
	if len(parsed.Hits) == 0 {
		return 0, fmt.Errorf("nenhum thread \"who is hiring\" encontrado")
	}

	var id int64
	if _, err := fmt.Sscanf(parsed.Hits[0].ObjectID, "%d", &id); err != nil {
		return 0, fmt.Errorf("id de thread inválido %q: %w", parsed.Hits[0].ObjectID, err)
	}
	return id, nil
}

func (c *HackerNewsCrawler) fetchKids(threadID int64) ([]int64, error) {
	item, err := fetchHNItem(threadID)
	if err != nil {
		return nil, err
	}
	return item.Kids, nil
}

// fetchComments busca cada comentário concorrentemente, limitado a
// hnWorkerCount goroutines em paralelo via um semáforo (buffered channel),
// com um delay de hnCommentDelay antes de cada request individual.
func (c *HackerNewsCrawler) fetchComments(ids []int64) []models.Job {
	sem := make(chan struct{}, hnWorkerCount)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var jobs []models.Job

	for _, id := range ids {
		wg.Add(1)
		sem <- struct{}{}
		go func(id int64) {
			defer wg.Done()
			defer func() { <-sem }()

			time.Sleep(hnCommentDelay)

			job, ok := c.fetchComment(id)
			if !ok {
				return
			}
			mu.Lock()
			jobs = append(jobs, job)
			mu.Unlock()
		}(id)
	}

	wg.Wait()
	return jobs
}

func (c *HackerNewsCrawler) fetchComment(id int64) (models.Job, bool) {
	item, err := fetchHNItem(id)
	if err != nil {
		slog.Warn("hackernews: erro ao buscar comentário", "id", id, "error", err)
		return models.Job{}, false
	}
	if item.Type != "comment" || item.Dead || item.Deleted || item.Text == "" {
		return models.Job{}, false
	}
	if !matchesAnyTerm(item.Text, c.searchTerms) {
		return models.Job{}, false
	}

	return parseHNComment(item), true
}

func fetchHNItem(id int64) (hnItem, error) {
	body, err := httpGet(fmt.Sprintf(hnItemURLTemplate, id))
	if err != nil {
		return hnItem{}, err
	}

	var item hnItem
	if err := json.Unmarshal(body, &item); err != nil {
		return hnItem{}, fmt.Errorf("erro ao parsear item %d: %w", id, err)
	}
	return item, nil
}

// parseHNComment extrai company/title/location/salary/url do texto
// (semi-estruturado, em HTML) de um comentário do thread "who is hiring".
// É um parsing best-effort — os comentários não seguem um formato fixo.
func parseHNComment(item hnItem) models.Job {
	commentURL := fmt.Sprintf(hnCommentURLTemplate, item.ID)
	text := stripHTML(item.Text)

	company, title := extractHNCompanyAndTitle(text)
	location := extractHNLocation(text)
	salary := hnSalaryRe.FindString(text)
	jobURL := hnURLRe.FindString(text)
	if jobURL == "" {
		jobURL = commentURL
	}

	return models.Job{
		ID:          hashID(commentURL),
		Source:      "hackernews",
		Title:       title,
		Company:     company,
		Location:    location,
		URL:         jobURL,
		Description: text,
		Salary:      salary,
		FoundAt:     time.Unix(item.Time, 0),
		Status:      "new",
	}
}

func extractHNCompanyAndTitle(text string) (company, title string) {
	firstLine := firstLineOf(text)

	parts := strings.SplitN(firstLine, "|", 3)
	if len(parts) >= 2 {
		company = strings.TrimSpace(parts[0])
		title = strings.TrimSpace(parts[1])
	} else if m := hnAllCapsRe.FindString(firstLine); m != "" {
		company = strings.TrimSpace(m)
	}

	if company == "" {
		company = "Unknown"
	}
	if title == "" {
		title = truncate(text, 80)
	}
	return company, title
}

func extractHNLocation(text string) string {
	var parts []string
	if m := hnLocationKeywordRe.FindString(text); m != "" {
		parts = append(parts, m)
	}
	if m := hnCityRe.FindString(text); m != "" {
		parts = append(parts, m)
	}
	return strings.Join(parts, " · ")
}

// firstLineOf retorna o texto até a primeira quebra de linha (stripHTML já
// normaliza \n internos, então usamos o primeiro "." ou os primeiros ~200
// chars como aproximação de "primeira linha" de um texto já achatado).
func firstLineOf(text string) string {
	if len(text) > 200 {
		return text[:200]
	}
	return text
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return strings.TrimSpace(s[:n])
}
