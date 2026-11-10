package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

type NoteRepository struct {
	db     *pgxpool.Pool
	logger *zap.Logger
}

func NewNoteRepository(db *pgxpool.Pool, logger *zap.Logger) *NoteRepository {
	return &NoteRepository{
		db:     db,
		logger: logger.With(zap.String("repository", "notes")),
	}
}

type noteListCursor struct {
	IsPinned  bool
	UpdatedAt time.Time
	ID        uuid.UUID
}

type noteSyncCursor struct {
	UpdatedAt time.Time
	ID        uuid.UUID
}

// ---------------------------------------------------------------------------
// CRUD – notes
// ---------------------------------------------------------------------------

func (r *NoteRepository) CreateNote(ctx context.Context, note *domain.Note) error {
	_, err := r.createNote(ctx, note, nil)
	return err
}

// CreateMeetingNote uses a user-scoped unique key, including concurrent retries.
// Existing notes are returned as-is: edits and trash state must never be overwritten.
func (r *NoteRepository) CreateMeetingNote(ctx context.Context, note *domain.Note, jobID string) (*domain.Note, bool, error) {
	created, err := r.createNote(ctx, note, &jobID)
	if err != nil || created {
		return note, created, err
	}
	existing, err := r.GetMeetingNote(ctx, note.UserID, jobID)
	return existing, false, err
}

func (r *NoteRepository) GetMeetingNote(ctx context.Context, userID int64, jobID string) (*domain.Note, error) {
	return scanNote(r.db.QueryRow(ctx, `SELECT id, user_id, folder_id, title, content, content_doc, content_text, type, checklist_items,
		is_pinned, is_archived, is_deleted, deleted_at, created_at, updated_at, version, color, tags, is_favorite
		FROM notes WHERE user_id = $1 AND meeting_job_id = $2`, userID, jobID))
}

func (r *NoteRepository) createNote(ctx context.Context, note *domain.Note, jobID *string) (bool, error) {
	checklistJSON, err := marshalChecklistItems(note.ChecklistItems)
	if err != nil {
		return false, err
	}
	contentDocJSON, err := domain.MarshalContentDoc(note.ContentDoc)
	if err != nil {
		return false, fmt.Errorf("marshal content_doc: %w", err)
	}

	query := `
		INSERT INTO notes (
			id, user_id, folder_id, title, content, content_doc, content_text, type, checklist_items,
			is_pinned, is_archived, is_deleted, deleted_at, version, created_at, updated_at, meeting_job_id, color,
			tags, is_favorite
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9,
			$10, $11, $12, $13, $14, $15, $16, $17, $18,
			$19, $20
		)
		ON CONFLICT (user_id, meeting_job_id) WHERE meeting_job_id IS NOT NULL DO NOTHING
	`

	tags := note.Tags
	if tags == nil {
		tags = []string{}
	}
	result, err := r.db.Exec(ctx, query,
		note.ID, note.UserID, note.FolderID, note.Title, note.Content, string(contentDocJSON), note.ContentText, note.Type, string(checklistJSON),
		note.IsPinned, note.IsArchived, note.IsDeleted, note.DeletedAt, note.Version, note.CreatedAt, note.UpdatedAt, jobID, note.Color,
		tags, note.IsFavorite,
	)
	if err != nil {
		return false, fmt.Errorf("create note: %w", err)
	}

	return result.RowsAffected() == 1, nil
}

func (r *NoteRepository) GetNoteByID(ctx context.Context, userID int64, noteID uuid.UUID) (*domain.Note, error) {
	query := `
		SELECT id, user_id, folder_id, title, content, content_doc, content_text, type, checklist_items,
		       is_pinned, is_archived, is_deleted, deleted_at, created_at, updated_at, version, color,
		       tags, is_favorite
		FROM notes
		WHERE id = $1
		  AND (
			user_id = $2
			OR (
				(
					EXISTS (
						SELECT 1
						FROM note_shares ns
						WHERE ns.note_id = notes.id AND ns.user_id = $2 AND ns.is_hidden = false
					)
					OR EXISTS (
						SELECT 1
						FROM messages m
						WHERE m.is_deleted = false
						  AND (
							(m.metadata->>'note_id') = notes.id::text
							OR (m.metadata->>'noteId') = notes.id::text
						  )
						  AND (
							lower(COALESCE(m.metadata->>'kind', '')) = 'note'
							OR lower(COALESCE(m.metadata->>'type', '')) = 'note'
						  )
						  AND (
							EXISTS (
								SELECT 1
								FROM conversation_participants cp
								WHERE cp.conversation_id = m.conversation_id
								  AND cp.user_id = $2
								  AND cp.left_at IS NULL
							)
							OR EXISTS (
								SELECT 1
								FROM group_members gm
								WHERE gm.group_id = m.conversation_id
								  AND gm.user_id = $2
								  AND gm.left_at IS NULL
							)
						  )
					)
				)
				AND NOT EXISTS (
					SELECT 1 FROM note_shares ns
					WHERE ns.note_id = notes.id AND ns.user_id = $2 AND ns.is_hidden = true
				)
			)
		  )
	`

	note, err := scanNote(r.db.QueryRow(ctx, query, noteID, userID))
	if err != nil {
		return nil, err
	}

	return note, nil
}

func (r *NoteRepository) GetOwnedNoteByID(ctx context.Context, userID int64, noteID uuid.UUID) (*domain.Note, error) {
	query := `
		SELECT id, user_id, folder_id, title, content, content_doc, content_text, type, checklist_items,
		       is_pinned, is_archived, is_deleted, deleted_at, created_at, updated_at, version, color,
		       tags, is_favorite
		FROM notes
		WHERE id = $1 AND user_id = $2
	`

	note, err := scanNote(r.db.QueryRow(ctx, query, noteID, userID))
	if err != nil {
		return nil, err
	}

	return note, nil
}

