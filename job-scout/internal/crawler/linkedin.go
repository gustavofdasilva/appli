package crawler

import (
	"errors"

	"job-scout/internal/models"
)

// LinkedInCrawler é um placeholder — o LinkedIn exige autenticação e possui
// proteções anti-scraping agressivas, então não é suportado em v1.
type LinkedInCrawler struct{}

// NewLinkedInCrawler cria um novo crawler (não implementado) para o LinkedIn.
func NewLinkedInCrawler() *LinkedInCrawler {
	return &LinkedInCrawler{}
}

func (c *LinkedInCrawler) Name() string {
	return "linkedin"
}

func (c *LinkedInCrawler) Fetch(term string, maxPages int) ([]models.Job, error) {
	return nil, errors.New("linkedin crawler not implemented in v1 — enable via config when ready")
}
