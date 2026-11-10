package service

import (
	"context"
	"github.com/west6ide/workspace-backend/internal/i18n"
	"github.com/west6ide/workspace-backend/internal/repository"
)

func uiContext(contexts []context.Context) context.Context {
	if len(contexts) > 0 && contexts[0] != nil {
		return contexts[0]
	}
	return context.Background()
}

func localizedAIDocument(ctx context.Context, doc *repository.AIDocument) *repository.AIDocument {
	if doc == nil {
		return nil
	}
	copy := *doc
	if doc.ErrorCode == nil && doc.ErrorMessage == nil {
		return &copy
	}
	source := "Документ не удалось обработать. Попробуйте повторить загрузку."
	if doc.ErrorCode != nil {
		switch *doc.ErrorCode {
		case "file_too_large":
			source = "Документ превышает допустимый размер."
		case "text_not_found":
			source = "В документе не найден читаемый текст. Для скана нужен OCR."
		case "storage_error":
			source = "Не удалось прочитать файл из хранилища."
		case "read_error":
			source = "Не удалось прочитать содержимое документа."
		case "index_error":
			source = "Не удалось подготовить поиск по документу."
		}
	}
	message := i18n.UIMessage(ctx, source)
	copy.ErrorMessage = &message
	return &copy
}
