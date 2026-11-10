package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

const (
	directCallRingingConflictWindow = 45 * time.Second
)

func keepsCallOpen(status string) bool {
	switch domain.NormalizeCallStatus(status) {
	case domain.CallStatusRinging, domain.CallStatusConnecting, domain.CallStatusAccepted, domain.CallStatusOngoing:
		return true
	default:
		return false
	}
}

type CallHistoryRepository struct {
	db     *pgxpool.Pool
	logger *zap.Logger
}

type AcceptCallParams struct {
	CallID    uuid.UUID
	UserID    int64
	DeviceID  string
	SessionID string
}

type AcceptCallResult struct {
	Call                   *domain.CallHistory
	PreviousStatus         string
	Updated                bool
	Idempotent             bool
	Forbidden              bool
	AcceptedOnOtherDevice  bool
	AcceptedBySameEndpoint bool
}

func NewCallHistoryRepository(db *pgxpool.Pool, logger *zap.Logger) *CallHistoryRepository {
	return &CallHistoryRepository{
		db:     db,
		logger: logger.With(zap.String("repository", "call_history")),
	}
}

func scanCallHistory(scanner interface{ Scan(dest ...any) error }, c *domain.CallHistory) error {
	var chatID sql.NullString
	var fromName sql.NullString
	var callType sql.NullString
	var acceptedByUserID sql.NullInt64
	var acceptedByDeviceID sql.NullString
	var acceptedBySessionID sql.NullString
	var acceptedAt sql.NullTime

	if err := scanner.Scan(
		&c.ID,
		&c.CallID,
		&c.FromUserID,
		&c.ToUserID,
		&c.RoomID,
		&callType,
		&chatID,
		&fromName,
		&c.IsVideo,
		&c.Status,
		&acceptedByUserID,
		&acceptedByDeviceID,
		&acceptedBySessionID,
		&acceptedAt,
		&c.CreatedAt,
		&c.UpdatedAt,
		&c.EndedAt,
	); err != nil {
		return err
	}

	if chatID.Valid {
		value := strings.TrimSpace(chatID.String)
		c.ChatID = &value
	}
	if callType.Valid {
		c.CallType = strings.TrimSpace(callType.String)
	}
	if c.CallType == "" {
		c.CallType = domain.CallTypePrivate
	}
	if fromName.Valid {
		value := strings.TrimSpace(fromName.String)
		c.FromName = &value
	}
	if acceptedByUserID.Valid {
		value := acceptedByUserID.Int64
		c.AcceptedByUserID = &value
	}
	if acceptedByDeviceID.Valid {
		value := strings.TrimSpace(acceptedByDeviceID.String)
		c.AcceptedByDeviceID = &value
	}
	if acceptedBySessionID.Valid {
		value := strings.TrimSpace(acceptedBySessionID.String)
		c.AcceptedBySessionID = &value
	}
	if acceptedAt.Valid {
		value := acceptedAt.Time
		c.AcceptedAt = &value
	}

	return nil
}

func sameNonEmptyValue(left, right string) bool {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	return left != "" && right != "" && left == right
}

func isSameAcceptEndpoint(call *domain.CallHistory, deviceID, sessionID string) bool {
	if call == nil {
		return false
	}
	if sameNonEmptyValue(optionalStringValue(call.AcceptedByDeviceID), deviceID) {
		return true
	}
	if sameNonEmptyValue(optionalStringValue(call.AcceptedBySessionID), sessionID) {
		return true
	}
	if strings.TrimSpace(deviceID) == "" && strings.TrimSpace(sessionID) == "" {
		return true
	}
	if strings.TrimSpace(optionalStringValue(call.AcceptedByDeviceID)) == "" &&
		strings.TrimSpace(optionalStringValue(call.AcceptedBySessionID)) == "" {
		return true
	}
	return false
}

func optionalStringValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

