package domain

import "time"

const AuthProviderEgovIDP = "EGOV_IDP"

type EgovProfile struct {
	UserID       int64      `json:"user_id" db:"user_id"`
	IIN          string     `json:"iin" db:"iin"`
	Phone        string     `json:"phone" db:"phone"`
	Email        string     `json:"email" db:"email"`
	FirstName    string     `json:"first_name" db:"first_name"`
	LastName     string     `json:"last_name" db:"last_name"`
	Patronymic   string     `json:"patronymic" db:"patronymic"`
	AuthLevel    string     `json:"auth_level" db:"auth_level"`
	UserType     string     `json:"user_type" db:"user_type"`
	BIN          string     `json:"bin" db:"bin"`
	NameRu       string     `json:"name_ru" db:"name_ru"`
	NameKk       string     `json:"name_kk" db:"name_kk"`
	AuthProvider string     `json:"auth_provider" db:"auth_provider"`
	CreatedAt    time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at" db:"updated_at"`
	LastLoginAt  *time.Time `json:"last_login_at,omitempty" db:"last_login_at"`
}
