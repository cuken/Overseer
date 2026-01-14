package main

import (
	"strings"

	"github.com/cuken/overseer/internal/logger"
)

// formatIDHighlights returns a map of ID -> highlighted ID string
func formatIDHighlights(ids []string) map[string]string {
	highlights := make(map[string]string)
	if len(ids) == 0 {
		return highlights
	}

	for _, id := range ids {
		// Minimum length to consider is 4 (os- + 1 char)
		uniqueLen := len(id)
		for l := 4; l <= len(id); l++ {
			prefix := id[:l]
			isUnique := true
			for _, other := range ids {
				if id == other {
					continue
				}
				if strings.HasPrefix(other, prefix) {
					isUnique = false
					break
				}
			}
			if isUnique {
				uniqueLen = l
				break
			}
		}

		// Highlight unique part in Magenta, the rest in Gray
		highlighted := logger.Magenta + id[:uniqueLen] + logger.Reset + logger.Gray + id[uniqueLen:] + logger.Reset
		highlights[id] = highlighted
	}
	return highlights
}
