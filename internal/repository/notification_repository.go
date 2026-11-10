package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

type NotificationRepository struct {
	db     *pgxpool.Pool
	logger *zap.Logger
}

func NewNotificationRepository(db *pgxpool.Pool, logger *zap.Logger) *NotificationRepository {
	return &NotificationRepository{
		db:     db,
		logger: logger.With(zap.String("repository", "notification")),
	}
}

func (r *NotificationRepository) Create(ctx context.Context, n *domain.Notification) error {
	query := `
		INSERT INTO notifications (id, user_id, organizer_id, type, title, content, is_read, metadata, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`
	metaJSON, _ := json.Marshal(n.Metadata)

	_, err := r.db.Exec(ctx, query,
		n.ID, n.UserID, n.OrganizerID, n.Type, n.Title, n.Content, n.IsRead, metaJSON, n.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("create notification: %w", err)
	}
	return nil
}

func (r *NotificationRepository) CreateInternal(ctx context.Context, n *domain.Notification) error {
	query := `
		INSERT INTO notifications (
			id, user_id, organizer_id, type, title, content, short_text, is_read, metadata,
			source_system_name, notification_type, application_number, action_url,
			source_system_code, external_message_id, notification_status, push_status,
			error_message, sent_at, created_at, processed_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21)
	`
	metaJSON, _ := json.Marshal(n.Metadata)
	updatedAt := n.CreatedAt
	if n.UpdatedAt != nil {
		updatedAt = *n.UpdatedAt
	}

	_, err := r.db.Exec(ctx, query,
		n.ID, n.UserID, n.OrganizerID, n.Type, n.Title, n.Content, n.ShortText, n.IsRead, metaJSON,
		n.SourceSystemName, n.NotificationType, n.ApplicationNumber, n.ActionURL,
		n.SourceSystemCode, n.ExternalMessageID, n.Status, n.PushStatus,
		n.ErrorMessage, n.SentAt, n.CreatedAt, n.ProcessedAt, updatedAt,
	)
	if err != nil {
		return fmt.Errorf("create internal notification: %w", err)
	}
	return nil
}

func (r *NotificationRepository) FindByExternalMessageID(ctx context.Context, sourceSystemCode, externalMessageID string) (*domain.Notification, error) {
	query := `
		SELECT id, user_id, type, title, content, short_text, source_system_name,
		       notification_type, application_number, action_url, is_read, metadata,
		       source_system_code, external_message_id, notification_status, push_status,
		       error_message, sent_at, created_at, processed_at, updated_at
		FROM notifications
		WHERE source_system_code = $1 AND external_message_id = $2
		LIMIT 1
	`

	var n domain.Notification
	var metaBytes []byte
	err := r.db.QueryRow(ctx, query, sourceSystemCode, externalMessageID).Scan(
		&n.ID, &n.UserID, &n.Type, &n.Title, &n.Content, &n.ShortText, &n.SourceSystemName,
		&n.NotificationType, &n.ApplicationNumber, &n.ActionURL, &n.IsRead, &metaBytes,
		&n.SourceSystemCode, &n.ExternalMessageID, &n.Status, &n.PushStatus,
		&n.ErrorMessage, &n.SentAt, &n.CreatedAt, &n.ProcessedAt, &n.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("find notification by external message id: %w", err)
	}
	if len(metaBytes) > 0 {
		_ = json.Unmarshal(metaBytes, &n.Metadata)
	}
	return &n, nil
}

func (r *NotificationRepository) UpdateInternalProcessingResult(ctx context.Context, id uuid.UUID, status, pushStatus string, errorMessage *string, updatedAt time.Time) error {
	query := `
		UPDATE notifications
		SET notification_status = $2,
		    push_status = $3,
		    error_message = $4,
		    processed_at = $5,
		    updated_at = $5
		WHERE id = $1
	`
	_, err := r.db.Exec(ctx, query, id, status, pushStatus, errorMessage, updatedAt)
	if err != nil {
		return fmt.Errorf("update internal notification processing result: %w", err)
	}
	return nil
}

func (r *NotificationRepository) GetUserNotifications(ctx context.Context, userID int64, limit, offset int) ([]domain.Notification, error) {
	query := `
		SELECT id, user_id, organizer_id, type, title, content, short_text, source_system_name,
		       notification_type, application_number, action_url, is_read, metadata,
		       source_system_code, external_message_id, notification_status, sent_at, created_at, processed_at
		FROM notifications
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`
	rows, err := r.db.Query(ctx, query, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var notifications []domain.Notification
	eventIDs := make([]uuid.UUID, 0)
	eventIDMap := make(map[uuid.UUID][]int) // event_id -> list of indices in notifications slice

	for rows.Next() {
		var n domain.Notification
		var metaBytes []byte
		if err := rows.Scan(
			&n.ID, &n.UserID, &n.OrganizerID, &n.Type, &n.Title, &n.Content, &n.ShortText, &n.SourceSystemName,
			&n.NotificationType, &n.ApplicationNumber, &n.ActionURL, &n.IsRead, &metaBytes,
			&n.SourceSystemCode, &n.ExternalMessageID, &n.Status, &n.SentAt, &n.CreatedAt, &n.ProcessedAt,
		); err != nil {
			return nil, err
		}
		if len(metaBytes) > 0 {
			_ = json.Unmarshal(metaBytes, &n.Metadata)
		}

		idx := len(notifications)
		notifications = append(notifications, n)

		// Identify calendar invites with event_id
		if n.Type == "calendar_invite" || n.Type == "calendar_event_created" || n.Type == "calendar_event_updated" {
			if n.Metadata != nil {
				if val, ok := n.Metadata["event_id"]; ok {
					if evtID, err := uuid.Parse(val); err == nil {
						eventIDs = append(eventIDs, evtID)
						eventIDMap[evtID] = append(eventIDMap[evtID], idx)
					}
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Batch fetch attendee statuses if we have any eventIDs
	if len(eventIDs) > 0 {
		statusQuery := `
			SELECT event_id, status
			FROM event_attendees
			WHERE user_id = $1 AND event_id = ANY($2)
		`
		statusRows, err := r.db.Query(ctx, statusQuery, userID, eventIDs)
		if err == nil {
			defer statusRows.Close()
			for statusRows.Next() {
				var evtID uuid.UUID
				var status string
				if err := statusRows.Scan(&evtID, &status); err == nil {
					if indices, ok := eventIDMap[evtID]; ok {
						for _, idx := range indices {
							if notifications[idx].Metadata == nil {
								notifications[idx].Metadata = make(map[string]string)
							}
							notifications[idx].Metadata["invite_response_status"] = status
							notifications[idx].Metadata["calendar_invite_response_status"] = status
							notifications[idx].InviteResponseStatus = &status
						}
					}
				}
			}
		} else {
			r.logger.Error("failed to fetch attendee statuses for notifications", zap.Error(err))
		}
	}

	return notifications, nil
}

func (r *NotificationRepository) MarkAsRead(ctx context.Context, id uuid.UUID, userID int64) error {
	query := `UPDATE notifications SET is_read = true WHERE id = $1 AND user_id = $2`
	_, err := r.db.Exec(ctx, query, id, userID)
	return err
}

func (r *NotificationRepository) MarkAllAsRead(ctx context.Context, userID int64) error {
	query := `UPDATE notifications SET is_read = true WHERE user_id = $1 AND is_read = false`
	_, err := r.db.Exec(ctx, query, userID)
	if err != nil {
		return fmt.Errorf("mark all as read: %w", err)
	}
	return nil
}
