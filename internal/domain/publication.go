package domain

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	PublicationStatusDraft     = "draft"
	PublicationStatusPublished = "published"
	PublicationStatusScheduled = "scheduled"
	PublicationStatusArchived  = "archived"

	// Audience targeting
	PublicationAudienceAll        = "all"
	PublicationAudienceMinistry   = "ministry"
	PublicationAudienceBranch     = "branch"
	PublicationAudienceDepartment = "department"

	// Media attachment types
	AttachmentTypeImage    = "image"
	AttachmentTypeDocument = "document"
	AttachmentTypePDF      = "pdf"
	AttachmentTypeVideo    = "video"
	AttachmentTypeAudio    = "audio"
	AttachmentTypeLink     = "link"

	// Visual editor block types for publications
	PublicationBlockTypeParagraph = "paragraph"
	PublicationBlockTypeHeading   = "heading"
	PublicationBlockTypeQuote     = "quote"
	PublicationBlockTypeChecklist = "checklist"
	PublicationBlockTypeTable     = "table"
	PublicationBlockTypeImage     = "image"
	PublicationBlockTypeGallery   = "gallery"
	PublicationBlockTypeVideo     = "video"
	PublicationBlockTypeFile      = "file"

	// Comment & discussion moderation
	CommentStatusActive             = "active"
	CommentStatusHidden             = "hidden"
	CommentStatusDeletedByModerator = "deleted_by_moderator"

	ModerationActionHide    = "hide"
	ModerationActionRestore = "restore"
	ModerationActionDelete  = "delete"

	// Publication Importance (NWS-006)
	PublicationImportanceNormal    = "normal"
	PublicationImportanceImportant = "important"
	PublicationImportanceUrgent    = "urgent"

	// Banner Color Themes (NWS-006)
	BannerColorUrgent  = "urgent"
	BannerColorWarning = "warning"
	BannerColorInfo    = "info"
	BannerColorPrimary = "primary"

	// Notification event types
	NotificationTypePublicationAlert = "publication_alert"
)

type Publication struct {
	ID                      uuid.UUID   `json:"id" db:"id"`
	AuthorID                int64       `json:"author_id" db:"author_id"`
	ApprovedByID            *int64      `json:"approved_by_id,omitempty" db:"approved_by_id"`
	SourceType              string      `json:"source_type" db:"source_type"` // 'user' | 'ministry' | 'state_body' | 'department'
	MinistryID              *int64      `json:"ministry_id,omitempty" db:"ministry_id"`
	StateBodyID             *int64      `json:"state_body_id,omitempty" db:"state_body_id"`
	DepartmentID            *int64      `json:"department_id,omitempty" db:"department_id"`
	Type                    string      `json:"type" db:"type"` // 'news' | 'announcement' | 'document' | 'event' | 'appeal'
	Title                   string      `json:"title" db:"title"`
	Content                 *string     `json:"content,omitempty" db:"content"`
	ContentDoc              *ContentDoc `json:"content_doc,omitempty" db:"content_doc"`
	ContentHTML             string      `json:"content_html,omitempty" db:"content_html"`
	CoverImageURL           *string     `json:"cover_image_url,omitempty" db:"cover_image_url"`
	Status                  string      `json:"status" db:"status"`                   // 'draft' | 'published' | 'scheduled' | 'archived'
	TargetAudience          string      `json:"target_audience" db:"target_audience"` // 'all' | 'ministry' | 'branch' | 'department'
	TargetMinistryID        *int64      `json:"target_ministry_id,omitempty" db:"target_ministry_id"`
	TargetDepartmentID      *int64      `json:"target_department_id,omitempty" db:"target_department_id"`
	TargetMinistryIDs       []int64     `json:"target_ministry_ids,omitempty" db:"target_ministry_ids"`
	TargetDepartmentIDs     []int64     `json:"target_department_ids,omitempty" db:"target_department_ids"`
	Importance              string      `json:"importance" db:"importance"` // 'normal' | 'important' | 'urgent'
	IsPinned                bool        `json:"is_pinned" db:"is_pinned"`
	PinnedAt                *time.Time  `json:"pinned_at,omitempty" db:"pinned_at"`
	PinnedByID              *int64      `json:"pinned_by_id,omitempty" db:"pinned_by"`
	PinnedUntil             *time.Time  `json:"pinned_until,omitempty" db:"pinned_until"`
	IsBanner                bool        `json:"is_banner" db:"is_banner"`
	BannerUntil             *time.Time  `json:"banner_until,omitempty" db:"banner_until"`
	BannerActionText        *string     `json:"banner_action_text,omitempty" db:"banner_action_text"`
	BannerActionURL         *string     `json:"banner_action_url,omitempty" db:"banner_action_url"`
	BannerColor             *string     `json:"banner_color,omitempty" db:"banner_color"`
	NotificationSentAt      *time.Time  `json:"notification_sent_at,omitempty" db:"notification_sent_at"`
	AllowComments           bool        `json:"allow_comments" db:"allow_comments"`
	RequiresAcknowledgement bool        `json:"requires_acknowledgement" db:"requires_acknowledgement"`
	ScheduledAt             *time.Time  `json:"scheduled_at,omitempty" db:"scheduled_at"`
	PublishedAt             time.Time   `json:"published_at" db:"published_at"`
	ArchiveAt               *time.Time  `json:"archive_at,omitempty" db:"archive_at"`
	ArchivedAt              *time.Time  `json:"archived_at,omitempty" db:"archived_at"`
	ViewsCount              int         `json:"views_count" db:"views_count"`
	SharesCount             int         `json:"shares_count" db:"shares_count"`
	CreatedAt               time.Time   `json:"created_at" db:"created_at"`
	UpdatedAt               time.Time   `json:"updated_at" db:"updated_at"`
	DeletedAt               *time.Time  `json:"-" db:"deleted_at"`

	// Enriched fields loaded separately or via joins
	Attachments       []PublicationAttachment `json:"attachments,omitempty" db:"-"`
	LikesCount        int                     `json:"likes_count" db:"-"`
	CommentsCount     int                     `json:"comments_count" db:"-"`
	IsLiked           bool                    `json:"is_liked" db:"-"`
	IsAcknowledged    bool                    `json:"is_acknowledged" db:"-"`
	IsBannerDismissed bool                    `json:"is_banner_dismissed" db:"-"`
}

