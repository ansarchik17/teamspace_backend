package domain

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestRedactRoomBooking(t *testing.T) {
	eventID := uuid.New()
	description := "Private medical discussion"
	booking := RoomBooking{ID: uuid.New(), EventID: &eventID, UserID: 1, Title: "Secret meeting", Description: &description, User: &UserResponse{Email: "private@example.com"}}
	if own := RedactRoomBooking(booking, 1); own.Title != booking.Title || own.EventID == nil {
		t.Fatal("own details must remain visible")
	}
	other := RedactRoomBooking(booking, 2)
	data, err := json.Marshal(other)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{booking.Title, description, eventID.String(), booking.ID.String(), booking.User.Email} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("private data leaked: %s", data)
		}
	}
	if other.Title != "Занято" || other.User != nil || other.UserID != 0 {
		t.Fatalf("invalid redaction: %+v", other)
	}
}
