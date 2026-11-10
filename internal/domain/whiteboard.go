package domain

import "time"

const (
	WhiteboardMessageTypeInit   = "init"
	WhiteboardMessageTypeDraw   = "draw"
	WhiteboardMessageTypeUndo   = "undo"
	WhiteboardMessageTypeClear  = "clear"
	WhiteboardMessageTypeCursor = "cursor"
	WhiteboardMessageTypePing   = "ping"
	WhiteboardMessageTypePong   = "pong"
	WhiteboardMessageTypeError  = "error"

	WhiteboardToolBrush  = "brush"
	WhiteboardToolEraser = "eraser"
)

type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type DrawAction struct {
	ID          string  `json:"id"`
	Tool        string  `json:"tool"`
	Color       string  `json:"color"`
	StrokeWidth float64 `json:"strokeWidth"`
	Points      []Point `json:"points"`
}

type WSMessage struct {
	Type     string      `json:"type"`
	RoomID   string      `json:"roomId"`
	UserID   uint        `json:"userId,omitempty"`
	Action   *DrawAction `json:"action,omitempty"`
	ActionID string      `json:"actionId,omitempty"`
	Payload  any         `json:"payload,omitempty"`
	SentAt   time.Time   `json:"sentAt,omitempty"`
}

type WhiteboardInitPayload struct {
	Actions []WSMessage `json:"actions"`
}
