package service

import "time"

// MessageEditWindow and MessageDeleteForEveryoneWindow are part of the public
// messenger contract. Keep docs/msg-004-moderation-api.md in sync when changed.
const (
	MessageEditWindow              = 48 * time.Hour
	MessageDeleteForEveryoneWindow = 48 * time.Hour
)

type MessageDeleteScope string

const (
	MessageDeleteForMe       MessageDeleteScope = "me"
	MessageDeleteForEveryone MessageDeleteScope = "everyone"
)

func isWithinModerationWindow(createdAt, now time.Time, window time.Duration) bool {
	return !now.After(createdAt.Add(window))
}
