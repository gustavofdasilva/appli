package crawler

import (
	"html"
	"regexp"
	"strings"
)

var (
	htmlBlockTagRe = regexp.MustCompile(`(?i)</?(p|br|div|li|ul|ol|h[1-6])[^>]*>`)
	htmlTagRe      = regexp.MustCompile(`<[^>]*>`)
)

// stripHTML remove tags HTML de s — usado nas descrições vindas de feeds RSS
// e da API do Hacker News, que trazem HTML bruto em vez de texto puro.
// Tags de bloco/quebra viram espaço antes do restante ser removido (pra não
// grudar palavras de parágrafos diferentes), e entidades HTML (&amp;,
// &#x2F; etc) são decodificadas por último.
func stripHTML(s string) string {
	s = htmlBlockTagRe.ReplaceAllString(s, " ")
	s = htmlTagRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	return strings.Join(strings.Fields(s), " ")
}