func (r *NoteRepository) ListNotes(ctx context.Context, filter domain.NoteListFilter) ([]domain.Note, string, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	baseQuery := `
		SELECT id, user_id, folder_id, title, content, content_doc, content_text, type, checklist_items,
		       is_pinned, is_archived, is_deleted, deleted_at, created_at, updated_at, version, color,
		       tags, is_favorite
		FROM notes
		WHERE `
	if filter.IsDeleted != nil && *filter.IsDeleted {
		// A non-owner hides a shared note instead of moving it into trash.
		// The deleted list is therefore owner-only and can use the notes index.
		baseQuery += "user_id = $1"
	} else {
		baseQuery += `(
			user_id = $1
			OR (
				(
					EXISTS (
						SELECT 1
						FROM note_shares ns
						WHERE ns.note_id = notes.id AND ns.user_id = $1 AND ns.is_hidden = false
					)
					OR EXISTS (
						SELECT 1
						FROM messages m
						WHERE m.is_deleted = false
						  AND (
							(m.metadata->>'note_id') = notes.id::text
							OR (m.metadata->>'noteId') = notes.id::text
						  )
						  AND (
							lower(COALESCE(m.metadata->>'kind', '')) = 'note'
							OR lower(COALESCE(m.metadata->>'type', '')) = 'note'
						  )
						  AND (
							EXISTS (
								SELECT 1
								FROM conversation_participants cp
								WHERE cp.conversation_id = m.conversation_id
								  AND cp.user_id = $1
								  AND cp.left_at IS NULL
							)
							OR EXISTS (
								SELECT 1
								FROM group_members gm
								WHERE gm.group_id = m.conversation_id
								  AND gm.user_id = $1
								  AND gm.left_at IS NULL
							)
						  )
					)
				)
				AND NOT EXISTS (
					SELECT 1 FROM note_shares ns
					WHERE ns.note_id = notes.id AND ns.user_id = $1 AND ns.is_hidden = true
				)
			)
		)`
	}
	args := []any{filter.UserID}
	arg := 2
	// Retain the visibility checks above; scope never grants additional access.
	switch filter.Scope {
	case "owned":
		baseQuery += " AND user_id = $1"
	case "shared":
		baseQuery += " AND user_id <> $1 AND is_deleted = FALSE"
	}

	if filter.FolderID != nil {
		if *filter.FolderID == uuid.Nil {
			baseQuery += " AND user_id = $1 AND folder_id IS NULL"
		} else {
			baseQuery += fmt.Sprintf(" AND user_id = $1 AND folder_id = $%d", arg)
			args = append(args, *filter.FolderID)
			arg++
		}
	}
	if filter.IsDeleted != nil {
		baseQuery += fmt.Sprintf(" AND is_deleted = $%d", arg)
		args = append(args, *filter.IsDeleted)
		arg++
		if *filter.IsDeleted {
			retentionLimit := time.Now().Add(-domain.NoteTrashRetention)
			baseQuery += fmt.Sprintf(" AND (deleted_at IS NULL OR deleted_at >= $%d)", arg)
			args = append(args, retentionLimit)
			arg++
		}
	} else {
		baseQuery += " AND is_deleted = FALSE"
	}
	if filter.IsArchived != nil {
		baseQuery += fmt.Sprintf(" AND is_archived = $%d", arg)
		args = append(args, *filter.IsArchived)
		arg++
	}
	if filter.IsFavorite != nil {
		baseQuery += fmt.Sprintf(" AND is_favorite = $%d", arg)
		args = append(args, *filter.IsFavorite)
		arg++
	}
	if tag := domain.NormalizeNoteTag(filter.Tag); tag != "" {
		baseQuery += fmt.Sprintf(" AND tags @> ARRAY[$%d]::text[]", arg)
		args = append(args, tag)
		arg++
	}
	if q := strings.TrimSpace(filter.Query); q != "" {
		baseQuery += fmt.Sprintf(`
			AND (
				search_vector @@ websearch_to_tsquery('simple', $%d)
				OR title ILIKE $%d
				OR content ILIKE $%d
				OR content_text ILIKE $%d
				OR $%d = ANY(tags)
				OR array_to_string(tags, ' ') ILIKE $%d
			)
		`, arg, arg+1, arg+1, arg+1, arg, arg+1)
		args = append(args, q, "%"+q+"%")
		arg += 2
	}

	if filter.Cursor != "" {
		cursor, err := decodeNoteListCursor(filter.Cursor)
		if err != nil {
			return nil, "", err
		}
		if filter.IsDeleted != nil && *filter.IsDeleted {
			baseQuery += fmt.Sprintf(" AND (COALESCE(deleted_at, updated_at), id) < ($%d, $%d)", arg, arg+1)
			args = append(args, cursor.UpdatedAt, cursor.ID)
		} else {
			baseQuery += fmt.Sprintf(`
			AND (
				CASE WHEN is_pinned THEN 1 ELSE 0 END < $%d
				OR (
					CASE WHEN is_pinned THEN 1 ELSE 0 END = $%d
					AND (
						updated_at < $%d
						OR (updated_at = $%d AND id < $%d)
					)
				)
			)
		`, arg, arg, arg+1, arg+1, arg+2)
			pinnedValue := 0
			if cursor.IsPinned {
				pinnedValue = 1
			}
			args = append(args, pinnedValue, cursor.UpdatedAt, cursor.ID)
		}
	}

	orderBy := " ORDER BY is_pinned DESC, updated_at DESC, id DESC"
	if filter.IsDeleted != nil && *filter.IsDeleted {
		orderBy = " ORDER BY COALESCE(deleted_at, updated_at) DESC, id DESC"
	}
	query := fmt.Sprintf("%s%s LIMIT %d", baseQuery, orderBy, limit+1)

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("list notes: %w", err)
	}
	defer rows.Close()

	notes := make([]domain.Note, 0, limit)
	for rows.Next() {
		note, err := scanNote(rows)
		if err != nil {
			return nil, "", err
		}
		notes = append(notes, *note)
	}

	var nextCursor string
	if len(notes) > limit {
		last := notes[limit-1]
		cursorTime := last.UpdatedAt
		if filter.IsDeleted != nil && *filter.IsDeleted && last.DeletedAt != nil {
			cursorTime = *last.DeletedAt
		}
		nextCursor = encodeNoteListCursor(noteListCursor{
			IsPinned:  last.IsPinned,
			UpdatedAt: cursorTime,
			ID:        last.ID,
		})
		notes = notes[:limit]
	}

	return notes, nextCursor, nil
}

