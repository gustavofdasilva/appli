package crawler

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"time"

	"job-scout/internal/models"
)

const (
	gupyAPIBaseURL = "https://employability-portal.gupy.io/api/v1/jobs"
	gupyPageLimit  = 10
)

// gupyResponse representa o corpo da resposta da API pública de vagas da Gupy.
type gupyResponse struct {
	Data       []gupyJob      `json:"data"`
	Pagination gupyPagination `json:"pagination"`
}

type gupyPagination struct {
	Total  int `json:"total"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

type gupyJob struct {
	ID             int    `json:"id"`
	Name           string `json:"name"`
	Description    string `json:"description"`
	CareerPageName string `json:"careerPageName"`
	WorkplaceType  string `json:"workplaceType"` // "on-site" | "hybrid" | "remote"
	City           string `json:"city"`
	State          string `json:"state"`
	JobUrl         string `json:"jobUrl"`
	PublishedDate  string `json:"publishedDate"`
}

// GupyCrawler busca vagas na API pública da Gupy (gupy.io). A descrição
// completa já vem na listagem, então não é necessário um request secundário
// por vaga.
type GupyCrawler struct{}

// NewGupyCrawler cria um novo crawler para a Gupy.
func NewGupyCrawler() *GupyCrawler {
	return &GupyCrawler{}
}

func (c *GupyCrawler) Name() string {
	return "gupy"
}

func (c *GupyCrawler) Fetch(term string, maxPages int) ([]models.Job, error) {
	var jobs []models.Job

	for offset := 0; offset < maxPages*gupyPageLimit; offset += gupyPageLimit {
		reqURL := fmt.Sprintf("%s?jobName=%s&offset=%d&limit=%d", gupyAPIBaseURL, url.QueryEscape(term), offset, gupyPageLimit)

		body, err := httpGet(reqURL)
		if err != nil {
			return jobs, fmt.Errorf("erro ao buscar offset %d de %q na gupy: %w", offset, term, err)
		}

		var parsed gupyResponse
		if err := json.Unmarshal(body, &parsed); err != nil {
			return jobs, fmt.Errorf("erro ao parsear resposta da gupy para %q: %w", term, err)
		}

		if len(parsed.Data) == 0 {
			slog.Debug("gupy: nenhuma vaga encontrada, encerrando paginação", "term", term, "offset", offset)
			break
		}

		for _, gj := range parsed.Data {
			if gj.JobUrl == "" {
				continue
			}
			jobs = append(jobs, models.Job{
				ID:          hashID(gj.JobUrl),
				Source:      "gupy",
				Title:       gj.Name,
				Company:     gj.CareerPageName,
				Location:    gupyLocation(gj),
				URL:         gj.JobUrl,
				Description: gj.Description,
				FoundAt:     gupyPublishedDate(gj.PublishedDate),
				Status:      "new",
			})
		}

		nextOffset := offset + gupyPageLimit
		if nextOffset >= parsed.Pagination.Total || nextOffset >= maxPages*gupyPageLimit {
			break
		}

		randomDelay()
	}

	return jobs, nil
}

func gupyLocation(j gupyJob) string {
	loc := j.City
	if j.State != "" {
		if loc != "" {
			loc += " - " + j.State
		} else {
			loc = j.State
		}
	}

	switch j.WorkplaceType {
	case "remote":
		if loc == "" {
			return "Remoto"
		}
		return "Remoto · " + loc
	case "hybrid":
		if loc == "" {
			return "Híbrido"
		}
		return "Híbrido · " + loc
	default:
		return loc
	}
}

func gupyPublishedDate(raw string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Now()
	}
	return t
}
