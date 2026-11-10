package domain

import (
	"time"

	"github.com/google/uuid"
)

// NotebookChat represents a chat session for the external Notebook AI
type NotebookChat struct {
	ID        uuid.UUID `json:"id" db:"id"`
	UserID    int64     `json:"user_id" db:"user_id"`
	Title     string    `json:"title" db:"title"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

const (
	NotebookMessageTypeText   = "text"
	NotebookMessageTypeCards  = "cards"
	NotebookMessageTypeTest   = "test"
	NotebookMessageTypeTable  = "table"
	NotebookMessageTypeReport = "report"

	NotebookMessageTypeCompare      = "compare"
	NotebookMessageTypePresentation = "presentation"
	NotebookMessageTypeChart        = "chart"
	NotebookMessageTypeMermaid      = "mermaid"
)

// NotebookMessage represents a message in a NotebookChat
type NotebookMessage struct {
	ID        uuid.UUID `json:"id" db:"id"`
	ChatID    uuid.UUID `json:"chat_id" db:"chat_id"`
	Role      string    `json:"role" db:"role"` // "user" or "assistant"
	Type      string    `json:"type" db:"type"` // text, cards, test, table, report, compare, presentation, chart, mermaid
	Content   string    `json:"content" db:"content"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

// CreateNotebookChatRequest is the request to create a new chat
type CreateNotebookChatRequest struct {
	Title string `json:"title" binding:"required"`
}

// SaveNotebookMessageRequest is the request to save a message pair
// Usually the frontend sends both the user query and the AI answer
type SaveNotebookMessageRequest struct {
	UserMessage      string `json:"user_message" binding:"required"`
	AssistantMessage string `json:"assistant_message" binding:"required"`
	AssistantType    string `json:"assistant_type"` // Optional, defaults to "text"
}

type UpdateNotebookChatRequest struct {
	Title string `json:"title" binding:"required"`
}

// SaveNotebookDocsRequest is the request to update chat context documents
type SaveNotebookDocsRequest struct {
	DocumentIDs []uuid.UUID `json:"document_ids" binding:"required"`
}