func (r *NoteRepository) ListSyncNotes(ctx context.Context, filter domain.NoteSyncFilter) ([]domain.Note, string, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 100
	}

	query := `
		SELECT id, user_id, folder_id, title, content, content_doc, content_text, type, checklist_items,
		       is_pinned, is_archived, is_deleted, deleted_at, created_at, updated_at, version, color,
		       tags, is_favorite
		FROM notes
		WHERE (
			user_id = $1
			OR (
				(
					EXISTS (
						SELECT 1
						FROM note_shares ns
						WHERE ns.note_id = notes.id AND ns.user_id = $1 AND ns.is_hidden = false
					)
					OR EXISTS (
						SELECT 1
						FROM messages m
						WHERE m.is_deleted = false
						  AND (
							(m.metadata->>'note_id') = notes.id::text
							OR (m.metadata->>'noteId') = notes.id::text
						  )
						  AND (
							lower(COALESCE(m.metadata->>'kind', '')) = 'note'
							OR lower(COALESCE(m.metadata->>'type', '')) = 'note'
						  )
						  AND (
							EXISTS (
								SELECT 1
								FROM conversation_participants cp
								WHERE cp.conversation_id = m.conversation_id
								  AND cp.user_id = $1
								  AND cp.left_at IS NULL
							)
							OR EXISTS (
								SELECT 1
								FROM group_members gm
								WHERE gm.group_id = m.conversation_id
								  AND gm.user_id = $1
								  AND gm.left_at IS NULL
							)
						  )
					)
				)
				AND NOT EXISTS (
					SELECT 1 FROM note_shares ns
					WHERE ns.note_id = notes.id AND ns.user_id = $1 AND ns.is_hidden = true
				)
			)
		)
		  AND (
		    updated_at > $2
		    OR (updated_at = $2 AND id > $3)
		  )
	`
	args := []any{filter.UserID, filter.Since, uuid.Nil}
	arg := 4

	if filter.Cursor != "" {
		cursor, err := decodeNoteSyncCursor(filter.Cursor)
		if err != nil {
			return nil, "", err
		}
		args[1] = cursor.UpdatedAt
		args[2] = cursor.ID
	}

	query += fmt.Sprintf(" ORDER BY updated_at ASC, id ASC LIMIT $%d", arg)
	args = append(args, limit+1)

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("list sync notes: %w", err)
	}
	defer rows.Close()

	notes := make([]domain.Note, 0, limit)
	for rows.Next() {
		note, err := scanNote(rows)
		if err != nil {
			return nil, "", err
		}
		notes = append(notes, *note)
	}

	var nextCursor string
	if len(notes) > limit {
		last := notes[limit-1]
		nextCursor = encodeNoteSyncCursor(noteSyncCursor{UpdatedAt: last.UpdatedAt, ID: last.ID})
		notes = notes[:limit]
	}

	return notes, nextCursor, nil
}

func (r *NoteRepository) SaveNote(ctx context.Context, note *domain.Note, expectedVersion int) error {
	checklistJSON, err := marshalChecklistItems(note.ChecklistItems)
	if err != nil {
		return err
	}
	contentDocJSON, err := domain.MarshalContentDoc(note.ContentDoc)
	if err != nil {
		return fmt.Errorf("marshal content_doc: %w", err)
	}

	query := `
		UPDATE notes
		SET folder_id = $1,
		    title = $2,
		    content = $3,
		    content_doc = $4,
		    content_text = $5,
		    type = $6,
		    checklist_items = $7,
		    is_pinned = $8,
		    is_archived = $9,
		    is_deleted = $10,
		    deleted_at = $11,
		    updated_at = $12,
		    version = version + 1,
		    color = $16,
		    tags = $17,
		    is_favorite = $18
		WHERE id = $13
		  AND user_id = $14
		  AND version = $15
		RETURNING version, updated_at
	`

	tags := note.Tags
	if tags == nil {
		tags = []string{}
	}
	err = r.db.QueryRow(ctx, query,
		note.FolderID, note.Title, note.Content, string(contentDocJSON), note.ContentText, note.Type, string(checklistJSON),
		note.IsPinned, note.IsArchived, note.IsDeleted, note.DeletedAt, time.Now(),
		note.ID, note.UserID, expectedVersion, note.Color,
		tags, note.IsFavorite,
	).Scan(&note.Version, &note.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("note version conflict")
		}
		return fmt.Errorf("save note: %w", err)
	}

	return nil
}

func (r *NoteRepository) HardDeleteNote(ctx context.Context, userID int64, noteID uuid.UUID, expectedVersion int) ([]domain.NoteAttachment, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var version int
	var noteUserID int64
	var isDeleted bool
	err = tx.QueryRow(ctx, `SELECT user_id, version, is_deleted FROM notes WHERE id = $1 FOR UPDATE`, noteID).Scan(&noteUserID, &version, &isDeleted)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			r.logger.Debug("hard delete: note not found", zap.String("id", noteID.String()))
			return nil, fmt.Errorf("note not found")
		}
		return nil, fmt.Errorf("load note version: %w", err)
	}

	if noteUserID != userID {
		r.logger.Debug("hard delete: not owner", zap.String("id", noteID.String()), zap.Int64("user_id", userID), zap.Int64("owner_id", noteUserID))
		return nil, fmt.Errorf("note not found")
	}
	if version != expectedVersion {
		return nil, fmt.Errorf("note version conflict")
	}
	if !isDeleted {
		return nil, fmt.Errorf("note must be in trash")
	}

	attachments, err := r.listAttachmentsQuerier(ctx, tx, userID, noteID)
	if err != nil {
		return nil, err
	}
	photoRows, err := tx.Query(ctx, `SELECT storage_key FROM note_photo_attachments WHERE note_id = $1`, noteID)
	if err != nil {
		return nil, err
	}
	for photoRows.Next() {
		var key string
		if err := photoRows.Scan(&key); err != nil {
			photoRows.Close()
			return nil, err
		}
		attachments = append(attachments, domain.NoteAttachment{ObjectName: key})
	}
	err = photoRows.Err()
	photoRows.Close()
	if err != nil {
		return nil, err
	}
	audioRows, err := tx.Query(ctx, `SELECT storage_key FROM note_audio_attachments WHERE note_id = $1`, noteID)
	if err != nil {
		return nil, err
	}
	for audioRows.Next() {
		var key string
		if err := audioRows.Scan(&key); err != nil {
			audioRows.Close()
			return nil, err
		}
		attachments = append(attachments, domain.NoteAttachment{ObjectName: key})
	}
	err = audioRows.Err()
	audioRows.Close()
	if err != nil {
		return nil, err
	}
	if err := enqueueNoteObjectCleanup(ctx, tx, attachments); err != nil {
		return nil, err
	}

	res, err := tx.Exec(ctx, `DELETE FROM notes WHERE id = $1 AND user_id = $2 AND is_deleted = TRUE AND version = $3`, noteID, userID, expectedVersion)
	if err != nil {
		return nil, fmt.Errorf("hard delete note: %w", err)
	}
	if res.RowsAffected() == 0 {
		return nil, fmt.Errorf("note not found")
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit tx: %w", err)
	}

	return attachments, nil
}

// ---------------------------------------------------------------------------
// CRUD – folders
// ---------------------------------------------------------------------------

func (r *NoteRepository) CreateFolder(ctx context.Context, folder *domain.NoteFolder) error {
	query := `
		INSERT INTO note_folders (id, user_id, name, sort_order, color, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`
	_, err := r.db.Exec(ctx, query, folder.ID, folder.UserID, folder.Name, folder.SortOrder, folder.Color, folder.CreatedAt, folder.UpdatedAt)
	if err != nil {
		return fmt.Errorf("create note folder: %w", err)
	}
	return nil
}

