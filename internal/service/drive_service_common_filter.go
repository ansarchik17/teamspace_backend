package service

import (
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/west6ide/workspace-backend/internal/domain"
)

// FilterAndSortDriveItems фильтрует и сортирует список элементов DriveItem
func FilterAndSortDriveItems(items []domain.DriveItem, fileType string, ownerID *int64, sortBy, sortOrder string) []domain.DriveItem {
	// 1. Фильтрация
	filtered := make([]domain.DriveItem, 0, len(items))

	fileType = strings.ToLower(strings.TrimSpace(fileType))

	for _, item := range items {
		// Фильтр по владельцу
		if ownerID != nil {
			if item.Type == domain.ResourceFolder {
				if item.Folder.OwnerID != *ownerID {
					continue
				}
			} else {
				if item.File.OwnerID != *ownerID {
					continue
				}
			}
		}

		// Фильтр по типу файла
		if fileType != "" {
			if item.Type == domain.ResourceFolder {
				// При фильтрации по типу файлов папки обычно скрываются
				// (поведение как в Google Drive при выборе "PDF")
				continue
			}

			if !matchFileType(item.File, fileType) {
				continue
			}
		}

		filtered = append(filtered, item)
	}

	// 2. Сортировка
	sortBy = strings.ToLower(strings.TrimSpace(sortBy))
	sortOrder = strings.ToLower(strings.TrimSpace(sortOrder))

	sort.Slice(filtered, func(i, j int) bool {
		var less bool

		switch sortBy {
		case "name", "имя":
			nameI := getItemName(filtered[i])
			nameJ := getItemName(filtered[j])
			less = strings.ToLower(nameI) < strings.ToLower(nameJ)

		case "size", "размер":
			sizeI := getItemSize(filtered[i])
			sizeJ := getItemSize(filtered[j])
			less = sizeI < sizeJ

		case "date", "дата":
			fallthrough
		default:
			dateI := getItemDate(filtered[i])
			dateJ := getItemDate(filtered[j])
			less = dateI.Before(dateJ)
		}

		// Применяем порядок сортировки
		if sortOrder == "asc" {
			return less
		}
		return !less // desc
	})

	return filtered
}

// matchFileType проверяет соответствие файла типу/миму
func matchFileType(file *domain.DriveFile, filterType string) bool {
	// Проверяем точное совпадение расширения (без точки)
	fileName := strings.ToLower(file.RealName)
	ext := strings.TrimPrefix(filepath.Ext(fileName), ".") // "pdf"

	if ext == filterType {
		return true
	}

	// Проверяем категории по mime_type
	mimeType := strings.ToLower(file.MimeType)

	switch filterType {
	case "image", "картинка", "фото":
		return strings.HasPrefix(mimeType, "image/")
	case "video", "видео":
		return strings.HasPrefix(mimeType, "video/")
	case "audio", "аудио":
		return strings.HasPrefix(mimeType, "audio/")
	case "document", "документ":
		return strings.HasPrefix(mimeType, "application/") || strings.HasPrefix(mimeType, "text/")
	case "pdf":
		return mimeType == "application/pdf"
	case "word", "doc", "docx":
		return strings.Contains(mimeType, "word") || ext == "doc" || ext == "docx"
	case "excel", "xls", "xlsx":
		return strings.Contains(mimeType, "excel") || strings.Contains(mimeType, "spreadsheet") || ext == "xls" || ext == "xlsx"
	case "powerpoint", "ppt", "pptx":
		return strings.Contains(mimeType, "powerpoint") || strings.Contains(mimeType, "presentation") || ext == "ppt" || ext == "pptx"
	case "archive", "zip", "rar":
		return strings.Contains(mimeType, "zip") || strings.Contains(mimeType, "compressed") || strings.Contains(mimeType, "tar") || ext == "zip" || ext == "rar" || ext == "7z"
	}

	return false
}

// Пагинация списка (in-memory)
func PaginateItems(items []domain.DriveItem, limit, offset int) []domain.DriveItem {
	total := len(items)
	if offset >= total {
		return []domain.DriveItem{}
	}

	end := offset + limit
	if end > total || limit <= 0 {
		end = total
	}

	return items[offset:end]
}

// Вспомогательные функции (дублируются для независимости, так как в drive_service_shared они приватные)
func getItemName(item domain.DriveItem) string {
	if item.Type == domain.ResourceFolder {
		return item.Folder.Name
	}
	return item.File.RealName
}

func getItemSize(item domain.DriveItem) int64 {
	if item.Type == domain.ResourceFolder {
		return 0
	}
	return item.File.Size
}

func getItemDate(item domain.DriveItem) time.Time {
	if item.Type == domain.ResourceFolder {
		return item.Folder.UpdatedAt
	}
	return item.File.UpdatedAt
}
