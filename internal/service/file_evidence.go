package service

import (
	"encoding/json"
	"strings"

	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
)

// Tool evidence must fit the common reader's 24 KB envelope. Prioritize match
// anchors; neighboring context is optional. Never hide that evidence was cut.
func boundFileEvidence(input *domain.DriveSearchQueryResponse) (*domain.DriveSearchQueryResponse, bool) {
	result := *input
	result.Sources = make([]domain.DriveSearchSource, 0, len(input.Sources))
	budget := 3600
	truncated := false
	for _, source := range input.Sources {
		copySource := source
		copySource.Chunks = nil
		copySource.ChunkIndices = nil
		for _, chunk := range source.Chunks {
			if chunk.IsContext {
				truncated = true
				continue
			}
			if budget <= 0 {
				truncated = true
				continue
			}
			text := []rune(chunk.Content)
			if len(text) > budget {
				chunk.Content = string(text[:budget])
				locator := map[string]any{}
				for k, v := range chunk.Locator {
					locator[k] = v
				}
				locator["excerpt_truncated"] = true
				chunk.Locator = locator
				truncated = true
			}
			budget -= len([]rune(chunk.Content))
			copySource.Chunks = append(copySource.Chunks, chunk)
			copySource.ChunkIndices = append(copySource.ChunkIndices, chunk.ChunkIndex)
		}
		if len(copySource.Chunks) > 0 {
			result.Sources = append(result.Sources, copySource)
		} else {
			truncated = true
		}
	}
	result.TotalFiles = len(result.Sources)
	if truncated {
		result.Warnings = append(append([]string{}, input.Warnings...), "Показаны выбранные фрагменты. Для полного чтения откройте конкретный файл.")
	}
	return &result, truncated
}

func boundChatEvidence(input []repository.AIDocumentChunk) ([]repository.AIDocumentChunk, bool) {
	result := []repository.AIDocumentChunk{}
	budget := 3600
	truncated := false
	for _, chunk := range input {
		if budget <= 0 {
			truncated = true
			break
		}
		text := []rune(chunk.Text)
		if len(text) > budget {
			start := 0
			// Expanded context carries the original match offset. Keep evidence from
			// that anchor instead of accidentally returning only its preceding chunk.
			if offset, ok := chunk.Locator["anchor_offset_runes"].(int); ok {
				start = offset
			}
			if start >= len(text) {
				start = 0
			}
			end := start + budget
			if end > len(text) {
				end = len(text)
			}
			chunk.Text = string(text[start:end])
			locator := map[string]any{}
			for k, v := range chunk.Locator {
				locator[k] = v
			}
			locator["excerpt_truncated"] = true
			chunk.Locator = locator
			truncated = true
		}
		result = append(result, chunk)
		budget -= len([]rune(chunk.Text))
	}
	return result, truncated
}

func fileEvidenceWidget(result *domain.DriveSearchQueryResponse) *SmartAgentWidget {
	files := []map[string]any{}
	snippets := []map[string]any{}
	for _, source := range result.Sources {
		files = append(files, map[string]any{"id": source.FileID, "real_name": source.FileName, "score": source.Score})
		for _, chunk := range source.Chunks {
			if !chunk.IsContext {
				snippets = append(snippets, map[string]any{"file_id": source.FileID, "file_name": source.FileName, "chunk_index": chunk.ChunkIndex, "score": chunk.Score})
			}
		}
	}
	return &SmartAgentWidget{Type: "drive_content_answer", Title: "Источники из файлов", Data: map[string]any{"question": result.Question, "files": files, "snippets": snippets}}
}

func fileEvidenceFitsEnvelope(data any) bool {
	encoded, err := json.Marshal(data)
	return err == nil && len(encoded) <= 20000
}

// No content-based inference: a caller must request a narrower source when the
// structured metadata alone is too large for one tool response.
func fileEvidenceTooLarge() AIDataResult {
	return AIDataResult{Status: "too_large", Truncated: true, Message: strings.Join([]string{"Уточните запрос или укажите один файл.", "Источники не помещаются в контекст."}, " ")}
}
