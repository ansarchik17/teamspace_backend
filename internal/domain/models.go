package domain

import (
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/fileurl"
)

const (
	StatusOnline   = "online"
	StatusOffline  = "offline"
	StatusSick     = "sick"
	StatusVacation = "vacation"
)

// User represents a user in the system
type User struct {
	EmploymentAdmission *EmploymentAdmission `json:"employment_admission,omitempty" db:"-"`
	Language            string               `json:"language" db:"language"`
	ID                  int64                `json:"id" db:"id"`
	KeycloakID          *string              `json:"keycloak_id,omitempty" db:"keycloak_id"`
	IIN                 *string              `json:"iin,omitempty" db:"iin"`

	Email        string `json:"email" db:"email"`
	Username     string `json:"username" db:"username"`
	PasswordHash string `json:"-" db:"password_hash"`

	FirstName                string     `json:"first_name" db:"first_name"`
	LastName                 string     `json:"last_name" db:"last_name"`
	AvatarURL                *string    `json:"avatar_url,omitempty" db:"avatar_url"`
	Bio                      *string    `json:"bio,omitempty" db:"bio"`
	Position                 *string    `json:"position,omitempty" db:"position"`
	IsActive                 bool       `json:"is_active" db:"is_active"`
	AvailabilityStatus       *string    `json:"availability_status" db:"availability_status"`
	FullNameLocal            *string    `json:"full_name_local,omitempty" db:"full_name_local"`
	CanViewFullDriveActivity bool       `json:"can_view_full_drive_activity" db:"can_view_full_drive_activity"`
	IsFirstLogin             bool       `json:"is_first_login" db:"is_first_login"`
	LastLoginAt              *time.Time `json:"last_login_at,omitempty" db:"last_login_at"`
	LastSeenAt               *time.Time `json:"last_seen_at,omitempty" db:"last_seen_at"`
	CreatedAt                time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt                time.Time  `json:"updated_at" db:"updated_at"`
	DeletedAt                *time.Time `json:"-" db:"deleted_at"`

	Role Role `json:"role" db:"role"`
	// OrganizationID, TeamID and ManagerID are the neutral corporate scope.
	// The legacy hierarchy fields below are kept only for the data migration
	// boundary and are intentionally never serialized to API clients.
	OrganizationID        *uuid.UUID `json:"organization_id,omitempty" db:"organization_id"`
	TeamID                *uuid.UUID `json:"team_id,omitempty" db:"team_id"`
	ManagerID             *int64     `json:"manager_id,omitempty" db:"manager_id"`
	PrimeMinisterID       *int64     `json:"-" db:"prime_minister_id"`
	DeputyPrimeMinisterID *int64     `json:"-" db:"deputy_prime_minister_id"`
	MinistryID            *int64     `json:"-" db:"ministry_id"`
	StateBodyID           *int64     `json:"-" db:"state_body_id"`
	DepartmentID          *int64     `json:"-" db:"department_id"`
	DivisionID            *int64     `json:"-" db:"division_id"`
	PositionName          string     `json:"position_name,omitempty" db:"-"`
	DepartmentName        string     `json:"department_name,omitempty" db:"-"`
	OrganizationName      string     `json:"organization_name,omitempty" db:"-"`
}

type Ministry struct {
	ID                    int64         `json:"id" db:"id"`
	Name                  string        `json:"name" db:"name"`
	LeaderID              *int64        `json:"leader_id,omitempty" db:"leader_id"`
	HeadOfStaffID         *int64        `json:"head_of_staff_id,omitempty" db:"head_of_staff_id"`
	DeputyPrimeMinisterID *int64        `json:"deputy_prime_minister_id,omitempty" db:"deputy_prime_minister_id"`
	AdminID               *int64        `json:"admin_id,omitempty" db:"admin_id"`
	AdminName             string        `json:"admin_name,omitempty" db:"-"`
	AdminUser             *UserResponse `json:"admin_user,omitempty" db:"-"`
	CreatedAt             time.Time     `json:"created_at" db:"created_at"`
}
type UserFilters struct {
	PositionName     string `json:"position_name"`
	DepartmentName   string `json:"department_name"`
	OrganizationName string `json:"organization_name"`
}

