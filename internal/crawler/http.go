package crawler

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"time"
)

const userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36 job-scout/1.0"

var httpClient = &http.Client{Timeout: 20 * time.Second}

// httpGet faz uma requisição GET com um User-Agent de navegador e retorna o corpo da resposta.
func httpGet(url string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("erro ao criar requisição para %q: %w", url, err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept-Language", "pt-BR,pt;q=0.9,en;q=0.8")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("erro ao requisitar %q: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status inesperado %d ao requisitar %q", resp.StatusCode, url)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("erro ao ler resposta de %q: %w", url, err)
	}
	return body, nil
}

// hashID gera um ID determinístico (SHA256) a partir da URL da vaga.
func hashID(url string) string {
	sum := sha256.Sum256([]byte(url))
	return hex.EncodeToString(sum[:])
}

const (
	minRequestDelay = 2 * time.Second
	maxRequestDelay = 3 * time.Second
)

// randomDelay pausa a execução por um período aleatório entre minRequestDelay e
// maxRequestDelay, usado para não sobrecarregar os sites de origem e reduzir a
// chance de bloqueio por rate limiting.
func randomDelay() {
	jitter := time.Duration(rand.Int63n(int64(maxRequestDelay - minRequestDelay)))
	time.Sleep(minRequestDelay + jitter)
}
