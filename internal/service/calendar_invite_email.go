package service

import (
	"fmt"

	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/i18n"
)

type CalendarInviteEmailInput struct {
	Language      i18n.Locale
	EventID       uuid.UUID
	EventTitle    string
	OrganizerName string
	AttendeeName  string
	StartTime     time.Time
	Location      string
	Description   string
	BaseURL       string
	JWTSecret     string
	IsUpdate      bool
	IsCancel      bool
}

// GenerateCalendarActionToken creates a short-lived token for email actions
func GenerateCalendarActionToken(secret string, userID int64, eventID uuid.UUID, action string) (string, error) {
	claims := jwt.MapClaims{
		"user_id":  userID,
		"event_id": eventID.String(),
		"action":   action,
		"exp":      time.Now().Add(7 * 24 * time.Hour).Unix(),
		"iat":      time.Now().Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

type CalendarActionToken struct {
	UserID  int64
	EventID uuid.UUID
	Action  string
}

func ParseCalendarActionToken(secret string, tokenStr string) (*CalendarActionToken, error) {
	token, err := jwt.Parse(tokenStr, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return []byte(secret), nil
	})

	if err != nil || !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, fmt.Errorf("invalid claims")
	}

	userID := int64(claims["user_id"].(float64))
	eventID, err := uuid.Parse(claims["event_id"].(string))
	if err != nil {
		return nil, fmt.Errorf("invalid event_id")
	}
	action := claims["action"].(string)

	return &CalendarActionToken{
		UserID:  userID,
		EventID: eventID,
		Action:  action,
	}, nil
}
