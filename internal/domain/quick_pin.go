package domain

import "time"

type QuickPinCredential struct {
	ID                    int64     `db:"id" json:"id"`
	UserID                int64     `db:"user_id" json:"user_id"`
	DeviceID              string    `db:"device_id" json:"device_id"`
	PinHash               string    `db:"pin_hash" json:"-"`
	EncryptedRefreshToken string    `db:"encrypted_refresh_token" json:"-"`
	ClientID              *string   `db:"client_id" json:"-"`
	Active                bool      `db:"active" json:"active"`
	CreatedAt             time.Time `db:"created_at" json:"created_at"`
	UpdatedAt             time.Time `db:"updated_at" json:"updated_at"`
}
