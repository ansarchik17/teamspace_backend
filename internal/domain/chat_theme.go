package domain

import (
	"time"

	"github.com/google/uuid"
)

// ChatTheme represents a user's custom global chat theme
type ChatTheme struct {
	ID                 uuid.UUID `json:"id" db:"id"`
	UserID             int64     `json:"user_id" db:"user_id"`
	MessageColorHex    *string   `json:"message_color_hex,omitempty" db:"message_color_hex"`
	BackgroundType     string    `json:"background_type" db:"background_type"` // "color", "image", "default"
	BackgroundColorHex *string   `json:"background_color_hex,omitempty" db:"background_color_hex"`
	BackgroundImageURL *string   `json:"background_image_url,omitempty" db:"background_image_url"`
	CreatedAt          time.Time `json:"created_at" db:"created_at"`
	UpdatedAt          time.Time `json:"updated_at" db:"updated_at"`
}

// ChatThemeConfig is the JSON structure expected by the frontend
type ChatThemeConfig struct {
	MessageColorHex    *string `json:"messageColorHex"`
	BackgroundType     string  `json:"backgroundType" binding:"required,oneof=color image default"`
	BackgroundColorHex *string `json:"backgroundColorHex"`
	BackgroundImageURL *string `json:"backgroundImageUrl"`
}

// UpdateChatThemeRequest represents the payload to update a theme
type UpdateChatThemeRequest struct {
	Theme ChatThemeConfig `json:"chatTheme" binding:"required"`
}
