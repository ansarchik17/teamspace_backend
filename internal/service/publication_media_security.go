package service

import (
	"context"
	"fmt"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
	"net/url"
	"strings"
	"time"
)

func publicationMediaKey(ownerID int64, raw string) (string, bool, error) {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 8192 {
		return "", false, fmt.Errorf("invalid attachment URL")
	}
	key := raw
	if strings.HasSuffix(u.Path, "/api/v1/files/download") {
		key = u.Query().Get("object_name")
	} else if u.Scheme != "" || u.Host != "" {
		if !domain.ValidPublicationURL(raw) {
			return "", false, fmt.Errorf("invalid attachment URL")
		}
		return "", false, nil
	}
	if strings.Contains(key, "..") || strings.ContainsAny(key, "\\\r\n\t?#") {
		return "", false, fmt.Errorf("invalid attachment object key")
	}
	for _, kind := range []string{"image", "document", "pdf", "video", "audio"} {
		prefix := fmt.Sprintf("%s/%d/", kind, ownerID)
		if strings.HasPrefix(key, prefix) && len(key) > len(prefix) {
			return key, true, nil
		}
	}
	return "", false, fmt.Errorf("attachment object must belong to publication author")
}

func validatePublicationAttachments(ownerID int64, requests []domain.CreateAttachmentRequest) error {
	if len(requests) > 100 {
		return fmt.Errorf("too many publication attachments")
	}
	for _, a := range requests {
		if (a.FileSize != nil && *a.FileSize < 0) || (a.OrderIndex != nil && *a.OrderIndex < 0) || (a.Caption != nil && len(*a.Caption) > 4096) {
			return fmt.Errorf("invalid attachment metadata")
		}
		for _, raw := range []string{a.FileURL} {
			if _, _, err := publicationMediaKey(ownerID, raw); err != nil {
				return err
			}
		}
		if a.ThumbnailURL != nil {
			if _, _, err := publicationMediaKey(ownerID, *a.ThumbnailURL); err != nil {
				return err
			}
		}
		if a.VideoEmbedURL != nil {
			if _, _, _, err := domain.ParseVideoEmbed(*a.VideoEmbedURL); err != nil {
				return err
			}
		}
		if len(a.FileName) > 255 || a.FileName == "" {
			return fmt.Errorf("invalid attachment filename")
		}
	}
	return nil
}

func (s *PublicationService) signedPublicationMediaURL(ownerID int64, raw string) (string, error) {
	key, storage, err := publicationMediaKey(ownerID, raw)
	if err != nil {
		return "", err
	}
	if !storage {
		return raw, nil
	}
	if s.fileService == nil {
		return "", fmt.Errorf("file storage service is unavailable")
	}
	return s.fileService.GenerateSignedURL(key, 24*time.Hour), nil
}

func (s *PublicationService) queueFailedPublicationUploads(ctx context.Context, keys []string) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := s.repo.QueuePublicationObjectCleanup(cleanupCtx, keys); err != nil {
		s.logger.Error("cannot enqueue failed publication uploads", zap.Error(err))
	}
}

func (s *PublicationService) signPublicationAttachment(ownerID int64, att domain.PublicationAttachment) domain.PublicationAttachment {
	att.FileURL, _ = s.signedPublicationMediaURL(ownerID, att.FileURL)
	if att.ThumbnailURL != nil {
		signed, err := s.signedPublicationMediaURL(ownerID, *att.ThumbnailURL)
		if err != nil {
			att.ThumbnailURL = nil
		} else {
			att.ThumbnailURL = &signed
		}
	}
	if att.VideoEmbedURL != nil {
		if provider, embed, _, err := domain.ParseVideoEmbed(*att.VideoEmbedURL); err != nil {
			att.VideoEmbedURL = nil
			att.VideoProvider = nil
		} else {
			att.VideoEmbedURL = &embed
			att.VideoProvider = &provider
		}
	}
	return att
}
