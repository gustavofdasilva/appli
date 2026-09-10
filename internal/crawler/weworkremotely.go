package crawler

import (
	"encoding/xml"
	"log/slog"
	"strings"
	"time"

	"job-scout/internal/models"
)

// wwrUserAgent é enviado em vez do userAgent padrão (de navegador) porque
// os feeds RSS da WeWorkRemotely são públicos e oficiais — não há
// necessidade de se passar por um browser.
const wwrUserAgent = "Mozilla/5.0 (compatible; job-scout/1.0)"

// wwrFeeds são os feeds RSS de categorias de programação consumidos a cada
// execução do crawler. A WeWorkRemotely não oferece busca por termo via
// RSS, então o filtro por search_terms é feito no cliente (ver Fetch).
var wwrFeeds = []string{
	"https://weworkremotely.com/categories/remote-back-end-programming-jobs.rss",
	"https://weworkremotely.com/categories/remote-full-stack-programming-jobs.rss",
}

type wwrRSS struct {
	Channel struct {
		Items []wwrItem `xml:"item"`
	} `xml:"channel"`
}

type wwrItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	PubDate     string `xml:"pubDate"`
	Region      string `xml:"region"`
	Description string `xml:"description"`
}

// WeWorkRemotelyCrawler busca vagas nos feeds RSS públicos da
// WeWorkRemotely (weworkremotely.com) — sem autenticação, sem anti-bot e
// sem paginação (os feeds trazem todas as vagas da categoria de uma vez).
type WeWorkRemotelyCrawler struct {
	searchTerms []string
}

// NewWeWorkRemotelyCrawler cria um novo crawler para a WeWorkRemotely,
// filtrando as vagas dos feeds pelos termos informados.
func NewWeWorkRemotelyCrawler(searchTerms []string) *WeWorkRemotelyCrawler {
	return &WeWorkRemotelyCrawler{searchTerms: searchTerms}
}

func (c *WeWorkRemotelyCrawler) Name() string {
	return "weworkremotely"
}

// IgnoresSearchTerms indica ao Orchestrator que os search_terms já são
// aplicados internamente (ver Fetch) e que ele deve chamar Fetch uma única
// vez, em vez de um request por termo.
func (c *WeWorkRemotelyCrawler) IgnoresSearchTerms() bool {
	return true
}

// Fetch busca e filtra as vagas dos feeds RSS configurados. term e maxPages
// são ignorados — os termos de busca usados no filtro vêm de searchTerms
// (informado em NewWeWorkRemotelyCrawler) e não há paginação nos feeds.
func (c *WeWorkRemotelyCrawler) Fetch(_ string, _ int) ([]models.Job, error) {
	var jobs []models.Job

	for i, feedURL := range wwrFeeds {
		if i > 0 {
			time.Sleep(2 * time.Second)
		}

		body, err := httpGetWithUA(feedURL, wwrUserAgent)
		if err != nil {
			slog.Error("weworkremotely: erro ao buscar feed", "feed", feedURL, "error", err)
			continue
		}

		var rss wwrRSS
		if err := xml.Unmarshal(body, &rss); err != nil {
			slog.Error("weworkremotely: erro ao parsear feed", "feed", feedURL, "error", err)
			continue
		}

		found := 0
		for _, item := range rss.Channel.Items {
			job, ok := parseWWRItem(item)
			if !ok {
				continue
			}
			if !matchesAnyTerm(job.Title+" "+job.Description, c.searchTerms) {
				continue
			}
			jobs = append(jobs, job)
			found++
		}

		slog.Info("weworkremotely: feed processado", "feed", feedURL, "vagas_encontradas", found)
	}

	return jobs, nil
}

func parseWWRItem(item wwrItem) (models.Job, bool) {
	if item.Link == "" {
		return models.Job{}, false
	}

	company, title := splitWWRTitle(item.Title)
	if title == "" {
		return models.Job{}, false
	}

	return models.Job{
		ID:          hashID(item.Link),
		Source:      "weworkremotely",
		Title:       title,
		Company:     company,
		Location:    strings.TrimSpace(item.Region),
		URL:         item.Link,
		Description: stripHTML(item.Description),
		FoundAt:     parseWWRPubDate(item.PubDate),
		Status:      "new",
	}, true
}

// splitWWRTitle separa o campo <title> do feed ("Empresa: Cargo") em
// company e title. Se não houver ":", o título inteiro vira title e company
// fica vazio.
func splitWWRTitle(raw string) (company, title string) {
	idx := strings.Index(raw, ":")
	if idx < 0 {
		return "", strings.TrimSpace(raw)
	}
	return strings.TrimSpace(raw[:idx]), strings.TrimSpace(raw[idx+1:])
}

func parseWWRPubDate(raw string) time.Time {
	t, err := time.Parse(time.RFC1123Z, strings.TrimSpace(raw))
	if err != nil {
		return time.Now()
	}
	return t
}

// matchesAnyTerm verifica se text contém algum dos termos informados
// (case-insensitive). Uma lista de termos vazia é tratada como "sem
// filtro" — todas as vagas passam.
func matchesAnyTerm(text string, terms []string) bool {
	if len(terms) == 0 {
		return true
	}
	lower := strings.ToLower(text)
	for _, term := range terms {
		if term == "" {
			continue
		}
		if strings.Contains(lower, strings.ToLower(term)) {
			return true
		}
	}
	return false
}