type UserResponse struct {
	EmploymentAdmission *EmploymentAdmission `json:"employment_admission,omitempty"`
	Language            string               `json:"language"`
	ID                  int64                `json:"id"`
	KeycloakID          *string              `json:"keycloak_id,omitempty"`
	IIN                 *string              `json:"iin,omitempty"`

	Email    string `json:"email"`
	Username string `json:"username"`

	FirstName                          string     `json:"first_name"`
	LastName                           string     `json:"last_name"`
	FullName                           string     `json:"full_name"`
	AvatarURL                          *string    `json:"avatar_url,omitempty"`
	Bio                                *string    `json:"bio,omitempty"`
	Position                           *string    `json:"position,omitempty"`
	IsActive                           bool       `json:"is_active"`
	AvailabilityStatus                 *string    `json:"availability_status"`
	FullNameLocal                      *string    `json:"full_name_local,omitempty"`
	CanViewFullDriveActivity           bool       `json:"can_view_full_drive_activity"`
	CanViewFullDriveActivityByPosition bool       `json:"can_view_full_drive_activity_by_position"`
	CanViewFullDriveActivityEffective  bool       `json:"can_view_full_drive_activity_effective"`
	IsFirstLogin                       bool       `json:"is_first_login"`
	LastLoginAt                        *time.Time `json:"last_login_at,omitempty"`
	LastSeenAt                         *time.Time `json:"last_seen_at,omitempty"`
	CreatedAt                          time.Time  `json:"created_at"`

	Role                  Role       `json:"role"`
	OrganizationID        *uuid.UUID `json:"organization_id,omitempty"`
	TeamID                *uuid.UUID `json:"team_id,omitempty"`
	ManagerID             *int64     `json:"manager_id,omitempty"`
	PrimeMinisterID       *int64     `json:"-"`
	DeputyPrimeMinisterID *int64     `json:"-"`
	MinistryID            *int64     `json:"-"`
	StateBodyID           *int64     `json:"-"`
	DepartmentID          *int64     `json:"-"`
	DivisionID            *int64     `json:"-"`
	PositionName          string     `json:"position_name,omitempty"`
	DepartmentName        string     `json:"department_name,omitempty"`
	OrganizationName      string     `json:"organization_name,omitempty"`
	IsSupport             bool       `json:"is_support"`
	DocType               string     `json:"doc_type,omitempty"`
	JobName               string     `json:"job_name,omitempty"`
	SubName               string     `json:"sub_name,omitempty"`
	PersonStatus          string     `json:"person_status,omitempty"`
	AbsenceStatus         string     `json:"absence_status,omitempty"`
	LastOrderNumber       string     `json:"last_order_number,omitempty"`
	LastOrderDate         *time.Time `json:"last_order_date,omitempty"`
}

