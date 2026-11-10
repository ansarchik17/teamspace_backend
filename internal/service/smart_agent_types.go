package service

// LLMMessage represents a message in the conversation history
type LLMMessage struct {
	Role    string `json:"role"`    // "system", "user", "assistant", "tool"
	Content string `json:"content"` // The text content
	// For "tool" role, we might need ToolCallID
	ToolCallID string `json:"tool_call_id,omitempty"`
	// For "assistant" role, we might have ToolCalls
	ToolCalls []LLMToolCall `json:"tool_calls,omitempty"`
	// Name is optional for tool messages
	Name string `json:"name,omitempty"`
}

type LLMToolCall struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"` // "function"
	Function LLMToolFuncCall `json:"function"`
}

type LLMToolFuncCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}

// OpenAI Request/Response structures
type OpenAIRequest struct {
	Model       string       `json:"model"`
	Messages    []LLMMessage `json:"messages"`
	Tools       []LLMTool    `json:"tools,omitempty"`
	Temperature float64      `json:"temperature,omitempty"`
}

type LLMTool struct {
	Type     string      `json:"type"` // "function"
	Function LLMFunction `json:"function"`
}

type LLMFunction struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"` // JSON Schema
}

type OpenAIResponse struct {
	ID      string         `json:"id"`
	Choices []OpenAIChoice `json:"choices"`
	Usage   OpenAIUsage    `json:"usage"`
}

type OpenAIChoice struct {
	Index        int        `json:"index"`
	Message      LLMMessage `json:"message"`
	FinishReason string     `json:"finish_reason"` // "stop", "tool_calls"
}

type OpenAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// SmartAgent Chat API structures

type SmartAgentChatRequest struct {
	Message             string       `json:"message" binding:"required"`
	ConversationHistory []LLMMessage `json:"conversation_history,omitempty"` // For stateless client-side history
	SessionID           string       `json:"session_id,omitempty"`           // For stateful server-side history
	ForceNewSession     bool         `json:"force_new_session,omitempty"`
	OperationID         string       `json:"operation_id,omitempty"`
}

type SmartAgentWidget struct {
	Type  string      `json:"type"`            // e.g., "user_list", "task_list", "event_preview", "action_confirmation"
	Title string      `json:"title,omitempty"` // User-friendly title for the widget
	Data  interface{} `json:"data"`            // The structured data for the frontend to render
}

type SmartAgentChatResponse struct {
	Reply               string            `json:"reply"`
	ConversationHistory []LLMMessage      `json:"conversation_history"` // Returns full history (optional for stateful)
	SessionID           string            `json:"session_id"`
	Widget              *SmartAgentWidget `json:"widget,omitempty"` // The UI widget if any tool produced one
}