func (r *NoteRepository) ListFolders(ctx context.Context, userID int64) ([]domain.NoteFolder, error) {
	query := `
		SELECT f.id, f.user_id, f.name, f.sort_order, f.color, f.created_at, f.updated_at,
		       COUNT(n.id) FILTER (WHERE n.is_deleted = FALSE) AS note_count
		FROM note_folders f
		LEFT JOIN notes n ON n.folder_id = f.id AND n.user_id = f.user_id
		WHERE f.user_id = $1
		GROUP BY f.id, f.user_id, f.name, f.sort_order, f.color, f.created_at, f.updated_at
		ORDER BY f.sort_order ASC, f.created_at ASC
	`
	rows, err := r.db.Query(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("list note folders: %w", err)
	}
	defer rows.Close()

	out := make([]domain.NoteFolder, 0)
	for rows.Next() {
		var folder domain.NoteFolder
		if err := rows.Scan(&folder.ID, &folder.UserID, &folder.Name, &folder.SortOrder, &folder.Color, &folder.CreatedAt, &folder.UpdatedAt, &folder.NoteCount); err != nil {
			return nil, err
		}
		out = append(out, folder)
	}
	return out, nil
}

func (r *NoteRepository) GetFolderByID(ctx context.Context, userID int64, folderID uuid.UUID) (*domain.NoteFolder, error) {
	query := `
		SELECT f.id, f.user_id, f.name, f.sort_order, f.color, f.created_at, f.updated_at,
		       COUNT(n.id) FILTER (WHERE n.is_deleted = FALSE) AS note_count
		FROM note_folders f
		LEFT JOIN notes n ON n.folder_id = f.id AND n.user_id = f.user_id
		WHERE f.id = $1 AND f.user_id = $2
		GROUP BY f.id, f.user_id, f.name, f.sort_order, f.color, f.created_at, f.updated_at
	`
	var folder domain.NoteFolder
	err := r.db.QueryRow(ctx, query, folderID, userID).Scan(
		&folder.ID, &folder.UserID, &folder.Name, &folder.SortOrder, &folder.Color, &folder.CreatedAt, &folder.UpdatedAt, &folder.NoteCount,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("folder not found")
		}
		return nil, fmt.Errorf("get note folder: %w", err)
	}
	return &folder, nil
}

func (r *NoteRepository) UpdateFolder(ctx context.Context, folder *domain.NoteFolder) error {
	query := `
		UPDATE note_folders
		SET name = $1, sort_order = $2, color = $3, updated_at = $4
		WHERE id = $5 AND user_id = $6
	`
	res, err := r.db.Exec(ctx, query, folder.Name, folder.SortOrder, folder.Color, folder.UpdatedAt, folder.ID, folder.UserID)
	if err != nil {
		return fmt.Errorf("update note folder: %w", err)
	}
	if res.RowsAffected() == 0 {
		return fmt.Errorf("folder not found")
	}
	return nil
}

func (r *NoteRepository) DeleteFolder(ctx context.Context, userID int64, folderID uuid.UUID) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `UPDATE notes SET folder_id = NULL, updated_at = NOW(), version = version + 1 WHERE folder_id = $1 AND user_id = $2`, folderID, userID); err != nil {
		return fmt.Errorf("detach notes from folder: %w", err)
	}

	res, err := tx.Exec(ctx, `DELETE FROM note_folders WHERE id = $1 AND user_id = $2`, folderID, userID)
	if err != nil {
		return fmt.Errorf("delete note folder: %w", err)
	}
	if res.RowsAffected() == 0 {
		return fmt.Errorf("folder not found")
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Legacy attachments (direct upload via files table)
// ---------------------------------------------------------------------------

func (r *NoteRepository) AddAttachment(ctx context.Context, userID int64, noteID uuid.UUID, file *domain.DriveFile) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := r.ensureNoteOwnership(ctx, tx, userID, noteID); err != nil {
		return err
	}

	if err := createDriveFile(ctx, tx, file, userID); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO note_attachments (note_id, file_id, created_at)
		VALUES ($1, $2, $3)
	`, noteID, file.ID, time.Now()); err != nil {
		return fmt.Errorf("add note attachment: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

func (r *NoteRepository) ListAttachments(ctx context.Context, userID int64, noteID uuid.UUID) ([]domain.NoteAttachment, error) {
	return r.listAttachmentsQuerier(ctx, r.db, userID, noteID)
}

func (r *NoteRepository) ListAttachmentsForNotes(ctx context.Context, userID int64, noteIDs []uuid.UUID) (map[uuid.UUID][]domain.NoteAttachment, error) {
	out := make(map[uuid.UUID][]domain.NoteAttachment, len(noteIDs))
	if len(noteIDs) == 0 {
		return out, nil
	}

	rows, err := r.db.Query(ctx, `
		SELECT na.note_id, f.id, f.real_name, f.mime_type, f.size, f.object_name, na.created_at
		FROM note_attachments na
		JOIN notes n ON n.id = na.note_id
		JOIN files f ON f.id = na.file_id
		WHERE na.note_id = ANY($1)
		  AND (
			n.user_id = $2
			OR EXISTS (
				SELECT 1
				FROM note_shares ns
				WHERE ns.note_id = n.id AND ns.user_id = $2
			)
		  )
		  AND f.deleted_at IS NULL
		ORDER BY na.note_id, na.created_at DESC
	`, noteIDs, userID)
	if err != nil {
		return nil, fmt.Errorf("list note attachments batch: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var noteID uuid.UUID
		var item domain.NoteAttachment
		if err := rows.Scan(&noteID, &item.FileID, &item.RealName, &item.MimeType, &item.Size, &item.ObjectName, &item.CreatedAt); err != nil {
			return nil, err
		}
		out[noteID] = append(out[noteID], item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *NoteRepository) RemoveAttachment(ctx context.Context, userID int64, noteID, fileID uuid.UUID) (*domain.NoteAttachment, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := r.ensureNoteOwnership(ctx, tx, userID, noteID); err != nil {
		return nil, err
	}

	query := `
		SELECT f.id, f.real_name, f.mime_type, f.size, f.object_name, na.created_at
		FROM note_attachments na
		JOIN files f ON f.id = na.file_id
		WHERE na.note_id = $1 AND na.file_id = $2 AND f.owner_id = $3
	`
	var attachment domain.NoteAttachment
	err = tx.QueryRow(ctx, query, noteID, fileID, userID).Scan(
		&attachment.FileID, &attachment.RealName, &attachment.MimeType, &attachment.Size, &attachment.ObjectName, &attachment.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("attachment not found")
		}
		return nil, fmt.Errorf("load attachment: %w", err)
	}

	if _, err := tx.Exec(ctx, `DELETE FROM note_attachments WHERE note_id = $1 AND file_id = $2`, noteID, fileID); err != nil {
		return nil, fmt.Errorf("delete note attachment: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE files SET deleted_at = NOW(), updated_at = NOW() WHERE id = $1 AND owner_id = $2`, fileID, userID); err != nil {
		return nil, fmt.Errorf("soft delete file metadata: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit tx: %w", err)
	}
	return &attachment, nil
}

