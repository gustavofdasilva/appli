// Package checklist usa um LLM (via gateway OmniRoute self-hosted, com API
// compatível com OpenAI) para gerar, sob demanda, um checklist de
// palavras-chave e pontos a destacar num currículo personalizado para uma
// vaga específica — sem gerar o arquivo do currículo em si.
package checklist

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"job-scout/internal/models"
)

const maxTokens = 4096

const systemPrompt = "Você é um especialista em otimização de currículos para ATS e recrutadores.\n" +
	"Responda APENAS com JSON válido, sem markdown, sem explicações."

const userPromptTemplate = `## Meu Perfil
%s

## Vaga
Título: %s
Empresa: %s
Descrição: %s

Compare meu perfil com a vaga e monte um checklist do que eu preciso
garantir que apareça no currículo que vou enviar pra essa vaga específica.

Retorne exatamente nesse formato:
{
  "keywords": ["<termo técnico ou palavra-chave curta da vaga, ex: nome de tecnologia/certificação>", "..."],
  "points": ["<frase curta orientando o que destacar ou reescrever no currículo pra essa vaga>", "..."]
}`

var httpClient = &http.Client{Timeout: 60 * time.Second}

// Generator usa um LLM exposto pelo gateway OmniRoute para gerar checklists
// de currículo sob demanda.
type Generator struct {
	baseURL string
	apiKey  string
	model   string
}

// NewGenerator cria um novo Generator apontando para o endpoint
// OpenAI-compatible do OmniRoute (baseURL, ex: "http://omniroute:20128/v1"),
// com a chave de API e o modelo a serem usados.
func NewGenerator(baseURL, apiKey, model string) *Generator {
	return &Generator{baseURL: baseURL, apiKey: apiKey, model: model}
}

// Checklist é o formato esperado no JSON retornado pelo modelo.
type Checklist struct {
	Keywords []string `json:"keywords"`
	Points   []string `json:"points"`
}

// Generate compara profile com a vaga informada e retorna um Checklist de
// palavras-chave e pontos a destacar no currículo dessa vaga específica.
func (g *Generator) Generate(profile string, job models.Job) (Checklist, error) {
	prompt := fmt.Sprintf(userPromptTemplate, profile, job.Title, job.Company, job.Description)

	text, err := g.callAPI(prompt)
	if err != nil {
		return Checklist{}, fmt.Errorf("erro ao gerar checklist de currículo para vaga %q: %w", job.Title, err)
	}

	return parseChecklist(text)
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

func (g *Generator) callAPI(prompt string) (string, error) {
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

	return choice.Message.Content, nil
}

// parseChecklist faz o parse do JSON retornado pelo modelo, removendo
// eventuais blocos de código markdown que o modelo tenha adicionado por
// engano.
func parseChecklist(text string) (Checklist, error) {
	cleaned := strings.TrimSpace(text)
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)

	var result Checklist
	if err := json.Unmarshal([]byte(cleaned), &result); err != nil {
		return Checklist{}, fmt.Errorf("erro ao parsear JSON do checklist: %w (texto: %q)", err, cleaned)
	}
	return result, nil
}
