package agent

import "github.com/west6ide/workspace-backend/internal/domain"

type ChatV2Request struct {
	SessionID string                 `json:"session_id,omitempty"`
	Message   string                 `json:"message" binding:"required"`
	InputType string                 `json:"input_type,omitempty"`
	Locale    string                 `json:"locale,omitempty"`
	Timezone  string                 `json:"timezone,omitempty"`
	Context   map[string]interface{} `json:"context,omitempty"`
	Options   domain.AgentOptions    `json:"options,omitempty"`
}

type CreateSessionRequest struct {
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

type ListSessionsQuery struct {
	Limit int `form:"limit"`
}

type UpdateSessionRequest struct {
	Title string `json:"title" binding:"required"`
}

type ExecuteActionRequest struct {
	SessionID         string `json:"session_id" binding:"required"`
	ConfirmationToken string `json:"confirmation_token" binding:"required"`
}

type FeedbackRequest struct {
	SessionID string                 `json:"session_id" binding:"required"`
	MessageID *string                `json:"message_id,omitempty"`
	Rating    int                    `json:"rating" binding:"required"`
	Comment   string                 `json:"comment,omitempty"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}