type PublicationAttachment struct {
	ID            uuid.UUID `json:"id" db:"id"`
	PublicationID uuid.UUID `json:"publication_id" db:"publication_id"`
	FileURL       string    `json:"file_url" db:"file_url"`
	FileName      string    `json:"file_name" db:"file_name"`
	FileType      string    `json:"file_type" db:"file_type"` // 'image' | 'file' | 'document' | 'pdf' | 'video' | 'audio' | 'link'
	FileSize      *int64    `json:"file_size,omitempty" db:"file_size"`
	Caption       *string   `json:"caption,omitempty" db:"caption"`
	OrderIndex    int       `json:"order_index" db:"order_index"`
	MimeType      *string   `json:"mime_type,omitempty" db:"mime_type"`
	ThumbnailURL  *string   `json:"thumbnail_url,omitempty" db:"thumbnail_url"`
	VideoProvider *string   `json:"video_provider,omitempty" db:"video_provider"`
	VideoEmbedURL *string   `json:"video_embed_url,omitempty" db:"video_embed_url"`
	CreatedAt     time.Time `json:"created_at" db:"created_at"`
}

type PublicationComment struct {
	ID               uuid.UUID  `json:"id" db:"id"`
	PublicationID    uuid.UUID  `json:"publication_id" db:"publication_id"`
	AuthorID         int64      `json:"author_id" db:"author_id"`
	ParentID         *uuid.UUID `json:"parent_id,omitempty" db:"parent_id"`
	ReplyToUserID    *int64     `json:"reply_to_user_id,omitempty" db:"reply_to_user_id"`
	Content          string     `json:"content" db:"content"`
	IsPinned         bool       `json:"is_pinned" db:"is_pinned"`
	PinnedAt         *time.Time `json:"pinned_at,omitempty" db:"pinned_at"`
	PinnedByID       *int64     `json:"pinned_by_id,omitempty" db:"pinned_by"`
	Status           string     `json:"status" db:"status"`
	IsHidden         bool       `json:"is_hidden" db:"is_hidden"`
	ModerationReason *string    `json:"moderation_reason,omitempty" db:"moderation_reason"`
	ModeratedByID    *int64     `json:"moderated_by_id,omitempty" db:"moderated_by"`
	ModeratedAt      *time.Time `json:"moderated_at,omitempty" db:"moderated_at"`
	CreatedAt        time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at" db:"updated_at"`
	DeletedAt        *time.Time `json:"-" db:"deleted_at"`
}

