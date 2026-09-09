package crawler

import (
	"strings"

	"golang.org/x/net/html"
)

// attr retorna o valor do atributo informado do nó, se existir.
func attr(n *html.Node, key string) (string, bool) {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val, true
		}
	}
	return "", false
}

// hasClass verifica se o nó possui a classe CSS informada.
func hasClass(n *html.Node, class string) bool {
	val, ok := attr(n, "class")
	if !ok {
		return false
	}
	for _, c := range strings.Fields(val) {
		if c == class {
			return true
		}
	}
	return false
}

// isTag verifica se o nó é um elemento com a tag informada.
func isTag(n *html.Node, tag string) bool {
	return n != nil && n.Type == html.ElementNode && n.Data == tag
}

// findAll percorre a árvore a partir de n e retorna todos os nós que satisfazem match.
func findAll(n *html.Node, match func(*html.Node) bool) []*html.Node {
	var out []*html.Node
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if match(node) {
			out = append(out, node)
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return out
}

// find percorre a árvore a partir de n e retorna o primeiro nó que satisfaz match.
func find(n *html.Node, match func(*html.Node) bool) *html.Node {
	var result *html.Node
	var walk func(*html.Node) bool
	walk = func(node *html.Node) bool {
		if match(node) {
			result = node
			return true
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			if walk(c) {
				return true
			}
		}
		return false
	}
	walk(n)
	return result
}

// textContent retorna o texto concatenado de todos os nós de texto descendentes de n.
func textContent(n *html.Node) string {
	if n == nil {
		return ""
	}
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.TextNode {
			sb.WriteString(node.Data)
			sb.WriteString(" ")
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.Join(strings.Fields(sb.String()), " ")
}
