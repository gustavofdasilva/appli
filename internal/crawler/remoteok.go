package crawler

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"job-scout/internal/models"
)

const remoteOKAPIURL = "https://remoteok.com/api"

// remoteOKJob representa uma vaga individual retornada pela API da RemoteOK.
// A API mistura tipos (o primeiro elemento do array é um objeto de metadata,
// não uma vaga), então os campos aqui são decodificados de forma tolerante.
type remoteOKJob struct {
	Slug        string   `json:"slug"`
	Position    string   `json:"position"`
	Company     string   `json:"company"`
	Description string   `json:"description"`
	URL         string   `json:"url"`
	Salary      string   `json:"salary"`
	Location    string   `json:"location"`
	Tags        []string `json:"tags"`
}

// RemoteOKCrawler busca vagas na API pública da RemoteOK (remoteok.com).
type RemoteOKCrawler struct{}

// NewRemoteOKCrawler cria um novo crawler para a RemoteOK.
func NewRemoteOKCrawler() *RemoteOKCrawler {
	return &RemoteOKCrawler{}
}

func (c *RemoteOKCrawler) Name() string {
	return "remoteok"
}

// Fetch busca vagas na RemoteOK filtrando pelo termo informado. A API retorna
// todas as vagas de uma vez (sem paginação), então maxPages é ignorado.
func (c *RemoteOKCrawler) Fetch(term string, maxPages int) ([]models.Job, error) {
	body, err := httpGet(remoteOKAPIURL)
	if err != nil {
		return nil, fmt.Errorf("erro ao buscar vagas na remoteok: %w", err)
	}

	var raw []json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("erro ao parsear resposta da remoteok: %w", err)
	}

	termLower := strings.ToLower(term)

	var jobs []models.Job
	for i, r := range raw {
		if i == 0 {
			// Primeiro elemento é metadata (legal notice), não uma vaga.
			continue
		}

		var rj remoteOKJob
		if err := json.Unmarshal(r, &rj); err != nil {
			continue
		}
		if rj.URL == "" {
			continue
		}
		if !remoteOKMatches(rj, termLower) {
			continue
		}

		jobs = append(jobs, models.Job{
			ID:          hashID(rj.URL),
			Source:      "remoteok",
			Title:       rj.Position,
			Company:     rj.Company,
			Location:    rj.Location,
			URL:         rj.URL,
			Description: rj.Description,
			Salary:      rj.Salary,
			FoundAt:     time.Now(),
			Status:      "new",
		})
	}

	return jobs, nil
}

func remoteOKMatches(j remoteOKJob, termLower string) bool {
	if strings.Contains(strings.ToLower(j.Position), termLower) {
		return true
	}
	for _, tag := range j.Tags {
		if strings.Contains(strings.ToLower(tag), termLower) {
			return true
		}
	}
	return false
}