type CreatePublicationRequest struct {
	SourceType              string                    `json:"source_type" binding:"required,oneof=user ministry state_body department"`
	Type                    string                    `json:"type" binding:"required,oneof=news announcement document event appeal"`
	Title                   string                    `json:"title" binding:"required,max=500"`
	Content                 string                    `json:"content"`
	ContentDoc              *ContentDoc               `json:"content_doc,omitempty"`
	ContentHTML             string                    `json:"content_html,omitempty"`
	CoverImageURL           *string                   `json:"cover_image_url,omitempty"`
	Status                  string                    `json:"status,omitempty"` // 'draft' | 'published' | 'scheduled'
	TargetAudience          string                    `json:"target_audience" binding:"required,oneof=all ministry branch department"`
	TargetMinistryID        *int64                    `json:"target_ministry_id,omitempty"`
	TargetDepartmentID      *int64                    `json:"target_department_id,omitempty"`
	TargetMinistryIDs       []int64                   `json:"target_ministry_ids,omitempty"`
	TargetDepartmentIDs     []int64                   `json:"target_department_ids,omitempty"`
	Importance              string                    `json:"importance" binding:"required,oneof=normal important urgent"`
	IsPinned                *bool                     `json:"is_pinned,omitempty"`
	PinnedUntil             *time.Time                `json:"pinned_until,omitempty"`
	IsBanner                *bool                     `json:"is_banner,omitempty"`
	BannerUntil             *time.Time                `json:"banner_until,omitempty"`
	BannerActionText        *string                   `json:"banner_action_text,omitempty"`
	BannerActionURL         *string                   `json:"banner_action_url,omitempty"`
	BannerColor             *string                   `json:"banner_color,omitempty"`
	AllowComments           *bool                     `json:"allow_comments"` // pointer to know if it's set
	RequiresAcknowledgement bool                      `json:"requires_acknowledgement"`
	ScheduledAt             *time.Time                `json:"scheduled_at,omitempty"`
	ArchiveAt               *time.Time                `json:"archive_at,omitempty"`
	Attachments             []CreateAttachmentRequest `json:"attachments"`
}

type UpdatePublicationRequest struct {
	SourceType              string                    `json:"source_type" binding:"required,oneof=user ministry state_body department"`
	Type                    string                    `json:"type" binding:"required,oneof=news announcement document event appeal"`
	Title                   string                    `json:"title" binding:"required,max=500"`
	Content                 string                    `json:"content"`
	ContentDoc              *ContentDoc               `json:"content_doc,omitempty"`
	ContentHTML             string                    `json:"content_html,omitempty"`
	CoverImageURL           *string                   `json:"cover_image_url,omitempty"`
	Status                  string                    `json:"status,omitempty"`
	TargetAudience          string                    `json:"target_audience" binding:"required,oneof=all ministry branch department"`
	TargetMinistryID        *int64                    `json:"target_ministry_id,omitempty"`
	TargetDepartmentID      *int64                    `json:"target_department_id,omitempty"`
	TargetMinistryIDs       []int64                   `json:"target_ministry_ids,omitempty"`
	TargetDepartmentIDs     []int64                   `json:"target_department_ids,omitempty"`
	Importance              string                    `json:"importance" binding:"required,oneof=normal important urgent"`
	IsPinned                *bool                     `json:"is_pinned,omitempty"`
	PinnedUntil             *time.Time                `json:"pinned_until,omitempty"`
	IsBanner                *bool                     `json:"is_banner,omitempty"`
	BannerUntil             *time.Time                `json:"banner_until,omitempty"`
	BannerActionText        *string                   `json:"banner_action_text,omitempty"`
	BannerActionURL         *string                   `json:"banner_action_url,omitempty"`
	BannerColor             *string                   `json:"banner_color,omitempty"`
	AllowComments           *bool                     `json:"allow_comments"`
	RequiresAcknowledgement bool                      `json:"requires_acknowledgement"`
	ScheduledAt             *time.Time                `json:"scheduled_at,omitempty"`
	ArchiveAt               *time.Time                `json:"archive_at,omitempty"`
	Attachments             []CreateAttachmentRequest `json:"attachments"`
}

type CreateAttachmentRequest struct {
	ID            *uuid.UUID `json:"id,omitempty"`
	FileURL       string     `json:"file_url" binding:"required"`
	FileName      string     `json:"file_name" binding:"required"`
	FileType      string     `json:"file_type" binding:"required"`
	FileSize      *int64     `json:"file_size,omitempty"`
	Caption       *string    `json:"caption,omitempty"`
	OrderIndex    *int       `json:"order_index,omitempty"`
	MimeType      *string    `json:"mime_type,omitempty"`
	ThumbnailURL  *string    `json:"thumbnail_url,omitempty"`
	VideoProvider *string    `json:"video_provider,omitempty"`
	VideoEmbedURL *string    `json:"video_embed_url,omitempty"`
}

