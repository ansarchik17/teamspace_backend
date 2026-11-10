package repository

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func (r *CalendarRepository) MeetingRoomScope(ctx context.Context, userID int64) (uuid.UUID, bool, error) {
	var org uuid.UUID
	var manage bool
	err := r.db.QueryRow(ctx, `SELECT o.id,(u.role='admin' OR COALESCE(o.owner_id=u.id,false))
 FROM users u JOIN organizations o ON o.id=u.organization_id
 WHERE u.id=$1 AND u.is_active AND u.deleted_at IS NULL AND o.status='active'`, userID).Scan(&org, &manage)
	if errors.Is(err, pgx.ErrNoRows) {
		return org, false, apperrors.Forbidden("active company membership required")
	}
	return org, manage, err
}

const meetingSiteColumns = `id,organization_id,name,address,city,timezone,working_hours,is_active,created_at,updated_at`

func scanMeetingSite(row pgx.Row) (*domain.MeetingSite, error) {
	var s domain.MeetingSite
	err := row.Scan(&s.ID, &s.OrganizationID, &s.Name, &s.Address, &s.City, &s.Timezone, &s.WorkingHours, &s.IsActive, &s.CreatedAt, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperrors.NotFound("site")
	}
	return &s, err
}
func (r *CalendarRepository) ListMeetingSites(ctx context.Context, org uuid.UUID, includeInactive bool) ([]domain.MeetingSite, error) {
	rows, err := r.db.Query(ctx, `SELECT `+meetingSiteColumns+` FROM meeting_sites WHERE organization_id=$1 AND deleted_at IS NULL AND ($2 OR is_active) ORDER BY name,id`, org, includeInactive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.MeetingSite{}
	for rows.Next() {
		s, err := scanMeetingSite(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *s)
	}
	return items, rows.Err()
}
func (r *CalendarRepository) GetMeetingSite(ctx context.Context, org, id uuid.UUID) (*domain.MeetingSite, error) {
	return scanMeetingSite(r.db.QueryRow(ctx, `SELECT `+meetingSiteColumns+` FROM meeting_sites WHERE id=$1 AND organization_id=$2 AND deleted_at IS NULL`, id, org))
}
func (r *CalendarRepository) SaveMeetingSite(ctx context.Context, s *domain.MeetingSite, create bool) error {
	var err error
	if create {
		err = r.db.QueryRow(ctx, `INSERT INTO meeting_sites(id,organization_id,name,address,city,timezone,working_hours,is_active) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING created_at,updated_at`, s.ID, s.OrganizationID, s.Name, s.Address, s.City, s.Timezone, s.WorkingHours, s.IsActive).Scan(&s.CreatedAt, &s.UpdatedAt)
	} else {
		err = r.db.QueryRow(ctx, `UPDATE meeting_sites SET name=$3,address=$4,city=$5,timezone=$6,working_hours=$7,is_active=$8,updated_at=clock_timestamp() WHERE id=$1 AND organization_id=$2 AND deleted_at IS NULL RETURNING created_at,updated_at`, s.ID, s.OrganizationID, s.Name, s.Address, s.City, s.Timezone, s.WorkingHours, s.IsActive).Scan(&s.CreatedAt, &s.UpdatedAt)
	}
	return calendarResourceError(err)
}
func (r *CalendarRepository) DeleteMeetingSite(ctx context.Context, org, id uuid.UUID) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var found uuid.UUID
	if err = tx.QueryRow(ctx, `SELECT id FROM meeting_sites WHERE id=$1 AND organization_id=$2 AND deleted_at IS NULL FOR UPDATE`, id, org).Scan(&found); err != nil {
		return calendarResourceError(err)
	}
	var hasRooms bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM meeting_rooms WHERE site_id=$1 AND deleted_at IS NULL)`, id).Scan(&hasRooms); err != nil {
		return err
	}
	if hasRooms {
		return apperrors.New("SITE_HAS_ROOMS", "site has rooms", 409)
	}
	if _, err = tx.Exec(ctx, `UPDATE meeting_sites SET deleted_at=now(),is_active=false,updated_at=now() WHERE id=$1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func calendarResourceError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return apperrors.NotFound("calendar resource")
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch {
		case pg.Code == "23505" && pg.ConstraintName == "meeting_room_site_name":
			return apperrors.New("ROOM_NAME_TAKEN", "room name taken", 409)
		case pg.Code == "P7601":
			return apperrors.New("ATTENDANCE_NOT_ALLOWED", "attendance not allowed", 422)
		case pg.Code == "P7602":
			return apperrors.RoomNotFound()
		case pg.Code == "P7603":
			return apperrors.RoomInactive()
		}
	}
	return err
}