// Create preserves call history record.
func (r *CallHistoryRepository) Create(ctx context.Context, c *domain.CallHistory) error {
	if c == nil {
		return fmt.Errorf("create call history: payload is nil")
	}

	var chatID any = nil
	if c.ChatID != nil && strings.TrimSpace(*c.ChatID) != "" {
		chatID = strings.TrimSpace(*c.ChatID)
	}

	var fromName any = nil
	if c.FromName != nil && strings.TrimSpace(*c.FromName) != "" {
		fromName = strings.TrimSpace(*c.FromName)
	}
	callType := strings.TrimSpace(c.CallType)
	if callType == "" {
		callType = domain.CallTypePrivate
	}

	cmd, err := r.db.Exec(ctx, `
		INSERT INTO call_history (
			call_id,
			from_user_id,
			to_user_id,
			room_id,
			call_type,
			chat_id,
			from_name,
			is_video,
			status
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (call_id) DO NOTHING
	`,
		c.CallID,
		c.FromUserID,
		c.ToUserID,
		strings.TrimSpace(c.RoomID),
		callType,
		chatID,
		fromName,
		c.IsVideo,
		c.Status,
	)
	if err != nil {
		return fmt.Errorf("create call history: %w", err)
	}

	if cmd.RowsAffected() == 0 {
		return fmt.Errorf("call history not inserted: conflict on call_id=%s", c.CallID.String())
	}

	return nil
}

// CreateAndReturn (optional)
func (r *CallHistoryRepository) CreateAndReturn(ctx context.Context, c *domain.CallHistory) error {
	if c == nil {
		return fmt.Errorf("create call history: payload is nil")
	}

	err := r.db.QueryRow(ctx, `
		INSERT INTO call_history (
			call_id,
			from_user_id,
			to_user_id,
			room_id,
			call_type,
			chat_id,
			from_name,
			is_video,
			status
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (call_id) DO UPDATE
		SET status = EXCLUDED.status,
			updated_at = now()
		RETURNING id, created_at, updated_at
	`,
		c.CallID,
		c.FromUserID,
		c.ToUserID,
		strings.TrimSpace(c.RoomID),
		func() string {
			callType := strings.TrimSpace(c.CallType)
			if callType == "" {
				return domain.CallTypePrivate
			}
			return callType
		}(),
		c.ChatID,
		c.FromName,
		c.IsVideo,
		c.Status,
	).Scan(&c.ID, &c.CreatedAt, &c.UpdatedAt)

	if err != nil {
		return fmt.Errorf("create call history returning: %w", err)
	}
	return nil
}

// CreateDirectCallOrGetExisting creates a new direct call atomically for a user pair.
// If another active call between the same two users already exists, it returns that call instead.
func (r *CallHistoryRepository) CreateDirectCallOrGetExisting(ctx context.Context, c *domain.CallHistory) (*domain.CallHistory, bool, error) {
	if c == nil {
		return nil, false, fmt.Errorf("create direct call: payload is nil")
	}
	if c.FromUserID == 0 || c.ToUserID == 0 {
		return nil, false, fmt.Errorf("create direct call: both users are required")
	}

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("begin direct call tx: %w", err)
	}

	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	if err := r.lockDirectCallUsers(ctx, tx, c.FromUserID, c.ToUserID); err != nil {
		return nil, false, fmt.Errorf("lock direct call users: %w", err)
	}

	if err := r.expireStaleDirectCallsTx(ctx, tx, c.FromUserID, c.ToUserID); err != nil {
		return nil, false, fmt.Errorf("expire stale direct calls: %w", err)
	}

	existingCall, err := r.getActiveDirectCallByUsersTx(ctx, tx, c.FromUserID, c.ToUserID)
	if err != nil {
		return nil, false, fmt.Errorf("lookup existing direct call: %w", err)
	}
	if existingCall != nil {
		if err := tx.Commit(ctx); err != nil {
			return nil, false, fmt.Errorf("commit direct call tx: %w", err)
		}
		committed = true
		return existingCall, false, nil
	}

	var chatID any
	if c.ChatID != nil && strings.TrimSpace(*c.ChatID) != "" {
		chatID = strings.TrimSpace(*c.ChatID)
	}

	var fromName any
	if c.FromName != nil && strings.TrimSpace(*c.FromName) != "" {
		fromName = strings.TrimSpace(*c.FromName)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO call_history (
			call_id,
			from_user_id,
			to_user_id,
			room_id,
			call_type,
			chat_id,
			from_name,
			is_video,
			status
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
	`,
		c.CallID,
		c.FromUserID,
		c.ToUserID,
		strings.TrimSpace(c.RoomID),
		func() string {
			callType := strings.TrimSpace(c.CallType)
			if callType == "" {
				return domain.CallTypePrivate
			}
			return callType
		}(),
		chatID,
		fromName,
		c.IsVideo,
		c.Status,
	); err != nil {
		return nil, false, fmt.Errorf("insert direct call history: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO call_participants (call_id, user_id, status, role, joined_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (call_id, user_id) DO UPDATE
		SET status = EXCLUDED.status,
		    joined_at = EXCLUDED.joined_at,
		    left_at = NULL
	`, c.CallID, c.FromUserID, "joined", "host"); err != nil {
		return nil, false, fmt.Errorf("insert direct call participant: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, false, fmt.Errorf("commit direct call tx: %w", err)
	}
	committed = true

	return c, true, nil
}

func (r *CallHistoryRepository) lockDirectCallUsers(ctx context.Context, tx pgx.Tx, userA, userB int64) error {
	if userB < userA {
		userA, userB = userB, userA
	}

	// Use advisory transaction locks instead of row locks on users so
	// direct-call creation is serialized even if a user row is missing or
	// temporarily unavailable in the users table.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, userA); err != nil {
		return err
	}

	if userA == userB {
		return nil
	}

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, userB); err != nil {
		return err
	}

	return nil
}