// ---------------------------------------------------------------------------
// Photo attachments (presigned-URL flow)
// ---------------------------------------------------------------------------

func (r *NoteRepository) CreatePhotoAttachment(ctx context.Context, a *domain.NotePhotoAttachment) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO note_photo_attachments
			(id, note_id, user_id, storage_key, mime_type, size_bytes, width, height, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`, a.ID, a.NoteID, a.UserID, a.StorageKey, a.MimeType, a.SizeBytes, a.Width, a.Height, a.Status, a.CreatedAt, a.UpdatedAt)
	if err != nil {
		return fmt.Errorf("create photo attachment: %w", err)
	}
	return nil
}

func (r *NoteRepository) GetPhotoAttachment(ctx context.Context, userID int64, noteID, attachmentID uuid.UUID) (*domain.NotePhotoAttachment, error) {
	var a domain.NotePhotoAttachment
	err := r.db.QueryRow(ctx, `
		SELECT id, note_id, user_id, storage_key, mime_type, size_bytes, width, height, status, created_at, updated_at
		FROM note_photo_attachments
		WHERE id = $1 AND note_id = $2 AND user_id = $3 AND status != 'deleted'
	`, attachmentID, noteID, userID).Scan(
		&a.ID, &a.NoteID, &a.UserID, &a.StorageKey, &a.MimeType, &a.SizeBytes, &a.Width, &a.Height, &a.Status, &a.CreatedAt, &a.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("attachment not found")
		}
		return nil, fmt.Errorf("get photo attachment: %w", err)
	}
	return &a, nil
}

func (r *NoteRepository) ListPhotoAttachments(ctx context.Context, userID int64, noteID uuid.UUID) ([]domain.NotePhotoAttachment, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, note_id, user_id, storage_key, mime_type, size_bytes, width, height, status, created_at, updated_at
		FROM note_photo_attachments
		WHERE note_id = $1
		  AND user_id = (
			SELECT n.user_id
			FROM notes n
			WHERE n.id = $1
			  AND (
				n.user_id = $2
				OR EXISTS (
					SELECT 1
					FROM note_shares ns
					WHERE ns.note_id = n.id AND ns.user_id = $2
				)
			  )
		  )
		  AND status = 'uploaded'
		ORDER BY created_at DESC
	`, noteID, userID)
	if err != nil {
		return nil, fmt.Errorf("list photo attachments: %w", err)
	}
	defer rows.Close()

	items := make([]domain.NotePhotoAttachment, 0)
	for rows.Next() {
		var a domain.NotePhotoAttachment
		if err := rows.Scan(
			&a.ID, &a.NoteID, &a.UserID, &a.StorageKey, &a.MimeType, &a.SizeBytes, &a.Width, &a.Height, &a.Status, &a.CreatedAt, &a.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan photo attachment: %w", err)
		}
		items = append(items, a)
	}
	return items, nil
}

func (r *NoteRepository) ListPhotoAttachmentsForNotes(ctx context.Context, userID int64, noteIDs []uuid.UUID) (map[uuid.UUID][]domain.NotePhotoAttachment, error) {
	out := make(map[uuid.UUID][]domain.NotePhotoAttachment, len(noteIDs))
	if len(noteIDs) == 0 {
		return out, nil
	}

	rows, err := r.db.Query(ctx, `
		SELECT npa.id, npa.note_id, npa.user_id, npa.storage_key, npa.mime_type, npa.size_bytes,
		       npa.width, npa.height, npa.status, npa.created_at, npa.updated_at
		FROM note_photo_attachments npa
		JOIN notes n ON n.id = npa.note_id
		WHERE npa.note_id = ANY($1)
		  AND npa.user_id = n.user_id
		  AND (
			n.user_id = $2
			OR EXISTS (
				SELECT 1
				FROM note_shares ns
				WHERE ns.note_id = n.id AND ns.user_id = $2
			)
		  )
		  AND npa.status = 'uploaded'
		ORDER BY npa.note_id, npa.created_at DESC
	`, noteIDs, userID)
	if err != nil {
		return nil, fmt.Errorf("list photo attachments batch: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var a domain.NotePhotoAttachment
		if err := rows.Scan(
			&a.ID, &a.NoteID, &a.UserID, &a.StorageKey, &a.MimeType, &a.SizeBytes,
			&a.Width, &a.Height, &a.Status, &a.CreatedAt, &a.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan photo attachment batch: %w", err)
		}
		out[a.NoteID] = append(out[a.NoteID], a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *NoteRepository) MarkPhotoAttachmentUploaded(ctx context.Context, attachmentID uuid.UUID, width, height *int) error {
	now := time.Now()
	res, err := r.db.Exec(ctx, `
		UPDATE note_photo_attachments
		SET status = 'uploaded', width = $2, height = $3, updated_at = $4
		WHERE id = $1 AND status = 'pending'
	`, attachmentID, width, height, now)
	if err != nil {
		return fmt.Errorf("mark photo attachment uploaded: %w", err)
	}
	if res.RowsAffected() == 0 {
		return fmt.Errorf("attachment not found")
	}
	return nil
}

func (r *NoteRepository) DeletePhotoAttachment(ctx context.Context, userID int64, noteID, attachmentID uuid.UUID) (string, error) {
	var storageKey string
	err := r.db.QueryRow(ctx, `
		UPDATE note_photo_attachments
		SET status = 'deleted', updated_at = NOW()
		WHERE id = $1 AND note_id = $2 AND user_id = $3 AND status != 'deleted'
		RETURNING storage_key
	`, attachmentID, noteID, userID).Scan(&storageKey)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("attachment not found")
		}
		return "", fmt.Errorf("delete photo attachment: %w", err)
	}
	return storageKey, nil
}

// ---------------------------------------------------------------------------
// Audio attachments (NTS-004)
// ---------------------------------------------------------------------------

func (r *NoteRepository) CreateAudioAttachment(ctx context.Context, a *domain.NoteAudioAttachment) error {
	waveformJSON, err := json.Marshal(a.Waveform)
	if err != nil {
		waveformJSON = []byte("[]")
	}
	_, err = r.db.Exec(ctx, `
		INSERT INTO note_audio_attachments
			(id, note_id, user_id, storage_key, file_name, mime_type, size_bytes, duration, waveform, is_recording, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
	`, a.ID, a.NoteID, a.UserID, a.StorageKey, a.FileName, a.MimeType, a.SizeBytes, a.Duration, waveformJSON, a.IsRecording, a.Status, a.CreatedAt, a.UpdatedAt)
	if err != nil {
		return fmt.Errorf("create audio attachment: %w", err)
	}
	return nil
}

func (r *NoteRepository) GetAudioAttachment(ctx context.Context, userID int64, noteID, attachmentID uuid.UUID) (*domain.NoteAudioAttachment, error) {
	var a domain.NoteAudioAttachment
	var waveformJSON []byte
	err := r.db.QueryRow(ctx, `
		SELECT id, note_id, user_id, storage_key, file_name, mime_type, size_bytes, duration, waveform, is_recording, status, created_at, updated_at
		FROM note_audio_attachments
		WHERE id = $1 AND note_id = $2
		  AND (
			user_id = $3
			OR (status = 'uploaded' AND EXISTS (
				SELECT 1 FROM note_shares ns WHERE ns.note_id = $2 AND ns.user_id = $3 AND ns.is_hidden = false
			))
		  )
		  AND status != 'deleted'
	`, attachmentID, noteID, userID).Scan(
		&a.ID, &a.NoteID, &a.UserID, &a.StorageKey, &a.FileName, &a.MimeType, &a.SizeBytes, &a.Duration, &waveformJSON, &a.IsRecording, &a.Status, &a.CreatedAt, &a.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("attachment not found")
		}
		return nil, fmt.Errorf("get audio attachment: %w", err)
	}
	if len(waveformJSON) > 0 {
		_ = json.Unmarshal(waveformJSON, &a.Waveform)
	}
	return &a, nil
}

