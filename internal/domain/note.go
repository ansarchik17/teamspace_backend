package domain

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

type ContentValidationCode string

const (
	ContentValidationInvalidMark          ContentValidationCode = "INVALID_MARK"
	ContentValidationInvalidParagraphRuns ContentValidationCode = "INVALID_PARAGRAPH_RUNS"
)

type ContentValidationError struct {
	Code    ContentValidationCode
	Message string
}

func (e *ContentValidationError) Error() string {
	return e.Message
}

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const (
	NoteTypeText      = "text"
	NoteTypeChecklist = "checklist"
	NoteTypeRich      = "rich"
	NoteTypeAudio     = "audio"

	NoteAuditEventCreate     = "create"
	NoteAuditEventUpdate     = "update"
	NoteAuditEventDelete     = "delete"
	NoteAuditEventRestore    = "restore"
	NoteAuditEventHardDelete = "hard_delete"

	// content_doc block types
	NoteBlockTypeParagraph = "paragraph"
	NoteBlockTypeChecklist = "checklist"
	NoteBlockTypeTable     = "table"
	NoteBlockTypeImage     = "image"
	NoteBlockTypeAudio     = "audio"

	// allowed text marks
	NoteMarkBold   = "bold"
	NoteMarkItalic = "italic"
	NoteMarkStrike = "strike"

	// NotePhotoAttachment statuses
	NotePhotoStatusPending  = "pending"
	NotePhotoStatusUploaded = "uploaded"
	NotePhotoStatusDeleted  = "deleted"

	// NoteAudioAttachment statuses
	NoteAudioStatusPending  = "pending"
	NoteAudioStatusUploaded = "uploaded"
	NoteAudioStatusDeleted  = "deleted"

	// limits
	NoteMaxPhotoSizeBytes = 15 * 1024 * 1024  // 15 MB
	NoteMaxAudioSizeBytes = 100 * 1024 * 1024 // 100 MB
	NoteTableMaxRows      = 500
	NoteTableMaxCols      = 100
	NoteChecklistMaxLen   = 500
	NoteTrashRetention    = 30 * 24 * time.Hour // 30 days retention in trash
)

// ---------------------------------------------------------------------------
// Rich content doc types
// ---------------------------------------------------------------------------

// ContentDoc is the top-level rich document stored in content_doc column.
// @Description Rich document containing mixed blocks (paragraph, checklist, table, image)
type ContentDoc struct {
	// always "doc"
	Type string `json:"type"`
	// document schema version, currently 1
	Version int               `json:"version"`
	Blocks  []ContentBlock    `json:"blocks"`
	Page    *NotePageMetadata `json:"page,omitempty"`
}

// NotePageMetadata is stored with the document, not in a browser-local cache.
type NotePageMetadata struct {
	Icon        string `json:"icon,omitempty"`
	Description string `json:"description,omitempty"`
}

// GalleryItem represents an item in an image gallery block.
type GalleryItem struct {
	ID           string `json:"id,omitempty"`
	URL          string `json:"url"`
	Caption      string `json:"caption,omitempty"`
	ThumbnailURL string `json:"thumbnail_url,omitempty"`
	Alt          string `json:"alt,omitempty"`
	Width        int    `json:"width,omitempty"`
	Height       int    `json:"height,omitempty"`
}

// ContentBlock is a discriminated union block inside a ContentDoc.
type ContentBlock struct {
	// unique within the document
	ID   string `json:"id"`
	Type string `json:"type"`

	// paragraph / heading / quote
	Runs     []ContentRun `json:"runs,omitempty"`
	ListType string       `json:"list_type,omitempty"`
	Level    int          `json:"level,omitempty"`

	// checklist
	Items []ChecklistBlockItem `json:"items,omitempty"`

	// table
	Rows [][]TableCell `json:"rows,omitempty"`

	// image / media
	AttachmentID string `json:"attachmentId,omitempty"`
	Alt          string `json:"alt,omitempty"`
	URL          string `json:"url,omitempty"`
	Caption      string `json:"caption,omitempty"`

	// gallery
	Images []GalleryItem `json:"images,omitempty"`

	// video
	EmbedURL string `json:"embed_url,omitempty"`
	Provider string `json:"provider,omitempty"`

	// file / document / pdf
	FileName string `json:"file_name,omitempty"`
	FileSize int64  `json:"file_size,omitempty"`
	MimeType string `json:"mime_type,omitempty"`

	// audio (NTS-004)
	Duration    *float64  `json:"duration,omitempty"`
	Waveform    []float64 `json:"waveform,omitempty"`
	IsRecording bool      `json:"is_recording,omitempty"`
}

