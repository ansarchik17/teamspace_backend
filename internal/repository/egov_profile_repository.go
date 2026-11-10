package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

type EgovProfileRepository struct {
	db     *pgxpool.Pool
	logger *zap.Logger
}

func NewEgovProfileRepository(db *pgxpool.Pool, logger *zap.Logger) *EgovProfileRepository {
	return &EgovProfileRepository{
		db:     db,
		logger: logger.With(zap.String("repository", "egov_profile")),
	}
}

func (r *EgovProfileRepository) GetByUserID(ctx context.Context, userID int64) (*domain.EgovProfile, error) {
	var profile domain.EgovProfile
	err := r.db.QueryRow(ctx, `
		SELECT user_id, iin, phone, email, first_name, last_name, patronymic, auth_level, user_type, bin, name_ru, name_kk, auth_provider, created_at, updated_at, last_login_at
		FROM user_egov_profiles
		WHERE user_id = $1
	`, userID).Scan(
		&profile.UserID,
		&profile.IIN,
		&profile.Phone,
		&profile.Email,
		&profile.FirstName,
		&profile.LastName,
		&profile.Patronymic,
		&profile.AuthLevel,
		&profile.UserType,
		&profile.BIN,
		&profile.NameRu,
		&profile.NameKk,
		&profile.AuthProvider,
		&profile.CreatedAt,
		&profile.UpdatedAt,
		&profile.LastLoginAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("egov profile not found")
		}
		return nil, fmt.Errorf("get egov profile by user id: %w", err)
	}
	return &profile, nil
}

func (r *EgovProfileRepository) GetByPhone(ctx context.Context, phone string) (*domain.EgovProfile, error) {
	phone = strings.TrimSpace(phone)
	if phone == "" {
		return nil, fmt.Errorf("egov profile not found")
	}

	var profile domain.EgovProfile
	err := r.db.QueryRow(ctx, `
		SELECT user_id, iin, phone, email, first_name, last_name, patronymic, auth_level, user_type, bin, name_ru, name_kk, auth_provider, created_at, updated_at, last_login_at
		FROM user_egov_profiles
		WHERE phone = $1
	`, phone).Scan(
		&profile.UserID,
		&profile.IIN,
		&profile.Phone,
		&profile.Email,
		&profile.FirstName,
		&profile.LastName,
		&profile.Patronymic,
		&profile.AuthLevel,
		&profile.UserType,
		&profile.BIN,
		&profile.NameRu,
		&profile.NameKk,
		&profile.AuthProvider,
		&profile.CreatedAt,
		&profile.UpdatedAt,
		&profile.LastLoginAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("egov profile not found")
		}
		return nil, fmt.Errorf("get egov profile by phone: %w", err)
	}
	return &profile, nil
}

func (r *EgovProfileRepository) Upsert(ctx context.Context, profile *domain.EgovProfile) error {
	if profile == nil {
		return fmt.Errorf("egov profile is nil")
	}
	now := time.Now()
	if profile.CreatedAt.IsZero() {
		profile.CreatedAt = now
	}
	profile.UpdatedAt = now

	_, err := r.db.Exec(ctx, `
		INSERT INTO user_egov_profiles (
			user_id, iin, phone, email, first_name, last_name, patronymic, auth_level, user_type, bin, name_ru, name_kk, auth_provider, created_at, updated_at, last_login_at
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
		ON CONFLICT (user_id) DO UPDATE SET
			iin = EXCLUDED.iin,
			phone = EXCLUDED.phone,
			email = EXCLUDED.email,
			first_name = EXCLUDED.first_name,
			last_name = EXCLUDED.last_name,
			patronymic = EXCLUDED.patronymic,
			auth_level = EXCLUDED.auth_level,
			user_type = EXCLUDED.user_type,
			bin = EXCLUDED.bin,
			name_ru = EXCLUDED.name_ru,
			name_kk = EXCLUDED.name_kk,
			auth_provider = EXCLUDED.auth_provider,
			updated_at = EXCLUDED.updated_at,
			last_login_at = EXCLUDED.last_login_at
	`, profile.UserID, profile.IIN, profile.Phone, profile.Email, profile.FirstName, profile.LastName, profile.Patronymic, profile.AuthLevel, profile.UserType, profile.BIN, profile.NameRu, profile.NameKk, profile.AuthProvider, profile.CreatedAt, profile.UpdatedAt, profile.LastLoginAt)
	if err != nil {
		return fmt.Errorf("upsert egov profile: %w", err)
	}
	return nil
}