type AddVideoEmbedRequest struct {
	VideoURL string  `json:"video_url" binding:"required"`
	Caption  *string `json:"caption,omitempty"`
	Title    *string `json:"title,omitempty"`
}

type ReorderAttachmentsRequest struct {
	AttachmentIDs []uuid.UUID `json:"attachment_ids" binding:"required"`
}

type CreateCommentRequest struct {
	Content       string     `json:"content" binding:"required"`
	ParentID      *uuid.UUID `json:"parent_id,omitempty"`
	ReplyToUserID *int64     `json:"reply_to_user_id,omitempty"`
}

type ModerateCommentRequest struct {
	Action string  `json:"action" binding:"required,oneof=hide restore delete"`
	Reason *string `json:"reason,omitempty"`
}

type LockCommentsRequest struct {
	AllowComments bool    `json:"allow_comments"`
	Reason        *string `json:"reason,omitempty"`
}

type SetReactionRequest struct {
	Reaction string `json:"reaction" binding:"required,max=32"`
}

type PinPublicationRequest struct {
	PinnedUntil *time.Time `json:"pinned_until,omitempty"`
}

type SetBannerRequest struct {
	IsBanner         bool       `json:"is_banner"`
	BannerUntil      *time.Time `json:"banner_until,omitempty"`
	BannerActionText *string    `json:"banner_action_text,omitempty"`
	BannerActionURL  *string    `json:"banner_action_url,omitempty"`
	BannerColor      *string    `json:"banner_color,omitempty"`
}

type BroadcastPublicationRequest struct {
	SendPush  *bool `json:"send_push,omitempty"`
	SendEmail *bool `json:"send_email,omitempty"`
	Force     bool  `json:"force,omitempty"`
}

type BroadcastRecord struct {
	ID             uuid.UUID `json:"id"`
	PublicationID  uuid.UUID `json:"publication_id"`
	TriggeredBy    UserMini  `json:"triggered_by"`
	Importance     string    `json:"importance"`
	TargetAudience string    `json:"target_audience"`
	SentPushCount  int       `json:"sent_push_count"`
	SentEmailCount int       `json:"sent_email_count"`
	SentAt         time.Time `json:"sent_at"`
	Status         string    `json:"status"`
	ErrorMessage   *string   `json:"error_message,omitempty"`
}

type PublicationBroadcastStatsResponse struct {
	PublicationID          uuid.UUID         `json:"publication_id"`
	Importance             string            `json:"importance"`
	TotalTargetUsers       int               `json:"total_target_users"`
	AcknowledgedCount      int               `json:"acknowledged_count"`
	AcknowledgementPercent float64           `json:"acknowledgement_percent"`
	SentPushCount          int               `json:"sent_push_count"`
	SentEmailCount         int               `json:"sent_email_count"`
	LastBroadcastAt        *time.Time        `json:"last_broadcast_at,omitempty"`
	Broadcasts             []BroadcastRecord `json:"broadcasts"`
}

type PublicationAcknowledgementUser struct {
	User           UserMini  `json:"user"`
	AcknowledgedAt time.Time `json:"acknowledged_at"`
}

type PublicationAcknowledgementsResponse struct {
	TotalCount int                              `json:"total_count"`
	Users      []PublicationAcknowledgementUser `json:"users"`
}

type UserMini struct {
	ID        int64   `json:"id"`
	FirstName string  `json:"first_name"`
	LastName  string  `json:"last_name"`
	FullName  string  `json:"full_name"`
	AvatarURL *string `json:"avatar_url,omitempty"`
	Position  *string `json:"position,omitempty"`
}

type PublicationLikeUser struct {
	ID        int64     `json:"id"`
	FirstName string    `json:"first_name"`
	LastName  string    `json:"last_name"`
	FullName  string    `json:"full_name"`
	AvatarURL *string   `json:"avatar_url,omitempty"`
	Position  *string   `json:"position,omitempty"`
	LikedAt   time.Time `json:"liked_at"`
}

type PublicationViewUser struct {
	ID        int64     `json:"id"`
	FirstName string    `json:"first_name"`
	LastName  string    `json:"last_name"`
	FullName  string    `json:"full_name"`
	AvatarURL *string   `json:"avatar_url,omitempty"`
	Position  *string   `json:"position,omitempty"`
	ViewedAt  time.Time `json:"viewed_at"`
}

