package events

import "time"

type Event struct {
	Meta EventMeta `json:"meta"`
	Data any       `json:"data"`
}

type EventMeta struct {
	EventID   string    `json:"event_id"`
	EventType string    `json:"event_type"`
	Timestamp time.Time `json:"timestamp"`
	Producer  string    `json:"producer"`
}
