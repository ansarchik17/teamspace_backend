package domain

import "time"

// OfficeUser matches DocsAPI.setUsers(c=info); it exposes no email or private profile data.
type OfficeUser struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Image string `json:"image"`
}

type OfficeParticipant struct {
	OfficeUser
	LastSeenAt time.Time `json:"last_seen_at"`
}

type OfficePresenceRequest struct {
	Action string `json:"action" binding:"required,oneof=heartbeat leave"`
}
