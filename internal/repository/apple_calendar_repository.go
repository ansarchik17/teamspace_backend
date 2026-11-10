package repository

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/west6ide/workspace-backend/internal/domain"
)

type AppleCalendarRepository struct {
	db *pgxpool.Pool
}

func NewAppleCalendarRepository(db *pgxpool.Pool) *AppleCalendarRepository {
	return &AppleCalendarRepository{db: db}
}

// GetUserAppleSyncStatus gets the Apple Calendar sync status for a user
func (r *AppleCalendarRepository) GetUserAppleSyncStatus(ctx context.Context, userID int64) (*domain.AppleCalendarSyncStatus, error) {
	var status domain.AppleCalendarSyncStatus
	query := `
		SELECT 
			id as user_id, 
			apple_calendar_email, 
			apple_app_specific_password, 
			apple_calendar_sync_enabled,
			apple_calendar_id
		FROM users 
		WHERE id = $1
	`
	err := r.db.QueryRow(ctx, query, userID).Scan(
		&status.UserID,
		&status.AppleCalendarEmail,
		&status.AppleAppSpecificPassword,
		&status.AppleCalendarSyncEnabled,
		&status.AppleCalendarID,
	)
	if err != nil {
		return nil, err
	}
	return &status, nil
}

// UpdateAppleCredentials updates the Apple credentials for a user
func (r *AppleCalendarRepository) UpdateAppleCredentials(ctx context.Context, userID int64, email, password string) error {
	query := `
		UPDATE users 
		SET 
			apple_calendar_email = $1,
			apple_app_specific_password = $2,
			updated_at = NOW()
		WHERE id = $3
	`
	_, err := r.db.Exec(ctx, query, email, password, userID)
	return err
}

// EnableAppleCalendarSync enables or disables Apple Calendar sync for a user
func (r *AppleCalendarRepository) EnableAppleCalendarSync(ctx context.Context, userID int64, enabled bool, calendarID string) error {
	query := `
		UPDATE users 
		SET 
			apple_calendar_sync_enabled = $1,
			apple_calendar_id = $2,
			updated_at = NOW()
		WHERE id = $3
	`
	_, err := r.db.Exec(ctx, query, enabled, calendarID, userID)
	return err
}

// DisconnectAppleCalendar removes all Apple Calendar integration data for a user
func (r *AppleCalendarRepository) DisconnectAppleCalendar(ctx context.Context, userID int64) error {
	query := `
		UPDATE users 
		SET 
			apple_calendar_email = NULL,
			apple_app_specific_password = NULL,
			apple_calendar_sync_enabled = FALSE,
			apple_calendar_id = NULL,
			updated_at = NOW()
		WHERE id = $1
	`
	_, err := r.db.Exec(ctx, query, userID)
	return err
}

// CreateEventMapping creates a mapping between our event and Apple Calendar event
func (r *AppleCalendarRepository) CreateEventMapping(ctx context.Context, eventID uuid.UUID, userID int64, appleEventID, appleCalendarID string) error {
	query := `
		INSERT INTO apple_calendar_event_mappings (
			event_id, user_id, apple_event_id, apple_calendar_id, last_synced_at
		) VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (event_id, user_id) DO UPDATE 
		SET 
			apple_event_id = EXCLUDED.apple_event_id,
			apple_calendar_id = EXCLUDED.apple_calendar_id,
			last_synced_at = NOW(),
			updated_at = NOW()
	`
	_, err := r.db.Exec(ctx, query, eventID, userID, appleEventID, appleCalendarID)
	return err
}

// GetEventMapping gets the Apple Calendar mapping for an event
func (r *AppleCalendarRepository) GetEventMapping(ctx context.Context, eventID uuid.UUID, userID int64) (*domain.AppleCalendarEventMapping, error) {
	var mapping domain.AppleCalendarEventMapping
	query := `
		SELECT id, event_id, user_id, apple_event_id, apple_calendar_id, last_synced_at, created_at, updated_at
		FROM apple_calendar_event_mappings
		WHERE event_id = $1 AND user_id = $2
	`
	err := r.db.QueryRow(ctx, query, eventID, userID).Scan(
		&mapping.ID,
		&mapping.EventID,
		&mapping.UserID,
		&mapping.AppleEventID,
		&mapping.AppleCalendarID,
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
func (r *AppleCalendarRepository) DeleteEventMapping(ctx context.Context, eventID uuid.UUID, userID int64) error {
	query := `DELETE FROM apple_calendar_event_mappings WHERE event_id = $1 AND user_id = $2`
	_, err := r.db.Exec(ctx, query, eventID, userID)
	return err
}

// GetAllUserEventMappings gets all event mappings for a user
func (r *AppleCalendarRepository) GetAllUserEventMappings(ctx context.Context, userID int64) ([]domain.AppleCalendarEventMapping, error) {
	query := `
		SELECT id, event_id, user_id, apple_event_id, apple_calendar_id, last_synced_at, created_at, updated_at
		FROM apple_calendar_event_mappings
		WHERE user_id = $1
		ORDER BY created_at DESC
	`
	rows, err := r.db.Query(ctx, query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var mappings []domain.AppleCalendarEventMapping
	for rows.Next() {
		var m domain.AppleCalendarEventMapping
		err := rows.Scan(
			&m.ID,
			&m.EventID,
			&m.UserID,
			&m.AppleEventID,
			&m.AppleCalendarID,
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
