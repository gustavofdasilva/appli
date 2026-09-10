// Package resume gera currículos personalizados em Markdown/PDF via um LLM
// (via gateway OmniRoute self-hosted, com API compatível com OpenAI),
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
	maxTokens = 4096
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

// Generator usa um LLM exposto pelo gateway OmniRoute para gerar currículos
// personalizados em Markdown e os converte para PDF via pandoc.
type Generator struct {
	baseURL   string
	apiKey    string
	model     string
	outputDir string
}

// NewGenerator cria um novo Generator apontando para o endpoint
// OpenAI-compatible do OmniRoute (baseURL, ex: "http://omniroute:20128/v1"),
// que salva os currículos em outputDir.
func NewGenerator(baseURL, apiKey, model, outputDir string) *Generator {
	return &Generator{baseURL: baseURL, apiKey: apiKey, model: model, outputDir: outputDir}
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

	reqBody := chatRequest{
		Model:     g.model,
		MaxTokens: maxTokens,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: prompt},
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("erro ao serializar requisição: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, g.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("erro ao criar requisição: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+g.apiKey)

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("erro ao chamar api do omniroute: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("erro ao ler resposta do omniroute: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var apiErr chatErrorResponse
		_ = json.Unmarshal(respBody, &apiErr)
		msg := apiErr.Error.Message
		if msg == "" {
			msg = string(respBody)
		}
		return "", fmt.Errorf("status inesperado %d do omniroute: %s", resp.StatusCode, msg)
	}

	var parsed chatResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", fmt.Errorf("erro ao parsear resposta do omniroute: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("resposta do omniroute sem conteúdo")
	}

	choice := parsed.Choices[0]
	if choice.Message.Content == "" {
		return "", fmt.Errorf("resposta do omniroute vazia (finish_reason=%q) — se o modelo roteado for de raciocínio, os tokens de pensamento podem ter consumido todo o max_tokens=%d antes de gerar a resposta final", choice.FinishReason, maxTokens)
	}

	return cleanMarkdown(choice.Message.Content), nil
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

type chatRequest struct {
	Model     string        `json:"model"`
	MaxTokens int           `json:"max_tokens"`
	Messages  []chatMessage `json:"messages"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message      chatMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
}

type chatErrorResponse struct {
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}