func (r *CallHistoryRepository) expireStaleDirectCallsTx(ctx context.Context, tx pgx.Tx, userA, userB int64) error {
	_, err := tx.Exec(ctx, `
		WITH expired AS (
			UPDATE call_history
			SET status = $3,
			    updated_at = NOW(),
			    ended_at = NOW()
			WHERE (
				  (from_user_id = $1 AND to_user_id = $2)
			   OR (from_user_id = $2 AND to_user_id = $1)
			)
			  AND to_user_id <> 0
			  AND status IN ('created', 'ringing', 'connecting', 'initiated')
			  AND created_at < NOW() - make_interval(secs => $4)
			RETURNING call_id
		)
		UPDATE call_participants
		SET status = 'left',
		    left_at = NOW()
		WHERE call_id IN (SELECT call_id FROM expired)
		  AND status = 'joined'
	`, userA, userB, domain.CallStatusNoAnswer, int(directCallRingingConflictWindow.Seconds()))
	return err
}

func (r *CallHistoryRepository) EndStaleDirectCallsForUsers(ctx context.Context, userA, userB int64) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin stale direct call cleanup tx: %w", err)
	}

	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	if err := r.lockDirectCallUsers(ctx, tx, userA, userB); err != nil {
		return fmt.Errorf("lock direct call users: %w", err)
	}

	if err := r.expireStaleDirectCallsTx(ctx, tx, userA, userB); err != nil {
		return fmt.Errorf("expire stale direct calls: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit stale direct call cleanup tx: %w", err)
	}
	committed = true

	return nil
}

func (r *CallHistoryRepository) getActiveDirectCallByUsersTx(ctx context.Context, tx pgx.Tx, userA, userB int64) (*domain.CallHistory, error) {
	var c domain.CallHistory

	row := tx.QueryRow(ctx, `
			SELECT
			id, call_id, from_user_id, to_user_id, room_id, call_type,
			chat_id, from_name, is_video, status,
			accepted_by_user_id, accepted_by_device_id, accepted_by_session_id, accepted_at,
			created_at, updated_at, ended_at
		FROM call_history
		WHERE (
			  (from_user_id = $1 AND to_user_id = $2)
		   OR (from_user_id = $2 AND to_user_id = $1)
		)
		  AND to_user_id <> 0
		  AND status IN ('created', 'ringing', 'connecting', 'initiated', 'accepted', 'ongoing')
		ORDER BY created_at ASC
		LIMIT 1
	`, userA, userB)
	if err := scanCallHistory(row, &c); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	return &c, nil
}