type PublicationLikesResponse struct {
	Likes []PublicationLikeUser `json:"likes"`
	Count int                   `json:"count"`
}

type PublicationActivityResponse struct {
	Likes      []PublicationLikeUser `json:"likes"`
	Views      []PublicationViewUser `json:"views"`
	LikesCount int                   `json:"likes_count"`
	ViewsCount int                   `json:"views_count"`
}

// NWS-007: Read analytics, acknowledgements and export models

type AcknowledgePublicationResponse struct {
	Status         string    `json:"status"`
	Acknowledged   bool      `json:"acknowledged"`
	AcknowledgedAt time.Time `json:"acknowledged_at"`
}

type PublicationDepartmentAnalytics struct {
	DepartmentID           int64   `json:"department_id"`
	DepartmentName         string  `json:"department_name"`
	TotalUsers             int     `json:"total_users"`
	ViewsCount             int     `json:"views_count"`
	ViewPercent            float64 `json:"view_percent"`
	AcknowledgedCount      int     `json:"acknowledged_count"`
	AcknowledgementPercent float64 `json:"acknowledgement_percent"`
}

type PublicationMinistryAnalytics struct {
	MinistryID             int64   `json:"ministry_id"`
	MinistryName           string  `json:"ministry_name"`
	TotalUsers             int     `json:"total_users"`
	ViewsCount             int     `json:"views_count"`
	ViewPercent            float64 `json:"view_percent"`
	AcknowledgedCount      int     `json:"acknowledged_count"`
	AcknowledgementPercent float64 `json:"acknowledgement_percent"`
}

type PublicationAnalyticsResponse struct {
	PublicationID           uuid.UUID                        `json:"publication_id"`
	Title                   string                           `json:"title"`
	RequiresAcknowledgement bool                             `json:"requires_acknowledgement"`
	TargetAudience          string                           `json:"target_audience"`
	TotalTargetUsers        int                              `json:"total_target_users"`
	ViewsCount              int                              `json:"views_count"`
	ViewPercent             float64                          `json:"view_percent"`
	AcknowledgedCount       int                              `json:"acknowledged_count"`
	AcknowledgementPercent  float64                          `json:"acknowledgement_percent"`
	UnreadCount             int                              `json:"unread_count"`
	UnacknowledgedCount     int                              `json:"unacknowledged_count"`
	FirstViewAt             *time.Time                       `json:"first_view_at,omitempty"`
	LastViewAt              *time.Time                       `json:"last_view_at,omitempty"`
	FirstAcknowledgedAt     *time.Time                       `json:"first_acknowledged_at,omitempty"`
	LastAcknowledgedAt      *time.Time                       `json:"last_acknowledged_at,omitempty"`
	DepartmentStats         []PublicationDepartmentAnalytics `json:"department_stats"`
	MinistryStats           []PublicationMinistryAnalytics   `json:"ministry_stats"`
}

type PublicationReaderItem struct {
	User           UserMini   `json:"user"`
	Email          string     `json:"email"`
	DepartmentID   *int64     `json:"department_id,omitempty"`
	DepartmentName *string    `json:"department_name,omitempty"`
	MinistryID     *int64     `json:"ministry_id,omitempty"`
	MinistryName   *string    `json:"ministry_name,omitempty"`
	IsViewed       bool       `json:"is_viewed"`
	ViewedAt       *time.Time `json:"viewed_at,omitempty"`
	IsAcknowledged bool       `json:"is_acknowledged"`
	AcknowledgedAt *time.Time `json:"acknowledged_at,omitempty"`
}

type PublicationReadersFilter struct {
	Status       string `form:"status"` // "all", "read", "unread", "acknowledged", "unacknowledged"
	DepartmentID *int64 `form:"department_id"`
	MinistryID   *int64 `form:"ministry_id"`
	Search       string `form:"search"`
	Page         int    `form:"page"`
	Limit        int    `form:"limit"`
}

type PublicationReadersResponse struct {
	PublicationID uuid.UUID               `json:"publication_id"`
	TotalCount    int                     `json:"total_count"`
	Page          int                     `json:"page"`
	Limit         int                     `json:"limit"`
	Readers       []PublicationReaderItem `json:"readers"`
}

