package domain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"
	_ "time/tzdata"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

// ODOAssignmentEvent is a complete source snapshot, never a partial patch.
type ODOAssignmentEvent struct {
	SchemaVersion  int        `json:"schema_version"`
	EventID        uuid.UUID  `json:"event_id"`
	EventType      string     `json:"event_type"`
	OccurredAt     time.Time  `json:"occurred_at"`
	Revision       int64      `json:"revision"`
	OrganizationID uuid.UUID  `json:"organization_id"`
	SourceSystem   string     `json:"source_system"`
	IdentityIssuer string     `json:"identity_issuer"`
	AssignmentID   string     `json:"assignment_id"`
	DocumentID     string     `json:"document_id"`
	AuthorSub      string     `json:"author_sub"`
	AssigneeSub    string     `json:"assignee_sub"`
	Title          string     `json:"title"`
	Description    string     `json:"description"`
	DepartmentID   *string    `json:"department_id"`
	Priority       string     `json:"priority"`
	DueDate        *string    `json:"due_date"`
	Timezone       string     `json:"timezone"`
	Status         string     `json:"status"`
	SourceURL      *string    `json:"source_url"`
	CompletedAt    *time.Time `json:"completed_at"`
	ClosedAt       *time.Time `json:"closed_at"`
}

type ODOAssignmentReceipt struct {
	Task             *Task     `json:"task"`
	BoardID          uuid.UUID `json:"board_id"`
	SourceSystem     string    `json:"source_system"`
	OrganizationID   uuid.UUID `json:"organization_id"`
	AssignmentID     string    `json:"assignment_id"`
	EventID          uuid.UUID `json:"event_id"`
	ReceivedRevision int64     `json:"received_revision"`
	CurrentRevision  int64     `json:"current_revision"`
	Applied          bool      `json:"applied"`
	Outcome          string    `json:"outcome"`
}

// DecodeODOObject rejects duplicate keys and trailing JSON even on the legacy path.
func DecodeODOObject(body []byte) (map[string]json.RawMessage, error) {
	d := json.NewDecoder(bytes.NewReader(body))
	tok, err := d.Token()
	if err != nil || tok != json.Delim('{') {
		return nil, fmt.Errorf("assignment must be an object")
	}
	fields := map[string]json.RawMessage{}
	for d.More() {
		tok, err = d.Token()
		if err != nil {
			return nil, err
		}
		key, ok := tok.(string)
		if !ok {
			return nil, fmt.Errorf("invalid property")
		}
		if _, exists := fields[key]; exists {
			return nil, fmt.Errorf("duplicate property %s", key)
		}
		var value json.RawMessage
		if err = d.Decode(&value); err != nil {
			return nil, err
		}
		fields[key] = value
	}
	if _, err = d.Token(); err != nil {
		return nil, err
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing JSON")
	}
	return fields, nil
}

