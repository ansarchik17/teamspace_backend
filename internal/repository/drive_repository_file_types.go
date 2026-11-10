package repository

import (
	"context"
)

// FileTypeInfo представляет информацию о типе файлов
type FileTypeInfo struct {
	Type  string `json:"type"`
	Count int    `json:"count"`
	Label string `json:"label"`
}

// GetFileTypes возвращает статический список принимаемых форматов файлов
func (r *DriveRepository) GetFileTypes(ctx context.Context, userID int64) ([]FileTypeInfo, error) {
	// Список всех поддерживаемых форматов (для фильтров на фронте)
	fileTypes := []FileTypeInfo{
		// Документы
		{Type: "pdf", Label: "PDF", Count: 0},
		{Type: "doc", Label: "Word (DOC)", Count: 0},
		{Type: "docx", Label: "Word (DOCX)", Count: 0},
		{Type: "xls", Label: "Excel (XLS)", Count: 0},
		{Type: "xlsx", Label: "Excel (XLSX)", Count: 0},
		{Type: "ppt", Label: "PowerPoint (PPT)", Count: 0},
		{Type: "pptx", Label: "PowerPoint (PPTX)", Count: 0},
		{Type: "txt", Label: "Текст (TXT)", Count: 0},
		{Type: "csv", Label: "CSV", Count: 0},
		{Type: "json", Label: "JSON", Count: 0},
		{Type: "xml", Label: "XML", Count: 0},

		// Изображения
		{Type: "jpg", Label: "JPEG", Count: 0},
		{Type: "jpeg", Label: "JPEG", Count: 0},
		{Type: "png", Label: "PNG", Count: 0},
		{Type: "gif", Label: "GIF", Count: 0},
		{Type: "webp", Label: "WebP", Count: 0},
		{Type: "svg", Label: "SVG", Count: 0},

		// Видео
		{Type: "mp4", Label: "MP4", Count: 0},
		{Type: "mov", Label: "MOV", Count: 0},
		{Type: "avi", Label: "AVI", Count: 0},
		{Type: "mkv", Label: "MKV", Count: 0},

		// Аудио
		{Type: "mp3", Label: "MP3", Count: 0},
		{Type: "wav", Label: "WAV", Count: 0},
		{Type: "ogg", Label: "OGG", Count: 0},

		// Архивы
		{Type: "zip", Label: "ZIP", Count: 0},
		{Type: "rar", Label: "RAR", Count: 0},
		{Type: "7z", Label: "7Z", Count: 0},
		{Type: "tar", Label: "TAR", Count: 0},
		{Type: "gz", Label: "GZ", Count: 0},
	}

	return fileTypes, nil
}