func (r *NoteRepository) ListAudioAttachments(ctx context.Context, userID int64, noteID uuid.UUID) ([]domain.NoteAudioAttachment, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, note_id, user_id, storage_key, file_name, mime_type, size_bytes, duration, waveform, is_recording, status, created_at, updated_at
		FROM note_audio_attachments
		WHERE note_id = $1
		  AND user_id = (
			SELECT n.user_id
			FROM notes n
			WHERE n.id = $1
			  AND (
				n.user_id = $2
				OR EXISTS (
					SELECT 1
					FROM note_shares ns
					WHERE ns.note_id = n.id AND ns.user_id = $2
				)
			  )
		  )
		  AND status = 'uploaded'
		ORDER BY created_at DESC
	`, noteID, userID)
	if err != nil {
		return nil, fmt.Errorf("list audio attachments: %w", err)
	}
	defer rows.Close()

	items := make([]domain.NoteAudioAttachment, 0)
	for rows.Next() {
		var a domain.NoteAudioAttachment
		var waveformJSON []byte
		if err := rows.Scan(
			&a.ID, &a.NoteID, &a.UserID, &a.StorageKey, &a.FileName, &a.MimeType, &a.SizeBytes, &a.Duration, &waveformJSON, &a.IsRecording, &a.Status, &a.CreatedAt, &a.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan audio attachment: %w", err)
		}
		if len(waveformJSON) > 0 {
			_ = json.Unmarshal(waveformJSON, &a.Waveform)
		}
		items = append(items, a)
	}
	return items, nil
}

func (r *NoteRepository) ListAudioAttachmentsForNotes(ctx context.Context, userID int64, noteIDs []uuid.UUID) (map[uuid.UUID][]domain.NoteAudioAttachment, error) {
	out := make(map[uuid.UUID][]domain.NoteAudioAttachment, len(noteIDs))
	if len(noteIDs) == 0 {
		return out, nil
	}

	rows, err := r.db.Query(ctx, `
		SELECT naa.id, naa.note_id, naa.user_id, naa.storage_key, naa.file_name, naa.mime_type, naa.size_bytes,
		       naa.duration, naa.waveform, naa.is_recording, naa.status, naa.created_at, naa.updated_at
		FROM note_audio_attachments naa
		JOIN notes n ON n.id = naa.note_id
		WHERE naa.note_id = ANY($1)
		  AND naa.user_id = n.user_id
		  AND (
			n.user_id = $2
			OR EXISTS (
				SELECT 1
				FROM note_shares ns
				WHERE ns.note_id = n.id AND ns.user_id = $2
			)
		  )
		  AND naa.status = 'uploaded'
		ORDER BY naa.note_id, naa.created_at DESC
	`, noteIDs, userID)
	if err != nil {
		return nil, fmt.Errorf("list audio attachments batch: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var a domain.NoteAudioAttachment
		var waveformJSON []byte
		if err := rows.Scan(
			&a.ID, &a.NoteID, &a.UserID, &a.StorageKey, &a.FileName, &a.MimeType, &a.SizeBytes,
			&a.Duration, &waveformJSON, &a.IsRecording, &a.Status, &a.CreatedAt, &a.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan audio attachment batch: %w", err)
		}
		if len(waveformJSON) > 0 {
			_ = json.Unmarshal(waveformJSON, &a.Waveform)
		}
		out[a.NoteID] = append(out[a.NoteID], a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *NoteRepository) MarkAudioAttachmentUploaded(ctx context.Context, attachmentID uuid.UUID, duration *float64, waveform []float64, fileName *string) error {
	now := time.Now()
	var waveformJSON []byte
	if len(waveform) > 0 {
		waveformJSON, _ = json.Marshal(waveform)
	}

	query := `
		UPDATE note_audio_attachments
		SET status = 'uploaded',
		    duration = COALESCE($2, duration),
		    waveform = CASE WHEN $3::jsonb IS NOT NULL THEN $3::jsonb ELSE waveform END,
		    file_name = CASE WHEN $4::text IS NOT NULL AND $4::text != '' THEN $4::text ELSE file_name END,
		    updated_at = $5
		WHERE id = $1 AND status = 'pending'
	`
	res, err := r.db.Exec(ctx, query, attachmentID, duration, waveformJSON, fileName, now)
	if err != nil {
		return fmt.Errorf("mark audio attachment uploaded: %w", err)
	}
	if res.RowsAffected() == 0 {
		return fmt.Errorf("attachment not found")
	}
	return nil
}

func (r *NoteRepository) FinalizeAudioAttachment(ctx context.Context, attachmentID uuid.UUID, uploadKey, finalKey string, duration *float64, waveform []float64, fileName *string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var waveformJSON []byte
	if len(waveform) > 0 {
		waveformJSON, err = json.Marshal(waveform)
		if err != nil {
			return err
		}
	}
	res, err := tx.Exec(ctx, `UPDATE note_audio_attachments SET status='uploaded', storage_key=$3, duration=COALESCE($4,duration), waveform=CASE WHEN $5::jsonb IS NOT NULL THEN $5::jsonb ELSE waveform END, file_name=CASE WHEN $6::text IS NOT NULL AND $6::text!='' THEN $6::text ELSE file_name END, updated_at=NOW() WHERE id=$1 AND storage_key=$2 AND status='pending'`, attachmentID, uploadKey, finalKey, duration, waveformJSON, fileName)
	if err != nil {
		return err
	}
	if res.RowsAffected() != 1 {
		return fmt.Errorf("attachment not found")
	}
	if err := enqueueNoteObjectCleanup(ctx, tx, []domain.NoteAttachment{{ObjectName: uploadKey}}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *NoteRepository) DeleteAudioAttachment(ctx context.Context, userID int64, noteID, attachmentID uuid.UUID) (string, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var storageKey string
	err = tx.QueryRow(ctx, `
		UPDATE note_audio_attachments
		SET status = 'deleted', updated_at = NOW()
		WHERE id = $1 AND note_id = $2 AND user_id = $3 AND status != 'deleted'
		RETURNING storage_key
	`, attachmentID, noteID, userID).Scan(&storageKey)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("attachment not found")
		}
		return "", fmt.Errorf("delete audio attachment: %w", err)
	}
	if err := enqueueNoteObjectCleanup(ctx, tx, []domain.NoteAttachment{{ObjectName: storageKey}}); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return storageKey, nil
}

func (r *NoteRepository) EnqueueAudioObjectCleanup(ctx context.Context, storageKey string) error {
	if storageKey == "" {
		return nil
	}
	_, err := r.db.Exec(ctx, `INSERT INTO note_object_cleanup(object_name) VALUES($1) ON CONFLICT DO NOTHING`, storageKey)
	return err
}

func (r *NoteRepository) ExpirePendingAudioUploads(ctx context.Context, before time.Time) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `UPDATE note_audio_attachments SET status='deleted',updated_at=NOW() WHERE id IN (SELECT id FROM note_audio_attachments WHERE status='pending' AND created_at<$1 ORDER BY created_at LIMIT 100 FOR UPDATE SKIP LOCKED) RETURNING storage_key`, before)
	if err != nil {
		return err
	}
	var attachments []domain.NoteAttachment
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return err
		}
		attachments = append(attachments, domain.NoteAttachment{ObjectName: key})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if err := enqueueNoteObjectCleanup(ctx, tx, attachments); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ---------------------------------------------------------------------------
// Audit events
// ---------------------------------------------------------------------------

func (r *NoteRepository) CreateAuditEvent(ctx context.Context, noteID uuid.UUID, userID int64, eventType string, payload map[string]any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal audit payload: %w", err)
	}
	_, err = r.db.Exec(ctx, `
		INSERT INTO note_audit_events (id, note_id, user_id, event_type, payload, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, uuid.New(), noteID, userID, eventType, string(data), time.Now())
	if err != nil {
		return fmt.Errorf("create note audit event: %w", err)
	}
	return nil
}

