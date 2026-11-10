package documentsearch

import (
	"strings"
	"unicode"
)

type TextChunk struct {
	Ordinal   int
	Text      string
	StartLine int
	EndLine   int
	StartChar int
	EndChar   int
}

// Split uses bounded overlapping windows without cutting words when a nearby
// paragraph/word boundary exists. Offsets refer to the exact extracted text.
func Split(text string, size, overlap int) []TextChunk {
	if size < 100 {
		size = 3000
	}
	if overlap < 0 || overlap >= size/2 {
		overlap = size / 10
	}
	runes := []rune(text)
	result := []TextChunk{}
	line := 1
	for start := 0; start < len(runes); {
		end := start + size
		if end > len(runes) {
			end = len(runes)
		}
		if end < len(runes) {
			for at := end; at > start+size*3/4; at-- {
				if unicode.IsSpace(runes[at-1]) {
					end = at
					break
				}
			}
		}
		left, right := start, end
		for left < right && unicode.IsSpace(runes[left]) {
			left++
		}
		for right > left && unicode.IsSpace(runes[right-1]) {
			right--
		}
		if left < right {
			result = append(result, TextChunk{Ordinal: len(result), Text: string(runes[left:right]), StartLine: line + strings.Count(string(runes[start:left]), "\n"), EndLine: line + strings.Count(string(runes[start:right]), "\n"), StartChar: left, EndChar: right})
		}
		if end == len(runes) {
			break
		}
		next := end - overlap
		if next <= start {
			next = end
		}
		for next < end && next > 0 && !unicode.IsSpace(runes[next-1]) {
			next++
		}
		line += strings.Count(string(runes[start:next]), "\n")
		start = next
	}
	return result
}
