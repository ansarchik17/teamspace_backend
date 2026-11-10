package service

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

const MaxContactAvatarBytes = 5 << 20

type contactFileStorage interface {
	UploadFile(context.Context, int64, string, string, int64, io.Reader) (string, error)
	DeleteFile(context.Context, string) error
	GenerateSignedURL(string, time.Duration) string
}

func (s *OrganizationService) SetContactFileService(files *FileService) {
	if files != nil {
		s.files = files
	}
}

func (s *OrganizationService) refreshContactAvatar(contact *domain.Contact) {
	if contact.AvatarURL == nil || s.files == nil {
		return
	}
	parsed, err := url.Parse(*contact.AvatarURL)
	if err != nil {
		return
	}
	if strings.HasSuffix(parsed.Path, "/api/v1/files/download") {
		if object := parsed.Query().Get("object_name"); object != "" {
			signed := s.files.GenerateSignedURL(object, 24*time.Hour)
			contact.AvatarURL = &signed
		}
	}
}

func (s *OrganizationService) GetContactProfile(ctx context.Context, actor, target int64) (*domain.ContactProfile, error) {
	profile, err := s.repo.GetContactProfile(ctx, actor, target)
	if err != nil {
		return nil, err
	}
	s.refreshContactAvatar(&profile.Contact)
	for i := range profile.ManagementChain {
		s.refreshContactAvatar(&profile.ManagementChain[i])
	}
	if profile.Manager != nil {
		s.refreshContactAvatar(profile.Manager)
	}
	profile.Actions = buildContactActions(actor, profile)
	return profile, nil
}

func (s *OrganizationService) UpdateContact(ctx context.Context, actor, target int64, req domain.UpdateContactRequest) error {
	normalized, err := req.Normalize()
	if err != nil {
		return apperrors.Validation(err.Error())
	}
	return s.repo.UpdateContact(ctx, actor, target, normalized)
}

func (s *OrganizationService) DirectReports(ctx context.Context, actor, target int64, f domain.ContactFilter) (*domain.ContactList, error) {
	page, err := s.repo.ListContacts(ctx, actor, domain.ContactFilter{IDs: []int64{target}, Limit: 1})
	if err != nil {
		return nil, err
	}
	if len(page.Items) == 0 {
		return nil, apperrors.NotFound("contact")
	}
	f.IDs = nil
	f.ManagerID = &target
	return s.ListContacts(ctx, actor, f)
}

// normalizeContactAvatar validates actual image data and strips ancillary metadata.
func normalizeContactAvatar(reader io.Reader) ([]byte, string, error) {
	data, err := io.ReadAll(io.LimitReader(reader, MaxContactAvatarBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) == 0 || len(data) > MaxContactAvatarBytes {
		return nil, "", apperrors.Validation("avatar must be between 1 byte and 5 MiB")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "jpeg" && format != "png") {
		return nil, "", apperrors.Validation("avatar must be a valid JPEG or PNG")
	}
	if config.Width < 1 || config.Height < 1 || config.Width > 2048 || config.Height > 2048 {
		return nil, "", apperrors.Validation("avatar dimensions must be at most 2048 by 2048")
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", apperrors.Validation("invalid avatar image")
	}
	var output bytes.Buffer
	ext := ".png"
	if format == "jpeg" {
		ext = ".jpg"
		err = jpeg.Encode(&output, img, &jpeg.Options{Quality: 90})
	} else {
		err = png.Encode(&output, img)
	}
	if err != nil {
		return nil, "", err
	}
	if output.Len() > MaxContactAvatarBytes {
		return nil, "", apperrors.Validation("normalized avatar exceeds 5 MiB")
	}
	return output.Bytes(), ext, nil
}

func (s *OrganizationService) UploadContactAvatar(ctx context.Context, actor, target int64, reader io.Reader) (*domain.ContactProfile, error) {
	if actor != target {
		return nil, apperrors.Forbidden("only your own avatar can be updated")
	}
	if s.files == nil {
		return nil, apperrors.Internal("avatar storage is unavailable")
	}
	if _, err := s.repo.GetContactProfile(ctx, actor, target); err != nil {
		return nil, err
	}
	data, ext, err := normalizeContactAvatar(reader)
	if err != nil {
		return nil, err
	}
	object, err := s.files.UploadFile(ctx, actor, "avatar"+ext, "image", int64(len(data)), bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	avatar := s.files.GenerateSignedURL(object, 24*time.Hour)
	if err = s.repo.SetContactAvatar(ctx, actor, avatar); err != nil {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_ = s.files.DeleteFile(cleanup, object)
		return nil, err
	}
	return s.GetContactProfile(ctx, actor, target)
}