// ContentRun is a text run inside a paragraph.
type ContentRun struct {
	Text  string   `json:"text"`
	Marks []string `json:"marks,omitempty"`
}

// ChecklistBlockItem is a single item in a checklist block.
type ChecklistBlockItem struct {
	ID      string `json:"id"`
	Text    string `json:"text"`
	Checked bool   `json:"checked"`
}

// TableCell is a single cell in a table block.
type TableCell struct {
	Text string `json:"text"`
}

// Validate returns an error if the ContentDoc violates MVP rules.
func (d *ContentDoc) Validate() error {
	if d == nil {
		return nil
	}
	if (d.Type != "" && d.Type != "doc") || (d.Version != 0 && d.Version != 1) {
		return fmt.Errorf("unsupported content_doc schema")
	}
	if len(d.Blocks) > 2000 {
		return fmt.Errorf("content_doc exceeds 2000 blocks")
	}
	if d.Page != nil && (len(d.Page.Icon) > 256 || len(d.Page.Description) > 16384) {
		return fmt.Errorf("content_doc page metadata exceeds size limit")
	}
	data, err := json.Marshal(d)
	if err != nil || len(data) > 2*1024*1024 {
		return fmt.Errorf("content_doc exceeds 2 MiB")
	}
	for i, b := range d.Blocks {
		if err := b.validate(i); err != nil {
			return err
		}
	}
	return nil
}

func (b *ContentBlock) validate(idx int) error {
	if b.ListType != "" && (b.Type != NoteBlockTypeParagraph || (b.ListType != "bullet" && b.ListType != "ordered")) {
		return fmt.Errorf("block[%d]: invalid list_type", idx)
	}
	switch b.Type {
	case NoteBlockTypeParagraph:
		if len(b.Runs) == 0 {
			return &ContentValidationError{
				Code:    ContentValidationInvalidParagraphRuns,
				Message: fmt.Sprintf("block[%d]: paragraph must contain at least one run", idx),
			}
		}
		for runIdx, run := range b.Runs {
			if len(run.Text) > 1024*1024 || len(run.Marks) > 10 {
				return fmt.Errorf("block[%d].runs[%d]: content limit exceeded", idx, runIdx)
			}
			seenMarks := make(map[string]struct{}, len(run.Marks))
			for _, mark := range run.Marks {
				isValid := validNoteMark(mark)
				if !isValid {
					return &ContentValidationError{
						Code:    ContentValidationInvalidMark,
						Message: fmt.Sprintf("block[%d].runs[%d]: unknown mark %q", idx, runIdx, mark),
					}
				}
				if _, exists := seenMarks[mark]; exists {
					return &ContentValidationError{
						Code:    ContentValidationInvalidParagraphRuns,
						Message: fmt.Sprintf("block[%d].runs[%d]: duplicate mark %q", idx, runIdx, mark),
					}
				}
				seenMarks[mark] = struct{}{}
			}
		}
	case NoteBlockTypeChecklist:
		if len(b.Items) > 1000 {
			return fmt.Errorf("block[%d]: checklist exceeds 1000 items", idx)
		}
		for j, item := range b.Items {
			if len([]rune(item.Text)) > NoteChecklistMaxLen {
				return fmt.Errorf("block[%d].items[%d]: text exceeds %d chars", idx, j, NoteChecklistMaxLen)
			}
		}
	case NoteBlockTypeTable:
		if len(b.Rows) == 0 {
			return fmt.Errorf("block[%d]: table must have at least 1 row", idx)
		}
		if len(b.Rows) > NoteTableMaxRows {
			return fmt.Errorf("block[%d]: table exceeds max rows (%d)", idx, NoteTableMaxRows)
		}
		expectedCols := len(b.Rows[0])
		for r, row := range b.Rows {
			if len(row) == 0 || len(row) > NoteTableMaxCols {
				return fmt.Errorf("block[%d].rows[%d]: column count must be 1..%d", idx, r, NoteTableMaxCols)
			}
			if len(row) != expectedCols {
				return fmt.Errorf("block[%d].rows[%d]: table must be rectangular", idx, r)
			}
		}
	case NoteBlockTypeImage:
		if b.AttachmentID == "" {
			return fmt.Errorf("block[%d]: image block missing attachmentId", idx)
		}
	case NoteBlockTypeAudio:
		if b.AttachmentID == "" && b.URL == "" {
			return fmt.Errorf("block[%d]: audio block missing attachmentId or url", idx)
		}
	default:
		return fmt.Errorf("block[%d]: unknown block type %q", idx, b.Type)
	}
	return nil
}

var noteHexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// Empty color clears the note label.
func ValidNoteColor(color string) bool { return color == "" || noteHexColor.MatchString(color) }

var noteRGBColor = regexp.MustCompile(`^rgb\(\s*(\d{1,3})\s*,\s*(\d{1,3})\s*,\s*(\d{1,3})\s*\)$`)

func validNoteMark(mark string) bool {
	if mark == NoteMarkBold || mark == NoteMarkItalic || mark == NoteMarkStrike || mark == "heading1" || mark == "heading2" || mark == "heading3" {
		return true
	}
	if !strings.HasPrefix(mark, "color:") {
		return false
	}
	color := strings.TrimPrefix(mark, "color:")
	if noteHexColor.MatchString(color) {
		return true
	}
	if parts := noteRGBColor.FindStringSubmatch(color); parts != nil {
		for _, part := range parts[1:] {
			var n int
			if _, err := fmt.Sscanf(part, "%d", &n); err != nil || n > 255 {
				return false
			}
		}
		return true
	}
	return false
}

// ExtractPlainText extracts a plain-text string from a ContentDoc for FTS.
func (d *ContentDoc) ExtractPlainText() string {
	if d == nil {
		return ""
	}
	buf := ""
	for _, b := range d.Blocks {
		switch b.Type {
		case NoteBlockTypeParagraph:
			for _, run := range b.Runs {
				buf += run.Text + " "
			}
		case NoteBlockTypeChecklist:
			for _, item := range b.Items {
				buf += item.Text + " "
			}
		case NoteBlockTypeTable:
			for _, row := range b.Rows {
				for _, cell := range row {
					buf += cell.Text + " "
				}
			}
		case NoteBlockTypeAudio:
			if b.Caption != "" {
				buf += b.Caption + " "
			}
		}
	}
	return buf
}

// MarshalContentDoc encodes to JSON bytes.
func MarshalContentDoc(d *ContentDoc) ([]byte, error) {
	if d == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(d)
}

