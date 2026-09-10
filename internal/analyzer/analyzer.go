// Package analyzer usa um LLM (via gateway OmniRoute self-hosted, com API
// compatível com OpenAI) para avaliar o fit de vagas encontradas pelos
// crawlers contra o perfil do usuário.
package analyzer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"time"

	"job-scout/internal/models"
)

const (
	maxTokens  = 4096
	maxRetries = 3
)

const systemPrompt = "Você é um especialista em recrutamento e análise de fit cultural/técnico.\n" +
	"Responda APENAS com JSON válido, sem markdown, sem explicações."

var httpClient = &http.Client{Timeout: 60 * time.Second}

// Analyzer usa um LLM exposto pelo gateway OmniRoute para analisar o fit de
// vagas contra o perfil do usuário.
type Analyzer struct {
	baseURL string
	apiKey  string
	model   string
}

// NewAnalyzer cria um novo Analyzer apontando para o endpoint OpenAI-compatible
// do OmniRoute (baseURL, ex: "http://omniroute:20128/v1"), com a chave de API
// e o modelo a serem usados.
func NewAnalyzer(baseURL, apiKey, model string) *Analyzer {
	return &Analyzer{baseURL: baseURL, apiKey: apiKey, model: model}
}

// AnalyzeJob lê o perfil do usuário em profilePath (sem cachear) e usa o LLM
// configurado para avaliar o fit da vaga informada, retornando uma
// models.Analysis preenchida com o job_id já associado.
func (a *Analyzer) AnalyzeJob(profilePath string, job models.Job) (models.Analysis, error) {
	profileBytes, err := os.ReadFile(profilePath)
	if err != nil {
		return models.Analysis{}, fmt.Errorf("erro ao ler perfil %q: %w", profilePath, err)
	}

	prompt := buildPrompt(string(profileBytes), job)

	result, err := a.callWithRetry(prompt)
	if err != nil {
		return models.Analysis{}, fmt.Errorf("erro ao analisar vaga %q: %w", job.Title, err)
	}

	slog.Info("vaga analisada", "title", job.Title, "fit_score", result.FitScore)

	return models.Analysis{
		JobID:        job.ID,
		FitScore:     result.FitScore,
		Summary:      result.Summary,
		Benefits:     strings.Join(result.Benefits, "; "),
		FitReasoning: result.FitReasoning,
	}, nil
}

func buildPrompt(profile string, job models.Job) string {
	return fmt.Sprintf(`## Meu Perfil
%s

## Vaga
Título: %s
Empresa: %s
Descrição: %s

Retorne exatamente nesse formato:
{
  "fit_score": <0-100>,
  "summary": "<resumo da vaga em 3-4 linhas em PT-BR>",
  "benefits": ["<benefício 1>", "<benefício 2>"],
  "fit_reasoning": "<por que essa vaga é ou não é boa pra mim, em 2-3 linhas>"
}`, profile, job.Title, job.Company, job.Description)
}

// analysisResult é o formato esperado no JSON retornado pelo modelo.
type analysisResult struct {
	FitScore     int      `json:"fit_score"`
	Summary      string   `json:"summary"`
	Benefits     []string `json:"benefits"`
	FitReasoning string   `json:"fit_reasoning"`
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

// retryableError representa um erro transitório do gateway OmniRoute (rate
// limit ou erro de servidor) que justifica uma nova tentativa.
type retryableError struct {
	statusCode int
	message    string
}

func (e *retryableError) Error() string {
	return fmt.Sprintf("erro transitório do omniroute (status %d): %s", e.statusCode, e.message)
}

// callWithRetry chama o gateway OmniRoute, tentando novamente com backoff
// exponencial (e jitter aleatório) quando a resposta indica rate limit ou
// erro de servidor, até maxRetries tentativas.
func (a *Analyzer) callWithRetry(prompt string) (analysisResult, error) {
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			delay := backoffDelay(attempt)
			slog.Warn("omniroute: erro transitório, aguardando antes de tentar novamente",
				"attempt", attempt+1, "delay", delay, "error", lastErr)
			time.Sleep(delay)
		}

		text, err := a.callAPI(prompt)
		if err != nil {
			if rlErr, ok := err.(*retryableError); ok {
				lastErr = rlErr
				continue
			}
			return analysisResult{}, err
		}

		return parseResult(text)
	}

	return analysisResult{}, fmt.Errorf("esgotadas %d tentativas: %w", maxRetries, lastErr)
}

func backoffDelay(attempt int) time.Duration {
	base := time.Duration(1<<uint(attempt)) * time.Second
	jitter := time.Duration(rand.Int63n(int64(500 * time.Millisecond)))
	return base + jitter
}

func (a *Analyzer) callAPI(prompt string) (string, error) {
	reqBody := chatRequest{
		Model:     a.model,
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

	req, err := http.NewRequest(http.MethodPost, a.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("erro ao criar requisição: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.apiKey)

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

		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= http.StatusInternalServerError {
			return "", &retryableError{statusCode: resp.StatusCode, message: msg}
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

	return choice.Message.Content, nil
}

// parseResult faz o parse do JSON retornado pelo modelo, removendo eventuais
// blocos de código markdown que o modelo tenha adicionado por engano.
func parseResult(text string) (analysisResult, error) {
	cleaned := strings.TrimSpace(text)
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)

	var result analysisResult
	if err := json.Unmarshal([]byte(cleaned), &result); err != nil {
		return analysisResult{}, fmt.Errorf("erro ao parsear JSON da análise: %w (texto: %q)", err, cleaned)
	}
	return result, nil
}