func (r *NoteRepository) GrantSharedAccess(ctx context.Context, noteID uuid.UUID, grantedBy int64, userIDs []int64) error {
	if len(userIDs) == 0 {
		return nil
	}

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := r.ensureNoteOwnership(ctx, tx, grantedBy, noteID); err != nil {
		return err
	}

	for _, userID := range userIDs {
		if userID == grantedBy {
			continue
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO note_shares (note_id, user_id, granted_by, created_at)
			VALUES ($1, $2, $3, NOW())
			ON CONFLICT (note_id, user_id) DO UPDATE
			SET granted_by = EXCLUDED.granted_by
		`, noteID, userID, grantedBy); err != nil {
			return fmt.Errorf("grant note share: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

func (r *NoteRepository) RevokeSharedAccess(ctx context.Context, noteID uuid.UUID, userID int64) error {
	res, err := r.db.Exec(ctx, `DELETE FROM note_shares WHERE note_id = $1 AND user_id = $2`, noteID, userID)
	if err != nil {
		return fmt.Errorf("revoke shared access: %w", err)
	}
	if res.RowsAffected() == 0 {
		return fmt.Errorf("share not found")
	}
	return nil
}

func (r *NoteRepository) HideNoteForUser(ctx context.Context, noteID uuid.UUID, userID int64) error {
	// A guessed UUID must not allow writes to another user's share records.
	if _, err := r.GetNoteByID(ctx, userID, noteID); err != nil {
		return err
	}
	_, err := r.db.Exec(ctx, `
		INSERT INTO note_shares (note_id, user_id, granted_by, is_hidden, created_at)
		VALUES ($1, $2, $2, TRUE, NOW())
		ON CONFLICT (note_id, user_id) DO UPDATE
		SET is_hidden = TRUE
	`, noteID, userID)
	if err != nil {
		return fmt.Errorf("hide note for user: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (r *NoteRepository) ensureNoteOwnership(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, userID int64, noteID uuid.UUID) error {
	var exists bool
	err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM notes WHERE id = $1 AND user_id = $2)`, noteID, userID).Scan(&exists)
	if err != nil {
		return fmt.Errorf("check note ownership: %w", err)
	}
	if !exists {
		return fmt.Errorf("note not found")
	}
	return nil
}

func (r *NoteRepository) listAttachmentsQuerier(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, userID int64, noteID uuid.UUID) ([]domain.NoteAttachment, error) {
	query := `
		SELECT f.id, f.real_name, f.mime_type, f.size, f.object_name, na.created_at
		FROM note_attachments na
		JOIN notes n ON n.id = na.note_id
		JOIN files f ON f.id = na.file_id
	WHERE na.note_id = $1
	  AND (
		n.user_id = $2
		OR EXISTS (
			SELECT 1
			FROM note_shares ns
			WHERE ns.note_id = n.id AND ns.user_id = $2
		)
	  )
	  AND f.deleted_at IS NULL
		ORDER BY na.created_at DESC
	`
	rows, err := q.Query(ctx, query, noteID, userID)
	if err != nil {
		return nil, fmt.Errorf("list note attachments: %w", err)
	}
	defer rows.Close()

	out := make([]domain.NoteAttachment, 0)
	for rows.Next() {
		var item domain.NoteAttachment
		if err := rows.Scan(&item.FileID, &item.RealName, &item.MimeType, &item.Size, &item.ObjectName, &item.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

func scanNote(row interface {
	Scan(...any) error
}) (*domain.Note, error) {
	var note domain.Note
	var checklistRaw []byte
	var contentDocRaw []byte
	var tags []string
	err := row.Scan(
		&note.ID, &note.UserID, &note.FolderID, &note.Title, &note.Content, &contentDocRaw, &note.ContentText, &note.Type, &checklistRaw,
		&note.IsPinned, &note.IsArchived, &note.IsDeleted, &note.DeletedAt, &note.CreatedAt, &note.UpdatedAt, &note.Version, &note.Color,
		&tags, &note.IsFavorite,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("note not found")
		}
		return nil, err
	}

	if tags == nil {
		note.Tags = []string{}
	} else {
		note.Tags = tags
	}

	note.ChecklistItems, err = unmarshalChecklistItems(checklistRaw)
	if err != nil {
		return nil, err
	}

	note.ContentDoc, err = domain.UnmarshalContentDoc(contentDocRaw)
	if err != nil {
		return nil, fmt.Errorf("unmarshal content_doc: %w", err)
	}

	if note.IsDeleted && note.DeletedAt != nil {
		note.ExpiresAt = domain.CalculateTrashExpiresAt(note.DeletedAt)
	}

	return &note, nil
}

func (r *NoteRepository) GetUserTags(ctx context.Context, userID int64) ([]domain.NoteTagSummary, error) {
	query := `
		SELECT tag, COUNT(*) AS count
		FROM (
			SELECT unnest(tags) AS tag
			FROM notes
			WHERE user_id = $1 AND is_deleted = FALSE
		) t
		WHERE tag <> ''
		GROUP BY tag
		ORDER BY count DESC, tag ASC
	`
	rows, err := r.db.Query(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("get user tags: %w", err)
	}
	defer rows.Close()

	out := make([]domain.NoteTagSummary, 0)
	for rows.Next() {
		var item domain.NoteTagSummary
		if err := rows.Scan(&item.Tag, &item.Count); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("get user tags: %w", err)
	}
	return out, nil
}

func marshalChecklistItems(items []domain.ChecklistItem) ([]byte, error) {
	if len(items) == 0 {
		return []byte("[]"), nil
	}
	data, err := json.Marshal(items)
	if err != nil {
		return nil, fmt.Errorf("marshal checklist items: %w", err)
	}
	return data, nil
}

func unmarshalChecklistItems(data []byte) ([]domain.ChecklistItem, error) {
	if len(data) == 0 {
		return []domain.ChecklistItem{}, nil
	}
	var items []domain.ChecklistItem
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, fmt.Errorf("unmarshal checklist items: %w", err)
	}
	return items, nil
}

func createDriveFile(ctx context.Context, tx pgx.Tx, file *domain.DriveFile, createdBy int64) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO files (
			id, owner_id, folder_id, real_name, object_name, size, mime_type, version, scope,
			onlyoffice_key, source_system_code, source_system_name, external_message_id, created_at, updated_at, deleted_at
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,NULL)
	`, file.ID, file.OwnerID, file.FolderID, file.RealName, file.ObjectName, file.Size, file.MimeType, file.Version, file.Scope, file.Key, file.SourceSystemCode, file.SourceSystemName, file.ExternalMessageID, file.CreatedAt, file.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert file: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO file_versions (id, file_id, version, object_name, size, created_by, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
	`, uuid.New(), file.ID, file.Version, file.ObjectName, file.Size, createdBy, time.Now())
	if err != nil {
		return fmt.Errorf("insert file version: %w", err)
	}
	return nil
}