type PublicationReadersExportFilter struct {
	Format       string `form:"format"` // "xlsx" or "csv"
	Status       string `form:"status"` // "all", "read", "unread", "acknowledged", "unacknowledged"
	DepartmentID *int64 `form:"department_id"`
	MinistryID   *int64 `form:"ministry_id"`
	Search       string `form:"search"`
}

type PublicationResponse struct {
	ID                      uuid.UUID               `json:"id"`
	SourceType              string                  `json:"source_type"` // 'user' | 'ministry' | 'state_body' | 'department'
	Type                    string                  `json:"type"`        // 'news' | 'announcement' | 'document' | 'event' | 'appeal'
	Title                   string                  `json:"title"`
	Content                 string                  `json:"content"`
	ContentDoc              *ContentDoc             `json:"content_doc,omitempty"`
	ContentHTML             string                  `json:"content_html,omitempty"`
	CoverImageURL           *string                 `json:"cover_image_url,omitempty"`
	Status                  string                  `json:"status"` // 'draft' | 'published' | 'scheduled' | 'archived'
	TargetAudience          string                  `json:"target_audience"`
	TargetMinistryID        *int64                  `json:"target_ministry_id,omitempty"`
	TargetDepartmentID      *int64                  `json:"target_department_id,omitempty"`
	TargetMinistryIDs       []int64                 `json:"target_ministry_ids,omitempty"`
	TargetDepartmentIDs     []int64                 `json:"target_department_ids,omitempty"`
	MinistryID              *int64                  `json:"ministry_id,omitempty"`
	StateBodyID             *int64                  `json:"state_body_id,omitempty"`
	DepartmentID            *int64                  `json:"department_id,omitempty"`
	Importance              string                  `json:"importance"`
	IsPinned                bool                    `json:"is_pinned"`
	PinnedAt                *time.Time              `json:"pinned_at,omitempty"`
	PinnedUntil             *time.Time              `json:"pinned_until,omitempty"`
	PinnedBy                *UserMini               `json:"pinned_by,omitempty"`
	IsBanner                bool                    `json:"is_banner"`
	BannerUntil             *time.Time              `json:"banner_until,omitempty"`
	BannerActionText        *string                 `json:"banner_action_text,omitempty"`
	BannerActionURL         *string                 `json:"banner_action_url,omitempty"`
	BannerColor             *string                 `json:"banner_color,omitempty"`
	IsBannerDismissed       bool                    `json:"is_banner_dismissed"`
	NotificationSentAt      *time.Time              `json:"notification_sent_at,omitempty"`
	AllowComments           bool                    `json:"allow_comments"`
	RequiresAcknowledgement bool                    `json:"requires_acknowledgement"`
	ScheduledAt             *time.Time              `json:"scheduled_at,omitempty"`
	PublishedAt             time.Time               `json:"published_at"`
	ArchiveAt               *time.Time              `json:"archive_at,omitempty"`
	ArchivedAt              *time.Time              `json:"archived_at,omitempty"`
	ViewsCount              int                     `json:"views_count"`
	LikesCount              int                     `json:"likes_count"`
	CommentsCount           int                     `json:"comments_count"`
	SharesCount             int                     `json:"shares_count"`
	IsLiked                 bool                    `json:"is_liked"`
	IsAcknowledged          bool                    `json:"is_acknowledged"`
	Acknowledged            bool                    `json:"acknowledged"`
	CreatedAt               time.Time               `json:"created_at"`
	UpdatedAt               time.Time               `json:"updated_at"`
	Attachments             []PublicationAttachment `json:"attachments"`
	Reactions               []ReactionSummary       `json:"reactions,omitempty"`
	UserReactions           []string                `json:"user_reactions,omitempty"`

	// Calculated UI presentation properties
	SourceName     string  `json:"source_name"`
	SourceSubtitle string  `json:"source_subtitle"`
	SourceAvatar   *string `json:"source_avatar"`

	// Detailed creator/approver info
	Author     UserMini  `json:"author"`
	ApprovedBy *UserMini `json:"approved_by,omitempty"`
}

type PublishPublicationRequest struct {
	ScheduledAt *time.Time `json:"scheduled_at,omitempty"`
	ArchiveAt   *time.Time `json:"archive_at,omitempty"`
}

type ArchivePublicationRequest struct {
	Reason *string `json:"reason,omitempty"`
}

type UnarchivePublicationRequest struct {
	TargetStatus string `json:"target_status,omitempty"` // 'published' (default) or 'draft'
}

