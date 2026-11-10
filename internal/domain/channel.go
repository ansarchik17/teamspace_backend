package domain

import (
	"time"

	"github.com/google/uuid"
)

// ChannelType: public | private
type ChannelType string

const (
	ChannelTypePublic  ChannelType = "public"
	ChannelTypePrivate ChannelType = "private"
)

// ChannelRole: owner | admin | subscriber
type ChannelRole string

const (
	ChannelRoleOwner      ChannelRole = "owner"
	ChannelRoleAdmin      ChannelRole = "admin"
	ChannelRoleSubscriber ChannelRole = "subscriber"
)

// Channel — главная сущность
type Channel struct {
	ID          uuid.UUID   `json:"id"           db:"id"`
	Name        string      `json:"name"         db:"name"`
	Description *string     `json:"description"  db:"description"`
	Username    *string     `json:"username"     db:"username"` // @handle
	AvatarURL   *string     `json:"avatar_url"   db:"avatar_url"`
	Type        ChannelType `json:"type"         db:"type"`
	CreatedBy   int64       `json:"created_by"   db:"created_by"`
	IsActive    bool        `json:"is_active"    db:"is_active"`
	MemberCount int         `json:"member_count" db:"member_count"`
	CreatedAt   time.Time   `json:"created_at"   db:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"   db:"updated_at"`
	DeletedAt   *time.Time  `json:"-"            db:"deleted_at"`
}

// ChannelView — для API ответа (обогащённые данные)
type ChannelView struct {
	Channel
	MyRole       *ChannelRole `json:"my_role,omitempty"` // роль текущего пользователя
	IsSubscribed bool         `json:"is_subscribed"`
	IsMuted      bool         `json:"is_muted"`
	UnreadCount  int          `json:"unread_count"`
	LastPost     *ChannelPost `json:"last_post,omitempty"`
}

// ChannelMember — подписчик канала
type ChannelMember struct {
	ChannelID uuid.UUID   `json:"channel_id" db:"channel_id"`
	UserID    int64       `json:"user_id"    db:"user_id"`
	Role      ChannelRole `json:"role"       db:"role"`
	IsMuted   bool        `json:"is_muted"   db:"is_muted"`
	JoinedAt  time.Time   `json:"joined_at"  db:"joined_at"`
	LeftAt    *time.Time  `json:"left_at"    db:"left_at"`
	CreatedAt time.Time   `json:"created_at" db:"created_at"`

	// Enriched fields for API
	UserFullName *string `json:"user_full_name,omitempty"`
	UserUsername *string `json:"user_username,omitempty"`
	UserAvatar   *string `json:"user_avatar,omitempty"`
}

// ChannelPost — пост в канале
type ChannelPost struct {
	ID                uuid.UUID  `json:"id"                         db:"id"`
	ChannelID         uuid.UUID  `json:"channel_id"                 db:"channel_id"`
	AuthorID          int64      `json:"author_id"                  db:"author_id"`
	ReplyToID         *uuid.UUID `json:"reply_to_id,omitempty"      db:"reply_to_id"`
	Content           *string    `json:"content"                    db:"content"`
	AttachmentURL     *string    `json:"attachment_url"             db:"attachment_url"`
	AttachmentType    *string    `json:"attachment_type"            db:"attachment_type"`
	AttachmentSize    *int64     `json:"attachment_size"            db:"attachment_size"`
	AttachmentName    *string    `json:"attachment_name,omitempty"  db:"attachment_name"`
	IsPinned          bool       `json:"is_pinned"                  db:"is_pinned"`
	IsDeleted         bool       `json:"is_deleted"                 db:"is_deleted"`
	IsEdited          bool       `json:"is_edited"                  db:"is_edited"`
	IsForwarded       bool       `json:"is_forwarded"               db:"is_forwarded"`
	OriginalChannelID *uuid.UUID `json:"original_channel_id,omitempty" db:"original_channel_id"`
	OriginalPostID    *uuid.UUID `json:"original_post_id,omitempty"    db:"original_post_id"`
	ViewsCount        int        `json:"views_count"                db:"views_count"`
	CommentsCount     int        `json:"comments_count"             db:"comments_count"`
	LinkedGroupID     *uuid.UUID `json:"linked_group_id,omitempty"  db:"linked_group_id"`
	Metadata          *string    `json:"metadata,omitempty"         db:"metadata"`
	EditedAt          *time.Time `json:"edited_at,omitempty"        db:"edited_at"`
	CreatedAt         time.Time  `json:"created_at"                 db:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"                 db:"updated_at"`
	DeletedAt         *time.Time `json:"-"                          db:"deleted_at"`
}

// ChannelPostView — для API ответа (обогащённые данные)
type ChannelPostView struct {
	ChannelPost
	AuthorName    string            `json:"author_name"`
	AuthorAvatar  *string           `json:"author_avatar,omitempty"`
	ChannelName   string            `json:"channel_name"`
	ChannelAvatar *string           `json:"channel_avatar,omitempty"`
	Reactions     []ReactionSummary `json:"reactions"`
	IsViewed      bool              `json:"is_viewed"`
}

// ChannelPostReplyNode represents one post in a nested channel reply tree.
type ChannelPostReplyNode struct {
	Post    *ChannelPostView        `json:"post"`
	Replies []*ChannelPostReplyNode `json:"replies"`
}

type ChannelPostThreadResponse struct {
	Root       *ChannelPostView        `json:"root"`
	Replies    []*ChannelPostReplyNode `json:"replies"`
	ReplyCount int                     `json:"reply_count"`
	Truncated  bool                    `json:"truncated"`
}

// ChannelPostReaction — реакция на пост
type ChannelPostReaction struct {
	PostID    uuid.UUID `json:"post_id"    db:"post_id"`
	UserID    int64     `json:"user_id"    db:"user_id"`
	Reaction  string    `json:"reaction"   db:"reaction"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}
