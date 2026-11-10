package auth

import "github.com/golang-jwt/jwt/v5"

type ContextUser struct {
	Name     string `json:"name,omitempty"`
	Email    string `json:"email,omitempty"`
	Username string `json:"username,omitempty"`
}

type Context struct {
	User           *ContextUser    `json:"user,omitempty"`
	Features       map[string]bool `json:"features,omitempty"`
	LinkedDeviceID string          `json:"linked_device_id,omitempty"`
}

type Claims struct {
	AuthenticationTime int64     `json:"auth_time,omitempty"`
	UserID             int64     `json:"user_id"`
	IIN                string    `json:"iin,omitempty"`
	Email              string    `json:"email"`
	Username           string    `json:"username"`
	FirstName          string    `json:"first_name,omitempty"`
	LastName           string    `json:"last_name,omitempty"`
	TokenType          TokenType `json:"token_type"`
	TokenID            string    `json:"token_id"`

	HasPin  bool     `json:"has_pin"`
	Context *Context `json:"context,omitempty"`

	jwt.RegisteredClaims
}