type ArchiveObsoleteRequest struct {
	OlderThanDays *int       `json:"older_than_days,omitempty"`
	BeforeDate    *time.Time `json:"before_date,omitempty"`
	Types         []string   `json:"types,omitempty"`
}

type ArchiveObsoleteResponse struct {
	ArchivedCount int64      `json:"archived_count"`
	CutoffDate    *time.Time `json:"cutoff_date,omitempty"`
}

type UpdatePublicationCoverRequest struct {
	CoverImageURL string `json:"cover_image_url" binding:"required"`
}

type CommentResponse struct {
	ID               uuid.UUID         `json:"id"`
	PublicationID    uuid.UUID         `json:"publication_id"`
	ParentID         *uuid.UUID        `json:"parent_id,omitempty"`
	Content          string            `json:"content"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
	Author           UserMini          `json:"author"`
	ReplyToUser      *UserMini         `json:"reply_to_user,omitempty"`
	IsPinned         bool              `json:"is_pinned"`
	PinnedAt         *time.Time        `json:"pinned_at,omitempty"`
	PinnedBy         *UserMini         `json:"pinned_by,omitempty"`
	IsHidden         bool              `json:"is_hidden"`
	Status           string            `json:"status"`
	ModerationReason *string           `json:"moderation_reason,omitempty"`
	RepliesCount     int               `json:"replies_count"`
	Replies          []CommentResponse `json:"replies,omitempty"`
	Reactions        []ReactionSummary `json:"reactions,omitempty"`
}

var pubRGBColor = regexp.MustCompile(`^rgb\(\s*(\d{1,3})\s*,\s*(\d{1,3})\s*,\s*(\d{1,3})\s*\)$`)

func validPublicationMark(mark string) bool {
	switch mark {
	case NoteMarkBold, NoteMarkItalic, NoteMarkStrike, "underline", "code", "link",
		"heading1", "heading2", "heading3", "subscript", "superscript", "highlight":
		return true
	}
	if strings.HasPrefix(mark, "link:") {
		return ValidPublicationURL(strings.TrimPrefix(mark, "link:"))
	}
	if strings.HasPrefix(mark, "color:") {
		color := strings.TrimPrefix(mark, "color:")
		if ValidNoteColor(color) {
			return true
		}
		if parts := pubRGBColor.FindStringSubmatch(color); parts != nil {
			for _, part := range parts[1:] {
				var n int
				if _, err := fmt.Sscanf(part, "%d", &n); err != nil || n > 255 {
					return false
				}
			}
			return true
		}
	}
	return false
}

// ValidatePublicationContentDoc returns nil if doc conforms to publication editor schema.
func ValidatePublicationContentDoc(d *ContentDoc) error {
	if d == nil {
		return nil
	}
	if (d.Type != "" && d.Type != "doc") || (d.Version != 0 && d.Version != 1) {
		return fmt.Errorf("unsupported publication content_doc schema")
	}
	if len(d.Blocks) > 2000 {
		return fmt.Errorf("publication content_doc exceeds 2000 blocks")
	}
	data, err := json.Marshal(d)
	if err != nil || len(data) > 4*1024*1024 {
		return fmt.Errorf("publication content_doc exceeds 4 MiB")
	}

	for i, b := range d.Blocks {
		if err := validatePublicationBlock(i, &b); err != nil {
			return err
		}
	}
	return nil
}

func validatePublicationBlock(idx int, b *ContentBlock) error {
	for _, raw := range []string{b.URL, b.EmbedURL} {
		if raw != "" && !ValidPublicationURL(raw) {
			return fmt.Errorf("block[%d]: invalid media URL", idx)
		}
	}
	for _, img := range b.Images {
		for _, raw := range []string{img.URL, img.ThumbnailURL} {
			if raw != "" && !ValidPublicationURL(raw) {
				return fmt.Errorf("block[%d]: invalid gallery URL", idx)
			}
		}
	}
	if b.Type == PublicationBlockTypeVideo && b.URL != "" {
		if _, _, _, err := ParseVideoEmbed(b.URL); err != nil {
			return fmt.Errorf("block[%d]: invalid video URL", idx)
		}
	}
	if b.EmbedURL != "" {
		if _, _, _, err := ParseVideoEmbed(b.EmbedURL); err != nil {
			return fmt.Errorf("block[%d]: invalid video embed", idx)
		}
	}
	switch b.Type {
	case PublicationBlockTypeParagraph, PublicationBlockTypeHeading, PublicationBlockTypeQuote:
		for runIdx, run := range b.Runs {
			if len(run.Text) > 1024*1024 || len(run.Marks) > 20 {
				return fmt.Errorf("block[%d].runs[%d]: content limit exceeded", idx, runIdx)
			}
			seenMarks := make(map[string]struct{}, len(run.Marks))
			for _, mark := range run.Marks {
				if !validPublicationMark(mark) {
					return fmt.Errorf("block[%d].runs[%d]: unknown mark %q", idx, runIdx, mark)
				}
				if _, exists := seenMarks[mark]; exists {
					return fmt.Errorf("block[%d].runs[%d]: duplicate mark %q", idx, runIdx, mark)
				}
				seenMarks[mark] = struct{}{}
			}
		}
	case PublicationBlockTypeChecklist:
		if len(b.Items) > 1000 {
			return fmt.Errorf("block[%d]: checklist exceeds 1000 items", idx)
		}
		for j, item := range b.Items {
			if len([]rune(item.Text)) > NoteChecklistMaxLen {
				return fmt.Errorf("block[%d].items[%d]: text exceeds %d chars", idx, j, NoteChecklistMaxLen)
			}
		}
	case PublicationBlockTypeTable:
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
	case PublicationBlockTypeImage:
		if b.AttachmentID == "" && b.URL == "" {
			return fmt.Errorf("block[%d]: image block missing url or attachmentId", idx)
		}
	case PublicationBlockTypeGallery:
		if len(b.Images) == 0 {
			return fmt.Errorf("block[%d]: gallery must contain at least 1 image", idx)
		}
		if len(b.Images) > 50 {
			return fmt.Errorf("block[%d]: gallery exceeds 50 images", idx)
		}
		for imgIdx, img := range b.Images {
			if img.URL == "" && img.ThumbnailURL == "" {
				return fmt.Errorf("block[%d].images[%d]: missing image url", idx, imgIdx)
			}
		}
	case PublicationBlockTypeVideo:
		if b.EmbedURL == "" && b.URL == "" {
			return fmt.Errorf("block[%d]: video block missing embed_url or url", idx)
		}
	case PublicationBlockTypeFile, "document", "pdf":
		if b.AttachmentID == "" && b.URL == "" {
			return fmt.Errorf("block[%d]: file block missing attachmentId or url", idx)
		}
	default:
		return fmt.Errorf("block[%d]: unknown publication block type %q", idx, b.Type)
	}
	return nil
}

// ExtractPublicationPlainText extracts plain text from all blocks in a publication ContentDoc for search/FTS.
func ExtractPublicationPlainText(d *ContentDoc) string {
	if d == nil {
		return ""
	}
	var sb strings.Builder
	for _, b := range d.Blocks {
		switch b.Type {
		case PublicationBlockTypeParagraph, PublicationBlockTypeHeading, PublicationBlockTypeQuote:
			for _, r := range b.Runs {
				if r.Text != "" {
					sb.WriteString(r.Text)
					sb.WriteString(" ")
				}
			}
		case PublicationBlockTypeChecklist:
			for _, it := range b.Items {
				if it.Text != "" {
					sb.WriteString(it.Text)
					sb.WriteString(" ")
				}
			}
		case PublicationBlockTypeTable:
			for _, row := range b.Rows {
				for _, cell := range row {
					if cell.Text != "" {
						sb.WriteString(cell.Text)
						sb.WriteString(" ")
					}
				}
			}
		case PublicationBlockTypeImage:
			if b.Caption != "" {
				sb.WriteString(b.Caption)
				sb.WriteString(" ")
			} else if b.Alt != "" {
				sb.WriteString(b.Alt)
				sb.WriteString(" ")
			}
		case PublicationBlockTypeGallery:
			if b.Caption != "" {
				sb.WriteString(b.Caption)
				sb.WriteString(" ")
			}
			for _, item := range b.Images {
				if item.Caption != "" {
					sb.WriteString(item.Caption)
					sb.WriteString(" ")
				} else if item.Alt != "" {
					sb.WriteString(item.Alt)
					sb.WriteString(" ")
				}
			}
		case PublicationBlockTypeVideo:
			if b.Caption != "" {
				sb.WriteString(b.Caption)
				sb.WriteString(" ")
			}
		case PublicationBlockTypeFile, "document", "pdf":
			if b.FileName != "" {
				sb.WriteString(b.FileName)
				sb.WriteString(" ")
			}
			if b.Caption != "" {
				sb.WriteString(b.Caption)
				sb.WriteString(" ")
			}
		}
	}
	return strings.TrimSpace(sb.String())
}
