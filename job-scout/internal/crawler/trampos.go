package crawler

import (
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"

	"job-scout/internal/models"
)

const tramposBaseURL = "https://trampos.co"

// TramposCrawler busca vagas via scraping do Trampos.co (trampos.co).
//
// O markup do site pode mudar com o tempo — se o parsing parar de encontrar
// vagas, os seletores abaixo são o primeiro lugar a revisar.
type TramposCrawler struct{}

// NewTramposCrawler cria um novo crawler para o Trampos.co.
func NewTramposCrawler() *TramposCrawler {
	return &TramposCrawler{}
}

func (c *TramposCrawler) Name() string {
	return "trampos"
}

func (c *TramposCrawler) Fetch(term string, maxPages int) ([]models.Job, error) {
	var jobs []models.Job

	for page := 1; page <= maxPages; page++ {
		pageURL := fmt.Sprintf("%s/oportunidades?term=%s&page=%d", tramposBaseURL, url.QueryEscape(term), page)

		body, err := httpGet(pageURL)
		if err != nil {
			return jobs, fmt.Errorf("erro ao buscar página %d de %q no trampos: %w", page, term, err)
		}

		doc, err := html.Parse(strings.NewReader(string(body)))
		if err != nil {
			return jobs, fmt.Errorf("erro ao parsear html da página %d de %q no trampos: %w", page, term, err)
		}

		cards := findAll(doc, func(n *html.Node) bool {
			return isTag(n, "div") && hasClass(n, "vacancy-item")
		})
		if len(cards) == 0 {
			slog.Debug("trampos: nenhuma vaga encontrada, encerrando paginação", "term", term, "page", page)
			break
		}

		for _, card := range cards {
			job, ok := parseTramposCard(card)
			if !ok {
				continue
			}

			randomDelay()
			if desc, err := fetchTramposDescription(job.URL); err != nil {
				slog.Warn("trampos: erro ao buscar descrição da vaga", "url", job.URL, "error", err)
			} else {
				job.Description = desc
			}

			jobs = append(jobs, job)
		}

		if page < maxPages {
			randomDelay()
		}
	}

	return jobs, nil
}

func parseTramposCard(card *html.Node) (models.Job, bool) {
	titleLink := find(card, func(n *html.Node) bool {
		return isTag(n, "a") && hasClass(n, "vacancy-title")
	})
	if titleLink == nil {
		titleLink = find(card, func(n *html.Node) bool { return isTag(n, "a") })
	}
	if titleLink == nil {
		return models.Job{}, false
	}

	href, ok := attr(titleLink, "href")
	if !ok || href == "" {
		return models.Job{}, false
	}
	jobURL := resolveURL(tramposBaseURL, href)

	title := textContent(titleLink)
	if title == "" {
		return models.Job{}, false
	}

	var company, location string
	if n := find(card, func(n *html.Node) bool { return isTag(n, "span") && hasClass(n, "company-name") }); n != nil {
		company = textContent(n)
	}
	if n := find(card, func(n *html.Node) bool { return isTag(n, "span") && hasClass(n, "vacancy-location") }); n != nil {
		location = textContent(n)
	}

	return models.Job{
		ID:       hashID(jobURL),
		Source:   "trampos",
		Title:    title,
		Company:  company,
		Location: location,
		URL:      jobURL,
		FoundAt:  time.Now(),
		Status:   "new",
	}, true
}

func fetchTramposDescription(jobURL string) (string, error) {
	body, err := httpGet(jobURL)
	if err != nil {
		return "", err
	}

	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return "", fmt.Errorf("erro ao parsear html da vaga %q: %w", jobURL, err)
	}

	desc := find(doc, func(n *html.Node) bool {
		return isTag(n, "div") && hasClass(n, "vacancy-description")
	})
	if desc == nil {
		return "", nil
	}
	return textContent(desc), nil
}