// ToResponse converts User to UserResponse
func (u *User) ToResponse() *UserResponse {
	language := u.Language
	if language == "" {
		language = "ru"
	}
	full := u.FirstName + " " + u.LastName
	posName := u.PositionName
	if posName == "" {
		posName = derefString(u.Position)
	}
	return &UserResponse{
		EmploymentAdmission:                u.EmploymentAdmission,
		Language:                           language,
		ID:                                 u.ID,
		KeycloakID:                         u.KeycloakID,
		IIN:                                u.IIN,
		Email:                              u.Email,
		Username:                           u.Username,
		FirstName:                          u.FirstName,
		LastName:                           u.LastName,
		FullName:                           full,
		AvatarURL:                          fileurl.SignLegacyDownloadURL(u.AvatarURL),
		Bio:                                u.Bio,
		Position:                           u.Position,
		IsActive:                           u.IsActive,
		AvailabilityStatus:                 u.AvailabilityStatus,
		FullNameLocal:                      u.FullNameLocal,
		CanViewFullDriveActivity:           u.CanViewFullDriveActivity,
		CanViewFullDriveActivityByPosition: PositionGrantsFullDriveActivity(posName),
		CanViewFullDriveActivityEffective:  u.CanViewFullDriveActivity || PositionGrantsFullDriveActivity(posName),
		IsFirstLogin:                       u.IsFirstLogin,
		LastLoginAt:                        u.LastLoginAt,
		LastSeenAt:                         u.LastSeenAt,
		CreatedAt:                          u.CreatedAt,
		Role:                               u.Role,
		OrganizationID:                     u.OrganizationID,
		TeamID:                             u.TeamID,
		ManagerID:                          u.ManagerID,
		PrimeMinisterID:                    u.PrimeMinisterID,
		DeputyPrimeMinisterID:              u.DeputyPrimeMinisterID,
		MinistryID:                         u.MinistryID,
		StateBodyID:                        u.StateBodyID,
		DepartmentID:                       u.DepartmentID,
		DivisionID:                         u.DivisionID,
		PositionName:                       posName,
		DepartmentName:                     u.DepartmentName,
		OrganizationName:                   u.OrganizationName,
		IsSupport:                          u.ID == SupportUserID,
	}
}

