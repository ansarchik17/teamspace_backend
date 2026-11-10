package service

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime/multipart"
	"path/filepath"
	"strings"

	_ "golang.org/x/image/webp"
)

func validatePublicationUpload(header *multipart.FileHeader, gallery bool) error {
	if header == nil || header.Size <= 0 || len(header.Filename) > 255 {
		return fmt.Errorf("invalid uploaded file")
	}
	limit := int64(50 << 20)
	if gallery {
		limit = 25 << 20
	}
	if header.Size > limit {
		return fmt.Errorf("uploaded file exceeds size limit")
	}
	file, err := header.Open()
	if err != nil {
		return err
	}
	defer file.Close()
	ext := strings.ToLower(filepath.Ext(header.Filename))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp":
		cfg, format, err := image.DecodeConfig(file)
		expected := map[string]string{".jpg": "jpeg", ".jpeg": "jpeg", ".png": "png", ".gif": "gif", ".webp": "webp"}[ext]
		if err != nil || format != expected || cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 40_000_000 {
			return fmt.Errorf("invalid image contents or dimensions")
		}
	case ".pdf":
		if gallery {
			return fmt.Errorf("gallery only accepts images")
		}
		magic := make([]byte, 5)
		if _, err := io.ReadFull(file, magic); err != nil || !bytes.Equal(magic, []byte("%PDF-")) {
			return fmt.Errorf("invalid PDF header")
		}
		tailStart := header.Size - 1024
		if tailStart < 0 {
			tailStart = 0
		}
		if _, err := file.Seek(tailStart, io.SeekStart); err != nil {
			return err
		}
		tail, err := io.ReadAll(io.LimitReader(file, 1024))
		if err != nil || !bytes.Contains(tail, []byte("%%EOF")) {
			return fmt.Errorf("invalid PDF end marker")
		}
	default:
		return fmt.Errorf("publication uploads only accept JPEG, PNG, GIF, WebP or PDF; use video embeds for video")
	}
	return nil
}

func validatePublicationUploadBatch(files []*multipart.FileHeader, gallery bool) error {
	if len(files) == 0 || len(files) > 50 {
		return fmt.Errorf("upload must contain 1..50 files")
	}
	total := int64(0)
	for _, file := range files {
		if err := validatePublicationUpload(file, gallery); err != nil {
			return err
		}
		total += file.Size
		if total > 100<<20 {
			return fmt.Errorf("upload exceeds total size limit")
		}
	}
	return nil
}
