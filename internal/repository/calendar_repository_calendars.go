package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func (r *CalendarRepository) EnsureDefaultCalendar(ctx context.Context, userID int64) (*domain.UserCalendar, error) {
	query := `
		INSERT INTO user_calendars (id, user_id, name, color, is_default, created_at, updated_at)
		VALUES ($1, $2, 'Мой календарь', '#2161F5', TRUE, $3, $3)
		ON CONFLICT (user_id) WHERE is_default DO UPDATE SET updated_at = user_calendars.updated_at
		RETURNING id, user_id, name, color, is_default, created_at, updated_at`
	now := time.Now().UTC()
	var calendar domain.UserCalendar
	err := r.db.QueryRow(ctx, query, uuid.New(), userID, now).Scan(
		&calendar.ID, &calendar.UserID, &calendar.Name, &calendar.Color,
		&calendar.IsDefault, &calendar.CreatedAt, &calendar.UpdatedAt,
	)
	return &calendar, err
}

func (r *CalendarRepository) ListCalendars(ctx context.Context, userID int64) ([]domain.UserCalendar, error) {
	if _, err := r.EnsureDefaultCalendar(ctx, userID); err != nil {
		return nil, err
	}
	rows, err := r.db.Query(ctx, `SELECT id, user_id, name, color, is_default, created_at, updated_at FROM user_calendars WHERE user_id=$1 ORDER BY is_default DESC, created_at ASC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.UserCalendar, 0, 4)
	for rows.Next() {
		var item domain.UserCalendar
		if err := rows.Scan(&item.ID, &item.UserID, &item.Name, &item.Color, &item.IsDefault, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *CalendarRepository) CreateCalendar(ctx context.Context, userID int64, name, color string) (*domain.UserCalendar, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("calendar name is required")
	}
	if strings.TrimSpace(color) == "" {
		color = "#2161F5"
	}
	now := time.Now().UTC()
	item := domain.UserCalendar{ID: uuid.New(), UserID: userID, Name: name, Color: color, CreatedAt: now, UpdatedAt: now}
	err := r.db.QueryRow(ctx, `INSERT INTO user_calendars (id,user_id,name,color,is_default,created_at,updated_at) VALUES ($1,$2,$3,$4,FALSE,$5,$5) RETURNING id,user_id,name,color,is_default,created_at,updated_at`, item.ID, userID, item.Name, item.Color, now).Scan(&item.ID, &item.UserID, &item.Name, &item.Color, &item.IsDefault, &item.CreatedAt, &item.UpdatedAt)
	return &item, err
}

func (r *CalendarRepository) GetOwnedCalendar(ctx context.Context, userID int64, calendarID uuid.UUID) (*domain.UserCalendar, error) {
	var item domain.UserCalendar
	err := r.db.QueryRow(ctx, `SELECT id,user_id,name,color,is_default,created_at,updated_at FROM user_calendars WHERE id=$1 AND user_id=$2`, calendarID, userID).Scan(&item.ID, &item.UserID, &item.Name, &item.Color, &item.IsDefault, &item.CreatedAt, &item.UpdatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return &item, err
}
