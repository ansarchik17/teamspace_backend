package repository

import (
	"context"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func (r *DriveRepository) OfficeUserProfiles(ctx context.Context, ids []int64) ([]domain.OfficeUser, error) {
	rows, err := r.db.Query(ctx, `SELECT id,COALESCE(NULLIF(BTRIM(CONCAT_WS(' ',first_name,last_name)),''),NULLIF(username,''),'Пользователь '||id::text),COALESCE(avatar_url,'') FROM users WHERE id=ANY($1) AND deleted_at IS NULL ORDER BY id`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.OfficeUser, 0)
	for rows.Next() {
		var user domain.OfficeUser
		var id int64
		if err := rows.Scan(&id, &user.Name, &user.Image); err != nil {
			return nil, err
		}
		user.ID = strconv.FormatInt(id, 10)
		result = append(result, user)
	}
	return result, rows.Err()
}

func (r *DriveRepository) TouchOfficePresence(ctx context.Context, session, file uuid.UUID, user int64, key string) error {
	// Reject stale keys atomically with heartbeat registration.
	result, err := r.db.Exec(ctx, `INSERT INTO drive_office_presence(session_id,file_id,user_id,document_key)
 SELECT $1,id,$3,$4 FROM files WHERE id=$2 AND deleted_at IS NULL AND COALESCE(NULLIF(onlyoffice_key,''),id::text||'-v'||version::text)=$4
 ON CONFLICT(session_id) DO UPDATE SET last_seen_at=NOW()
 WHERE drive_office_presence.file_id=EXCLUDED.file_id AND drive_office_presence.user_id=EXCLUDED.user_id AND drive_office_presence.document_key=EXCLUDED.document_key`, session, file, user, key)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrDriveVersionConflict
	}
	// Bound retained data without requiring a deployment-specific scheduler.
	_, err = r.db.Exec(ctx, `DELETE FROM drive_office_presence WHERE last_seen_at<NOW()-INTERVAL '90 seconds'`)
	return err
}

func (r *DriveRepository) LeaveOfficePresence(ctx context.Context, session, file uuid.UUID, user int64) error {
	_, err := r.db.Exec(ctx, `DELETE FROM drive_office_presence WHERE session_id=$1 AND file_id=$2 AND user_id=$3`, session, file, user)
	return err
}

func (r *DriveRepository) OfficePresence(ctx context.Context, file uuid.UUID, key string) (map[int64]time.Time, error) {
	rows, err := r.db.Query(ctx, `SELECT user_id,MAX(last_seen_at) FROM drive_office_presence WHERE file_id=$1 AND document_key=$2 AND last_seen_at>NOW()-INTERVAL '90 seconds' GROUP BY user_id ORDER BY user_id`, file, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[int64]time.Time{}
	for rows.Next() {
		var id int64
		var seen time.Time
		if err := rows.Scan(&id, &seen); err != nil {
			return nil, err
		}
		result[id] = seen
	}
	return result, rows.Err()
}
