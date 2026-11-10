package service

import (
	"context"
	"strconv"
	"strings"

	"github.com/west6ide/workspace-backend/internal/domain"
)

type fileContentSearcher interface {
	Search(context.Context, int64, domain.DriveSearchQueryRequest) (*domain.DriveSearchQueryResponse, error)
}

func RegisterFileSearchReader(reader *HTTPAIUserDataReader, search fileContentSearcher) {
	if search == nil {
		return
	}
	reader.Register(AIDataDataset{Key: "drive_search", Title: "Поиск по содержимому файлов", Description: "Ищи сведения в доступных файлах Drive через query; id ограничивает поиск одним файлом. Фрагменты содержат координаты источника. При indexing=true часть файлов ещё не обработана; отсутствие совпадений не доказывает отсутствие информации.", Path: "search", DetailPath: "{id}", Params: []string{"query", "limit"}}, func(ctx context.Context, identity AIDataIdentity, q AIDataQuery) AIDataResult {
		for key := range q.Params {
			if key != "query" && key != "limit" {
				return AIDataResult{Status: "invalid_request"}
			}
		}
		question := strings.TrimSpace(q.Params["query"])
		if question == "" || len([]rune(question)) > 2000 {
			return AIDataResult{Status: "invalid_request"}
		}
		limit := 6
		if raw := q.Params["limit"]; raw != "" {
			var err error
			limit, err = strconv.Atoi(raw)
			if err != nil || limit < 1 || limit > 20 {
				return AIDataResult{Status: "invalid_request"}
			}
		}
		ids := []string{}
		if q.ID != "" {
			ids = append(ids, q.ID)
		}
		result, err := search.Search(ctx, identity.UserID, domain.DriveSearchQueryRequest{Question: question, FileIDs: ids, Limit: limit, ChunksPerFile: 4})
		if err != nil {
			return AIDataResult{Status: "unavailable", Message: "File search could not be completed with current permissions"}
		}
		result, truncated := boundFileEvidence(result)
		if !fileEvidenceFitsEnvelope(result) {
			return fileEvidenceTooLarge()
		}
		return AIDataResult{Status: "ok", Data: result, Truncated: truncated || result.Indexing || result.Incomplete}
	})
}
