package repository

import (
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

func encodeNoteListCursor(cursor noteListCursor) string {
	raw := fmt.Sprintf("%t|%s|%s", cursor.IsPinned, cursor.UpdatedAt.UTC().Format(time.RFC3339Nano), cursor.ID.String())
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeNoteListCursor(value string) (noteListCursor, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return noteListCursor{}, fmt.Errorf("invalid cursor")
	}
	parts := strings.Split(string(decoded), "|")
	if len(parts) != 3 {
		return noteListCursor{}, fmt.Errorf("invalid cursor")
	}

	updatedAt, err := time.Parse(time.RFC3339Nano, parts[1])
	if err != nil {
		return noteListCursor{}, fmt.Errorf("invalid cursor")
	}
	id, err := uuid.Parse(parts[2])
	if err != nil {
		return noteListCursor{}, fmt.Errorf("invalid cursor")
	}

	return noteListCursor{
		IsPinned:  parts[0] == "true",
		UpdatedAt: updatedAt,
		ID:        id,
	}, nil
}

func encodeNoteSyncCursor(cursor noteSyncCursor) string {
	raw := fmt.Sprintf("%s|%s", cursor.UpdatedAt.UTC().Format(time.RFC3339Nano), cursor.ID.String())
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeNoteSyncCursor(value string) (noteSyncCursor, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return noteSyncCursor{}, fmt.Errorf("invalid cursor")
	}
	parts := strings.Split(string(decoded), "|")
	if len(parts) != 2 {
		return noteSyncCursor{}, fmt.Errorf("invalid cursor")
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return noteSyncCursor{}, fmt.Errorf("invalid cursor")
	}
	id, err := uuid.Parse(parts[1])
	if err != nil {
		return noteSyncCursor{}, fmt.Errorf("invalid cursor")
	}

	return noteSyncCursor{
		UpdatedAt: updatedAt,
		ID:        id,
	}, nil
}
