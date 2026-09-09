// Package resume gera currículos personalizados em Markdown/PDF via LLM,
// priorizando as experiências mais relevantes para uma vaga específica.
package resume

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"job-scout/internal/models"
)

const (
	anthropicAPIURL  = "https://api.anthropic.com/v1/messages"
	anthropicVersion = "2023-06-01"
	anthropicModel   = "claude-haiku-4-5"
	maxTokens        = 4096
)

const systemPrompt = `Você é um especialista em criar currículos ATS-friendly.
Retorne APENAS o currículo em Markdown puro, sem explicações,
sem blocos de código, sem backticks.
Mantenha todas as informações verdadeiras do perfil — apenas reorganize
e enfatize o que é mais relevante pra essa vaga específica.`

const userPromptTemplate = `## Meu Perfil Base
%s

## Vaga Alvo
Título: %s
Empresa: %s
Descrição completa: %s

Gere o currículo personalizado em Markdown priorizando as experiências
e habilidades mais relevantes. Use palavras-chave da vaga naturalmente.`

var httpClient = &http.Client{Timeout: 120 * time.Second}

// Generator usa a API da Anthropic para gerar currículos personalizados em
// Markdown e os converte para PDF via pandoc.
type Generator struct {
	apiKey    string
	outputDir string
}

// NewGenerator cria um novo Generator que salva os currículos em outputDir.
func NewGenerator(apiKey, outputDir string) *Generator {
	return &Generator{apiKey: apiKey, outputDir: outputDir}
}

// Result é o resultado da geração de currículo para uma vaga.
type Result struct {
	Job        models.Job
	AnalysisID string
	Path       string
	Err        error
}

// Generate gera um currículo personalizado em Markdown para a vaga informada,
// salva-o em outputDir/{job_id}.md e tenta convertê-lo para PDF via pandoc.
// Retorna o caminho do PDF gerado ou, se a conversão falhar, o caminho do
// Markdown como fallback.
func (g *Generator) Generate(profile string, job models.Job, analysis models.Analysis) (string, error) {
	md, err := g.generateMarkdown(profile, job)
	if err != nil {
		return "", fmt.Errorf("erro ao gerar markdown do currículo para vaga %q: %w", job.Title, err)
	}

	if err := os.MkdirAll(g.outputDir, 0o755); err != nil {
		return "", fmt.Errorf("erro ao criar diretório de currículos %q: %w", g.outputDir, err)
	}

	mdFilename := job.ID + ".md"
	pdfFilename := job.ID + ".pdf"
	mdPath := filepath.Join(g.outputDir, mdFilename)

	if err := os.WriteFile(mdPath, []byte(md), 0o644); err != nil {
		return "", fmt.Errorf("erro ao salvar currículo em markdown %q: %w", mdPath, err)
	}

	cmd := exec.Command("pandoc", mdFilename, "-o", pdfFilename, "--pdf-engine=weasyprint")
	cmd.Dir = g.outputDir
	if out, err := cmd.CombinedOutput(); err != nil {
		slog.Warn("falha ao converter currículo para pdf via pandoc, mantendo apenas markdown",
			"job_id", job.ID, "error", err, "output", string(out))
		return mdPath, nil
	}

	return filepath.Join(g.outputDir, pdfFilename), nil
}

// GenerateForHighScoreJobs filtra as análises com fit_score >= minFitScore e
// gera um currículo para cada vaga correspondente.
func (g *Generator) GenerateForHighScoreJobs(profile string, jobs []models.Job, analyses []models.Analysis, minFitScore int) []Result {
	jobByID := make(map[string]models.Job, len(jobs))
	for _, j := range jobs {
		jobByID[j.ID] = j
	}

	var results []Result
	for _, a := range analyses {
		if a.FitScore < minFitScore {
			continue
		}
		job, ok := jobByID[a.JobID]
		if !ok {
			continue
		}

		path, err := g.Generate(profile, job, a)
		results = append(results, Result{Job: job, AnalysisID: a.ID, Path: path, Err: err})
	}

	return results
}

func (g *Generator) generateMarkdown(profile string, job models.Job) (string, error) {
	prompt := fmt.Sprintf(userPromptTemplate, profile, job.Title, job.Company, job.Description)

	reqBody := messagesRequest{
		Model:     anthropicModel,
		MaxTokens: maxTokens,
		System:    systemPrompt,
		Messages: []messageRequest{
			{Role: "user", Content: prompt},
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("erro ao serializar requisição: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, anthropicAPIURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("erro ao criar requisição: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", g.apiKey)
	req.Header.Set("anthropic-version", anthropicVersion)

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("erro ao chamar api da anthropic: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("erro ao ler resposta da anthropic: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var apiErr anthropicErrorResponse
		_ = json.Unmarshal(respBody, &apiErr)
		msg := apiErr.Error.Message
		if msg == "" {
			msg = string(respBody)
		}
		return "", fmt.Errorf("status inesperado %d da anthropic: %s", resp.StatusCode, msg)
	}

	var parsed messagesResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", fmt.Errorf("erro ao parsear resposta da anthropic: %w", err)
	}
	if len(parsed.Content) == 0 {
		return "", fmt.Errorf("resposta da anthropic sem conteúdo")
	}

	return cleanMarkdown(parsed.Content[0].Text), nil
}

// cleanMarkdown remove eventuais blocos de código markdown que o modelo
// tenha adicionado por engano ao redor do currículo.
func cleanMarkdown(text string) string {
	cleaned := strings.TrimSpace(text)
	cleaned = strings.TrimPrefix(cleaned, "```markdown")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	return strings.TrimSpace(cleaned)
}

type messagesRequest struct {
	Model     string           `json:"model"`
	MaxTokens int              `json:"max_tokens"`
	System    string           `json:"system"`
	Messages  []messageRequest `json:"messages"`
}

type messageRequest struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type messagesResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

type anthropicErrorResponse struct {
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}