// ListForUser returns call history for user
func (r *CallHistoryRepository) ListForUser(ctx context.Context, userID int64, limit, offset int, kind string) ([]domain.CallHistory, error) {
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	kind = strings.TrimSpace(strings.ToLower(kind))

	base := `
		SELECT
			id,
			call_id,
			from_user_id,
			to_user_id,
			room_id,
			call_type,
			chat_id,
			from_name,
			is_video,
			status,
			created_at,
			updated_at,
			ended_at
		FROM call_history
	`

	where := ""
	switch kind {
	case "incoming":
		where = " WHERE to_user_id = $1 "
	case "outgoing":
		where = " WHERE from_user_id = $1 "
	default: // all
		where = " WHERE (to_user_id = $1 OR from_user_id = $1) "
	}

	q := base + where + `
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`

	rows, err := r.db.Query(ctx, q, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list call history: %w", err)
	}
	defer rows.Close()

	res := make([]domain.CallHistory, 0, limit)
	for rows.Next() {
		var c domain.CallHistory
		var callType sql.NullString
		var chatID sql.NullString
		var fromName sql.NullString
		if err := rows.Scan(
			&c.ID,
			&c.CallID,
			&c.FromUserID,
			&c.ToUserID,
			&c.RoomID,
			&callType,
			&chatID,
			&fromName,
			&c.IsVideo,
			&c.Status,
			&c.CreatedAt,
			&c.UpdatedAt,
			&c.EndedAt,
		); err != nil {
			return nil, fmt.Errorf("list call history scan: %w", err)
		}
		if callType.Valid {
			c.CallType = strings.TrimSpace(callType.String)
		}
		if c.CallType == "" {
			c.CallType = domain.CallTypePrivate
		}
		if chatID.Valid {
			value := strings.TrimSpace(chatID.String)
			c.ChatID = &value
		}
		if fromName.Valid {
			value := strings.TrimSpace(fromName.String)
			c.FromName = &value
		}
		res = append(res, c)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list call history rows: %w", err)
	}

	return res, nil
}

func (r *CallHistoryRepository) GetByCallID(ctx context.Context, callID uuid.UUID) (*domain.CallHistory, error) {
	var c domain.CallHistory

	row := r.db.QueryRow(ctx, `
		SELECT
			id, call_id, from_user_id, to_user_id, room_id,
			call_type,
			chat_id, from_name, is_video, status,
			accepted_by_user_id, accepted_by_device_id, accepted_by_session_id, accepted_at,
			created_at, updated_at, ended_at
		FROM call_history
		WHERE call_id = $1
	`, callID)

	if err := scanCallHistory(row, &c); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get call history: %w", err)
	}

	return &c, nil
}

func (r *CallHistoryRepository) UpdateCallStatus(ctx context.Context, callID uuid.UUID, status string) error {
	var query string
	if keepsCallOpen(status) {
		query = `UPDATE call_history SET status = $1, updated_at = NOW() WHERE call_id = $2`
	} else {
		query = `UPDATE call_history SET status = $1, updated_at = NOW(), ended_at = NOW() WHERE call_id = $2`
	}

	_, err := r.db.Exec(ctx, query, status, callID)

	return err
}

func (r *CallHistoryRepository) UpdateCallType(ctx context.Context, callID uuid.UUID, callType string) error {
	callType = strings.TrimSpace(callType)
	if callType == "" {
		callType = domain.CallTypePrivate
	}

	_, err := r.db.Exec(ctx, `
		UPDATE call_history
		SET call_type = $1,
		    updated_at = NOW()
		WHERE call_id = $2
	`, callType, callID)
	if err != nil {
		return fmt.Errorf("update call type: %w", err)
	}
	return nil
}

