package domain

// ContactActions describes navigation and composer defaults; reading it has no side effects.
type ContactActions struct {
	Chat    ContactChatAction    `json:"chat"`
	Email   ContactEmailAction   `json:"email"`
	Meeting ContactMeetingAction `json:"meeting"`
}

type ContactChatAction struct {
	Label          string `json:"label"`
	Enabled        bool   `json:"enabled"`
	DisabledReason string `json:"disabled_reason,omitempty"`
	Method         string `json:"method,omitempty"`
	Endpoint       string `json:"endpoint,omitempty"`
	PeerID         *int64 `json:"peer_id,omitempty"`
}

type ContactEmailAction struct {
	Label          string `json:"label"`
	Enabled        bool   `json:"enabled"`
	DisabledReason string `json:"disabled_reason,omitempty"`
	Address        string `json:"address,omitempty"`
	MailtoURL      string `json:"mailto_url,omitempty"`
}

type ContactMeetingAction struct {
	Label                string  `json:"label"`
	Enabled              bool    `json:"enabled"`
	DisabledReason       string  `json:"disabled_reason,omitempty"`
	Method               string  `json:"method,omitempty"`
	Endpoint             string  `json:"endpoint,omitempty"`
	Title                string  `json:"title,omitempty"`
	AttendeeIDs          []int64 `json:"attendee_ids"`
	AvailabilityEndpoint string  `json:"availability_endpoint,omitempty"`
	SuggestTimesEndpoint string  `json:"suggest_times_endpoint,omitempty"`
}
