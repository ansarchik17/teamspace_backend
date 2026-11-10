package domain

import (
	"github.com/google/uuid"
	"strings"
	"time"
	"unicode/utf8"
)

type ODOCompleteRequest struct {
	CommandID        uuid.UUID `json:"command_id"`
	ExpectedRevision int64     `json:"expected_revision"`
	ResultComment    string    `json:"result_comment"`
}

func (r ODOCompleteRequest) Valid() bool {
	return r.CommandID != uuid.Nil && r.ExpectedRevision > 0 && r.ExpectedRevision < 9007199254740991 && strings.TrimSpace(r.ResultComment) != "" && utf8.RuneCountInString(r.ResultComment) <= 20000
}

// Actor/company/source identifiers are derived by the server, never by the UI.
type ODOCompleteCommand struct {
	CommandID        uuid.UUID `json:"command_id"`
	OrganizationID   uuid.UUID `json:"organization_id"`
	IdentityIssuer   string    `json:"identity_issuer"`
	ActorSub         string    `json:"actor_sub"`
	DocumentID       string    `json:"document_id"`
	ExpectedRevision int64     `json:"expected_revision"`
	ResultComment    string    `json:"result_comment"`
}

type ODOCompleteReceipt struct {
	CommandID        uuid.UUID `json:"command_id"`
	OrganizationID   uuid.UUID `json:"organization_id"`
	IdentityIssuer   string    `json:"identity_issuer"`
	ActorSub         string    `json:"actor_sub"`
	AssignmentID     string    `json:"assignment_id"`
	DocumentID       string    `json:"document_id"`
	ExpectedRevision int64     `json:"expected_revision"`
	Revision         int64     `json:"revision"`
	Status           string    `json:"status"`
	EventID          uuid.UUID `json:"event_id"`
	CompletedAt      time.Time `json:"completed_at"`
	Outcome          string    `json:"outcome"`
}

type ODOCommandTarget struct {
	OrganizationID                                     uuid.UUID
	AssignmentID, DocumentID, IdentityIssuer, ActorSub string
}
