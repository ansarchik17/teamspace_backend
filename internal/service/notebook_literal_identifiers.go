package service

import (
	"regexp"
	"strings"
	"unicode"
)

var notebookIdentifier = regexp.MustCompile(`[\p{L}\p{N}\p{M}]+(?:[-‐‑‒–—−][\p{L}\p{N}\p{M}]+)+`)
var notebookIdentifierDashes = strings.NewReplacer("‐", "-", "‑", "-", "‒", "-", "–", "-", "—", "-", "−", "-")

// A model may typographically rewrite a literal code despite the copy-exactly
// instruction. Restore only an unambiguous uppercase alphanumeric identifier
// actually present in the selected, readable excerpts. Never guess missing codes.
func preserveNotebookIdentifiers(content string, sources []NotebookResearchSource) string {
	literals := make(map[string]string)
	ambiguous := make(map[string]bool)
	for _, source := range sources {
		for _, excerpt := range source.Excerpts {
			for _, literal := range notebookIdentifier.FindAllString(excerpt.Text, -1) {
				letter, digit, eligible := false, false, true
				for _, r := range literal {
					if unicode.IsLetter(r) {
						letter = true
						if !unicode.IsUpper(r) {
							eligible = false
						}
					}
					if unicode.IsDigit(r) {
						digit = true
					}
				}
				if !eligible || !letter || !digit {
					continue
				}
				key := notebookIdentifierDashes.Replace(literal)
				if prior, exists := literals[key]; exists && prior != literal {
					ambiguous[key] = true
				}
				literals[key] = literal
			}
		}
	}
	return notebookIdentifier.ReplaceAllStringFunc(content, func(token string) string {
		key := notebookIdentifierDashes.Replace(token)
		if literal, exists := literals[key]; exists && !ambiguous[key] {
			return literal
		}
		return token
	})
}
