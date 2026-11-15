package service

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

// allowedPhotoMimeTypes contains MIME types accepted for photo attachments.
var allowedPhotoMimeTypes = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/heic": true,
	"image/webp": true,
}

type NoteService struct {
	repo        *repository.NoteRepository
	fileService *FileService
	cache       *AICache
	logger      *zap.Logger
}

type NoteServiceOption func(*NoteService)

func WithNoteCache(cache *AICache) NoteServiceOption {
	return func(s *NoteService) {
		s.cache = cache
	}
}

func NewNoteService(repo *repository.NoteRepository, fileService *FileService, logger *zap.Logger, opts ...NoteServiceOption) *NoteService {
	s := &NoteService{
		repo:        repo,
		fileService: fileService,
		logger:      logger.With(zap.String("service", "notes")),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	return s
}

const noteListCacheTTL = 30 * time.Second

func noteListVersionKey(userID int64) string {
	return fmt.Sprintf("notes:list:v:%d", userID)
}

func noteListCacheKey(filter domain.NoteListFilter, version int64) string {
	folderID := ""
	if filter.FolderID != nil {
		folderID = filter.FolderID.String()
	}
	isDeleted := ""
	if filter.IsDeleted != nil {
		isDeleted = fmt.Sprintf("%t", *filter.IsDeleted)
	}
	isArchived := ""
	if filter.IsArchived != nil {
		isArchived = fmt.Sprintf("%t", *filter.IsArchived)
	}
	isFavorite := ""
	if filter.IsFavorite != nil {
		isFavorite = fmt.Sprintf("%t", *filter.IsFavorite)
	}

	raw, _ := json.Marshal([]any{filter.UserID, version, filter.Scope, folderID, isDeleted, isArchived, isFavorite,
		domain.NormalizeNoteTag(filter.Tag), strings.TrimSpace(filter.Query), filter.Limit, filter.Cursor})
	sum := sha1.Sum(raw)
	return "notes:list:" + hex.EncodeToString(sum[:])
}

func (s *NoteService) invalidateNoteListCache(ctx context.Context, userID int64) {
	if s.cache == nil {
		return
	}
	s.cache.Increment(ctx, noteListVersionKey(userID))
}

func (s *NoteService) invalidateNoteListCacheForUsers(ctx context.Context, userIDs ...int64) {
	seen := make(map[int64]struct{}, len(userIDs))
	for _, userID := range userIDs {
		if userID == 0 {
			continue
		}
		if _, ok := seen[userID]; ok {
			continue
		}
		seen[userID] = struct{}{}
		s.invalidateNoteListCache(ctx, userID)
	}
}

// ---------------------------------------------------------------------------
// Note CRUD
// ---------------------------------------------------------------------------

func (s *NoteService) CreateNote(ctx context.Context, userID int64, req *domain.CreateNoteRequest) (*domain.Note, error) {
	return s.createNote(ctx, userID, req, "")
}

// SaveMeetingNote imports the authenticated user's result, not a provider job.
// The job key is only an idempotency key; it grants no access to another user's data.
func (s *NoteService) SaveMeetingNote(ctx context.Context, userID int64, jobID string, req *domain.CreateNoteRequest) (*domain.Note, error) {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" || len(jobID) > 200 || strings.ContainsAny(jobID, "\r\n\x00") {
		return nil, apperrors.BadRequest("invalid meeting job id")
	}
	if req.ContentDoc == nil || strings.TrimSpace(req.ContentDoc.ExtractPlainText()) == "" {
		return nil, apperrors.Validation("meeting result must contain text")
	}
	return s.createNote(ctx, userID, req, jobID)
}

