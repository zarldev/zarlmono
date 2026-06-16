package fetch

import (
	"fmt"
	"io"
	"strings"

	"golang.org/x/net/html"
)

// ExtractText reads HTML from r and returns the <title> text and the
// visible text content of the page body, up to maxChars bytes.
// <script>, <style>, <noscript>, and <head> nodes (including their
// entire subtrees) are skipped. Whitespace is collapsed — runs of
// spaces, tabs, and newlines become single spaces.
func ExtractText(r io.Reader, maxChars int) (string, string, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return "", "", fmt.Errorf("parse html: %w", err)
	}

	var b strings.Builder
	title := findTitle(doc)
	walkText(doc, &b)

	body := strings.TrimSpace(b.String())
	body = collapseWS(body)
	if len(body) > maxChars {
		body = body[:maxChars]
		// Try to truncate at the last complete sentence.
		if lastDot := strings.LastIndex(body, ". "); lastDot > maxChars/2 {
			body = body[:lastDot+1]
		}
		body += "\n\n[truncated]"
	}
	return title, body, nil
}

// findTitle walks the document tree looking for the first <title> element
// and returns its text content.
func findTitle(n *html.Node) string {
	var title string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "title" {
			if title == "" {
				title = textContent(n)
			}
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.TrimSpace(title)
}

// walkText walks the DOM tree and appends visible text content to b.
// Skips script, style, noscript, head subtrees entirely.
func walkText(n *html.Node, b *strings.Builder) {
	if n.Type == html.ElementNode {
		switch n.Data {
		case "script", "style", "noscript", "head", "title":
			return // skip entire subtree
		case "br", "p", "div", "li", "h1", "h2", "h3", "h4", "h5", "h6",
			"section", "article", "header", "footer", "blockquote", "pre", "hr":
			if b.Len() > 0 && b.String()[b.Len()-1] != '\n' {
				b.WriteByte('\n')
			}
		}
	}
	if n.Type == html.TextNode {
		s := strings.TrimSpace(n.Data)
		if s != "" {
			if b.Len() > 0 && b.String()[b.Len()-1] != '\n' && b.String()[b.Len()-1] != ' ' {
				b.WriteByte(' ')
			}
			b.WriteString(s)
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walkText(c, b)
	}
}

// textContent returns the concatenated text of n's subtree. Used for
// <title> extraction where we want the raw text without block-breaks.
func textContent(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

// collapseWS replaces any run of whitespace (spaces, tabs, newlines)
// with a single space.
func collapseWS(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return s
}
