package tui

import "strings"

type searchDocument struct {
	ID     string
	Fields []string
}

// searchDocuments returns indexes whose fields contain every normalized query
// token. Source order is preserved.
func searchDocuments(documents []searchDocument, query string) []int {
	tokens := strings.Fields(strings.ToLower(query))
	matches := make([]int, 0, len(documents))
	for index, document := range documents {
		haystack := strings.ToLower(strings.Join(document.Fields, " "))
		matched := true
		for _, token := range tokens {
			if !strings.Contains(haystack, token) {
				matched = false
				break
			}
		}
		if matched {
			matches = append(matches, index)
		}
	}
	return matches
}

func preserveSearchSelection(documents []searchDocument, matches []int, selectedID string) int {
	for cursor, sourceIndex := range matches {
		if documents[sourceIndex].ID == selectedID {
			return cursor
		}
	}
	if len(matches) == 0 {
		return 0
	}
	return 0
}