func (r *CallHistoryRepository) TransitionCallStatus(ctx context.Context, callID uuid.UUID, nextStatus string, allowedFrom ...string) (string, bool, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return "", false, fmt.Errorf("begin transition call tx: %w", err)
	}

	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	var currentStatus string
	if err := tx.QueryRow(ctx, `
		SELECT status
		FROM call_history
		WHERE call_id = $1
		FOR UPDATE
	`, callID).Scan(&currentStatus); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("lock call history for transition: %w", err)
	}

	if len(allowedFrom) > 0 {
		allowed := false
		normalizedCurrent := domain.NormalizeCallStatus(currentStatus)
		for _, candidate := range allowedFrom {
			if normalizedCurrent == domain.NormalizeCallStatus(candidate) {
				allowed = true
				break
			}
		}
		if !allowed {
			if err := tx.Commit(ctx); err != nil {
				return currentStatus, false, fmt.Errorf("commit transition no-op tx: %w", err)
			}
			committed = true
			return currentStatus, false, nil
		}
	}

	query := `UPDATE call_history SET status = $1, updated_at = NOW()`
	if keepsCallOpen(nextStatus) {
		query += ` WHERE call_id = $2`
	} else {
		query += `, ended_at = COALESCE(ended_at, NOW()) WHERE call_id = $2`
	}

	if _, err := tx.Exec(ctx, query, nextStatus, callID); err != nil {
		return currentStatus, false, fmt.Errorf("update call history transition: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return currentStatus, false, fmt.Errorf("commit transition call tx: %w", err)
	}
	committed = true

	return currentStatus, true, nil
}

func (r *CallHistoryRepository) AcceptCall(ctx context.Context, params AcceptCallParams) (*AcceptCallResult, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin accept call tx: %w", err)
	}

	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	var call domain.CallHistory
	row := tx.QueryRow(ctx, `
		SELECT
			id, call_id, from_user_id, to_user_id, room_id,
			call_type,
			chat_id, from_name, is_video, status,
			accepted_by_user_id, accepted_by_device_id, accepted_by_session_id, accepted_at,
			created_at, updated_at, ended_at
		FROM call_history
		WHERE call_id = $1
		FOR UPDATE
	`, params.CallID)
	if err := scanCallHistory(row, &call); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("load call for accept: %w", err)
	}

	result := &AcceptCallResult{
		Call:           &call,
		PreviousStatus: call.Status,
	}
	isGroupCall := strings.EqualFold(strings.TrimSpace(call.CallType), domain.CallTypeGroup) ||
		call.ToUserID == 0 ||
		strings.HasPrefix(strings.TrimSpace(call.RoomID), "call_group_")
	if !isGroupCall && call.ToUserID != 0 && call.ToUserID != params.UserID {
		result.Forbidden = true
	}
	if isGroupCall {
		var isParticipant bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM call_participants
				WHERE call_id = $1 AND user_id = $2
			)
		`, params.CallID, params.UserID).Scan(&isParticipant); err != nil {
			return nil, fmt.Errorf("check group call participant for accept: %w", err)
		}
		if !isParticipant {
			result.Forbidden = true
		}
	}

	deviceID := strings.TrimSpace(params.DeviceID)
	sessionID := strings.TrimSpace(params.SessionID)

	switch {
	case domain.IsCallRingingStatus(call.Status):
		if _, err := tx.Exec(ctx, `
			UPDATE call_history
			SET status = $1,
			    accepted_by_user_id = $2,
			    accepted_by_device_id = NULLIF($3, ''),
			    accepted_by_session_id = NULLIF($4, ''),
			    accepted_at = COALESCE(accepted_at, NOW()),
			    updated_at = NOW()
			WHERE call_id = $5
		`, domain.CallStatusAccepted, params.UserID, deviceID, sessionID, params.CallID); err != nil {
			return nil, fmt.Errorf("accept call: %w", err)
		}
		call.Status = domain.CallStatusAccepted
		if params.UserID != 0 {
			call.AcceptedByUserID = &params.UserID
		}
		call.AcceptedByDeviceID = nil
		call.AcceptedBySessionID = nil
		if deviceID != "" {
			call.AcceptedByDeviceID = &deviceID
		}
		if sessionID != "" {
			call.AcceptedBySessionID = &sessionID
		}
		now := time.Now().UTC()
		call.AcceptedAt = &now
		result.Updated = true
		result.AcceptedBySameEndpoint = true
	case domain.IsCallAcceptedStatus(call.Status):
		result.Idempotent = true
		result.AcceptedBySameEndpoint = isSameAcceptEndpoint(&call, deviceID, sessionID)
		result.AcceptedOnOtherDevice = !result.AcceptedBySameEndpoint
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit accept call tx: %w", err)
	}
	committed = true

	return result, nil
}

func (r *CallHistoryRepository) GetByRoomID(ctx context.Context, roomID string) (*domain.CallHistory, error) {
	var c domain.CallHistory

	row := r.db.QueryRow(ctx, `
		SELECT
			id, call_id, from_user_id, to_user_id, room_id,
			call_type,
			chat_id, from_name, is_video, status,
			accepted_by_user_id, accepted_by_device_id, accepted_by_session_id, accepted_at,
			created_at, updated_at, ended_at
		FROM call_history
		WHERE room_id = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, roomID)

	if err := scanCallHistory(row, &c); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get call history by room: %w", err)
	}

	return &c, nil
}

