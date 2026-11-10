package alem

import (
	"encoding/json"
	"fmt"
)

// --- Common ---

type ErrorResponse struct {
	Error ErrorDetails `json:"error"`
}

type ErrorDetails struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
	Param   string `json:"param,omitempty"`
}

type APIError struct {
	StatusCode int
	Details    ErrorDetails
	Response   ErrorResponse
}

func (e *APIError) Error() string {
	return fmt.Sprintf("Alem API Error %d: %s (Type: %s)", e.StatusCode, e.Details.Message, e.Details.Type)
}

// --- Models ---

type Model struct {
	ID      string `json:"id"`
	Object  string `json:"object"` // "model"
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
	Title   string `json:"title,omitempty"`
}

type ModelListResponse struct {
	Object string  `json:"object"` // "list"
	Data   []Model `json:"data"`
}

// --- Chat ---

type ChatCompletionRequest struct {
	Model       string         `json:"model"`
	Messages    []Message      `json:"messages"`
	Temperature float64        `json:"temperature,omitempty"`
	MaxTokens   int            `json:"max_tokens,omitempty"`
	TopP        float64        `json:"top_p,omitempty"`
	Stream      bool           `json:"stream,omitempty"`
	Tools       []FunctionTool `json:"tools,omitempty"`
	ToolChoice  string         `json:"tool_choice,omitempty"`
}

type Message struct {
	Role       string      `json:"role"`    // "user", "assistant"
	Content    interface{} `json:"content"` // string or []ContentPart
	ToolCalls  []ToolCall  `json:"tool_calls,omitempty"`
	ToolCallID string      `json:"tool_call_id,omitempty"`
	CardIDs    []string    `json:"-"` // Server presentation references, never provider tool arguments.
	AnswerMode string      `json:"-"` // Validated answer envelope metadata, not part of provider history.
}

// OpenAI-compatible function calling. Arguments remain untrusted until the
// application validates them against its read-only data catalog.
type FunctionTool struct {
	Type     string             `json:"type"`
	Function FunctionDefinition `json:"function"`
}
type FunctionDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}
type ToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ContentPart struct {
	Type     string    `json:"type"` // "text" or "image_url"
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
}

type ImageURL struct {
	URL string `json:"url"`
}

type QuotaStop struct {
	Scope                string `json:"scope"`
	ConsumedMicrocredits string `json:"consumed_microcredits"`
	ResetsAt             string `json:"resets_at"`
}

type ChatCompletionResponse struct {
	// AccountingUsage preserves missing/null fields; the legacy integer DTO
	// alone cannot distinguish absent usage from reported zero.
	AccountingUsage json.RawMessage  `json:"-"`
	ID              string           `json:"id"`
	Object          string           `json:"object"`
	Created         int64            `json:"created"`
	Model           string           `json:"model"`
	Choices         []Choice         `json:"choices"`
	Usage           Usage            `json:"usage"`
	DocumentSources []DocumentSource `json:"document_sources,omitempty"`
	DataSources     []DataSource     `json:"data_sources,omitempty"`
	DataCards       []DataCard       `json:"data_cards,omitempty"`
	QuotaStop       *QuotaStop       `json:"quota_stop,omitempty"`
}

func (r *ChatCompletionResponse) UnmarshalJSON(data []byte) error {
	type response ChatCompletionResponse
	var wire struct {
		*response
		RawUsage json.RawMessage `json:"usage"`
	}
	*r = ChatCompletionResponse{}
	wire.response = (*response)(r)
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	r.AccountingUsage = append(json.RawMessage(nil), wire.RawUsage...)
	if len(wire.RawUsage) != 0 {
		return json.Unmarshal(wire.RawUsage, &r.Usage)
	}
	return nil
}

// Card values come from authorized read results, not model-generated fields or URLs.
type DataCard struct {
	ID           string              `json:"id"`
	SourceID     string              `json:"source_id"`
	Title        string              `json:"title"`
	Service      string              `json:"service"`
	Status       string              `json:"status,omitempty"`
	DueAt        string              `json:"due_at,omitempty"`
	Href         string              `json:"href,omitempty"`
	Empty        bool                `json:"empty,omitempty"`
	Fields       []DataCardField     `json:"fields,omitempty"`
	ExternalURL  string              `json:"external_url,omitempty"`
	Confirmation *ActionConfirmation `json:"confirmation,omitempty"`
}

// The server creates these references; model output cannot authorize a write.
type ActionConfirmation struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at"`
}
type DataCardField struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type DataSource struct {
	ID          string `json:"id"`
	Dataset     string `json:"dataset"`
	Title       string `json:"title"`
	RetrievedAt string `json:"retrieved_at"`
	Status      string `json:"status"`
	Truncated   bool   `json:"truncated,omitempty"`
}

type DocumentSource struct {
	DocumentID string         `json:"document_id"`
	Name       string         `json:"name"`
	ChunkID    string         `json:"chunk_id"`
	Locator    map[string]any `json:"locator,omitempty"`
	Rank       int            `json:"rank"`
}

type Choice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// --- Streaming ---

type ChatCompletionChunk struct {
	ID      string        `json:"id"`
	Object  string        `json:"object"`
	Created int64         `json:"created"`
	Model   string        `json:"model"`
	Choices []ChunkChoice `json:"choices"`
}

type ChunkChoice struct {
	Index        int    `json:"index"`
	Delta        Delta  `json:"delta"`
	FinishReason string `json:"finish_reason"`
}

type Delta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

// --- Embeddings ---

type EmbeddingRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type EmbeddingResponse struct {
	Object string          `json:"object"`
	Data   []EmbeddingData `json:"data"`
	Model  string          `json:"model"`
	Usage  Usage           `json:"usage"`
}

type EmbeddingData struct {
	Object    string    `json:"object"` // "embedding"
	Index     int       `json:"index"`
	Embedding []float64 `json:"embedding"`
}

// --- Rerank ---

type RerankRequest struct {
	Model     string   `json:"model"`
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
	TopN      int      `json:"top_n,omitempty"`
}

type RerankResponse struct {
	Model   string         `json:"model"`
	Usage   Usage          `json:"usage"`
	Results []RerankResult `json:"results"`
}

type RerankResult struct {
	Index          int     `json:"index"`
	RelevanceScore float64 `json:"relevance_score"`
	Document       struct {
		Text string `json:"text"`
	} `json:"document,omitempty"` // Some APIs return this, strict Alem might just ref index
}