// UnmarshalContentDoc decodes JSON bytes into ContentDoc.
func UnmarshalContentDoc(data []byte) (*ContentDoc, error) {
	if len(data) == 0 || string(data) == "null" || string(data) == "{}" {
		return nil, nil
	}
	var d ContentDoc
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

// ---------------------------------------------------------------------------
// NotePhotoAttachment (presigned-URL model)
// ---------------------------------------------------------------------------

// NotePhotoAttachment represents a photo attachment using presigned URL flow.
// @Description Photo attachment for a note, uploaded via presigned URL
type NotePhotoAttachment struct {
	ID          uuid.UUID `json:"id"`
	NoteID      uuid.UUID `json:"note_id"`
	UserID      int64     `json:"user_id"`
	StorageKey  string    `json:"storage_key,omitempty"`
	MimeType    string    `json:"mime_type"`
	SizeBytes   int64     `json:"size_bytes"`
	Width       *int      `json:"width,omitempty"`
	Height      *int      `json:"height,omitempty"`
	Status      string    `json:"status"`
	DownloadURL string    `json:"download_url,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// PresignRequest is the body for POST /notes/{id}/attachments/presign
// @Description Request to create a presigned upload URL for a note photo
type PresignRequest struct {
	MimeType  string `json:"mime_type" binding:"required"`
	SizeBytes int64  `json:"size_bytes" binding:"required"`
}

// PresignResponse is returned by the presign endpoint.
// @Description Presigned upload URL response
type PresignResponse struct {
	AttachmentID string    `json:"attachment_id"`
	UploadURL    string    `json:"upload_url"`
	StorageKey   string    `json:"storage_key"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// ---------------------------------------------------------------------------
// Legacy types kept for backwards compatibility
// ---------------------------------------------------------------------------

// ChecklistItem is the legacy checklist item (used in old note type).
type ChecklistItem struct {
	ID       uuid.UUID `json:"id"`
	Text     string    `json:"text"`
	IsDone   bool      `json:"is_done"`
	Position int       `json:"position"`
}

// NoteAttachment is the attachment type (direct-upload or merged attachments flow).
type NoteAttachment struct {
	FileID      uuid.UUID `json:"file_id"`
	ID          uuid.UUID `json:"id,omitempty"`
	RealName    string    `json:"real_name"`
	MimeType    string    `json:"mime_type"`
	Size        int64     `json:"size"`
	DownloadURL string    `json:"download_url,omitempty"`
	ObjectName  string    `json:"-"`
	Duration    *float64  `json:"duration,omitempty"`
	Waveform    []float64 `json:"waveform,omitempty"`
	IsRecording bool      `json:"is_recording,omitempty"`
	Status      string    `json:"status,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// NoteAudioAttachment represents an audio recording or uploaded audio file for a note.
// @Description Audio attachment for a note (voice recording or ready audio file)
type NoteAudioAttachment struct {
	ID          uuid.UUID `json:"id"`
	NoteID      uuid.UUID `json:"note_id"`
	UserID      int64     `json:"user_id"`
	StorageKey  string    `json:"storage_key,omitempty"`
	FileName    string    `json:"file_name"`
	MimeType    string    `json:"mime_type"`
	SizeBytes   int64     `json:"size_bytes"`
	Duration    *float64  `json:"duration,omitempty"`
	Waveform    []float64 `json:"waveform,omitempty"`
	IsRecording bool      `json:"is_recording"`
	Status      string    `json:"status"`
	DownloadURL string    `json:"download_url,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ---------------------------------------------------------------------------
// Note domain model
// ---------------------------------------------------------------------------

// Note is the core note entity.
// @Description Note entity with rich content support
type Note struct {
	Color            string                `json:"color"`
	ID               uuid.UUID             `json:"id"`
	UserID           int64                 `json:"user_id"`
	FolderID         *uuid.UUID            `json:"folder_id,omitempty"`
	Title            string                `json:"title"`
	Content          string                `json:"content"`
	ContentDoc       *ContentDoc           `json:"content_doc,omitempty"`
	ContentText      string                `json:"content_text,omitempty"`
	Type             string                `json:"type"`
	ChecklistItems   []ChecklistItem       `json:"checklist_items,omitempty"`
	IsPinned         bool                  `json:"is_pinned"`
	IsArchived       bool                  `json:"is_archived"`
	IsFavorite       bool                  `json:"is_favorite"`
	Tags             []string              `json:"tags"`
	IsDeleted        bool                  `json:"is_deleted"`
	DeletedAt        *time.Time            `json:"deleted_at,omitempty"`
	ExpiresAt        *time.Time            `json:"expires_at,omitempty"`
	CreatedAt        time.Time             `json:"created_at"`
	UpdatedAt        time.Time             `json:"updated_at"`
	Version          int                   `json:"version"`
	Attachments      []NoteAttachment      `json:"attachments,omitempty"`
	AudioAttachments []NoteAudioAttachment `json:"audio_attachments,omitempty"`
}

// ---------------------------------------------------------------------------
// Folder
// ---------------------------------------------------------------------------

// NoteFolder is a folder that groups notes.
// @Description Folder for organizing notes
type NoteFolder struct {
	ID        uuid.UUID `json:"id"`
	UserID    int64     `json:"user_id"`
	Name      string    `json:"name"`
	SortOrder int       `json:"sort_order"`
	Color     string    `json:"color"`
	NoteCount int       `json:"note_count"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ---------------------------------------------------------------------------
// Filter / response types
// ---------------------------------------------------------------------------

// NoteListFilter holds query params for listing notes.
type NoteListFilter struct {
	Scope      string // empty/all, owned, or shared
	UserID     int64
	FolderID   *uuid.UUID
	IsDeleted  *bool
	IsArchived *bool
	IsFavorite *bool
	Tag        string
	Query      string
	Limit      int
	Cursor     string
}

// NoteTagSummary represents a tag and the count of active notes using it.
// @Description Tag summary with usage count
type NoteTagSummary struct {
	Tag   string `json:"tag"`
	Count int    `json:"count"`
}

// NoteListResponse is the paginated list response.
// @Description Paginated list of notes
type NoteListResponse struct {
	Items      []Note `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}

// NoteSyncFilter holds sync query params.
type NoteSyncFilter struct {
	UserID int64
	Since  time.Time
	Limit  int
	Cursor string
}

// NoteSyncResponse is the sync endpoint response.
// @Description Sync response containing changed notes
type NoteSyncResponse struct {
	Items      []Note `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}

// ---------------------------------------------------------------------------
// Request types
// ---------------------------------------------------------------------------

// CreateNoteRequest is the body for POST /notes.
// @Description Request body for creating a note
type CreateNoteRequest struct {
	Color          string          `json:"color"`
	FolderID       *uuid.UUID      `json:"folder_id"`
	Title          string          `json:"title"`
	Content        string          `json:"content"`
	ContentDoc     *ContentDoc     `json:"content_doc"`
	Type           string          `json:"type"`
	ChecklistItems []ChecklistItem `json:"checklist_items"`
	IsPinned       bool            `json:"is_pinned"`
	IsArchived     bool            `json:"is_archived"`
	IsFavorite     bool            `json:"is_favorite"`
	Tags           []string        `json:"tags,omitempty"`
}

// UpdateNoteRequest is the body for PATCH /notes/{id}.
// @Description Request body for updating a note (all fields optional except version)
type UpdateNoteRequest struct {
	Color          *string          `json:"color"`
	Title          *string          `json:"title"`
	Content        *string          `json:"content"`
	ContentDoc     *ContentDoc      `json:"content_doc"`
	Type           *string          `json:"type"`
	ChecklistItems *[]ChecklistItem `json:"checklist_items"`
	IsPinned       *bool            `json:"is_pinned"`
	IsArchived     *bool            `json:"is_archived"`
	IsFavorite     *bool            `json:"is_favorite"`
	Tags           *[]string        `json:"tags,omitempty"`
	FolderID       *uuid.UUID       `json:"folder_id"`
	Version        int              `json:"version" binding:"required"`
}

// AutosaveNoteRequest is the body for POST /notes/autosave and PUT /notes/{id}/autosave.
// @Description Request body for autosaving a note (creates if ID is omitted/nil, updates otherwise)
type AutosaveNoteRequest struct {
	ID             *uuid.UUID       `json:"id,omitempty"`
	FolderID       *uuid.UUID       `json:"folder_id,omitempty"`
	Title          string           `json:"title"`
	Content        string           `json:"content"`
	ContentDoc     *ContentDoc      `json:"content_doc,omitempty"`
	Type           string           `json:"type,omitempty"`
	ChecklistItems *[]ChecklistItem `json:"checklist_items,omitempty"`
	Color          *string          `json:"color,omitempty"`
	Tags           *[]string        `json:"tags,omitempty"`
	IsFavorite     *bool            `json:"is_favorite,omitempty"`
	Version        int              `json:"version,omitempty"`
}

// FavoriteNoteRequest is the body for PATCH /notes/{id}/favorite.
// @Description Request body for toggling favorite status
type FavoriteNoteRequest struct {
	IsFavorite bool `json:"is_favorite"`
	Version    int  `json:"version" binding:"required"`
}

// MoveNoteRequest is the body for POST /notes/{id}/move.
// @Description Request body for moving a note to a different folder
type MoveNoteRequest struct {
	FolderID *uuid.UUID `json:"folder_id"`
	Version  int        `json:"version" binding:"required"`
}

// VersionedNoteActionRequest is used for pin/unpin/delete/restore/hard-delete.
// @Description Request body containing only a version for optimistic concurrency
type VersionedNoteActionRequest struct {
	Version int `json:"version" binding:"required"`
}

// CreateNoteFolderRequest is the body for creating a folder.
// @Description Request body for creating a note folder
type CreateNoteFolderRequest struct {
	Name      string `json:"name" binding:"required"`
	SortOrder int    `json:"sort_order"`
	Color     string `json:"color"`
}

// UpdateNoteFolderRequest is the body for updating a folder.
// @Description Request body for updating a note folder
type UpdateNoteFolderRequest struct {
	Name      *string `json:"name"`
	SortOrder *int    `json:"sort_order"`
	Color     *string `json:"color"`
}

// SyncBatchRequest bundles multiple sync operations.
// @Description Batch sync request with upserts, deletes, and restores
type SyncBatchRequest struct {
	Upserts     []SyncNoteUpsertRequest `json:"upserts"`
	Deletes     []SyncNoteActionItem    `json:"deletes"`
	Restores    []SyncNoteActionItem    `json:"restores"`
	HardDeletes []SyncNoteActionItem    `json:"hard_deletes"`
}

// SyncNoteUpsertRequest is a single note upsert inside a batch.
// @Description Single note upsert in a sync batch
type SyncNoteUpsertRequest struct {
	Color          *string         `json:"color"`
	ID             uuid.UUID       `json:"id"`
	FolderID       *uuid.UUID      `json:"folder_id"`
	Title          string          `json:"title"`
	Content        string          `json:"content"`
	ContentDoc     *ContentDoc     `json:"content_doc"`
	Type           string          `json:"type"`
	ChecklistItems []ChecklistItem `json:"checklist_items"`
	IsPinned       bool            `json:"is_pinned"`
	IsArchived     bool            `json:"is_archived"`
	IsFavorite     *bool           `json:"is_favorite,omitempty"`
	Tags           []string        `json:"tags,omitempty"`
	IsDeleted      bool            `json:"is_deleted"`
	DeletedAt      *time.Time      `json:"deleted_at"`
	Version        int             `json:"version"`
	UpdatedAt      *time.Time      `json:"updated_at"`
}

// SyncNoteActionItem identifies a note action in a batch.
// @Description Note action item with ID and version for sync batch
type SyncNoteActionItem struct {
	ID      uuid.UUID `json:"id"`
	Version int       `json:"version"`
}

// SyncBatchResponse contains results of a sync batch.
// @Description Response for a sync batch operation
type SyncBatchResponse struct {
	Upserts     []Note      `json:"upserts,omitempty"`
	Deletes     []uuid.UUID `json:"deletes,omitempty"`
	Restores    []uuid.UUID `json:"restores,omitempty"`
	HardDeletes []uuid.UUID `json:"hard_deletes,omitempty"`
}

// ---------------------------------------------------------------------------
// Error response
// ---------------------------------------------------------------------------

// ErrorResponse wraps an ErrorPayload.
// @Description Unified error response format
type ErrorResponse struct {
	Error ErrorPayload `json:"error"`
}

// ErrorPayload is the body of an error response.
// @Description Error details
type ErrorPayload struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// NormalizeTags trims, lowercases, deduplicates, and limits tags.
func NormalizeNoteTag(tag string) string {
	clean := strings.Trim(strings.ToLower(strings.TrimSpace(tag)), "# \t\r\n")
	runes := []rune(clean)
	if len(runes) > 50 {
		return string(runes[:50])
	}
	return string(runes)
}

func NormalizeTags(tags []string) []string {
	if len(tags) == 0 {
		return []string{}
	}
	seen := make(map[string]struct{}, len(tags))
	result := make([]string, 0, len(tags))
	for _, t := range tags {
		clean := NormalizeNoteTag(t)
		if clean == "" {
			continue
		}
		if _, ok := seen[clean]; !ok {
			seen[clean] = struct{}{}
			result = append(result, clean)
		}
		if len(result) >= 50 {
			break
		}
	}
	return result
}

// TrashSummaryResponse contains trash statistics.
// @Description Trash summary response
type TrashSummaryResponse struct {
	Count int64 `json:"count"`
}

// EmptyTrashResponse contains result of emptying trash.
// @Description Result of emptying trash
type EmptyTrashResponse struct {
	PurgedCount int64 `json:"purged_count"`
}

// CalculateTrashExpiresAt calculates expiration time for a soft-deleted note.
func CalculateTrashExpiresAt(deletedAt *time.Time) *time.Time {
	if deletedAt == nil {
		return nil
	}
	t := deletedAt.Add(NoteTrashRetention)
	return &t
}

// IsTrashExpired checks if the note in trash has passed retention window.
func IsTrashExpired(deletedAt *time.Time, now time.Time) bool {
	if deletedAt == nil {
		return false
	}
	return now.After(deletedAt.Add(NoteTrashRetention))
}

// ---------------------------------------------------------------------------
// Audio notes & attachments (NTS-004)
// ---------------------------------------------------------------------------

// PresignAudioRequest is the body for POST /notes/{id}/audio/presign
// @Description Request body for presigning an audio upload
type PresignAudioRequest struct {
	MimeType    string   `json:"mime_type" binding:"required"`
	SizeBytes   int64    `json:"size_bytes" binding:"required"`
	FileName    string   `json:"file_name,omitempty"`
	Duration    *float64 `json:"duration,omitempty"`
	IsRecording bool     `json:"is_recording,omitempty"`
}

// CompleteAudioRequest is the body for POST /notes/{id}/audio/{attachmentId}/complete
// @Description Confirmation body for completing an audio upload
type CompleteAudioRequest struct {
	FileName *string   `json:"file_name,omitempty"`
	Duration *float64  `json:"duration,omitempty"`
	Waveform []float64 `json:"waveform,omitempty"`
}

// CreateAudioNoteRequest is parsed from multipart form when creating an audio note directly
type CreateAudioNoteRequest struct {
	Title       string     `json:"title"`
	Content     string     `json:"content"`
	FolderID    *uuid.UUID `json:"folder_id"`
	Color       string     `json:"color"`
	Tags        []string   `json:"tags"`
	Duration    *float64   `json:"duration"`
	Waveform    []float64  `json:"waveform"`
	IsRecording bool       `json:"is_recording"`
	IsPinned    bool       `json:"is_pinned"`
	IsArchived  bool       `json:"is_archived"`
	IsFavorite  bool       `json:"is_favorite"`
}

var allowedAudioMimeTypes = map[string]bool{
	"audio/mpeg":      true,
	"audio/mp3":       true,
	"audio/mp4":       true,
	"audio/m4a":       true,
	"audio/x-m4a":     true,
	"audio/wav":       true,
	"audio/wave":      true,
	"audio/x-wav":     true,
	"audio/vnd.wave":  true,
	"audio/webm":      true,
	"video/webm":      true, // browsers sometimes record audio with video/webm container
	"audio/ogg":       true,
	"application/ogg": true,
	"audio/aac":       true,
	"audio/x-aac":     true,
	"audio/flac":      true,
	"audio/x-flac":    true,
}

var allowedAudioExtensions = map[string]bool{
	".mp3":  true,
	".m4a":  true,
	".wav":  true,
	".webm": true,
	".ogg":  true,
	".aac":  true,
	".flac": true,
}

// IsAllowedAudioMimeType checks if the given MIME type is an accepted audio format.
func IsAllowedAudioMimeType(mime string) bool {
	clean := strings.ToLower(strings.TrimSpace(mime))
	if idx := strings.Index(clean, ";"); idx != -1 {
		clean = strings.TrimSpace(clean[:idx])
	}
	return allowedAudioMimeTypes[clean]
}

// IsAllowedAudioExtension checks if the given filename has an accepted audio extension.
func IsAllowedAudioExtension(filename string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	return allowedAudioExtensions[ext]
}
