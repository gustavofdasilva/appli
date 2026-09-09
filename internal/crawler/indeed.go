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

const indeedBaseURL = "https://br.indeed.com"

// IndeedCrawler busca vagas via scraping do Indeed Brasil (br.indeed.com).
//
// Desabilitado em config.yaml desde 2026-08-22: o Indeed passou a bloquear
// com 403 (WAF anti-bot) toda requisição, inclusive a página de busca
// principal — não é algo que os seletores de parsing resolvem. Reativar
// exigiria um browser headless (ex: chromedp) e provavelmente proxies
// residenciais para não cair no bloqueio.
type IndeedCrawler struct{}

// NewIndeedCrawler cria um novo crawler para o Indeed Brasil.
func NewIndeedCrawler() *IndeedCrawler {
	return &IndeedCrawler{}
}

func (c *IndeedCrawler) Name() string {
	return "indeed"
}

func (c *IndeedCrawler) Fetch(term string, maxPages int) ([]models.Job, error) {
	var jobs []models.Job

	for page := 0; page < maxPages; page++ {
		pageURL := fmt.Sprintf("%s/jobs?q=%s&l=Brasil&start=%d", indeedBaseURL, url.QueryEscape(term), page*10)

		body, err := httpGet(pageURL)
		if err != nil {
			return jobs, fmt.Errorf("erro ao buscar página %d de %q no indeed: %w", page, term, err)
		}

		doc, err := html.Parse(strings.NewReader(string(body)))
		if err != nil {
			return jobs, fmt.Errorf("erro ao parsear html da página %d de %q no indeed: %w", page, term, err)
		}

		cards := findAll(doc, func(n *html.Node) bool {
			return isTag(n, "div") && (hasClass(n, "job_seen_beacon") || hasClass(n, "cardOutline"))
		})
		if len(cards) == 0 {
			slog.Debug("indeed: nenhuma vaga encontrada, encerrando paginação", "term", term, "page", page)
			break
		}

		for _, card := range cards {
			job, ok := parseIndeedCard(card)
			if !ok {
				continue
			}

			randomDelay()
			if desc, err := fetchIndeedDescription(job.URL); err != nil {
				slog.Warn("indeed: erro ao buscar descrição da vaga", "url", job.URL, "error", err)
			} else {
				job.Description = desc
			}

			jobs = append(jobs, job)
		}

		if page < maxPages-1 {
			randomDelay()
		}
	}

	return jobs, nil
}

func parseIndeedCard(card *html.Node) (models.Job, bool) {
	titleLink := find(card, func(n *html.Node) bool {
		if !isTag(n, "a") {
			return false
		}
		if hasClass(n, "jcs-JobTitle") {
			return true
		}
		id, _ := attr(n, "id")
		return strings.HasPrefix(id, "job_")
	})
	if titleLink == nil {
		return models.Job{}, false
	}

	href, ok := attr(titleLink, "href")
	if !ok || href == "" {
		return models.Job{}, false
	}
	jobURL := resolveURL(indeedBaseURL, href)

	title := textContent(titleLink)
	if title == "" {
		if span := find(titleLink, func(n *html.Node) bool { return isTag(n, "span") }); span != nil {
			title = textContent(span)
		}
	}
	if title == "" {
		return models.Job{}, false
	}

	var company, location, snippet string
	if n := find(card, func(n *html.Node) bool { return isTag(n, "span") && hasClass(n, "companyName") }); n != nil {
		company = textContent(n)
	}
	if n := find(card, func(n *html.Node) bool { return isTag(n, "div") && hasClass(n, "companyLocation") }); n != nil {
		location = textContent(n)
	}
	if n := find(card, func(n *html.Node) bool { return isTag(n, "div") && hasClass(n, "job-snippet") }); n != nil {
		snippet = textContent(n)
	}

	return models.Job{
		ID:          hashID(jobURL),
		Source:      "indeed",
		Title:       title,
		Company:     company,
		Location:    location,
		URL:         jobURL,
		Description: snippet,
		FoundAt:     time.Now(),
		Status:      "new",
	}, true
}

func fetchIndeedDescription(jobURL string) (string, error) {
	body, err := httpGet(jobURL)
	if err != nil {
		return "", err
	}

	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return "", fmt.Errorf("erro ao parsear html da vaga %q: %w", jobURL, err)
	}

	desc := find(doc, func(n *html.Node) bool {
		id, _ := attr(n, "id")
		return id == "jobDescriptionText"
	})
	if desc == nil {
		return "", nil
	}
	return textContent(desc), nil
}

// resolveURL resolve href (absoluto ou relativo) contra a base informada.
func resolveURL(base, href string) string {
	baseURL, err := url.Parse(base)
	if err != nil {
		return href
	}
	refURL, err := url.Parse(href)
	if err != nil {
		return href
	}
	return baseURL.ResolveReference(refURL).String()
}
