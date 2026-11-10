package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/west6ide/workspace-backend/internal/domain"
)

type GoogleCalendarRepository struct {
	db *pgxpool.Pool
}

func NewGoogleCalendarRepository(db *pgxpool.Pool) *GoogleCalendarRepository {
	return &GoogleCalendarRepository{db: db}
}

// GetUserGoogleSyncStatus gets the Google Calendar sync status for a user
func (r *GoogleCalendarRepository) GetUserGoogleSyncStatus(ctx context.Context, userID int64) (*domain.GoogleCalendarSyncStatus, error) {
	var status domain.GoogleCalendarSyncStatus
	query := `
		SELECT 
			id as user_id, 
			google_access_token, 
			google_refresh_token, 
			google_token_expiry, 
			google_calendar_sync_enabled,
			google_calendar_id
		FROM users 
		WHERE id = $1
	`
	err := r.db.QueryRow(ctx, query, userID).Scan(
		&status.UserID,
		&status.GoogleAccessToken,
		&status.GoogleRefreshToken,
		&status.GoogleTokenExpiry,
		&status.GoogleCalendarSyncEnabled,
		&status.GoogleCalendarID,
	)
	if err != nil {
		return nil, err
	}
	return &status, nil
}

// UpdateGoogleTokens updates the Google OAuth tokens for a user
func (r *GoogleCalendarRepository) UpdateGoogleTokens(ctx context.Context, userID int64, accessToken, refreshToken string, expiry time.Time) error {
	query := `
		UPDATE users 
		SET 
			google_access_token = $1,
			google_refresh_token = $2,
			google_token_expiry = $3,
			updated_at = NOW()
		WHERE id = $4
	`
	_, err := r.db.Exec(ctx, query, accessToken, refreshToken, expiry, userID)
	return err
}

// EnableGoogleCalendarSync enables or disables Google Calendar sync for a user
func (r *GoogleCalendarRepository) EnableGoogleCalendarSync(ctx context.Context, userID int64, enabled bool, calendarID string) error {
	query := `
		UPDATE users 
		SET 
			google_calendar_sync_enabled = $1,
			google_calendar_id = $2,
			updated_at = NOW()
		WHERE id = $3
	`
	_, err := r.db.Exec(ctx, query, enabled, calendarID, userID)
	return err
}

// DisconnectGoogleCalendar removes all Google Calendar integration data for a user
func (r *GoogleCalendarRepository) DisconnectGoogleCalendar(ctx context.Context, userID int64) error {
	query := `
		UPDATE users 
		SET 
			google_access_token = NULL,
			google_refresh_token = NULL,
			google_token_expiry = NULL,
			google_calendar_sync_enabled = FALSE,
			google_calendar_id = NULL,
			updated_at = NOW()
		WHERE id = $1
	`
	_, err := r.db.Exec(ctx, query, userID)
	return err
}

// CreateEventMapping creates a mapping between our event and Google Calendar event
func (r *GoogleCalendarRepository) CreateEventMapping(ctx context.Context, eventID uuid.UUID, userID int64, googleEventID, googleCalendarID string) error {
	query := `
		INSERT INTO google_calendar_event_mappings (
			event_id, user_id, google_event_id, google_calendar_id, last_synced_at
		) VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (event_id, user_id) DO UPDATE 
		SET 
			google_event_id = EXCLUDED.google_event_id,
			google_calendar_id = EXCLUDED.google_calendar_id,
			last_synced_at = NOW(),
			updated_at = NOW()
	`
	_, err := r.db.Exec(ctx, query, eventID, userID, googleEventID, googleCalendarID)
	return err
}

// GetEventMapping gets the Google Calendar mapping for an event
func (r *GoogleCalendarRepository) GetEventMapping(ctx context.Context, eventID uuid.UUID, userID int64) (*domain.GoogleCalendarEventMapping, error) {
	var mapping domain.GoogleCalendarEventMapping
	query := `
		SELECT id, event_id, user_id, google_event_id, google_calendar_id, last_synced_at, created_at, updated_at
		FROM google_calendar_event_mappings
		WHERE event_id = $1 AND user_id = $2
	`
	err := r.db.QueryRow(ctx, query, eventID, userID).Scan(
		&mapping.ID,
		&mapping.EventID,
		&mapping.UserID,
		&mapping.GoogleEventID,
		&mapping.GoogleCalendarID,
		&mapping.LastSyncedAt,
		&mapping.CreatedAt,
		&mapping.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &mapping, nil
}

// DeleteEventMapping deletes the mapping for an event
func (r *GoogleCalendarRepository) DeleteEventMapping(ctx context.Context, eventID uuid.UUID, userID int64) error {
	query := `DELETE FROM google_calendar_event_mappings WHERE event_id = $1 AND user_id = $2`
	_, err := r.db.Exec(ctx, query, eventID, userID)
	return err
}

// GetAllUserEventMappings gets all event mappings for a user
func (r *GoogleCalendarRepository) GetAllUserEventMappings(ctx context.Context, userID int64) ([]domain.GoogleCalendarEventMapping, error) {
	query := `
		SELECT id, event_id, user_id, google_event_id, google_calendar_id, last_synced_at, created_at, updated_at
		FROM google_calendar_event_mappings
		WHERE user_id = $1
		ORDER BY created_at DESC
	`
	rows, err := r.db.Query(ctx, query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var mappings []domain.GoogleCalendarEventMapping
	for rows.Next() {
		var m domain.GoogleCalendarEventMapping
		err := rows.Scan(
			&m.ID,
			&m.EventID,
			&m.UserID,
			&m.GoogleEventID,
			&m.GoogleCalendarID,
			&m.LastSyncedAt,
			&m.CreatedAt,
			&m.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		mappings = append(mappings, m)
	}

	if err = rows.Err(); err != nil {
		return nil, err
	}

	return mappings, nil
}