// ---------------------------------------------------------------------
// Call Participants
// ---------------------------------------------------------------------

func (r *CallHistoryRepository) AddParticipant(ctx context.Context, p *domain.CallParticipant) error {
	query := `
		INSERT INTO call_participants (call_id, user_id, status, role, joined_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (call_id, user_id) DO UPDATE
		SET status = EXCLUDED.status,
		    joined_at = EXCLUDED.joined_at,
		    left_at = NULL -- reset left_at if rejoining
	`
	// Assume call_id and user_id are set in p
	_, err := r.db.Exec(ctx, query, p.CallID, p.UserID, p.Status, p.Role)
	if err != nil {
		return fmt.Errorf("add participant: %w", err)
	}
	return nil
}

func (r *CallHistoryRepository) UpdateParticipantStatus(ctx context.Context, callID uuid.UUID, userID int64, status string) error {
	query := `
		UPDATE call_participants
		SET status = $1, left_at = CASE WHEN $1 = 'left' THEN NOW() ELSE left_at END
		WHERE call_id = $2 AND user_id = $3
	`
	cmd, err := r.db.Exec(ctx, query, status, callID, userID)
	if err != nil {
		return fmt.Errorf("update participant status: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return fmt.Errorf("participant not found for update: call_id=%s, user_id=%d", callID, userID)
	}
	return nil
}

// GetActiveCallByUserID returns the active (accepted/ongoing) call for a user, if any.
// Used to detect if a user is already in a call before initiating a new one (Busy check).
func (r *CallHistoryRepository) GetActiveCallByUserID(ctx context.Context, userID int64) (*domain.CallHistory, error) {
	var c domain.CallHistory

	err := r.db.QueryRow(ctx, `
		SELECT
			c.id, c.call_id, c.from_user_id, c.to_user_id, c.room_id,
			c.call_type,
			c.chat_id, c.from_name, c.is_video, c.status, c.created_at, c.updated_at, c.ended_at
		FROM call_history c
		WHERE c.status IN ('accepted', 'ongoing')
		  AND c.call_id IN (
			  SELECT cp.call_id 
			  FROM call_participants cp 
			  WHERE cp.user_id = $1 AND cp.status = 'joined'
		  )
		ORDER BY c.updated_at DESC
		LIMIT 1
	`, userID).Scan(
		&c.ID,
		&c.CallID,
		&c.FromUserID,
		&c.ToUserID,
		&c.RoomID,
		&c.CallType,
		&c.ChatID,
		&c.FromName,
		&c.IsVideo,
		&c.Status,
		&c.CreatedAt,
		&c.UpdatedAt,
		&c.EndedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get active call by user: %w", err)
	}

	return &c, nil
}

func (r *CallHistoryRepository) GetParticipants(ctx context.Context, callID uuid.UUID) ([]domain.CallParticipant, error) {
	query := `
		SELECT
			cp.id, cp.call_id, cp.user_id, cp.status, cp.role, cp.joined_at, cp.left_at,
			u.username, u.avatar_url, concat(u.first_name, ' ', u.last_name) as full_name
		FROM call_participants cp
		JOIN users u ON cp.user_id = u.id
		WHERE cp.call_id = $1
		ORDER BY cp.joined_at ASC
	`
	rows, err := r.db.Query(ctx, query, callID)
	if err != nil {
		return nil, fmt.Errorf("get participants: %w", err)
	}
	defer rows.Close()

	var participants []domain.CallParticipant
	for rows.Next() {
		var p domain.CallParticipant
		var avatarURL *string
		if err := rows.Scan(
			&p.ID, &p.CallID, &p.UserID, &p.Status, &p.Role, &p.JoinedAt, &p.LeftAt,
			&p.Username, &avatarURL, &p.FullName,
		); err != nil {
			return nil, fmt.Errorf("scan participant: %w", err)
		}
		if avatarURL != nil {
			p.AvatarURL = *avatarURL
		}
		participants = append(participants, p)
	}
	return participants, nil
}

func (r *CallHistoryRepository) IsUserParticipant(ctx context.Context, callID uuid.UUID, userID int64) (bool, error) {
	var exists bool
	if err := r.db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM call_participants
			WHERE call_id = $1 AND user_id = $2
		)
	`, callID, userID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check call participant: %w", err)
	}
	return exists, nil
}

// ListActiveForUser returns recent candidate calls that may still be ringing or active.
// The handler performs the final LiveKit-aware filtering before exposing them to clients.
func (r *CallHistoryRepository) ListActiveForUser(ctx context.Context, userID int64, limit int) ([]domain.CallHistory, error) {
	if limit <= 0 || limit > 50 {
		limit = 10
	}

	rows, err := r.db.Query(ctx, `
		SELECT
			id,
			call_id,
			from_user_id,
			to_user_id,
			room_id,
			call_type,
			chat_id,
			from_name,
			is_video,
			status,
			created_at,
			updated_at,
			ended_at
		FROM (
			SELECT DISTINCT ON (c.room_id)
				c.id,
				c.call_id,
				c.from_user_id,
				c.to_user_id,
				c.room_id,
				c.call_type,
				c.chat_id,
				c.from_name,
				c.is_video,
				c.status,
				c.created_at,
				c.updated_at,
				c.ended_at
			FROM call_history c
			WHERE (
				c.to_user_id = $1
				OR c.from_user_id = $1
				OR EXISTS (
					SELECT 1
					FROM call_participants cp
					WHERE cp.call_id = c.call_id AND cp.user_id = $1
				)
			)
			  AND c.created_at >= NOW() - INTERVAL '24 hours'
			  AND c.status IN ('initiated', 'accepted', 'ongoing', 'missed')
			ORDER BY
				c.room_id,
				CASE
					WHEN c.status = 'ongoing' THEN 0
					WHEN c.status = 'accepted' THEN 1
					WHEN c.status = 'initiated' THEN 2
					ELSE 3
				END,
				c.updated_at DESC
		) active_calls
		ORDER BY updated_at DESC
		LIMIT $2
	`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("list active calls: %w", err)
	}
	defer rows.Close()

	res := make([]domain.CallHistory, 0, limit)
	for rows.Next() {
		var c domain.CallHistory
		var callType sql.NullString
		var chatID sql.NullString
		var fromName sql.NullString
		if err := rows.Scan(
			&c.ID,
			&c.CallID,
			&c.FromUserID,
			&c.ToUserID,
			&c.RoomID,
			&callType,
			&chatID,
			&fromName,
			&c.IsVideo,
			&c.Status,
			&c.CreatedAt,
			&c.UpdatedAt,
			&c.EndedAt,
		); err != nil {
			return nil, fmt.Errorf("scan active call history: %w", err)
		}
		if callType.Valid {
			c.CallType = strings.TrimSpace(callType.String)
		}
		if c.CallType == "" {
			c.CallType = domain.CallTypePrivate
		}
		if chatID.Valid {
			value := strings.TrimSpace(chatID.String)
			c.ChatID = &value
		}
		if fromName.Valid {
			value := strings.TrimSpace(fromName.String)
			c.FromName = &value
		}
		res = append(res, c)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("active call history rows: %w", err)
	}

	return res, nil
}
