// Package notifier envia notificações via Telegram quando uma vaga
// analisada atinge o fit_score mínimo configurado.
package notifier

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"job-scout/internal/models"
)

const telegramAPIURL = "https://api.telegram.org"

var httpClient = &http.Client{Timeout: 15 * time.Second}

// Telegram envia notificações de vagas com fit_score alto para um chat do
// Telegram via bot.
type Telegram struct {
	botToken string
	chatID   string
	minScore int
}

// NewTelegram cria um notifier do Telegram. Se botToken ou chatID estiverem
// vazios, NotifyJobAnalyzed não faz nada (notificações desabilitadas).
func NewTelegram(botToken, chatID string, minScore int) *Telegram {
	return &Telegram{botToken: botToken, chatID: chatID, minScore: minScore}
}

// Enabled indica se o notifier está configurado (bot token e chat id presentes).
func (t *Telegram) Enabled() bool {
	return t.botToken != "" && t.chatID != ""
}

// NotifyJobAnalyzed envia uma mensagem ao Telegram se o fit_score da análise
// atingir o mínimo configurado. Erros de envio são logados como warning e
// não interrompem o pipeline.
func (t *Telegram) NotifyJobAnalyzed(job models.Job, analysis models.Analysis) {
	if !t.Enabled() || analysis.FitScore < t.minScore {
		return
	}

	if err := t.send(buildMessage(job, analysis)); err != nil {
		slog.Warn("telegram: falha ao enviar notificação", "title", job.Title, "error", err)
	}
}

func buildMessage(job models.Job, analysis models.Analysis) string {
	msg := fmt.Sprintf("🎯 *Nova vaga com fit %d/100*\n\n*%s* — %s\n\n%s",
		analysis.FitScore, escapeMarkdown(job.Title), escapeMarkdown(job.Company), escapeMarkdown(analysis.Summary))
	if job.URL != "" {
		msg += "\n\n" + job.URL
	}
	return msg
}

// escapeMarkdown escapa caracteres especiais do parse_mode "Markdown" (legado)
// do Telegram, evitando que texto da vaga vindo do LLM quebre a formatação.
func escapeMarkdown(s string) string {
	replacer := strings.NewReplacer("_", "\\_", "*", "\\*", "`", "\\`", "[", "\\[")
	return replacer.Replace(s)
}

type sendMessageRequest struct {
	ChatID    string `json:"chat_id"`
	Text      string `json:"text"`
	ParseMode string `json:"parse_mode"`
}

type telegramErrorResponse struct {
	OK          bool   `json:"ok"`
	Description string `json:"description"`
}

func (t *Telegram) send(text string) error {
	reqBody := sendMessageRequest{ChatID: t.chatID, Text: text, ParseMode: "Markdown"}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("erro ao serializar mensagem: %w", err)
	}

	endpoint := fmt.Sprintf("%s/bot%s/sendMessage", telegramAPIURL, t.botToken)
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("erro ao criar requisição: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("erro ao chamar api do telegram: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("erro ao ler resposta do telegram: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var apiErr telegramErrorResponse
		_ = json.Unmarshal(respBody, &apiErr)
		msg := apiErr.Description
		if msg == "" {
			msg = string(respBody)
		}
		return fmt.Errorf("status inesperado %d do telegram: %s", resp.StatusCode, msg)
	}

	return nil
}