func (s *NoteService) createNote(ctx context.Context, userID int64, req *domain.CreateNoteRequest, jobID string) (*domain.Note, error) {
	if !domain.ValidNoteColor(req.Color) {
		return nil, apperrors.Validation("color must be #RRGGBB or empty")
	}
	if err := validateNotePayload(req.Type, req.ChecklistItems); err != nil {
		return nil, err
	}
	if req.ContentDoc != nil {
		if err := req.ContentDoc.Validate(); err != nil {
			return nil, mapContentDocValidationError(err)
		}
	}
	if req.FolderID != nil {
		if _, err := s.repo.GetFolderByID(ctx, userID, *req.FolderID); err != nil {
			return nil, mapNoteRepoError(err)
		}
	}

	now := time.Now()
	contentText := ""
	if req.ContentDoc != nil {
		contentText = req.ContentDoc.ExtractPlainText()
	}

	note := &domain.Note{
		Color:          req.Color,
		ID:             uuid.New(),
		UserID:         userID,
		FolderID:       req.FolderID,
		Title:          strings.TrimSpace(req.Title),
		Content:        req.Content,
		ContentDoc:     req.ContentDoc,
		ContentText:    contentText,
		Type:           normalizeNoteType(req.Type),
		ChecklistItems: normalizeChecklistItems(req.ChecklistItems),
		IsPinned:       req.IsPinned,
		IsArchived:     req.IsArchived,
		IsFavorite:     req.IsFavorite,
		Tags:           domain.NormalizeTags(req.Tags),
		IsDeleted:      false,
		Version:        1,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if jobID != "" {
		saved, created, err := s.repo.CreateMeetingNote(ctx, note, jobID)
		if err != nil {
			return nil, mapNoteRepoError(err)
		}
		if !created {
			if saved.IsDeleted {
				return nil, apperrors.Conflict("Заметка этого анализа находится в корзине. Восстановите её из корзины.")
			}
			return saved, nil
		}
	} else {
		if err := s.repo.CreateNote(ctx, note); err != nil {
			return nil, mapNoteRepoError(err)
		}
	}
	s.invalidateNoteListCache(ctx, userID)
	_ = s.repo.CreateAuditEvent(ctx, note.ID, userID, domain.NoteAuditEventCreate, map[string]any{"version": note.Version})
	return note, nil
}

func (s *NoteService) CreateNoteWithAttachments(
	ctx context.Context,
	userID int64,
	req *domain.CreateNoteRequest,
	attachments []CreateNoteAttachmentInput,
) (*domain.Note, error) {
	note, err := s.CreateNote(ctx, userID, req)
	if err != nil {
		return nil, err
	}

	uploadedFileIDs := make([]uuid.UUID, 0, len(attachments))
	for _, attachment := range attachments {
		item, uploadErr := s.UploadAttachment(ctx, userID, note.ID, attachment.FileType, attachment.Filename, attachment.Size, attachment.Reader)
		if uploadErr != nil {
			for _, fileID := range uploadedFileIDs {
				_ = s.DeleteAttachment(ctx, userID, note.ID, fileID)
			}
			if trashed, deleteErr := s.SoftDeleteNote(ctx, userID, note.ID, note.Version); deleteErr == nil {
				_ = s.HardDeleteNote(ctx, userID, note.ID, trashed.Version)
			}
			return nil, uploadErr
		}
		uploadedFileIDs = append(uploadedFileIDs, item.FileID)
	}

	return s.GetNote(ctx, userID, note.ID)
}

func (s *NoteService) GetNote(ctx context.Context, userID int64, noteID uuid.UUID) (*domain.Note, error) {
	note, err := s.repo.GetNoteByID(ctx, userID, noteID)
	if err != nil {
		return nil, mapNoteRepoError(err)
	}
	s.prepareNoteForViewer(note, userID)
	if err := s.attachAttachments(ctx, note); err != nil {
		return nil, err
	}
	return note, nil
}

func (s *NoteService) ListNotes(ctx context.Context, filter domain.NoteListFilter) (*domain.NoteListResponse, error) {
	filter.Scope = strings.TrimSpace(filter.Scope)
	switch filter.Scope {
	case "", "all", "owned", "shared":
	default:
		return nil, apperrors.Validation("scope must be all, owned, or shared")
	}
	if filter.Scope == "shared" && (filter.FolderID != nil || (filter.IsDeleted != nil && *filter.IsDeleted)) {
		return nil, apperrors.Validation("shared scope cannot be combined with folderId or isDeleted=true")
	}
	start := time.Now()
	cacheVersion := int64(0)
	cacheKey := ""
	if s.cache != nil {
		cacheVersion = s.cache.GetInt64(ctx, noteListVersionKey(filter.UserID))
		cacheKey = noteListCacheKey(filter, cacheVersion)
		var cached domain.NoteListResponse
		if hit, _ := s.cache.Get(ctx, cacheKey, &cached); hit {
			s.logger.Info("notes_list_built",
				zap.Int64("user_id", filter.UserID),
				zap.Int("items_count", len(cached.Items)),
				zap.Bool("cache_hit", true),
				zap.Int64("cache_version", cacheVersion),
				zap.Int64("total_ms", time.Since(start).Milliseconds()),
			)
			return &cached, nil
		}
	}

	notes, nextCursor, err := s.repo.ListNotes(ctx, filter)
	if err != nil {
		return nil, mapNoteRepoError(err)
	}
	notesFetchedAt := time.Now()
	if err := s.attachAttachmentsForNotes(ctx, filter.UserID, notes); err != nil {
		return nil, err
	}
	for i := range notes {
		s.prepareNoteForViewer(&notes[i], filter.UserID)
	}
	resp := &domain.NoteListResponse{Items: notes, NextCursor: nextCursor}
	if s.cache != nil && cacheKey != "" {
		s.cache.Set(ctx, cacheKey, resp, noteListCacheTTL)
	}

	s.logger.Info("notes_list_built",
		zap.Int64("user_id", filter.UserID),
		zap.Int("items_count", len(notes)),
		zap.Bool("cache_hit", false),
		zap.Int64("cache_version", cacheVersion),
		zap.Int64("notes_fetch_ms", notesFetchedAt.Sub(start).Milliseconds()),
		zap.Int64("total_ms", time.Since(start).Milliseconds()),
	)
	return resp, nil
}

func (s *NoteService) SearchNotes(ctx context.Context, filter domain.NoteListFilter) (*domain.NoteListResponse, error) {
	if strings.TrimSpace(filter.Query) == "" {
		return nil, apperrors.Validation("search query cannot be empty")
	}
	return s.ListNotes(ctx, filter)
}

func (s *NoteService) GetUserTags(ctx context.Context, userID int64) ([]domain.NoteTagSummary, error) {
	tags, err := s.repo.GetUserTags(ctx, userID)
	if err != nil {
		return nil, mapNoteRepoError(err)
	}
	return tags, nil
}

func (s *NoteService) UpdateNote(ctx context.Context, userID int64, noteID uuid.UUID, req *domain.UpdateNoteRequest) (*domain.Note, error) {
	if req.Color != nil && !domain.ValidNoteColor(*req.Color) {
		return nil, apperrors.Validation("color must be #RRGGBB or empty")
	}
	note, err := s.getEditableNote(ctx, userID, noteID)
	if err != nil {
		return nil, mapNoteRepoError(err)
	}
	if note.IsDeleted {
		return nil, apperrors.BadRequest("cannot update note in trash; restore it first")
	}

	if req.Title != nil {
		note.Title = strings.TrimSpace(*req.Title)
	}
	if req.Color != nil {
		note.Color = *req.Color
	}
	if req.Content != nil {
		note.Content = *req.Content
	}
	if req.ContentDoc != nil {
		if err := req.ContentDoc.Validate(); err != nil {
			return nil, mapContentDocValidationError(err)
		}
		note.ContentDoc = req.ContentDoc
		note.ContentText = req.ContentDoc.ExtractPlainText()
	}
	if req.Type != nil {
		note.Type = normalizeNoteType(*req.Type)
	}
	if req.ChecklistItems != nil {
		note.ChecklistItems = normalizeChecklistItems(*req.ChecklistItems)
	}
	if req.IsPinned != nil {
		note.IsPinned = *req.IsPinned
	}
	if req.IsArchived != nil {
		note.IsArchived = *req.IsArchived
	}
	if req.IsFavorite != nil {
		note.IsFavorite = *req.IsFavorite
	}
	if req.Tags != nil {
		note.Tags = domain.NormalizeTags(*req.Tags)
	}
	if req.FolderID != nil {
		if _, err := s.repo.GetFolderByID(ctx, userID, *req.FolderID); err != nil {
			return nil, mapNoteRepoError(err)
		}
		note.FolderID = req.FolderID
	}

	if err := validateNotePayload(note.Type, note.ChecklistItems); err != nil {
		return nil, err
	}

	if err := s.saveNoteWithVersionCheck(ctx, note, req.Version); err != nil {
		return nil, err
	}
	_ = s.repo.CreateAuditEvent(ctx, note.ID, userID, domain.NoteAuditEventUpdate, map[string]any{"version": note.Version})
	if err := s.attachAttachments(ctx, note); err != nil {
		return nil, err
	}
	return note, nil
}

func (s *NoteService) SetPinned(ctx context.Context, userID int64, noteID uuid.UUID, version int, pinned bool) (*domain.Note, error) {
	req := &domain.UpdateNoteRequest{IsPinned: &pinned, Version: version}
	return s.UpdateNote(ctx, userID, noteID, req)
}

func (s *NoteService) SetFavorite(ctx context.Context, userID int64, noteID uuid.UUID, version int, favorite bool) (*domain.Note, error) {
	req := &domain.UpdateNoteRequest{IsFavorite: &favorite, Version: version}
	return s.UpdateNote(ctx, userID, noteID, req)
}

func (s *NoteService) MoveNote(ctx context.Context, userID int64, noteID uuid.UUID, req *domain.MoveNoteRequest) (*domain.Note, error) {
	if req.FolderID != nil {
		if _, err := s.repo.GetFolderByID(ctx, userID, *req.FolderID); err != nil {
			return nil, mapNoteRepoError(err)
		}
	}
	note, err := s.getEditableNote(ctx, userID, noteID)
	if err != nil {
		return nil, mapNoteRepoError(err)
	}
	if note.IsDeleted {
		return nil, apperrors.BadRequest("cannot move note in trash; restore it first")
	}
	note.FolderID = req.FolderID
	if err := s.saveNoteWithVersionCheck(ctx, note, req.Version); err != nil {
		return nil, err
	}
	_ = s.repo.CreateAuditEvent(ctx, note.ID, userID, domain.NoteAuditEventUpdate, map[string]any{"version": note.Version, "action": "move"})
	if err := s.attachAttachments(ctx, note); err != nil {
		return nil, err
	}
	return note, nil
}

func (s *NoteService) SoftDeleteNote(ctx context.Context, userID int64, noteID uuid.UUID, version int) (*domain.Note, error) {
	note, err := s.getEditableNote(ctx, userID, noteID)
	if err != nil {
		return nil, mapNoteRepoError(err)
	}
	if note.IsDeleted {
		return nil, apperrors.BadRequest("note is already in trash")
	}
	now := time.Now()
	note.IsDeleted = true
	note.DeletedAt = &now
	note.IsPinned = false
	note.ExpiresAt = domain.CalculateTrashExpiresAt(&now)
	expectedVersion := note.Version
	if version > 0 {
		expectedVersion = version
	}
	if err := s.saveNoteWithVersionCheck(ctx, note, expectedVersion); err != nil {
		return nil, err
	}
	_ = s.repo.CreateAuditEvent(ctx, note.ID, userID, domain.NoteAuditEventDelete, map[string]any{"version": note.Version})
	if err := s.attachAttachments(ctx, note); err != nil {
		return nil, err
	}
	s.prepareNoteForViewer(note, userID)
	return note, nil
}

func (s *NoteService) RestoreNote(ctx context.Context, userID int64, noteID uuid.UUID, version int) (*domain.Note, error) {
	note, err := s.getEditableNote(ctx, userID, noteID)
	if err != nil {
		return nil, mapNoteRepoError(err)
	}
	if !note.IsDeleted {
		return nil, apperrors.BadRequest("note is not in trash")
	}
	if domain.IsTrashExpired(note.DeletedAt, time.Now()) {
		return nil, apperrors.BadRequest("cannot restore note: trash retention period has expired")
	}
	if note.FolderID != nil {
		if _, err := s.repo.GetFolderByID(ctx, userID, *note.FolderID); err != nil {
			note.FolderID = nil
		}
	}
	note.IsDeleted = false
	note.DeletedAt = nil
	note.ExpiresAt = nil
	expectedVersion := note.Version
	if version > 0 {
		expectedVersion = version
	}
	if err := s.saveNoteWithVersionCheck(ctx, note, expectedVersion); err != nil {
		return nil, err
	}
	_ = s.repo.CreateAuditEvent(ctx, note.ID, userID, domain.NoteAuditEventRestore, map[string]any{"version": note.Version})
	if err := s.attachAttachments(ctx, note); err != nil {
		return nil, err
	}
	s.prepareNoteForViewer(note, userID)
	return note, nil
}

func (s *NoteService) HardDeleteNote(ctx context.Context, userID int64, noteID uuid.UUID, version int) error {
	note, err := s.getEditableNote(ctx, userID, noteID)
	if err != nil {
		return err
	}
	expectedVersion := version
	if expectedVersion <= 0 {
		expectedVersion = note.Version
	}

	_, err = s.repo.HardDeleteNote(ctx, userID, noteID, expectedVersion)
	if err != nil {
		return mapNoteRepoError(err)
	}
	s.invalidateNoteListCache(ctx, userID)
	_ = s.repo.CreateAuditEvent(ctx, noteID, userID, domain.NoteAuditEventHardDelete, map[string]any{"version": expectedVersion})
	return nil
}

func (s *NoteService) AutosaveNote(ctx context.Context, userID int64, req *domain.AutosaveNoteRequest) (*domain.Note, error) {
	if req == nil {
		return nil, apperrors.Validation("request body is required")
	}
	if req.ID != nil && *req.ID != uuid.Nil && req.Version <= 0 {
		return nil, apperrors.Validation("positive version is required for autosave updates")
	}
	if req.Color != nil && !domain.ValidNoteColor(*req.Color) {
		return nil, apperrors.Validation("color must be #RRGGBB or empty")
	}
	if len(req.Title) > 4096 || len(req.Content) > 4<<20 {
		return nil, apperrors.Validation("note title or content is too large")
	}

	// Case 1: Create new note draft via autosave
	if req.ID == nil || *req.ID == uuid.Nil {
		color := ""
		if req.Color != nil {
			color = *req.Color
		}
		tags := []string{}
		if req.Tags != nil {
			tags = *req.Tags
		}
		fav := false
		if req.IsFavorite != nil {
			fav = *req.IsFavorite
		}
		checklist := []domain.ChecklistItem{}
		if req.ChecklistItems != nil {
			checklist = *req.ChecklistItems
		}
		createReq := domain.CreateNoteRequest{
			FolderID:       req.FolderID,
			Title:          req.Title,
			Content:        req.Content,
			ContentDoc:     req.ContentDoc,
			Type:           req.Type,
			ChecklistItems: checklist,
			Color:          color,
			Tags:           tags,
			IsFavorite:     fav,
		}
		return s.CreateNote(ctx, userID, &createReq)
	}

	// Case 2: Autosave existing note
	noteID := *req.ID
	note, err := s.getEditableNote(ctx, userID, noteID)
	if err != nil {
		return nil, mapNoteRepoError(err)
	}
	if note.IsDeleted {
		return nil, apperrors.BadRequest("cannot autosave note in trash; restore it first")
	}

	note.Title = strings.TrimSpace(req.Title)
	note.Content = req.Content
	if req.ContentDoc != nil {
		if err := req.ContentDoc.Validate(); err != nil {
			return nil, mapContentDocValidationError(err)
		}
		note.ContentDoc = req.ContentDoc
		note.ContentText = req.ContentDoc.ExtractPlainText()
	}
	if req.Type != "" {
		note.Type = normalizeNoteType(req.Type)
	}
	if req.ChecklistItems != nil {
		note.ChecklistItems = normalizeChecklistItems(*req.ChecklistItems)
	}
	if req.Color != nil {
		note.Color = *req.Color
	}
	if req.Tags != nil {
		note.Tags = domain.NormalizeTags(*req.Tags)
	}
	if req.IsFavorite != nil {
		note.IsFavorite = *req.IsFavorite
	}
	if req.FolderID != nil {
		if _, err := s.repo.GetFolderByID(ctx, userID, *req.FolderID); err != nil {
			return nil, mapNoteRepoError(err)
		}
		note.FolderID = req.FolderID
	}

	if err := validateNotePayload(note.Type, note.ChecklistItems); err != nil {
		return nil, err
	}

	expectedVersion := note.Version
	if req.Version > 0 {
		expectedVersion = req.Version
	}
	if err := s.saveNoteWithVersionCheck(ctx, note, expectedVersion); err != nil {
		return nil, err
	}
	_ = s.repo.CreateAuditEvent(ctx, note.ID, userID, domain.NoteAuditEventUpdate, map[string]any{"version": note.Version, "action": "autosave"})
	if err := s.attachAttachments(ctx, note); err != nil {
		return nil, err
	}
	s.prepareNoteForViewer(note, userID)
	return note, nil
}

func (s *NoteService) ListTrash(ctx context.Context, filter domain.NoteListFilter) (*domain.NoteListResponse, error) {
	isDeleted := true
	filter.IsDeleted = &isDeleted
	return s.ListNotes(ctx, filter)
}

func (s *NoteService) GetTrashCount(ctx context.Context, userID int64) (*domain.TrashSummaryResponse, error) {
	count, err := s.repo.GetTrashCount(ctx, userID, time.Now().Add(-domain.NoteTrashRetention))
	if err != nil {
		return nil, mapNoteRepoError(err)
	}
	return &domain.TrashSummaryResponse{Count: count}, nil
}

func (s *NoteService) EmptyTrash(ctx context.Context, userID int64) (*domain.EmptyTrashResponse, error) {
	purged, _, err := s.repo.EmptyTrash(ctx, userID)
	if err != nil {
		return nil, mapNoteRepoError(err)
	}
	s.invalidateNoteListCache(ctx, userID)
	_ = s.repo.CreateAuditEvent(ctx, uuid.Nil, userID, "empty_trash", map[string]any{"purged_count": purged})
	return &domain.EmptyTrashResponse{PurgedCount: purged}, nil
}

// RunObjectCleanup retries committed cleanup jobs, including after process restarts.
func (s *NoteService) RunObjectCleanup(ctx context.Context) {
	if s.fileService == nil {
		return
	}
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		batchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := s.repo.ExpirePendingAudioUploads(batchCtx, time.Now().Add(-time.Hour))
		if err == nil {
			err = s.repo.ProcessNoteObjectCleanup(batchCtx, s.fileService.DeleteFile)
		}
		cancel()
		if err != nil && ctx.Err() == nil {
			s.logger.Warn("note object cleanup batch failed", zap.Error(err))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *NoteService) CreateFolder(ctx context.Context, userID int64, req *domain.CreateNoteFolderRequest) (*domain.NoteFolder, error) {
	now := time.Now()
	folder := &domain.NoteFolder{
		ID:        uuid.New(),
		UserID:    userID,
		Name:      strings.TrimSpace(req.Name),
		SortOrder: req.SortOrder,
		Color:     strings.TrimSpace(req.Color),
		CreatedAt: now,
		UpdatedAt: now,
	}
	if folder.Name == "" {
		return nil, apperrors.Validation("folder name is required")
	}
	if folder.Color == "" {
		folder.Color = "#1687FF"
	}
	if err := s.repo.CreateFolder(ctx, folder); err != nil {
		return nil, mapNoteRepoError(err)
	}
	return folder, nil
}

func (s *NoteService) ListFolders(ctx context.Context, userID int64) ([]domain.NoteFolder, error) {
	return s.repo.ListFolders(ctx, userID)
}

func (s *NoteService) UpdateFolder(ctx context.Context, userID int64, folderID uuid.UUID, req *domain.UpdateNoteFolderRequest) (*domain.NoteFolder, error) {
	folder, err := s.repo.GetFolderByID(ctx, userID, folderID)
	if err != nil {
		return nil, mapNoteRepoError(err)
	}
	if req.Name != nil {
		folder.Name = strings.TrimSpace(*req.Name)
	}
	if req.SortOrder != nil {
		folder.SortOrder = *req.SortOrder
	}
	if req.Color != nil && strings.TrimSpace(*req.Color) != "" {
		folder.Color = strings.TrimSpace(*req.Color)
	}
	if folder.Name == "" {
		return nil, apperrors.Validation("folder name is required")
	}
	folder.UpdatedAt = time.Now()
	if err := s.repo.UpdateFolder(ctx, folder); err != nil {
		return nil, mapNoteRepoError(err)
	}
	return folder, nil
}

func (s *NoteService) DeleteFolder(ctx context.Context, userID int64, folderID uuid.UUID) error {
	if err := s.repo.DeleteFolder(ctx, userID, folderID); err != nil {
		return mapNoteRepoError(err)
	}
	s.invalidateNoteListCache(ctx, userID)
	return nil
}

// ---------------------------------------------------------------------------
// Sync
// ---------------------------------------------------------------------------

func (s *NoteService) ListSync(ctx context.Context, filter domain.NoteSyncFilter) (*domain.NoteSyncResponse, error) {
	notes, nextCursor, err := s.repo.ListSyncNotes(ctx, filter)
	if err != nil {
		return nil, mapNoteRepoError(err)
	}
	for i := range notes {
		s.prepareNoteForViewer(&notes[i], filter.UserID)
		if err := s.attachAttachments(ctx, &notes[i]); err != nil {
			return nil, err
		}
	}
	return &domain.NoteSyncResponse{Items: notes, NextCursor: nextCursor}, nil
}

func (s *NoteService) ApplySyncBatch(ctx context.Context, userID int64, req *domain.SyncBatchRequest) (*domain.SyncBatchResponse, error) {
	resp := &domain.SyncBatchResponse{}

	for _, item := range req.Upserts {
		note, err := s.applySyncUpsert(ctx, userID, item)
		if err != nil {
			return nil, err
		}
		resp.Upserts = append(resp.Upserts, *note)
	}
	for _, item := range req.Deletes {
		if _, err := s.SoftDeleteNote(ctx, userID, item.ID, item.Version); err != nil {
			return nil, err
		}
		resp.Deletes = append(resp.Deletes, item.ID)
	}
	for _, item := range req.Restores {
		if _, err := s.RestoreNote(ctx, userID, item.ID, item.Version); err != nil {
			return nil, err
		}
		resp.Restores = append(resp.Restores, item.ID)
	}
	for _, item := range req.HardDeletes {
		if err := s.HardDeleteNote(ctx, userID, item.ID, item.Version); err != nil {
			return nil, err
		}
		resp.HardDeletes = append(resp.HardDeletes, item.ID)
	}

	return resp, nil
}

// ---------------------------------------------------------------------------
// Legacy attachments (direct upload)
// ---------------------------------------------------------------------------

func (s *NoteService) UploadAttachment(ctx context.Context, userID int64, noteID uuid.UUID, fileType, filename string, size int64, reader io.Reader) (*domain.NoteAttachment, error) {
	if s.fileService == nil {
		return nil, apperrors.Internal("file service is unavailable")
	}
	if _, err := s.getEditableNote(ctx, userID, noteID); err != nil {
		return nil, mapNoteRepoError(err)
	}

	objectName, err := s.fileService.UploadFile(ctx, userID, filename, normalizeAttachmentType(fileType), size, reader)
	if err != nil {
		return nil, apperrors.ToAppError(err)
	}

	now := time.Now()
	file := &domain.DriveFile{
		ID:         uuid.New(),
		OwnerID:    userID,
		RealName:   filename,
		ObjectName: objectName,
		Size:       size,
		MimeType:   s.fileService.GetContentTypeByFilename(filename),
		Version:    1,
		Scope:      "drive",
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	if err := s.repo.AddAttachment(ctx, userID, noteID, file); err != nil {
		if delErr := s.fileService.DeleteFile(ctx, objectName); delErr != nil {
			s.logger.Warn("failed to rollback uploaded object", zap.String("object_name", objectName), zap.Error(delErr))
		}
		return nil, mapNoteRepoError(err)
	}
	s.invalidateNoteListCache(ctx, userID)

	url, err := s.fileService.GetFileURL(ctx, objectName, 24*time.Hour)
	if err != nil {
		return nil, apperrors.ToAppError(err)
	}

	return &domain.NoteAttachment{
		FileID:      file.ID,
		RealName:    filename,
		MimeType:    file.MimeType,
		Size:        size,
		ObjectName:  objectName,
		DownloadURL: url,
		CreatedAt:   now,
	}, nil
}

// UploadPhoto performs a direct multipart upload to the server and returns the updated note.
func (s *NoteService) UploadPhoto(ctx context.Context, userID int64, noteID uuid.UUID, filename string, size int64, reader io.Reader) (*domain.Note, error) {
	note, _, err := s.UploadPhotoWithID(ctx, userID, noteID, filename, size, reader)
	return note, err
}

// UploadPhotoWithID performs a direct multipart upload and returns both the updated note and the attachment ID.
func (s *NoteService) UploadPhotoWithID(ctx context.Context, userID int64, noteID uuid.UUID, filename string, size int64, reader io.Reader) (*domain.Note, uuid.UUID, error) {
	att, err := s.UploadAttachment(ctx, userID, noteID, "image", filename, size, reader)
	if err != nil {
		return nil, uuid.Nil, err
	}
	note, err := s.GetNote(ctx, userID, noteID)
	if err != nil {
		return nil, uuid.Nil, err
	}
	return note, att.FileID, nil
}

func (s *NoteService) ListAttachments(ctx context.Context, userID int64, noteID uuid.UUID) ([]domain.NoteAttachment, error) {
	items, err := s.repo.ListAttachments(ctx, userID, noteID)
	if err != nil {
		return nil, mapNoteRepoError(err)
	}
	if err := s.populateAttachmentURLs(ctx, items); err != nil {
		return nil, err
	}
	return items, nil
}
func (s *NoteService) DeleteAttachment(ctx context.Context, userID int64, noteID, fileID uuid.UUID) error {
	if _, err := s.getEditableNote(ctx, userID, noteID); err != nil {
		return err
	}
	attachment, err := s.repo.RemoveAttachment(ctx, userID, noteID, fileID)
	if err != nil {
		return mapNoteRepoError(err)
	}
	if s.fileService != nil && attachment.ObjectName != "" {
		if err := s.fileService.DeleteFile(ctx, attachment.ObjectName); err != nil {
			s.logger.Warn("failed to delete attachment object", zap.String("file_id", fileID.String()), zap.Error(err))
		}
	}
	s.invalidateNoteListCache(ctx, userID)
	return nil
}

// ---------------------------------------------------------------------------
// Photo attachments (presigned-URL flow)
// ---------------------------------------------------------------------------

// PresignPhotoUpload creates a pending NotePhotoAttachment and returns a presigned upload URL.
func (s *NoteService) PresignPhotoUpload(ctx context.Context, userID int64, noteID uuid.UUID, req *domain.PresignRequest) (*domain.PresignResponse, error) {
	if s.fileService == nil {
		return nil, apperrors.Internal("file service is unavailable")
	}
	if _, err := s.getEditableNote(ctx, userID, noteID); err != nil {
		return nil, mapNoteRepoError(err)
	}
	if !allowedPhotoMimeTypes[req.MimeType] {
		return nil, apperrors.New(apperrors.ErrCodeInvalidFileType, "allowed: image/jpeg, image/png, image/heic, image/webp", 400)
	}
	if req.SizeBytes > domain.NoteMaxPhotoSizeBytes {
		return nil, apperrors.FileTooBig(domain.NoteMaxPhotoSizeBytes)
	}

	attachmentID := uuid.New()
	storageKey := fmt.Sprintf("note-photos/%s/%s", noteID, attachmentID)

	// Generate a presigned URL valid for 15 minutes
	presignExpiry := 15 * time.Minute
	uploadURL, err := s.fileService.PresignPutURL(ctx, storageKey, presignExpiry)
	if err != nil {
		return nil, apperrors.ToAppError(err)
	}

	now := time.Now()
	pa := &domain.NotePhotoAttachment{
		ID:         attachmentID,
		NoteID:     noteID,
		UserID:     userID,
		StorageKey: storageKey,
		MimeType:   req.MimeType,
		SizeBytes:  req.SizeBytes,
		Status:     domain.NotePhotoStatusPending,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := s.repo.CreatePhotoAttachment(ctx, pa); err != nil {
		return nil, mapNoteRepoError(err)
	}

	_ = s.repo.CreateAuditEvent(ctx, noteID, userID, "attachment_uploaded", map[string]any{"attachment_id": attachmentID.String()})

	return &domain.PresignResponse{
		AttachmentID: attachmentID.String(),
		UploadURL:    uploadURL,
		StorageKey:   storageKey,
		ExpiresAt:    now.Add(presignExpiry),
	}, nil
}

// CompletePhotoUpload marks a pending photo attachment as uploaded.
func (s *NoteService) CompletePhotoUpload(ctx context.Context, userID int64, noteID, attachmentID uuid.UUID) (*domain.Note, error) {
	if _, err := s.getEditableNote(ctx, userID, noteID); err != nil {
		return nil, err
	}
	attachment, err := s.repo.GetPhotoAttachment(ctx, userID, noteID, attachmentID)
	if err != nil {
		return nil, mapNoteRepoError(err)
	}
	if attachment.Status == domain.NotePhotoStatusUploaded {
		return s.GetNote(ctx, userID, noteID)
	}
	if s.fileService == nil {
		return nil, apperrors.Internal("file service is unavailable")
	}
	if err := s.fileService.ObjectExists(ctx, attachment.StorageKey); err != nil {
		return nil, err
	}
	if err := s.repo.MarkPhotoAttachmentUploaded(ctx, attachmentID, nil, nil); err != nil {
		return nil, mapNoteRepoError(err)
	}
	s.invalidateNoteListCache(ctx, userID)
	note, err := s.GetNote(ctx, userID, noteID)
	if err != nil {
		return nil, err
	}
	_ = s.repo.CreateAuditEvent(ctx, noteID, userID, "attachment_uploaded_complete", map[string]any{"attachment_id": attachmentID.String()})
	return note, nil
}

func (s *NoteService) ShareNoteWithUsers(ctx context.Context, ownerID int64, noteID uuid.UUID, recipientIDs []int64) (*domain.Note, error) {
	note, err := s.repo.GetOwnedNoteByID(ctx, ownerID, noteID)
	if err != nil {
		return nil, mapNoteRepoError(err)
	}

	uniqueRecipients := make([]int64, 0, len(recipientIDs))
	seen := make(map[int64]struct{}, len(recipientIDs))
	for _, recipientID := range recipientIDs {
		if recipientID == ownerID {
			continue
		}
		if _, ok := seen[recipientID]; ok {
			continue
		}
		seen[recipientID] = struct{}{}
		uniqueRecipients = append(uniqueRecipients, recipientID)
	}

	if err := s.repo.GrantSharedAccess(ctx, noteID, ownerID, uniqueRecipients); err != nil {
		return nil, mapNoteRepoError(err)
	}
	s.invalidateNoteListCacheForUsers(ctx, append(uniqueRecipients, ownerID)...)

	_ = s.repo.CreateAuditEvent(ctx, noteID, ownerID, "share", map[string]any{
		"shared_with_user_ids": uniqueRecipients,
	})

	return note, nil
}

// DeletePhotoAttachment soft-deletes a photo attachment and attempts to remove it from storage.
func (s *NoteService) DeletePhotoAttachment(ctx context.Context, userID int64, noteID, attachmentID uuid.UUID) error {
	if _, err := s.getEditableNote(ctx, userID, noteID); err != nil {
		return err
	}
	storageKey, err := s.repo.DeletePhotoAttachment(ctx, userID, noteID, attachmentID)
	if err != nil {
		return mapNoteRepoError(err)
	}
	_ = s.repo.CreateAuditEvent(ctx, noteID, userID, "attachment_deleted", map[string]any{"attachment_id": attachmentID.String()})
	if s.fileService != nil && storageKey != "" {
		if err := s.fileService.DeleteFile(ctx, storageKey); err != nil {
			s.logger.Warn("failed to delete photo from storage", zap.String("key", storageKey), zap.Error(err))
		}
	}
	s.invalidateNoteListCache(ctx, userID)
	return nil
}

// ---------------------------------------------------------------------------
// Audio attachments & notes (NTS-004)
// ---------------------------------------------------------------------------

// UploadAudio performs direct multipart upload of audio or voice recording and attaches it to the note.
func (s *NoteService) UploadAudio(
	ctx context.Context,
	userID int64,
	noteID uuid.UUID,
	filename string,
	size int64,
	reader io.Reader,
	duration *float64,
	waveform []float64,
	isRecording bool,
) (*domain.Note, *domain.NoteAudioAttachment, error) {
	if s.fileService == nil {
		return nil, nil, apperrors.Internal("file service is unavailable")
	}
	if _, err := s.getEditableNote(ctx, userID, noteID); err != nil {
		return nil, nil, mapNoteRepoError(err)
	}
	if size > domain.NoteMaxAudioSizeBytes {
		return nil, nil, apperrors.FileTooBig(domain.NoteMaxAudioSizeBytes)
	}
	if size <= 0 {
		return nil, nil, apperrors.Validation("audio file must not be empty")
	}
	if err := validateAudioMetadata(duration, waveform); err != nil {
		return nil, nil, apperrors.Validation(err.Error())
	}

	cleanFilename := strings.TrimSpace(filename)
	if cleanFilename == "" || cleanFilename == "blob" {
		if isRecording {
			cleanFilename = "recording.webm"
		} else {
			cleanFilename = "audio.mp3"
		}
	}
	if filepath.Ext(cleanFilename) == "" {
		if isRecording {
			cleanFilename += ".webm"
		} else {
			cleanFilename += ".mp3"
		}
	}

	if !domain.IsAllowedAudioExtension(cleanFilename) {
		return nil, nil, apperrors.InvalidFileType("allowed: .mp3, .m4a, .wav, .webm, .ogg, .aac, .flac")
	}
	if err := validateAudioFilename(cleanFilename); err != nil {
		return nil, nil, apperrors.Validation(err.Error())
	}
	buffered := bufio.NewReader(reader)
	header, _ := buffered.Peek(64)
	if sniffAudioExtension(header) != strings.ToLower(filepath.Ext(cleanFilename)) {
		return nil, nil, apperrors.InvalidFileType("file content does not match its audio extension")
	}

	objectName, err := s.fileService.UploadFile(ctx, userID, cleanFilename, "audio", size, buffered)
	if err != nil {
		return nil, nil, apperrors.ToAppError(err)
	}

	now := time.Now()
	aa := &domain.NoteAudioAttachment{
		ID:          uuid.New(),
		NoteID:      noteID,
		UserID:      userID,
		StorageKey:  objectName,
		FileName:    cleanFilename,
		MimeType:    s.fileService.getContentType("audio", strings.ToLower(filepath.Ext(cleanFilename))),
		SizeBytes:   size,
		Duration:    duration,
		Waveform:    waveform,
		IsRecording: isRecording,
		Status:      domain.NoteAudioStatusUploaded,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := s.repo.CreateAudioAttachment(ctx, aa); err != nil {
		_ = s.fileService.DeleteFile(ctx, objectName)
		return nil, nil, mapNoteRepoError(err)
	}

	_ = s.repo.CreateAuditEvent(ctx, noteID, userID, "audio_uploaded", map[string]any{
		"attachment_id": aa.ID.String(),
		"is_recording":  isRecording,
		"file_name":     cleanFilename,
	})
	s.invalidateNoteListCache(ctx, userID)

	aa.DownloadURL, _ = s.fileService.GetFileURL(ctx, objectName, 24*time.Hour)
	note, err := s.GetNote(ctx, userID, noteID)
	if err != nil {
		return nil, nil, err
	}
	return note, aa, nil
}

// CreateAudioNote creates a new note of type "audio" with an uploaded audio file or dictaphone recording.
func (s *NoteService) CreateAudioNote(
	ctx context.Context,
	userID int64,
	req *domain.CreateAudioNoteRequest,
	filename string,
	size int64,
	reader io.Reader,
) (*domain.Note, error) {
	if s.fileService == nil {
		return nil, apperrors.Internal("file service is unavailable")
	}

	title := strings.TrimSpace(req.Title)
	if title == "" {
		if req.IsRecording {
			title = "Голосовая запись " + time.Now().Format("02.01.2006 15:04")
		} else if filename != "" && filename != "blob" {
			title = strings.TrimSuffix(filename, filepath.Ext(filename))
		} else {
			title = "Аудиозаметка " + time.Now().Format("02.01.2006 15:04")
		}
	}

	if req.Color != "" && !domain.ValidNoteColor(req.Color) {
		return nil, apperrors.Validation("color must be #RRGGBB or empty")
	}
	if req.FolderID != nil {
		if _, err := s.repo.GetFolderByID(ctx, userID, *req.FolderID); err != nil {
			return nil, mapNoteRepoError(err)
		}
	}

	now := time.Now()
	noteID := uuid.New()
	newNote := &domain.Note{
		ID:          noteID,
		UserID:      userID,
		FolderID:    req.FolderID,
		Title:       title,
		Content:     req.Content,
		ContentText: req.Content,
		Type:        domain.NoteTypeAudio,
		Color:       req.Color,
		Tags:        domain.NormalizeTags(req.Tags),
		IsPinned:    req.IsPinned,
		IsArchived:  req.IsArchived,
		IsFavorite:  req.IsFavorite,
		Version:     1,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := s.repo.CreateNote(ctx, newNote); err != nil {
		return nil, mapNoteRepoError(err)
	}

	_, audioAtt, err := s.UploadAudio(ctx, userID, noteID, filename, size, reader, req.Duration, req.Waveform, req.IsRecording)
	if err != nil {
		if trashed, deleteErr := s.SoftDeleteNote(ctx, userID, noteID, newNote.Version); deleteErr == nil {
			_ = s.HardDeleteNote(ctx, userID, noteID, trashed.Version)
		}
		return nil, err
	}

	// Build default content_doc with audio block
	newNote.ContentDoc = &domain.ContentDoc{
		Type:    "doc",
		Version: 1,
		Blocks: []domain.ContentBlock{
			{
				ID:           "audio-" + audioAtt.ID.String()[:8],
				Type:         domain.NoteBlockTypeAudio,
				AttachmentID: audioAtt.ID.String(),
				Duration:     req.Duration,
				Waveform:     req.Waveform,
				IsRecording:  req.IsRecording,
				Caption:      title,
			},
		},
	}
	if req.Content != "" {
		newNote.ContentDoc.Blocks = append(newNote.ContentDoc.Blocks, domain.ContentBlock{
			ID:   "p-1",
			Type: domain.NoteBlockTypeParagraph,
			Runs: []domain.ContentRun{{Text: req.Content}},
		})
	}
	newNote.ContentText = newNote.ContentDoc.ExtractPlainText()
	if err := s.repo.SaveNote(ctx, newNote, newNote.Version); err != nil {
		if trashed, deleteErr := s.SoftDeleteNote(ctx, userID, noteID, newNote.Version); deleteErr == nil {
			_ = s.HardDeleteNote(ctx, userID, noteID, trashed.Version)
		}
		return nil, mapNoteRepoError(err)
	}

	_ = s.repo.CreateAuditEvent(ctx, noteID, userID, domain.NoteAuditEventCreate, map[string]any{
		"type":          domain.NoteTypeAudio,
		"attachment_id": audioAtt.ID.String(),
		"is_recording":  req.IsRecording,
	})

	return s.GetNote(ctx, userID, noteID)
}

// PresignAudioUpload creates a pending NoteAudioAttachment and returns a presigned upload URL.
func (s *NoteService) PresignAudioUpload(ctx context.Context, userID int64, noteID uuid.UUID, req *domain.PresignAudioRequest) (*domain.PresignResponse, error) {
	if s.fileService == nil {
		return nil, apperrors.Internal("file service is unavailable")
	}
	if _, err := s.getEditableNote(ctx, userID, noteID); err != nil {
		return nil, mapNoteRepoError(err)
	}
	if !domain.IsAllowedAudioMimeType(req.MimeType) {
		return nil, apperrors.New(apperrors.ErrCodeInvalidFileType, "allowed: mp3, m4a, wav, webm, ogg, aac, flac", 400)
	}
	if req.SizeBytes > domain.NoteMaxAudioSizeBytes {
		return nil, apperrors.FileTooBig(domain.NoteMaxAudioSizeBytes)
	}
	if req.SizeBytes <= 0 {
		return nil, apperrors.Validation("size_bytes must be positive")
	}
	if err := validateAudioMetadata(req.Duration, nil); err != nil {
		return nil, apperrors.Validation(err.Error())
	}

	attachmentID := uuid.New()
	ext := audioExtensionForMIME(req.MimeType)
	if req.FileName != "" {
		if err := validateAudioFilename(req.FileName); err != nil {
			return nil, apperrors.Validation(err.Error())
		}
		if strings.ToLower(filepath.Ext(req.FileName)) != ext {
			return nil, apperrors.Validation("file extension does not match mime_type")
		}
	}

	storageKey := fmt.Sprintf("note-audios/%s/%s%s", noteID, attachmentID, ext)
	presignExpiry := 15 * time.Minute
	uploadURL, err := s.fileService.PresignPutURL(ctx, storageKey, presignExpiry)
	if err != nil {
		return nil, apperrors.ToAppError(err)
	}

	now := time.Now()
	cleanName := req.FileName
	if cleanName == "" {
		cleanName = fmt.Sprintf("audio%s", ext)
	}
	aa := &domain.NoteAudioAttachment{
		ID:          attachmentID,
		NoteID:      noteID,
		UserID:      userID,
		StorageKey:  storageKey,
		FileName:    cleanName,
		MimeType:    req.MimeType,
		SizeBytes:   req.SizeBytes,
		Duration:    req.Duration,
		IsRecording: req.IsRecording,
		Status:      domain.NoteAudioStatusPending,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.repo.CreateAudioAttachment(ctx, aa); err != nil {
		return nil, mapNoteRepoError(err)
	}

	_ = s.repo.CreateAuditEvent(ctx, noteID, userID, "audio_presigned", map[string]any{"attachment_id": attachmentID.String()})

	return &domain.PresignResponse{
		AttachmentID: attachmentID.String(),
		UploadURL:    uploadURL,
		StorageKey:   storageKey,
		ExpiresAt:    now.Add(presignExpiry),
	}, nil
}

// CompleteAudioUpload confirms successful upload to storage and marks attachment uploaded.
func (s *NoteService) CompleteAudioUpload(ctx context.Context, userID int64, noteID, attachmentID uuid.UUID, req *domain.CompleteAudioRequest) (*domain.Note, error) {
	attachment, err := s.repo.GetAudioAttachment(ctx, userID, noteID, attachmentID)
	if err != nil {
		return nil, mapNoteRepoError(err)
	}
	if attachment.UserID != userID {
		return nil, apperrors.Forbidden("only the owner can complete an audio upload")
	}
	if attachment.Status == domain.NoteAudioStatusUploaded {
		return s.GetNote(ctx, userID, noteID)
	}
	if s.fileService == nil {
		return nil, apperrors.Internal("file service is unavailable")
	}
	if err := s.checkStoredAudio(ctx, attachment.StorageKey, attachment.SizeBytes); err != nil {
		_ = s.repo.EnqueueAudioObjectCleanup(ctx, attachment.StorageKey)
		return nil, err
	}

	var duration *float64
	var waveform []float64
	var fileName *string
	if req != nil {
		duration = req.Duration
		waveform = req.Waveform
		fileName = req.FileName
	}
	if err := validateAudioMetadata(duration, waveform); err != nil {
		return nil, apperrors.Validation(err.Error())
	}
	if fileName != nil {
		if err := validateAudioFilename(*fileName); err != nil {
			return nil, apperrors.Validation(err.Error())
		}
		if strings.ToLower(filepath.Ext(*fileName)) != strings.ToLower(filepath.Ext(attachment.StorageKey)) {
			return nil, apperrors.Validation("file extension does not match uploaded audio")
		}
	}

	finalKey := fmt.Sprintf("note-audios/final/%s/%s%s", noteID, uuid.NewString(), strings.ToLower(filepath.Ext(attachment.StorageKey)))
	if err := s.fileService.CopyObject(ctx, attachment.StorageKey, finalKey); err != nil {
		return nil, err
	}
	if err := s.checkStoredAudio(ctx, finalKey, attachment.SizeBytes); err != nil {
		_ = s.repo.EnqueueAudioObjectCleanup(ctx, finalKey)
		_ = s.repo.EnqueueAudioObjectCleanup(ctx, attachment.StorageKey)
		return nil, err
	}
	if err := s.repo.FinalizeAudioAttachment(ctx, attachmentID, attachment.StorageKey, finalKey, duration, waveform, fileName); err != nil {
		_ = s.repo.EnqueueAudioObjectCleanup(ctx, finalKey)
		return nil, mapNoteRepoError(err)
	}
	s.invalidateNoteListCache(ctx, userID)
	note, err := s.GetNote(ctx, userID, noteID)
	if err != nil {
		return nil, err
	}
	_ = s.repo.CreateAuditEvent(ctx, noteID, userID, "audio_uploaded_complete", map[string]any{"attachment_id": attachmentID.String()})
	return note, nil
}

func (s *NoteService) checkStoredAudio(ctx context.Context, key string, expectedSize int64) error {
	obj, _, actualSize, _, err := s.fileService.OpenObject(ctx, key)
	if err != nil {
		return err
	}
	defer obj.Close()
	if actualSize != expectedSize || actualSize <= 0 || actualSize > domain.NoteMaxAudioSizeBytes {
		return apperrors.Validation("uploaded audio does not match declared size")
	}
	header := make([]byte, 64)
	n, readErr := io.ReadFull(obj, header)
	if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
		return apperrors.ToAppError(readErr)
	}
	if sniffAudioExtension(header[:n]) != strings.ToLower(filepath.Ext(key)) {
		return apperrors.Validation("uploaded audio does not match declared format")
	}
	return nil
}

// GetAudioAttachment retrieves a single audio attachment by ID.
func (s *NoteService) GetAudioAttachment(ctx context.Context, userID int64, noteID, attachmentID uuid.UUID) (*domain.NoteAudioAttachment, error) {
	att, err := s.repo.GetAudioAttachment(ctx, userID, noteID, attachmentID)
	if err != nil {
		return nil, mapNoteRepoError(err)
	}
	if s.fileService != nil && att.StorageKey != "" && att.Status == domain.NoteAudioStatusUploaded {
		att.DownloadURL, _ = s.fileService.GetFileURL(ctx, att.StorageKey, 24*time.Hour)
	}
	return att, nil
}

// ListAudioAttachments lists all audio attachments for a note.
func (s *NoteService) ListAudioAttachments(ctx context.Context, userID int64, noteID uuid.UUID) ([]domain.NoteAudioAttachment, error) {
	if _, err := s.repo.GetNoteByID(ctx, userID, noteID); err != nil {
		return nil, mapNoteRepoError(err)
	}
	items, err := s.repo.ListAudioAttachments(ctx, userID, noteID)
	if err != nil {
		return nil, mapNoteRepoError(err)
	}
	if err := s.populateAudioAttachmentURLs(ctx, items); err != nil {
		return nil, err
	}
	return items, nil
}

// DeleteAudioAttachment soft-deletes an audio attachment and removes it from storage.
func (s *NoteService) DeleteAudioAttachment(ctx context.Context, userID int64, noteID, attachmentID uuid.UUID) error {
	if _, err := s.getEditableNote(ctx, userID, noteID); err != nil {
		return err
	}
	_, err := s.repo.DeleteAudioAttachment(ctx, userID, noteID, attachmentID)
	if err != nil {
		return mapNoteRepoError(err)
	}
	_ = s.repo.CreateAuditEvent(ctx, noteID, userID, "audio_deleted", map[string]any{"attachment_id": attachmentID.String()})
	s.invalidateNoteListCache(ctx, userID)
	return nil
}

// OpenAudioObject retrieves an audio object for range-based streaming.
func (s *NoteService) OpenAudioObject(ctx context.Context, userID int64, noteID, attachmentID uuid.UUID) (*minio.Object, string, int64, string, time.Time, error) {
	att, err := s.repo.GetAudioAttachment(ctx, userID, noteID, attachmentID)
	if err != nil {
		return nil, "", 0, "", time.Time{}, mapNoteRepoError(err)
	}
	if att.Status != domain.NoteAudioStatusUploaded {
		return nil, "", 0, "", time.Time{}, apperrors.NotFound("audio is not ready")
	}
	if s.fileService == nil {
		return nil, "", 0, "", time.Time{}, apperrors.Internal("file service is unavailable")
	}
	obj, contentType, size, modTime, err := s.fileService.OpenObject(ctx, att.StorageKey)
	if err != nil {
		return nil, "", 0, "", time.Time{}, mapNoteRepoError(err)
	}
	if att.MimeType != "" {
		contentType = att.MimeType
	}
	fileName := att.FileName
	if fileName == "" {
		fileName = filepath.Base(att.StorageKey)
	}
	return obj, contentType, size, fileName, modTime, nil
}

func (s *NoteService) populateAudioAttachmentURLs(ctx context.Context, items []domain.NoteAudioAttachment) error {
	if s.fileService == nil {
		return nil
	}
	for i := range items {
		url, err := s.fileService.GetFileURL(ctx, items[i].StorageKey, 24*time.Hour)
		if err != nil {
			return apperrors.ToAppError(err)
		}
		items[i].DownloadURL = url
	}
	return nil
}

func (s *NoteService) mergeAudioAttachments(ctx context.Context, attachments []domain.NoteAttachment, audios []domain.NoteAudioAttachment) ([]domain.NoteAttachment, error) {
	out := make([]domain.NoteAttachment, 0, len(attachments)+len(audios))
	out = append(out, attachments...)
	for _, audio := range audios {
		downloadURL := audio.DownloadURL
		if downloadURL == "" && s.fileService != nil {
			var err error
			downloadURL, err = s.fileService.GetFileURL(ctx, audio.StorageKey, 24*time.Hour)
			if err != nil {
				return nil, err
			}
		}
		out = append(out, domain.NoteAttachment{
			FileID:      audio.ID,
			ID:          audio.ID,
			RealName:    audio.FileName,
			MimeType:    audio.MimeType,
			Size:        audio.SizeBytes,
			DownloadURL: downloadURL,
			ObjectName:  audio.StorageKey,
			Duration:    audio.Duration,
			Waveform:    audio.Waveform,
			IsRecording: audio.IsRecording,
			Status:      audio.Status,
			CreatedAt:   audio.CreatedAt,
		})
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

func (s *NoteService) applySyncUpsert(ctx context.Context, userID int64, item domain.SyncNoteUpsertRequest) (*domain.Note, error) {
	if item.Color != nil && !domain.ValidNoteColor(*item.Color) {
		return nil, apperrors.Validation("color must be #RRGGBB or empty")
	}
	if item.ID == uuid.Nil {
		return nil, apperrors.Validation("sync upsert id is required")
	}
	if err := validateNotePayload(item.Type, item.ChecklistItems); err != nil {
		return nil, err
	}
	if item.ContentDoc != nil {
		if err := item.ContentDoc.Validate(); err != nil {
			return nil, mapContentDocValidationError(err)
		}
	}
	if item.FolderID != nil {
		if _, err := s.repo.GetFolderByID(ctx, userID, *item.FolderID); err != nil {
			return nil, mapNoteRepoError(err)
		}
	}

	contentText := ""
	if item.ContentDoc != nil {
		contentText = item.ContentDoc.ExtractPlainText()
	}

	note, err := s.repo.GetNoteByID(ctx, userID, item.ID)
	if err != nil {
		if isNotFoundErr(err) {
			now := time.Now()
			if item.UpdatedAt != nil {
				now = *item.UpdatedAt
			}
			newNote := &domain.Note{
				ID:             item.ID,
				UserID:         userID,
				FolderID:       item.FolderID,
				Title:          strings.TrimSpace(item.Title),
				Content:        item.Content,
				ContentDoc:     item.ContentDoc,
				ContentText:    contentText,
				Type:           normalizeNoteType(item.Type),
				ChecklistItems: normalizeChecklistItems(item.ChecklistItems),
				IsPinned:       item.IsPinned,
				IsArchived:     item.IsArchived,
				Tags:           domain.NormalizeTags(item.Tags),
				IsDeleted:      item.IsDeleted,
				DeletedAt:      item.DeletedAt,
				Version:        maxInt(item.Version, 1),
				CreatedAt:      now,
				UpdatedAt:      now,
			}
			if item.Color != nil {
				newNote.Color = *item.Color
			}
			if item.IsFavorite != nil {
				newNote.IsFavorite = *item.IsFavorite
			}
			if err := s.repo.CreateNote(ctx, newNote); err != nil {
				return nil, mapNoteRepoError(err)
			}
			if err := s.attachAttachments(ctx, newNote); err != nil {
				return nil, err
			}
			return newNote, nil
		}
		return nil, mapNoteRepoError(err)
	}

	if note.UserID != userID {
		return nil, apperrors.Forbidden("only the owner can edit this note")
	}
	if item.Color != nil {
		note.Color = *item.Color
	}
	note.FolderID = item.FolderID
	note.Title = strings.TrimSpace(item.Title)
	note.Content = item.Content
	note.ContentDoc = item.ContentDoc
	note.ContentText = contentText
	note.Type = normalizeNoteType(item.Type)
	note.ChecklistItems = normalizeChecklistItems(item.ChecklistItems)
	note.IsPinned = item.IsPinned
	note.IsArchived = item.IsArchived
	if item.IsFavorite != nil {
		note.IsFavorite = *item.IsFavorite
	}
	if item.Tags != nil {
		note.Tags = domain.NormalizeTags(item.Tags)
	}
	note.IsDeleted = item.IsDeleted
	note.DeletedAt = item.DeletedAt
	if err := s.saveNoteWithVersionCheck(ctx, note, item.Version); err != nil {
		return nil, err
	}
	if err := s.attachAttachments(ctx, note); err != nil {
		return nil, err
	}
	return note, nil
}

func (s *NoteService) attachAttachments(ctx context.Context, note *domain.Note) error {
	attachments, err := s.repo.ListAttachments(ctx, note.UserID, note.ID)
	if err != nil {
		return mapNoteRepoError(err)
	}
	if err := s.populateAttachmentURLs(ctx, attachments); err != nil {
		return err
	}

	photoAttachments, err := s.repo.ListPhotoAttachments(ctx, note.UserID, note.ID)
	if err != nil {
		return mapNoteRepoError(err)
	}
	merged, err := s.mergePhotoAttachments(ctx, attachments, photoAttachments)
	if err != nil {
		return err
	}

	audioAttachments, err := s.repo.ListAudioAttachments(ctx, note.UserID, note.ID)
	if err != nil {
		return mapNoteRepoError(err)
	}
	if err := s.populateAudioAttachmentURLs(ctx, audioAttachments); err != nil {
		return err
	}
	note.AudioAttachments = audioAttachments

	mergedAudio, err := s.mergeAudioAttachments(ctx, merged, audioAttachments)
	if err != nil {
		return err
	}
	note.Attachments = mergedAudio
	return nil
}

func (s *NoteService) attachAttachmentsForNotes(ctx context.Context, viewerID int64, notes []domain.Note) error {
	if len(notes) == 0 {
		return nil
	}

	noteIDs := make([]uuid.UUID, 0, len(notes))
	for i := range notes {
		noteIDs = append(noteIDs, notes[i].ID)
	}

	attachmentsByNoteID, err := s.repo.ListAttachmentsForNotes(ctx, viewerID, noteIDs)
	if err != nil {
		return mapNoteRepoError(err)
	}
	photoAttachmentsByNoteID, err := s.repo.ListPhotoAttachmentsForNotes(ctx, viewerID, noteIDs)
	if err != nil {
		return mapNoteRepoError(err)
	}
	audioAttachmentsByNoteID, err := s.repo.ListAudioAttachmentsForNotes(ctx, viewerID, noteIDs)
	if err != nil {
		return mapNoteRepoError(err)
	}

	for i := range notes {
		attachments := attachmentsByNoteID[notes[i].ID]
		if err := s.populateAttachmentURLs(ctx, attachments); err != nil {
			return err
		}
		merged, err := s.mergePhotoAttachments(ctx, attachments, photoAttachmentsByNoteID[notes[i].ID])
		if err != nil {
			return err
		}

		audios := audioAttachmentsByNoteID[notes[i].ID]
		if err := s.populateAudioAttachmentURLs(ctx, audios); err != nil {
			return err
		}
		notes[i].AudioAttachments = audios

		mergedAudio, err := s.mergeAudioAttachments(ctx, merged, audios)
		if err != nil {
			return err
		}
		notes[i].Attachments = mergedAudio
	}

	return nil
}

func (s *NoteService) DeleteTableColumn(ctx context.Context, userID int64, noteID uuid.UUID, blockID string, colIndex int, version int) (*domain.Note, error) {
	note, tableBlock, err := s.loadMutableTableBlock(ctx, userID, noteID, blockID)
	if err != nil {
		return nil, err
	}
	if len(tableBlock.Rows) == 0 || len(tableBlock.Rows[0]) == 0 {
		return nil, apperrors.TableBlockNotFound()
	}
	if colIndex < 0 || colIndex >= len(tableBlock.Rows[0]) {
		return nil, apperrors.TableColumnOutOfRange()
	}
	if len(tableBlock.Rows[0]) == 1 {
		return nil, apperrors.TableMinSizeReached()
	}

	for rowIdx := range tableBlock.Rows {
		if colIndex >= len(tableBlock.Rows[rowIdx]) {
			return nil, apperrors.TableColumnOutOfRange()
		}
		tableBlock.Rows[rowIdx] = append(tableBlock.Rows[rowIdx][:colIndex], tableBlock.Rows[rowIdx][colIndex+1:]...)
	}

	note.ContentText = note.ContentDoc.ExtractPlainText()
	if err := s.saveNoteWithVersionCheck(ctx, note, version); err != nil {
		return nil, err
	}
	_ = s.repo.CreateAuditEvent(ctx, note.ID, userID, domain.NoteAuditEventUpdate, map[string]any{
		"version":   note.Version,
		"action":    "delete_table_column",
		"block_id":  blockID,
		"col_index": colIndex,
	})
	if err := s.attachAttachments(ctx, note); err != nil {
		return nil, err
	}
	return note, nil
}

func (s *NoteService) ClearTableCell(ctx context.Context, userID int64, noteID uuid.UUID, blockID string, rowIndex, colIndex, version int) (*domain.Note, error) {
	note, tableBlock, err := s.loadMutableTableBlock(ctx, userID, noteID, blockID)
	if err != nil {
		return nil, err
	}
	if rowIndex < 0 || rowIndex >= len(tableBlock.Rows) {
		return nil, apperrors.TableRowOutOfRange()
	}
	if colIndex < 0 || colIndex >= len(tableBlock.Rows[rowIndex]) {
		return nil, apperrors.TableColumnOutOfRange()
	}

	tableBlock.Rows[rowIndex][colIndex] = domain.TableCell{}
	note.ContentText = note.ContentDoc.ExtractPlainText()
	if err := s.saveNoteWithVersionCheck(ctx, note, version); err != nil {
		return nil, err
	}
	_ = s.repo.CreateAuditEvent(ctx, note.ID, userID, domain.NoteAuditEventUpdate, map[string]any{
		"version":   note.Version,
		"action":    "clear_table_cell",
		"block_id":  blockID,
		"row_index": rowIndex,
		"col_index": colIndex,
	})
	if err := s.attachAttachments(ctx, note); err != nil {
		return nil, err
	}
	return note, nil
}

func (s *NoteService) loadMutableTableBlock(ctx context.Context, userID int64, noteID uuid.UUID, blockID string) (*domain.Note, *domain.ContentBlock, error) {
	note, err := s.getEditableNote(ctx, userID, noteID)
	if err != nil {
		return nil, nil, mapNoteRepoError(err)
	}
	if note.ContentDoc == nil {
		return nil, nil, apperrors.TableBlockNotFound()
	}
	for i := range note.ContentDoc.Blocks {
		block := &note.ContentDoc.Blocks[i]
		if block.ID == blockID && block.Type == domain.NoteBlockTypeTable {
			return note, block, nil
		}
	}
	return nil, nil, apperrors.TableBlockNotFound()
}

// Shared access permits reading only. Check the authenticated user before any
// mutation, while keeping inaccessible notes indistinguishable from missing ones.
func (s *NoteService) getEditableNote(ctx context.Context, userID int64, noteID uuid.UUID) (*domain.Note, error) {
	note, err := s.repo.GetNoteByID(ctx, userID, noteID)
	if err != nil {
		return nil, mapNoteRepoError(err)
	}
	if note.UserID != userID {
		return nil, apperrors.Forbidden("only the owner can edit this note")
	}
	return note, nil
}

func (s *NoteService) saveNoteWithVersionCheck(ctx context.Context, note *domain.Note, expectedVersion int) error {
	if err := s.repo.SaveNote(ctx, note, expectedVersion); err != nil {
		appErr, _ := mapNoteRepoError(err).(*apperrors.AppError)
		if appErr != nil && appErr.Code == apperrors.ErrCodeNoteVersionConflict {
			serverNote, getErr := s.repo.GetNoteByID(ctx, note.UserID, note.ID)
			if getErr == nil {
				appErr = appErr.WithDetail("server_note", map[string]any{
					"id":          serverNote.ID,
					"version":     serverNote.Version,
					"title":       serverNote.Title,
					"content":     serverNote.Content,
					"content_doc": serverNote.ContentDoc,
					"type":        serverNote.Type,
					"updated_at":  serverNote.UpdatedAt,
				})
			}
		}
		return appErr
	}
	s.invalidateNoteListCache(ctx, note.UserID)
	return nil
}

func (s *NoteService) populateAttachmentURLs(ctx context.Context, items []domain.NoteAttachment) error {
	if s.fileService == nil {
		return nil
	}
	for i := range items {
		url, err := s.fileService.GetFileURL(ctx, items[i].ObjectName, 24*time.Hour)
		if err != nil {
			return apperrors.ToAppError(err)
		}
		items[i].DownloadURL = url
	}
	return nil
}

func (s *NoteService) mergePhotoAttachments(ctx context.Context, attachments []domain.NoteAttachment, photos []domain.NotePhotoAttachment) ([]domain.NoteAttachment, error) {
	out := make([]domain.NoteAttachment, 0, len(attachments)+len(photos))
	out = append(out, attachments...)
	if len(photos) > 0 && s.fileService == nil {
		return nil, apperrors.Internal("file service is unavailable")
	}
	for _, photo := range photos {
		downloadURL, err := s.fileService.GetFileURL(ctx, photo.StorageKey, 24*time.Hour)
		if err != nil {
			return nil, err
		}
		out = append(out, domain.NoteAttachment{
			FileID:      photo.ID,
			RealName:    "",
			MimeType:    photo.MimeType,
			Size:        photo.SizeBytes,
			DownloadURL: downloadURL,
			ObjectName:  photo.StorageKey,
			CreatedAt:   photo.CreatedAt,
		})
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Input types
// ---------------------------------------------------------------------------

type CreateNoteAttachmentInput struct {
	FileType string
	Filename string
	Size     int64
	Reader   io.Reader
}

// ---------------------------------------------------------------------------
// Validation helpers
// ---------------------------------------------------------------------------

func validateNotePayload(noteType string, items []domain.ChecklistItem) error {
	noteType = normalizeNoteType(noteType)
	if noteType != domain.NoteTypeText && noteType != domain.NoteTypeChecklist && noteType != domain.NoteTypeRich && noteType != domain.NoteTypeAudio {
		return apperrors.Validation("invalid note type")
	}
	if (noteType == domain.NoteTypeText || noteType == domain.NoteTypeAudio) && len(items) > 0 {
		return apperrors.Validation("checklist items are allowed only for checklist notes")
	}
	return nil
}

func normalizeChecklistItems(items []domain.ChecklistItem) []domain.ChecklistItem {
	if len(items) == 0 {
		return []domain.ChecklistItem{}
	}
	out := make([]domain.ChecklistItem, len(items))
	for i, item := range items {
		if item.ID == uuid.Nil {
			item.ID = uuid.New()
		}
		item.Text = strings.TrimSpace(item.Text)
		item.Position = i
		out[i] = item
	}
	return out
}

func normalizeNoteType(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return domain.NoteTypeText
	}
	return value
}

func normalizeAttachmentType(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	switch value {
	case "image", "video", "document", "file", "audio", "voice":
		return value
	default:
		return "file"
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (s *NoteService) prepareNoteForViewer(note *domain.Note, viewerID int64) {
	if note == nil {
		return
	}
	if note.IsDeleted && note.DeletedAt != nil {
		note.ExpiresAt = domain.CalculateTrashExpiresAt(note.DeletedAt)
	}
	if note.UserID == viewerID {
		return
	}

	note.FolderID = nil
	note.IsPinned = false
}

func isNotFoundErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "not found") || strings.Contains(msg, "no rows") || strings.Contains(msg, "violates foreign key")
}

func mapNoteRepoError(err error) error {
	if err == nil {
		return nil
	}
	if appErr := apperrors.GetAppError(err); appErr != nil {
		return appErr
	}

	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "note must be in trash"):
		return apperrors.BadRequest("note must be in trash")
	case strings.Contains(msg, "version conflict"):
		return apperrors.NoteVersionConflict()
	case strings.Contains(msg, "folder not found"):
		return apperrors.NoteFolderNotFound()
	case strings.Contains(msg, "attachment not found"):
		return apperrors.AttachmentNotFound()
	case strings.Contains(msg, "note not found"):
		return apperrors.NoteNotFound()
	default:
		return apperrors.Internal("An unexpected error occurred").WithError(err)
	}
}

func mapContentDocValidationError(err error) error {
	var validationErr *domain.ContentValidationError
	if errors.As(err, &validationErr) {
		switch validationErr.Code {
		case domain.ContentValidationInvalidMark:
			return apperrors.InvalidMark(validationErr.Message)
		case domain.ContentValidationInvalidParagraphRuns:
			return apperrors.InvalidParagraphRuns(validationErr.Message)
		}
	}
	return apperrors.New(apperrors.ErrCodeValidation, "invalid content_doc: "+err.Error(), 400)
}