func DecodeODOAssignmentEvent(body []byte, sourceOrigin string) (ODOAssignmentEvent, error) {
	var e ODOAssignmentEvent
	fields, err := DecodeODOObject(body)
	if err != nil {
		return e, err
	}
	required := strings.Fields("schema_version event_id event_type occurred_at revision organization_id source_system identity_issuer assignment_id document_id author_sub assignee_sub title description department_id priority due_date timezone status source_url completed_at closed_at")
	if len(fields) != len(required) {
		return e, fmt.Errorf("complete v1 snapshot required")
	}
	nullable := map[string]bool{"department_id": true, "due_date": true, "source_url": true, "completed_at": true, "closed_at": true}
	for _, key := range required {
		value, ok := fields[key]
		if !ok || (!nullable[key] && string(value) == "null") {
			return e, fmt.Errorf("missing property %s", key)
		}
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err = d.Decode(&e); err != nil {
		return e, err
	}
	for _, key := range []string{"occurred_at", "completed_at", "closed_at"} {
		if string(fields[key]) != "null" {
			var s string
			if json.Unmarshal(fields[key], &s) != nil || !strings.HasSuffix(s, "Z") {
				return e, fmt.Errorf("UTC timestamp required")
			}
		}
	}
	return e, e.Validate(sourceOrigin)
}

func (e ODOAssignmentEvent) Validate(sourceOrigin string) error {
	invalid := func() error { return fmt.Errorf("invalid ODO assignment snapshot") }
	if e.SchemaVersion != 1 || e.EventID == uuid.Nil || e.OrganizationID == uuid.Nil || e.Revision < 1 || e.Revision > 9007199254740991 || e.OccurredAt.IsZero() || e.SourceSystem != "documentolog" {
		return invalid()
	}
	if !odoOneOf(e.EventType, "created", "updated", "status_updated") || !odoOneOf(e.Priority, "low", "medium", "high", "critical") || !odoOneOf(e.Status, "assigned", "in_progress", "completed", "closed", "cancelled") {
		return invalid()
	}
	for _, id := range []string{e.AssignmentID, e.DocumentID, e.AuthorSub, e.AssigneeSub} {
		if !odoOpaqueID(id) {
			return invalid()
		}
	}
	if strings.HasPrefix(e.AuthorSub, "workspace:") || strings.HasPrefix(e.AssigneeSub, "workspace:") || strings.TrimSpace(e.Title) == "" || utf8.RuneCountInString(e.Title) > 255 || utf8.RuneCountInString(e.Description) > 20000 {
		return invalid()
	}
	issuer, err := url.Parse(e.IdentityIssuer)
	if err != nil || issuer.Scheme != "https" || issuer.Host == "" || issuer.User != nil || len(e.IdentityIssuer) > 2048 {
		return invalid()
	}
	if e.DepartmentID != nil && !odoOpaqueID(*e.DepartmentID) {
		return invalid()
	}
	if e.DueDate != nil {
		date, err := time.Parse("2006-01-02", *e.DueDate)
		if err != nil || date.Format("2006-01-02") != *e.DueDate {
			return invalid()
		}
	}
	if e.Timezone == "" || len(e.Timezone) > 100 || e.Timezone == "Local" {
		return invalid()
	}
	if _, err := time.LoadLocation(e.Timezone); err != nil {
		return invalid()
	}
	if e.CompletedAt != nil && e.Status != "completed" && e.Status != "closed" {
		return invalid()
	}
	if e.ClosedAt != nil && e.Status != "closed" {
		return invalid()
	}
	if e.ClosedAt != nil && e.CompletedAt != nil && e.ClosedAt.Before(*e.CompletedAt) {
		return invalid()
	}
	if e.SourceURL != nil {
		u, err := url.Parse(*e.SourceURL)
		origin, originErr := url.Parse(sourceOrigin)
		if err != nil || originErr != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || len(*e.SourceURL) > 2048 || origin.Scheme != "https" || origin.Host == "" || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" || (origin.Path != "" && origin.Path != "/") || !strings.EqualFold(u.Host, origin.Host) || u.RawQuery != "" || u.Fragment != "" {
			return invalid()
		}
	}
	return nil
}

func odoOpaqueID(s string) bool {
	return s != "" && utf8.RuneCountInString(s) <= 255 && !strings.ContainsFunc(s, unicode.IsSpace)
}
func odoOneOf(s string, values ...string) bool {
	for _, v := range values {
		if s == v {
			return true
		}
	}
	return false
}

func (e ODOAssignmentEvent) Hashes() (full, snapshot string) {
	b, _ := json.Marshal(e)
	h := sha256.Sum256(b)
	full = hex.EncodeToString(h[:])
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(b, &fields)
	delete(fields, "event_id")
	delete(fields, "event_type")
	delete(fields, "occurred_at")
	b, _ = json.Marshal(fields)
	h = sha256.Sum256(b)
	return full, hex.EncodeToString(h[:])
}

func (e ODOAssignmentEvent) DueAt() *time.Time {
	if e.DueDate == nil {
		return nil
	}
	loc, err := time.LoadLocation(e.Timezone)
	if err != nil {
		return nil
	}
	day, err := time.ParseInLocation("2006-01-02", *e.DueDate, loc)
	if err != nil {
		return nil
	}
	// PostgreSQL timestamps have microsecond precision; nanoseconds could round
	// the end of day into the following date when persisted.
	end := day.AddDate(0, 0, 1).Add(-time.Microsecond).UTC()
	return &end
}