func (r *NoteRepository) GetTrashCount(ctx context.Context, userID int64, since time.Time) (int64, error) {
	var count int64
	query := `
		SELECT COUNT(*)
		FROM notes
		WHERE user_id = $1 AND is_deleted = TRUE AND (deleted_at IS NULL OR deleted_at >= $2)
	`
	err := r.db.QueryRow(ctx, query, userID, since).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("get trash count: %w", err)
	}
	return count, nil
}

func (r *NoteRepository) EmptyTrash(ctx context.Context, userID int64) (int64, []domain.NoteAttachment, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return 0, nil, fmt.Errorf("begin empty trash tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	lockedRows, err := tx.Query(ctx, `SELECT id FROM notes WHERE user_id = $1 AND is_deleted = TRUE ORDER BY id FOR UPDATE`, userID)
	if err != nil {
		return 0, nil, err
	}
	ids := []uuid.UUID{}
	for lockedRows.Next() {
		var id uuid.UUID
		if err := lockedRows.Scan(&id); err != nil {
			lockedRows.Close()
			return 0, nil, err
		}
		ids = append(ids, id)
	}
	err = lockedRows.Err()
	lockedRows.Close()
	if err != nil {
		return 0, nil, err
	}

	attQuery := `
		SELECT f.id, f.real_name, f.mime_type, f.size, f.object_name, na.created_at
		FROM note_attachments na
		JOIN notes n ON n.id = na.note_id
		JOIN files f ON f.id = na.file_id
		WHERE n.id = ANY($1::uuid[])
	`
	rows, err := tx.Query(ctx, attQuery, ids)
	if err != nil {
		return 0, nil, fmt.Errorf("list attachments for empty trash: %w", err)
	}
	defer rows.Close()

	var attachments []domain.NoteAttachment
	for rows.Next() {
		var a domain.NoteAttachment
		if err := rows.Scan(&a.FileID, &a.RealName, &a.MimeType, &a.Size, &a.ObjectName, &a.CreatedAt); err != nil {
			return 0, nil, fmt.Errorf("scan attachment for empty trash: %w", err)
		}
		attachments = append(attachments, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, nil, err
	}

	photoQuery := `
		SELECT npa.id, '', npa.mime_type, npa.size_bytes, npa.storage_key, npa.created_at
		FROM note_photo_attachments npa
		JOIN notes n ON n.id = npa.note_id
		WHERE n.id = ANY($1::uuid[])
	`
	pRows, err := tx.Query(ctx, photoQuery, ids)
	if err != nil {
		return 0, nil, err
	}
	{
		for pRows.Next() {
			var a domain.NoteAttachment
			if err := pRows.Scan(&a.FileID, &a.RealName, &a.MimeType, &a.Size, &a.ObjectName, &a.CreatedAt); err != nil {
				pRows.Close()
				return 0, nil, err
			}
			attachments = append(attachments, a)
		}
		err = pRows.Err()
		pRows.Close()
		if err != nil {
			return 0, nil, err
		}
	}

	audioQuery := `
		SELECT naa.id, naa.file_name, naa.mime_type, naa.size_bytes, naa.storage_key, naa.created_at
		FROM note_audio_attachments naa
		JOIN notes n ON n.id = naa.note_id
		WHERE n.id = ANY($1::uuid[])
	`
	aRows, err := tx.Query(ctx, audioQuery, ids)
	if err != nil {
		return 0, nil, err
	}
	for aRows.Next() {
		var a domain.NoteAttachment
		if err := aRows.Scan(&a.FileID, &a.RealName, &a.MimeType, &a.Size, &a.ObjectName, &a.CreatedAt); err != nil {
			aRows.Close()
			return 0, nil, err
		}
		attachments = append(attachments, a)
	}
	err = aRows.Err()
	aRows.Close()
	if err != nil {
		return 0, nil, err
	}

	if err := enqueueNoteObjectCleanup(ctx, tx, attachments); err != nil {
		return 0, nil, err
	}

	res, err := tx.Exec(ctx, `DELETE FROM notes WHERE user_id = $1 AND is_deleted = TRUE AND id = ANY($2::uuid[])`, userID, ids)
	if err != nil {
		return 0, nil, fmt.Errorf("empty trash delete: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, nil, fmt.Errorf("commit empty trash tx: %w", err)
	}

	return res.RowsAffected(), attachments, nil
}
