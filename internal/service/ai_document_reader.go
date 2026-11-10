package service

import (
	"context"
	"io"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
)

func aiTextPage(text string, page int) map[string]any {
	chars := []rune(text)
	start := (page - 1) * 3500
	if start > len(chars) {
		start = len(chars)
	}
	end := start + 3500
	if end > len(chars) {
		end = len(chars)
	}
	return map[string]any{"text": string(chars[start:end]), "page": page, "has_more": end < len(chars), "next_page": page + 1, "total_chars": len(chars)}
}
func aiDocumentPage(query AIDataQuery) (int, error) {
	page := 1
	for key, value := range query.Params {
		if key != "page" {
			return 0, strconv.ErrSyntax
		}
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || n > 10000 {
			return 0, strconv.ErrSyntax
		}
		page = n
	}
	return page, nil
}

// Register document readers once, before serving requests. IDs and chat scope
// come from authenticated identity and object ACLs, never from model claims.
func RegisterAIDocumentReaders(reader *HTTPAIUserDataReader, drive *DriveService, files *FileService, extractor *ContentExtractionService, docs *AIDocumentService, repo *repository.AIDocumentRepository, chats *repository.AIRepository) {
	reader.extractor = extractor
	reader.Register(AIDataDataset{Key: "drive_content", Title: "Файл Drive", Description: "Текст файла Drive: сначала найди id через drive. Читай последующие части с page.", DetailPath: "{id}", Params: []string{"page"}}, func(ctx context.Context, identity AIDataIdentity, query AIDataQuery) AIDataResult {
		id, err := uuid.Parse(query.ID)
		page, pageErr := aiDocumentPage(query)
		if err != nil || pageErr != nil {
			return AIDataResult{Status: "invalid_request", Message: "File UUID and valid page are required"}
		}
		if drive == nil || files == nil || extractor == nil {
			return AIDataResult{Status: "unavailable"}
		}
		// Use the same object ACL as download, without activity/gamification writes.
		meta, err := drive.canAccessFile(ctx, identity.UserID, id, domain.RoleViewer)
		if err != nil || meta == nil {
			return AIDataResult{Status: "forbidden", Message: "File unavailable to this user"}
		}
		stream, _, size, err := files.DownloadFile(ctx, meta.ObjectName)
		if err != nil {
			return AIDataResult{Status: "unavailable"}
		}
		defer stream.Close()
		if size > AIDocumentMaxBytes {
			return AIDataResult{Status: "too_large"}
		}
		raw, err := io.ReadAll(io.LimitReader(stream, AIDocumentMaxBytes+1))
		if err != nil || int64(len(raw)) > AIDocumentMaxBytes {
			return AIDataResult{Status: "too_large"}
		}
		text, err := extractor.ExtractTextBytes(meta.RealName, raw)
		if err != nil {
			return AIDataResult{Status: "unavailable", Message: "This file could not be parsed; do not infer its contents"}
		}
		data := aiTextPage(text, page)
		data["id"] = query.ID
		data["name"] = meta.RealName
		return AIDataResult{Status: "ok", Data: data}
	})
	reader.Register(AIDataDataset{Key: "chat_documents", Title: "Документы этого чата", Description: "Вложения текущего чата: без id список и статус; параметр query ищет релевантные фрагменты по словам и смыслу. С id поиск ограничен этим документом; без query чтение страницами по три фрагмента.", Path: "list", DetailPath: "{id}", Params: []string{"page", "query"}}, func(ctx context.Context, identity AIDataIdentity, query AIDataQuery) AIDataResult {
		searchQuery := strings.TrimSpace(query.Params["query"])
		pageQuery := query
		pageQuery.Params = map[string]string{}
		for key, value := range query.Params {
			if key != "query" {
				pageQuery.Params[key] = value
			}
		}
		page, err := aiDocumentPage(pageQuery)
		if err != nil || docs == nil || repo == nil || len([]rune(searchQuery)) > 1024 {
			return AIDataResult{Status: "invalid_request"}
		}
		if searchQuery != "" {
			if query.Params["page"] != "" {
				return AIDataResult{Status: "invalid_request", Message: "Use query or page separately"}
			}
			ids := []string{}
			if query.ID != "" {
				if _, err := uuid.Parse(query.ID); err != nil {
					return AIDataResult{Status: "invalid_request"}
				}
				doc, err := docs.Get(ctx, identity.UserID, identity.ChatID, query.ID)
				if err != nil || doc == nil {
					return AIDataResult{Status: "forbidden"}
				}
				if doc.Status != "ready" {
					return AIDataResult{Status: "not_ready"}
				}
				ids = append(ids, query.ID)
			} else if err := docs.ensureOwner(ctx, identity.UserID, identity.ChatID); err != nil {
				return AIDataResult{Status: "forbidden"}
			}
			chunks, err := repo.SearchChunks(ctx, identity.UserID, identity.ChatID, searchQuery, ids, 12)
			if err != nil {
				return AIDataResult{Status: "unavailable"}
			}
			chunks, truncated := boundChatEvidence(chunks)
			data := map[string]any{"chunks": chunks, "query": searchQuery}
			if !fileEvidenceFitsEnvelope(data) {
				return fileEvidenceTooLarge()
			}
			return AIDataResult{Status: "ok", Data: data, Truncated: truncated}
		}
		if query.ID == "" {
			items, err := docs.List(ctx, identity.UserID, identity.ChatID)
			if err != nil {
				return AIDataResult{Status: "forbidden"}
			}
			return AIDataResult{Status: "ok", Data: items}
		}
		if _, err := uuid.Parse(query.ID); err != nil {
			return AIDataResult{Status: "invalid_request"}
		}
		doc, err := docs.Get(ctx, identity.UserID, identity.ChatID, query.ID)
		if err != nil || doc == nil {
			return AIDataResult{Status: "forbidden"}
		}
		if doc.Status != "ready" {
			return AIDataResult{Status: "not_ready", Message: "Document parsing has not completed", Data: doc}
		}
		chunks, err := repo.ReadDocumentPage(ctx, identity.UserID, identity.ChatID, query.ID, page)
		if err != nil {
			return AIDataResult{Status: "unavailable"}
		}
		more := len(chunks) > 3
		if more {
			chunks = chunks[:3]
		}
		return AIDataResult{Status: "ok", Data: map[string]any{"document_id": query.ID, "name": doc.Name, "chunks": chunks, "page": page, "has_more": more, "next_page": page + 1}}
	})
	reader.Register(AIDataDataset{Key: "chat_attachments", Title: "Вложения этого чата", Description: "Файлы, отправленные с вопросами текущего чата: без id список, с id сообщения извлечённый текст. page для следующих частей.", Path: "list", DetailPath: "{id}", Params: []string{"page"}}, func(ctx context.Context, identity AIDataIdentity, query AIDataQuery) AIDataResult {
		page, err := aiDocumentPage(query)
		if err != nil {
			return AIDataResult{Status: "invalid_request"}
		}
		if chats == nil {
			return AIDataResult{Status: "unavailable"}
		}
		if query.ID != "" {
			if _, err = uuid.Parse(query.ID); err != nil {
				return AIDataResult{Status: "invalid_request"}
			}
		}
		items, err := chats.ReadChatAttachments(ctx, identity.UserID, identity.ChatID, query.ID, page)
		if err != nil {
			return AIDataResult{Status: "unavailable"}
		}
		if query.ID != "" && len(items) == 0 {
			return AIDataResult{Status: "not_found"}
		}
		more := len(items) > 25
		if more {
			items = items[:25]
		}
		return AIDataResult{Status: "ok", Data: map[string]any{"items": items, "page": page, "has_more": more, "next_page": page + 1}}
	})

}