func (r *UserResponse) ApplyHRProfile(profile *UserHRProfile) {
	if r == nil || profile == nil {
		return
	}

	if r.IIN == nil && profile.IIN != "" {
		iin := profile.IIN
		r.IIN = &iin
	}

	r.DocType = profile.DocType
	r.JobName = profile.JobName
	r.SubName = profile.SubName
	r.PersonStatus = profile.PersonStatus
	r.AbsenceStatus = profile.AbsenceStatus
	r.LastOrderNumber = profile.LastOrderNumber
	r.LastOrderDate = profile.LastOrderDate

	if profile.JobName != "" {
		r.PositionName = profile.JobName
	}
	if profile.SubName != "" {
		r.DepartmentName = profile.SubName
	}
	if profile.NameGO != "" {
		r.OrganizationName = profile.NameGO
	}
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

const SupportUserID = 44

// UserDetailResponse includes full hierarchy names
type UserDetailResponse struct {
	UserResponse
	PrimeMinisterName       *string          `json:"-"`
	DeputyPrimeMinisterName *string          `json:"-"`
	MinistryName            *string          `json:"-"`
	StateBodyName           *string          `json:"-"`
	DepartmentName          *string          `json:"department_name,omitempty"`
	DivisionName            *string          `json:"-"`
	BossID                  *int64           `json:"boss_id,omitempty"`
	BossName                *string          `json:"boss_name,omitempty"`
	ReviewStats             *UserReviewStats `json:"review_stats,omitempty"`
	HasReviewedThisMonth    bool             `json:"has_reviewed_this_month"`
}

// UserBasic represents basic user info for permission lists
type UserBasic struct {
	ID        int64   `json:"id"`
	Email     string  `json:"email"`
	Username  string  `json:"username"`
	FirstName string  `json:"first_name"`
	LastName  string  `json:"last_name"`
	AvatarURL *string `json:"avatar_url,omitempty"`
}

// UserPermission represents user with their permission role
type UserPermission struct {
	ID        int64          `json:"id"`
	Email     string         `json:"email"`
	Username  string         `json:"username"`
	FirstName string         `json:"first_name"`
	LastName  string         `json:"last_name"`
	AvatarURL *string        `json:"avatar_url,omitempty"`
	Role      PermissionRole `json:"role"`
}

// Conversation represents a chat conversation
type Conversation struct {
	ID        uuid.UUID  `json:"id" db:"id"`
	Type      string     `json:"type" db:"type"` // personal or group
	CreatedBy int64      `json:"created_by" db:"created_by"`
	CreatedAt time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt time.Time  `json:"updated_at" db:"updated_at"`
	DeletedAt *time.Time `json:"-" db:"deleted_at"`
}

// ConversationParticipant represents a participant in a conversation
type ConversationParticipant struct {
	ConversationID uuid.UUID  `json:"conversation_id" db:"conversation_id"`
	UserID         int64      `json:"user_id" db:"user_id"`
	JoinedAt       time.Time  `json:"joined_at" db:"joined_at"`
	LeftAt         *time.Time `json:"left_at,omitempty" db:"left_at"`
	LastReadAt     *time.Time `json:"last_read_at,omitempty" db:"last_read_at"`
	IsMuted        bool       `json:"is_muted" db:"is_muted"`
	IsBlocked      bool       `json:"is_blocked" db:"is_blocked"`
	CreatedAt      time.Time  `json:"created_at" db:"created_at"`
}

// Message represents a chat message
type Message struct {
	ID               uuid.UUID         `json:"id" db:"id"`
	ConversationID   uuid.UUID         `json:"conversation_id" db:"conversation_id"`
	SenderID         int64             `json:"sender_id" db:"sender_id"`
	ReplyToID        *uuid.UUID        `json:"reply_to_id,omitempty" db:"reply_to_id"`
	Content          *string           `json:"content,omitempty" db:"content"`
	ContentHash      *string           `json:"content_hash,omitempty" db:"content_hash"`
	EncryptedKeys    map[string]string `json:"encrypted_keys,omitempty" db:"encrypted_keys"`
	Type             string            `json:"type" db:"type"` // text, image, video, file, audio, etc.
	AttachmentURL    *string           `json:"attachment_url,omitempty" db:"attachment_url"`
	AttachmentType   *string           `json:"attachment_type,omitempty" db:"attachment_type"`
	AttachmentSize   *int64            `json:"attachment_size,omitempty" db:"attachment_size"`
	AttachmentName   *string           `json:"attachment_name,omitempty" db:"attachment_name"`
	Metadata         *string           `json:"metadata,omitempty" db:"metadata"` // JSON metadata
	IsEdited         bool              `json:"is_edited" db:"is_edited"`
	IsForwarded      bool              `json:"is_forwarded" db:"is_forwarded"`
	OriginalSenderID *int64            `json:"original_sender_id,omitempty" db:"original_sender_id"`
	IsDeleted        bool              `json:"is_deleted" db:"is_deleted"`
	CreatedAt        time.Time         `json:"created_at" db:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at" db:"updated_at"`
	EditedAt         *time.Time        `json:"edited_at,omitempty" db:"edited_at"`
	DeletedAt        *time.Time        `json:"-" db:"deleted_at"`
	IsEphemeral      bool              `json:"is_ephemeral" db:"is_ephemeral"`
	TTLSeconds       *int              `json:"ttl_seconds,omitempty" db:"ttl_seconds"`
	ExpiresAt        *time.Time        `json:"expires_at,omitempty" db:"expires_at"`
}

// MessageStatus represents message delivery/read status
type MessageStatus struct {
	ID          uuid.UUID  `json:"id" db:"id"`
	MessageID   uuid.UUID  `json:"message_id" db:"message_id"`
	UserID      int64      `json:"user_id" db:"user_id"`
	DeliveredAt *time.Time `json:"delivered_at,omitempty" db:"delivered_at"`
	ReadAt      *time.Time `json:"read_at,omitempty" db:"read_at"`
	CreatedAt   time.Time  `json:"created_at" db:"created_at"`
}

// MessageReaction represents a user reaction to a message
type MessageReaction struct {
	ID           uuid.UUID `json:"id" db:"id"`
	MessageID    uuid.UUID `json:"message_id" db:"message_id"`
	UserID       int64     `json:"user_id" db:"user_id"`
	UserFullName string    `json:"user_full_name,omitempty"` // Added for display (First + Last)
	UserAvatar   *string   `json:"user_avatar,omitempty"`
	Reaction     string    `json:"reaction" db:"reaction"`
	CreatedAt    time.Time `json:"created_at" db:"created_at"`
}

// ReactionUser represents a user who reacted to a message
type ReactionUser struct {
	ID       int64   `json:"id"`
	FullName string  `json:"full_name"`
	Avatar   *string `json:"avatar,omitempty"`
}

// ReactionSummary groups reactions by emoji
type ReactionSummary struct {
	Reaction string         `json:"reaction"`
	Count    int            `json:"count"`
	Users    []ReactionUser `json:"users,omitempty"` // Detailed users who reacted
	Reacted  bool           `json:"reacted"`         // Whether current user reacted
}

// Group represents a group chat
type Group struct {
	ID          uuid.UUID  `json:"id" db:"id"`
	Name        string     `json:"name" db:"name"`
	Description *string    `json:"description,omitempty" db:"description"`
	AvatarURL   *string    `json:"avatar_url,omitempty" db:"avatar_url"`
	Type        string     `json:"type" db:"type"` // public, private
	MaxMembers  int        `json:"max_members" db:"max_members"`
	CreatedBy   int64      `json:"created_by" db:"created_by"`
	IsActive    bool       `json:"is_active" db:"is_active"`
	CreatedAt   time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at" db:"updated_at"`
	DeletedAt   *time.Time `json:"-" db:"deleted_at"`
}

// GroupMember represents a member of a group
type GroupMember struct {
	GroupID   uuid.UUID  `json:"group_id" db:"group_id"`
	UserID    int64      `json:"user_id" db:"user_id"`
	Role      string     `json:"role" db:"role"` // owner, admin, member
	JoinedAt  time.Time  `json:"joined_at" db:"joined_at"`
	LeftAt    *time.Time `json:"left_at,omitempty" db:"left_at"`
	IsMuted   bool       `json:"is_muted" db:"is_muted"`
	CreatedAt time.Time  `json:"created_at" db:"created_at"`
}

// PinnedChat represents a pinned chat
type PinnedChat struct {
	ID        uuid.UUID `json:"id" db:"id"`
	UserID    int64     `json:"user_id" db:"user_id"`
	ChatID    uuid.UUID `json:"chat_id" db:"chat_id"`
	ChatType  string    `json:"chat_type" db:"chat_type"` // conversation or group
	Position  int       `json:"position" db:"position"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

// PinnedMessage represents a pinned message
type PinnedMessage struct {
	ID        uuid.UUID `json:"id" db:"id"`
	ChatID    uuid.UUID `json:"chat_id" db:"chat_id"`
	ChatType  string    `json:"chat_type" db:"chat_type"`
	MessageID uuid.UUID `json:"message_id" db:"message_id"`
	PinnedBy  int64     `json:"pinned_by" db:"pinned_by"`
	Position  int       `json:"position" db:"position"`
	Content   string    `json:"content" db:"content"`
	Type      string    `json:"type" db:"type"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

// MessageView represents an enriched message with sender info
type MessageView struct {
	Message
	SequenceNumber         int64             `json:"sequence_number,omitempty"`
	SenderUsername         string            `json:"sender_username"`
	SenderFullName         string            `json:"sender_full_name"`
	SenderAvatar           *string           `json:"sender_avatar,omitempty"`
	OriginalSenderFullName *string           `json:"original_sender_full_name,omitempty"`
	ReplyTo                *MessageView      `json:"reply_to,omitempty"`
	ReadByCount            int               `json:"read_by_count"`
	DeliveredCount         int               `json:"delivered_count"`
	IsRead                 bool              `json:"is_read"` // Added for frontend convenience
	Reactions              []ReactionSummary `json:"reactions"`
	SearchChatType         string            `json:"chat_type,omitempty"`
	SearchChatName         string            `json:"chat_name,omitempty"`
	SearchHighlight        string            `json:"highlight,omitempty"`
	SearchRelevance        float64           `json:"relevance_score,omitempty"`
}

// MessageReplyNode represents one message in a nested reply tree.
type MessageReplyNode struct {
	Message *MessageView        `json:"message"`
	Replies []*MessageReplyNode `json:"replies"`
}

// MessageThreadResponse contains the selected root message and all visible
// descendants returned as a tree.
type MessageThreadResponse struct {
	Root       *MessageView        `json:"root"`
	Replies    []*MessageReplyNode `json:"replies"`
	ReplyCount int                 `json:"reply_count"`
	Truncated  bool                `json:"truncated"`
}

func (mv *MessageView) GetIDs() (uuid.UUID, uuid.UUID) {
	return mv.ID, mv.ConversationID
}

func (mv *MessageView) GetSenderDisplayName() string {
	if mv.SenderFullName != "" {
		return mv.SenderFullName
	}
	return mv.SenderUsername
}

func (mv *MessageView) GetMessageType() string {
	return mv.Type
}

// SharedMediaItem represents an image or video attachment from chat history.
type SharedMediaItem struct {
	MessageID      uuid.UUID         `json:"message_id"`
	SenderID       int64             `json:"sender_id"`
	SenderName     string            `json:"sender_name"`
	SentAt         time.Time         `json:"sent_at"`
	Type           string            `json:"type"`
	AttachmentURL  string            `json:"attachment_url"`
	AttachmentType *string           `json:"attachment_type,omitempty"`
	AttachmentSize *int64            `json:"attachment_size,omitempty"`
	EncryptedKeys  map[string]string `json:"encrypted_keys,omitempty"`
}

// SharedMediaResponse represents paginated shared media payload for a conversation.
type SharedMediaResponse struct {
	Items   []SharedMediaItem `json:"items"`
	Total   int               `json:"total"`
	HasMore bool              `json:"has_more"`
}

// MessageViewer represents a user who has viewed a message
type MessageViewer struct {
	UserID    int64     `json:"user_id"`
	FullName  string    `json:"full_name"`
	AvatarURL *string   `json:"avatar_url,omitempty"`
	ReadAt    time.Time `json:"read_at"`
}

// ConversationView represents an enriched conversation
type ConversationView struct {
	ID             uuid.UUID  `json:"id"`
	Type           string     `json:"type"` // "personal" | "group" | "ai" | "support" | "channel"
	PeerID         *int64     `json:"peer_id,omitempty"`
	PeerUsername   *string    `json:"peer_username,omitempty"`
	PeerFullName   *string    `json:"peer_full_name,omitempty"`
	PeerAvatar     *string    `json:"peer_avatar,omitempty"`
	PeerLastSeenAt *time.Time `json:"last_seen_at"` // <-- Added for mobile status sync
	GroupID        *uuid.UUID `json:"group_id,omitempty"`
	GroupName      *string    `json:"group_name,omitempty"`
	GroupAvatar    *string    `json:"group_avatar,omitempty"`
	// AI Chat fields
	AIModel *string `json:"ai_model,omitempty"`
	AITitle *string `json:"ai_title,omitempty"`
	// Channel fields
	ChannelID     *uuid.UUID   `json:"channel_id,omitempty"`
	ChannelName   *string      `json:"channel_name,omitempty"`
	ChannelAvatar *string      `json:"channel_avatar,omitempty"`
	IsChannel     bool         `json:"is_channel,omitempty"`
	MyChannelRole *string      `json:"my_channel_role,omitempty"` // "owner"|"admin"|"subscriber"
	LastMessage   *MessageView `json:"last_message,omitempty"`
	LastPost      *ChannelPost `json:"last_post,omitempty"` // last post for channel type
	UnreadCount   int          `json:"unread_count"`
	MentionCount  int          `json:"mention_count"`
	IsPinned      bool         `json:"is_pinned"`
	IsMuted       bool         `json:"is_muted"`
	CreatedAt     time.Time    `json:"created_at"`
	UpdatedAt     time.Time    `json:"updated_at"`
}

// GroupView represents an enriched group with member info
type GroupView struct {
	Group
	MemberRole   string       `json:"member_role"`
	MemberCount  int          `json:"member_count"`
	UnreadCount  int          `json:"unread_count"`
	MentionCount int          `json:"mention_count"`
	IsMuted      bool         `json:"is_muted"`
	LastMessage  *MessageView `json:"last_message,omitempty"`
}

// GroupMemberDetail represents detailed member info
type GroupMemberDetail struct {
	UserResponse
	Role string `json:"role"`
}

// GroupDetails represents a group with its members
type GroupDetails struct {
	Group
	Members     []GroupMemberDetail `json:"members"`
	MemberCount int                 `json:"member_count"`
}

// PublicApp represents a public application entry
type PublicApp struct {
	ID              int64     `json:"id" db:"id"`
	AppName         string    `json:"app_name" db:"app_name"`
	Link            string    `json:"link" db:"link"`
	AppPhoto        string    `json:"app_photo" db:"app_photo"`
	CreatedAt       time.Time `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time `json:"updated_at" db:"updated_at"`
	IsFavorite      bool      `json:"is_favorite,omitempty" db:"is_favorite"`
	IsGlobal        bool      `json:"is_global" db:"is_global"`
	IsPrivate       bool      `json:"is_private,omitempty" db:"is_private"`
	WebView         bool      `json:"web_view" db:"web_view"`
	Position        int       `json:"position" db:"position"`
	Category        string    `json:"category" db:"category"`
	Tags            []string  `json:"tags" db:"tags"`
	AverageRating   float64   `json:"average_rating" db:"average_rating"`
	RatingCount     int       `json:"rating_count" db:"rating_count"`
	MyRating        *int      `json:"my_rating,omitempty" db:"my_rating"`
	FavoriteCount   int       `json:"favorite_count" db:"favorite_count"`
	OrganizationIDs []int64   `json:"organization_ids" db:"organization_ids"`
}

type PublicAppFavorite struct {
	ID        int64     `json:"id" db:"id"`
	UserID    int64     `json:"user_id" db:"user_id"`
	AppID     int64     `json:"app_id" db:"app_id"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

type PublicAppRating struct {
	AppID     int64     `json:"app_id" db:"app_id"`
	UserID    int64     `json:"-" db:"user_id"`
	Rating    int       `json:"rating" db:"rating"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

type ChatFolder struct {
	ID        uuid.UUID `json:"id" db:"id"`
	Name      string    `json:"name" db:"name"`
	UserID    int64     `json:"user_id" db:"user_id"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

type ChatFolderItem struct {
	FolderID  uuid.UUID `json:"folder_id" db:"folder_id"`
	ChatID    uuid.UUID `json:"chat_id" db:"chat_id"`
	ChatType  string    `json:"chat_type" db:"chat_type"` // conversation or group
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

type ChatFolderView struct {
	ChatFolder
	Items []ChatFolderItem   `json:"-"` // Hidden from JSON, internal use
	Chats []ConversationView `json:"chats"`
}

type SendIncomingCallPushRequest struct {
	ToUserID   int64     `json:"to_user_id" binding:"required"`
	FromUserID int64     `json:"from_user_id" binding:"required"`
	FromName   string    `json:"from_name"` // optional
	CallID     uuid.UUID `json:"call_id" binding:"required"`
	RoomID     string    `json:"room_id" binding:"required"`
	ChatID     string    `json:"chat_id"` // optional
	IsVideo    *bool     `json:"is_video,omitempty"`
}
