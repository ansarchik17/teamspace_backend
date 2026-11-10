package handler

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestExtractSharedNoteID(t *testing.T) {
	noteID := uuid.New()

	tests := []struct {
		name     string
		metadata *string
		wantOK   bool
		wantID   uuid.UUID
	}{
		{
			name:     "share endpoint metadata",
			metadata: strPtr(`{"kind":"note","note_id":"` + noteID.String() + `"}`),
			wantOK:   true,
			wantID:   noteID,
		},
		{
			name:     "notes page metadata",
			metadata: strPtr(`{"type":"note","note_id":"` + noteID.String() + `"}`),
			wantOK:   true,
			wantID:   noteID,
		},
		{
			name:     "camelCase note id",
			metadata: strPtr(`{"type":"note","noteId":"` + noteID.String() + `"}`),
			wantOK:   true,
			wantID:   noteID,
		},
		{
			name:     "non note metadata",
			metadata: strPtr(`{"kind":"task","task_id":"` + uuid.New().String() + `"}`),
			wantOK:   false,
		},
		{
			name:     "invalid note id",
			metadata: strPtr(`{"type":"note","note_id":"broken"}`),
			wantOK:   false,
		},
		{
			name:   "nil metadata",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotID, gotOK := extractSharedNoteID(tt.metadata)
			assert.Equal(t, tt.wantOK, gotOK)
			assert.Equal(t, tt.wantID, gotID)
		})
	}
}

func strPtr(value string) *string {
	return &value
}
