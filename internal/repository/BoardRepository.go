package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

type BoardRepository struct {
	db     *pgxpool.Pool
	logger *zap.Logger
}

var boardPrefixPartRe = regexp.MustCompile(`[A-Za-z0-9]+`)

var ErrLabelExists = errors.New("label already exists")

func NewBoardRepository(db *pgxpool.Pool, logger *zap.Logger) *BoardRepository {
	return &BoardRepository{db: db, logger: logger.With(zap.String("repository", "board"))}
}

func (r *BoardRepository) CreateBoard(ctx context.Context, b *domain.Board) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if b.ID == uuid.Nil {
		b.ID = uuid.New()
	}
	now := time.Now()
	b.CreatedAt = now
	b.UpdatedAt = now
	if strings.TrimSpace(b.TaskKeyPrefix) == "" {
		b.TaskKeyPrefix = buildBoardTaskKeyPrefix(b.Name)
	}

	// 1. Create Board
	_, err = tx.Exec(ctx, `
		INSERT INTO boards (id, owner_id, name, system_key, task_key_prefix, created_at, updated_at)
		VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6, $7)
	`, b.ID, b.OwnerID, b.Name, b.SystemKey, b.TaskKeyPrefix, b.CreatedAt, b.UpdatedAt)
	if err != nil {
		r.logger.Error("insert board failed", pgErrFields(err)...)
		return fmt.Errorf("insert board: %w", err)
	}

	_, err = tx.Exec(ctx, `
        INSERT INTO board_members (board_id, user_id, role, team_role)
        VALUES ($1, $2, 'owner', NULL)
    `, b.ID, b.OwnerID)
	if err != nil {
		return fmt.Errorf("add owner member: %w", err)
	}

	for _, c := range domain.DefaultColumns {
		colID := uuid.New()
		_, err := tx.Exec(ctx, `
            INSERT INTO kanban_columns (id, board_id, user_id, key, name, position, is_system, created_at, updated_at)
            VALUES ($1, $2, $3, $4, $5, $6, true, $7, $8)
        `, colID, b.ID, b.OwnerID, c.Key, c.Name, c.Position, now, now)
		if err != nil {
			return fmt.Errorf("insert default col %s: %w", c.Key, err)
		}
	}

	return tx.Commit(ctx)
}

func (r *BoardRepository) EnsureSystemBoard(ctx context.Context, ownerID int64, systemKey, name string) (*domain.Board, error) {
	var id uuid.UUID
	err := r.db.QueryRow(ctx, `SELECT id FROM boards WHERE system_key=$1 AND deleted_at IS NULL`, systemKey).Scan(&id)
	if err == nil {
		return r.GetBoard(ctx, id)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	b := &domain.Board{OwnerID: ownerID, Name: name, SystemKey: systemKey}
	if err := r.CreateBoard(ctx, b); err != nil {
		if err2 := r.db.QueryRow(ctx, `SELECT id FROM boards WHERE system_key=$1 AND deleted_at IS NULL`, systemKey).Scan(&id); err2 == nil {
			return r.GetBoard(ctx, id)
		}
		return nil, err
	}
	return b, nil
}

// GetSystemBoardByID only accepts the requested board when it is the expected
// system board. This keeps a stale integration cache from redirecting tasks to
// an ordinary user board.
func (r *BoardRepository) GetSystemBoardByID(ctx context.Context, boardID uuid.UUID, systemKey string) (*domain.Board, error) {
	var id uuid.UUID
	if err := r.db.QueryRow(ctx, `SELECT id FROM boards WHERE id=$1 AND system_key=$2 AND deleted_at IS NULL`, boardID, systemKey).Scan(&id); err != nil {
		return nil, err
	}
	return r.GetBoard(ctx, id)
}

func buildBoardTaskKeyPrefix(name string) string {
	parts := boardPrefixPartRe.FindAllString(name, -1)
	if len(parts) == 0 {
		return "AB"
	}

	if len(parts) > 1 {
		return strings.ToUpper(string([]rune(parts[0])[0]) + string([]rune(parts[len(parts)-1])[0]))
	}

	var letters []rune
	for i, r := range parts[0] {
		if i == 0 || unicode.IsUpper(r) || unicode.IsDigit(r) {
			letters = append(letters, unicode.ToUpper(r))
		}
		if len(letters) == 2 {
			return string(letters)
		}
	}

	for _, r := range parts[0] {
		if len(letters) == 2 {
			break
		}
		upper := unicode.ToUpper(r)
		if len(letters) == 0 || letters[len(letters)-1] != upper {
			letters = append(letters, upper)
		}
	}
	if len(letters) == 1 {
		letters = append(letters, 'B')
	}
	if len(letters) == 0 {
		return "AB"
	}
	return string(letters)
}

func (r *BoardRepository) EnsureDefaultBoard(ctx context.Context, userID int64) error {
	var exists bool
	err := r.db.QueryRow(ctx, `
       SELECT EXISTS(
          SELECT 1 FROM boards b 
          JOIN board_members bm ON b.id = bm.board_id 
          WHERE bm.user_id = $1 AND bm.role = 'owner' AND b.deleted_at IS NULL
       )
    `, userID).Scan(&exists)

	if err != nil {
		return fmt.Errorf("check board exists: %w", err)
	}

	if exists {
		return nil
	}

	return r.CreateBoard(ctx, &domain.Board{
		ID:      uuid.New(),
		OwnerID: userID,
		Name:    "My Board",
	})
}

func (r *BoardRepository) GetBoardByOwnerID(ctx context.Context, userID int64) (*domain.Board, error) {
	b := &domain.Board{}
	err := r.db.QueryRow(ctx, `
			  SELECT b.id, b.owner_id, b.name, COALESCE(b.task_key_prefix, 'AB'), COALESCE(b.task_sequence, 0), COALESCE(b.background_mode, 'preset_color'), COALESCE(b.background_preset_id, 'preset_default'), b.background_image_url, b.created_at, b.updated_at
			  FROM boards b
			  JOIN board_members bm ON b.id = bm.board_id
			  WHERE bm.user_id = $1 AND bm.role = 'owner' AND b.deleted_at IS NULL
			  ORDER BY b.created_at
			  LIMIT 1
		   `, userID).Scan(&b.ID, &b.OwnerID, &b.Name, &b.TaskKeyPrefix, &b.TaskSequence, &b.BgMode, &b.BgPreset, &b.BgURL, &b.CreatedAt, &b.UpdatedAt)

	if err != nil {
		return nil, err
	}
	return b, nil
}

func (r *BoardRepository) GetBoard(ctx context.Context, boardID uuid.UUID) (*domain.Board, error) {
	b := &domain.Board{}
	err := r.db.QueryRow(ctx, `
        SELECT id, owner_id, name, COALESCE(task_key_prefix, 'AB'), COALESCE(task_sequence, 0), COALESCE(background_mode, 'preset_color'), COALESCE(background_preset_id, 'preset_default'), background_image_url, created_at, updated_at,COALESCE(to_jsonb(boards)->>'system_key','')
        FROM boards WHERE id=$1 AND deleted_at IS NULL
    `, boardID).Scan(&b.ID, &b.OwnerID, &b.Name, &b.TaskKeyPrefix, &b.TaskSequence, &b.BgMode, &b.BgPreset, &b.BgURL, &b.CreatedAt, &b.UpdatedAt, &b.SystemKey)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func (r *BoardRepository) UpdateBoardBackground(ctx context.Context, boardID uuid.UUID, mode string, presetID string, imageURL *string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE boards
		SET background_mode = $2, background_preset_id = $3, background_image_url = $4, updated_at = NOW()
		WHERE id = $1
	`, boardID, mode, presetID, imageURL)
	return err
}

func (r *BoardRepository) UpdateBoard(ctx context.Context, boardID uuid.UUID, name string) error {
	cmdTag, err := r.db.Exec(ctx, `
		UPDATE boards
		SET name = $2, updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL
	`, boardID, name)
	if err != nil {
		return fmt.Errorf("update board: %w", err)
	}

	if cmdTag.RowsAffected() == 0 {
		return fmt.Errorf("board not found")
	}

	return nil
}

func (r *BoardRepository) DeleteBoard(ctx context.Context, boardID uuid.UUID, userID int64) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	now := time.Now()

	if _, err := tx.Exec(ctx, `
		UPDATE task_comments
		SET deleted_at = $2
		WHERE task_id IN (
			SELECT id FROM tasks WHERE board_id = $1 AND deleted_at IS NULL
		)
		  AND deleted_at IS NULL
	`, boardID, now); err != nil {
		return fmt.Errorf("soft delete task comments: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		WITH changed AS (UPDATE tasks SET deleted_at=$2,updated_at=$2 WHERE board_id=$1 AND deleted_at IS NULL RETURNING id)
 INSERT INTO task_history(id,task_id,user_id,action,field_name,new_value,created_at)
 SELECT gen_random_uuid(),id,$3,'deleted','board_id',$1::text,NOW() FROM changed
	`, boardID, now, userID); err != nil {
		return fmt.Errorf("soft delete tasks: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE sprints
		SET deleted_at = $2, updated_at = $2
		WHERE board_id = $1 AND deleted_at IS NULL
	`, boardID, now); err != nil {
		return fmt.Errorf("soft delete sprints: %w", err)
	}

	cmdTag, err := tx.Exec(ctx, `
		UPDATE boards
		SET deleted_at = $2, updated_at = $2
		WHERE id = $1 AND deleted_at IS NULL
	`, boardID, now)
	if err != nil {
		return fmt.Errorf("soft delete board: %w", err)
	}

	if cmdTag.RowsAffected() == 0 {
		return fmt.Errorf("board not found")
	}

	return tx.Commit(ctx)
}

func (r *BoardRepository) ListBoards(ctx context.Context, userID int64) ([]*domain.Board, error) {
	rows, err := r.db.Query(ctx, `
        SELECT
            b.id,
            b.owner_id,
            COALESCE(u.first_name, '') AS owner_first_name,
            COALESCE(u.last_name, '') AS owner_last_name,
            COALESCE(NULLIF(u.full_name_local, ''), NULLIF(TRIM(CONCAT_WS(' ', u.last_name, u.first_name)), ''), u.username, '') AS owner_full_name,
            b.name,
            COALESCE(b.task_key_prefix, 'AB'),
            b.manual_completion_percent,
            COALESCE(b.task_sequence, 0),
            COALESCE(b.background_mode, 'preset_color'),
            COALESCE(b.background_preset_id, 'preset_default'),
            b.background_image_url,
            b.created_at,
            b.updated_at,
            COUNT(t.id)::int AS total_tasks,
            COUNT(t.id) FILTER (
                WHERE UPPER(COALESCE(t.status, '')) IN ('DONE', 'COMPLETED')
                   OR UPPER(COALESCE(t.status, '')) = UPPER($2)
                   OR UPPER(COALESCE(c.key, '')) = 'DONE'
            )::int AS completed_tasks,
            MIN(t.due_at) FILTER (
                WHERE t.due_at IS NOT NULL
                  AND t.due_at >= CURRENT_DATE
                  AND NOT (
                    UPPER(COALESCE(t.status, '')) IN ('DONE', 'COMPLETED')
                    OR UPPER(COALESCE(t.status, '')) = UPPER($2)
                    OR UPPER(COALESCE(c.key, '')) = 'DONE'
                  )
            ) AS next_due_at
        FROM boards b
        LEFT JOIN kanban_columns c
            ON c.board_id = b.id
        LEFT JOIN tasks t
            ON t.board_id = b.id
           AND t.column_id = c.id
           AND t.deleted_at IS NULL
           AND t.parent_id IS NULL
        LEFT JOIN users u
            ON u.id = b.owner_id
        WHERE b.deleted_at IS NULL
          -- Documentolog assignments are shown in the dedicated "Поручения"
          -- section, not as a shared Kanban board.
          AND COALESCE(b.system_key, '') <> 'documentolog-assignments'
          AND COALESCE(b.system_key, '') NOT LIKE 'documentolog-assignments:%'
          AND (
                b.owner_id = $1
                OR EXISTS (SELECT 1 FROM board_members bm WHERE bm.board_id = b.id AND bm.user_id = $1)
                OR `+kanbanExplicitBoardAccessExists(`b.id`, `$1`)+`
                OR `+kanbanLeadershipBoardAccessExists(`b.id`, `$1`)+`
          )
        GROUP BY b.id, b.owner_id, u.first_name, u.last_name, u.full_name_local, u.username, b.name, b.task_key_prefix, b.task_sequence, b.background_mode, b.background_preset_id, b.background_image_url, b.created_at, b.updated_at
        ORDER BY b.created_at DESC
    `, userID, domain.StatusDone)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var boards []*domain.Board
	for rows.Next() {
		b := &domain.Board{}
		var manualCompletionPercent sql.NullInt32
		if err := rows.Scan(
			&b.ID,
			&b.OwnerID,
			&b.OwnerFirstName,
			&b.OwnerLastName,
			&b.OwnerFullName,
			&b.Name,
			&b.TaskKeyPrefix,
			&manualCompletionPercent,
			&b.TaskSequence,
			&b.BgMode,
			&b.BgPreset,
			&b.BgURL,
			&b.CreatedAt,
			&b.UpdatedAt,
			&b.Progress.TotalTasks,
			&b.Progress.CompletedTasks,
			&b.NextDueAt,
		); err != nil {
			return nil, err
		}
		if manualCompletionPercent.Valid {
			value := int(manualCompletionPercent.Int32)
			b.ManualCompletionPercent = &value
		}
		boards = append(boards, b)
	}
	return boards, nil
}

func (r *BoardRepository) AddMembers(ctx context.Context, members []*domain.BoardMember) error {
	if len(members) == 0 {
		return nil
	}

	var queryBuilder strings.Builder
	queryBuilder.WriteString("INSERT INTO board_members (board_id, user_id, role, team_role, joined_at) VALUES ")

	vals := make([]interface{}, 0, len(members)*4)
	paramIdx := 1

	for i, m := range members {
		if i > 0 {
			queryBuilder.WriteString(",")
		}

		queryBuilder.WriteString(fmt.Sprintf("($%d, $%d, $%d, $%d, NOW())", paramIdx, paramIdx+1, paramIdx+2, paramIdx+3))

		vals = append(vals, m.BoardID, m.UserID, m.Role, m.TeamRole)
		paramIdx += 4
	}

	queryBuilder.WriteString(" ON CONFLICT (board_id, user_id) DO NOTHING")

	_, err := r.db.Exec(ctx, queryBuilder.String(), vals...)
	return err
}

func (r *BoardRepository) RemoveMember(ctx context.Context, boardID uuid.UUID, userID int64) error {
	_, err := r.db.Exec(ctx, `
        DELETE FROM board_members WHERE board_id=$1 AND user_id=$2
    `, boardID, userID)
	return err
}

func (r *BoardRepository) UpdateMemberRole(ctx context.Context, boardID uuid.UUID, userID int64, newRole string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE board_members SET role = $3 WHERE board_id = $1 AND user_id = $2
	`, boardID, userID, newRole)
	return err
}

func (r *BoardRepository) UpdateMemberTeamRole(ctx context.Context, boardID uuid.UUID, userID int64, teamRole *string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE board_members SET team_role = $3 WHERE board_id = $1 AND user_id = $2
	`, boardID, userID, teamRole)
	return err
}

func (r *BoardRepository) GetMembers(ctx context.Context, boardID uuid.UUID) ([]*domain.BoardMember, error) {
	rows, err := r.db.Query(ctx, `
        SELECT
            bm.board_id,
            bm.user_id,
            TRIM(COALESCE(u.first_name, '') || ' ' || COALESCE(u.last_name, '')) AS full_name,
            u.avatar_url AS avatar,
            bm.role,
            bm.team_role,
            bm.joined_at
        FROM board_members bm
        JOIN users u ON u.id = bm.user_id
        WHERE bm.board_id = $1
        ORDER BY bm.joined_at ASC
    `, boardID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var members []*domain.BoardMember
	for rows.Next() {
		m := &domain.BoardMember{}
		if err := rows.Scan(&m.BoardID, &m.UserID, &m.FullName, &m.Avatar, &m.Role, &m.TeamRole, &m.JoinedAt); err != nil {
			return nil, err
		}
		members = append(members, m)
	}
	return members, nil
}

func (r *BoardRepository) GetBoardMemberRole(ctx context.Context, boardID uuid.UUID, userID int64) (string, error) {
	var role string
	err := r.db.QueryRow(ctx, `
		SELECT role FROM board_members WHERE board_id = $1 AND user_id = $2
	`, boardID, userID).Scan(&role)
	if err != nil {
		return "", err
	}
	return role, nil
}

func (r *BoardRepository) IsMember(ctx context.Context, boardID uuid.UUID, userID int64) (bool, error) {
	var exists bool
	err := r.db.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM boards b
			WHERE b.id = $1
			  AND b.deleted_at IS NULL
			  AND COALESCE(to_jsonb(b)->>'system_key','')<>'documentolog-assignments'
			  AND COALESCE(to_jsonb(b)->>'system_key','') NOT LIKE 'documentolog-assignments:%'
			  AND (
					EXISTS (SELECT 1 FROM board_members bm WHERE bm.board_id = b.id AND bm.user_id = $2)
					OR `+kanbanExplicitBoardAccessExists(`b.id`, `$2`)+`
					OR `+kanbanLeadershipBoardAccessExists(`b.id`, `$2`)+`
			  )
		)
	`, boardID, userID).Scan(&exists)
	return exists, err
}

func (r *BoardRepository) HasKanbanAccess(ctx context.Context, boardID uuid.UUID, userID int64) (bool, error) {
	var exists bool
	err := r.db.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM boards b
			WHERE b.id = $1
			  AND b.deleted_at IS NULL
			  AND COALESCE(to_jsonb(b)->>'system_key','')<>'documentolog-assignments'
			  AND COALESCE(to_jsonb(b)->>'system_key','') NOT LIKE 'documentolog-assignments:%'
			  AND (
					b.owner_id = $2
					OR EXISTS (SELECT 1 FROM board_members bm WHERE bm.board_id = b.id AND bm.user_id = $2)
					OR `+kanbanExplicitBoardAccessExists(`b.id`, `$2`)+`
					OR `+kanbanLeadershipBoardAccessExists(`b.id`, `$2`)+`
			  )
		)
	`, boardID, userID).Scan(&exists)
	return exists, err
}

func (r *BoardRepository) CreateSprint(ctx context.Context, s *domain.Sprint) error {
	if s.ID == uuid.Nil {
		s.ID = uuid.New()
	}
	now := time.Now()
	s.CreatedAt = now
	s.UpdatedAt = now
	_, err := r.db.Exec(ctx, `
        INSERT INTO sprints (id, board_id, goal_id, project_id, name, status, start_date, end_date, created_at, updated_at)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
    `, s.ID, s.BoardID, s.GoalID, s.ProjectID, s.Name, s.Status, s.StartDate, s.EndDate, s.CreatedAt, s.UpdatedAt)
	return err
}

func (r *BoardRepository) UpdateSprint(ctx context.Context, boardID, sprintID uuid.UUID, goalID, projectID *uuid.UUID, name, status string, startDate, endDate *time.Time) error {
	cmdTag, err := r.db.Exec(ctx, `
        UPDATE sprints
        SET goal_id = $3, project_id = $4, name = $5, status = $6, start_date = $7, end_date = $8, updated_at = NOW()
        WHERE id = $1 AND board_id = $2 AND deleted_at IS NULL
    `, sprintID, boardID, goalID, projectID, name, status, startDate, endDate)
	if err != nil {
		return err
	}
	if cmdTag.RowsAffected() == 0 {
		return fmt.Errorf("sprint not found")
	}
	return nil
}

func (r *BoardRepository) CreateGoal(ctx context.Context, g *domain.Goal) error {
	if g.ID == uuid.Nil {
		g.ID = uuid.New()
	}
	now := time.Now()
	g.CreatedAt = now
	g.UpdatedAt = now
	_, err := r.db.Exec(ctx, `
        INSERT INTO goals (id, board_id, name, description, created_by, created_at, updated_at)
        VALUES ($1, $2, $3, $4, $5, $6, $7)
    `, g.ID, g.BoardID, g.Name, g.Description, g.CreatedBy, g.CreatedAt, g.UpdatedAt)
	return err
}

func (r *BoardRepository) UpdateGoal(ctx context.Context, boardID, goalID uuid.UUID, name string, description *string) error {
	cmdTag, err := r.db.Exec(ctx, `
        UPDATE goals
        SET name = $3, description = $4, updated_at = NOW()
        WHERE id = $1 AND board_id = $2 AND deleted_at IS NULL
    `, goalID, boardID, name, description)
	if err != nil {
		return err
	}
	if cmdTag.RowsAffected() == 0 {
		return fmt.Errorf("goal not found")
	}
	return nil
}

func (r *BoardRepository) DeleteGoal(ctx context.Context, boardID, goalID uuid.UUID, userID int64) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	now := time.Now()
	if _, err := tx.Exec(ctx, `
        WITH changed AS (UPDATE tasks SET goal_id=NULL,updated_at=$2 WHERE board_id=$1 AND goal_id=$3 AND deleted_at IS NULL RETURNING id)
 INSERT INTO task_history(id,task_id,user_id,action,field_name,old_value,created_at)
 SELECT gen_random_uuid(),id,$4,'updated','goal_id',$3::text,NOW() FROM changed
    `, boardID, now, goalID, userID); err != nil {
		return fmt.Errorf("clear goal from tasks: %w", err)
	}
	if _, err := tx.Exec(ctx, `
        UPDATE projects
        SET goal_id = NULL, updated_at = $2
        WHERE board_id = $1 AND goal_id = $3 AND deleted_at IS NULL
    `, boardID, now, goalID); err != nil {
		return fmt.Errorf("clear goal from projects: %w", err)
	}
	if _, err := tx.Exec(ctx, `
        UPDATE sprints
        SET goal_id = NULL, updated_at = $2
        WHERE board_id = $1 AND goal_id = $3 AND deleted_at IS NULL
    `, boardID, now, goalID); err != nil {
		return fmt.Errorf("clear goal from sprints: %w", err)
	}

	cmdTag, err := tx.Exec(ctx, `
        UPDATE goals
        SET deleted_at = $3, updated_at = $3
        WHERE id = $1 AND board_id = $2 AND deleted_at IS NULL
    `, goalID, boardID, now)
	if err != nil {
		return fmt.Errorf("soft delete goal: %w", err)
	}
	if cmdTag.RowsAffected() == 0 {
		return fmt.Errorf("goal not found")
	}
	return tx.Commit(ctx)
}

func (r *BoardRepository) ListGoals(ctx context.Context, boardID uuid.UUID) ([]*domain.Goal, error) {
	rows, err := r.db.Query(ctx, `
        SELECT id, board_id, name, description, created_by, created_at, updated_at
        FROM goals
        WHERE board_id = $1 AND deleted_at IS NULL
        ORDER BY created_at DESC
    `, boardID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var goals []*domain.Goal
	for rows.Next() {
		g := &domain.Goal{}
		if err := rows.Scan(&g.ID, &g.BoardID, &g.Name, &g.Description, &g.CreatedBy, &g.CreatedAt, &g.UpdatedAt); err != nil {
			return nil, err
		}
		goals = append(goals, g)
	}
	return goals, nil
}

func (r *BoardRepository) GetGoal(ctx context.Context, goalID uuid.UUID) (*domain.Goal, error) {
	g := &domain.Goal{}
	err := r.db.QueryRow(ctx, `
        SELECT id, board_id, name, description, created_by, created_at, updated_at
        FROM goals
        WHERE id = $1 AND deleted_at IS NULL
    `, goalID).Scan(&g.ID, &g.BoardID, &g.Name, &g.Description, &g.CreatedBy, &g.CreatedAt, &g.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return g, nil
}

func (r *BoardRepository) ListGoalsForUser(ctx context.Context, userID int64) ([]*domain.GoalBoardItem, error) {
	rows, err := r.db.Query(ctx, `
        SELECT
            g.id,
            g.board_id,
            b.name AS board_name,
            g.name,
            g.description,
            g.created_by,
            g.created_at,
            g.updated_at
        FROM goals g
        JOIN boards b ON b.id = g.board_id
        WHERE g.deleted_at IS NULL
          AND b.deleted_at IS NULL
          AND (
                b.owner_id = $1
                OR EXISTS (SELECT 1 FROM board_members bm WHERE bm.board_id = b.id AND bm.user_id = $1)
                OR `+kanbanExplicitBoardAccessExists(`b.id`, `$1`)+`
                OR `+kanbanLeadershipBoardAccessExists(`b.id`, `$1`)+`
          )
        ORDER BY g.created_at DESC, g.id DESC
    `, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []*domain.GoalBoardItem
	for rows.Next() {
		item := &domain.GoalBoardItem{}
		if err := rows.Scan(
			&item.ID,
			&item.BoardID,
			&item.BoardName,
			&item.Name,
			&item.Description,
			&item.CreatedBy,
			&item.CreatedAt,
			&item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (r *BoardRepository) CreateProject(ctx context.Context, p *domain.Project) error {
	if p.ID == uuid.Nil {
		p.ID = uuid.New()
	}
	now := time.Now()
	p.CreatedAt = now
	p.UpdatedAt = now
	p.OwnerID = p.CreatedBy
	_, err := r.db.Exec(ctx, `
        INSERT INTO projects (id, board_id, goal_id, name, description, status, created_by, owner_id, created_at, updated_at)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $7, $8, $9)
    `, p.ID, p.BoardID, p.GoalID, p.Name, p.Description, p.Status, p.CreatedBy, p.CreatedAt, p.UpdatedAt)
	return err
}

func (r *BoardRepository) UpdateProject(ctx context.Context, boardID, projectID uuid.UUID, goalID *uuid.UUID, name string, description *string, status string) error {
	cmdTag, err := r.db.Exec(ctx, `
        UPDATE projects
        SET goal_id = $3, name = $4, description = $5, status = $6, updated_at = NOW()
        WHERE id = $1 AND board_id = $2 AND deleted_at IS NULL
    `, projectID, boardID, goalID, name, description, status)
	if err != nil {
		return err
	}
	if cmdTag.RowsAffected() == 0 {
		return fmt.Errorf("project not found")
	}
	return nil
}

func (r *BoardRepository) DeleteProject(ctx context.Context, boardID, projectID uuid.UUID, userID int64) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	now := time.Now()
	if _, err := tx.Exec(ctx, `
        WITH changed AS (UPDATE tasks SET project_id=NULL,updated_at=$2 WHERE board_id=$1 AND project_id=$3 AND deleted_at IS NULL RETURNING id)
 INSERT INTO task_history(id,task_id,user_id,action,field_name,old_value,created_at)
 SELECT gen_random_uuid(),id,$4,'updated','project_id',$3::text,NOW() FROM changed
    `, boardID, now, projectID, userID); err != nil {
		return fmt.Errorf("clear project from tasks: %w", err)
	}
	if _, err := tx.Exec(ctx, `
        UPDATE sprints
        SET project_id = NULL, updated_at = $2
        WHERE board_id = $1 AND project_id = $3 AND deleted_at IS NULL
    `, boardID, now, projectID); err != nil {
		return fmt.Errorf("clear project from sprints: %w", err)
	}

	cmdTag, err := tx.Exec(ctx, `
        UPDATE projects
        SET deleted_at = $3, updated_at = $3
        WHERE id = $1 AND board_id = $2 AND deleted_at IS NULL
    `, projectID, boardID, now)
	if err != nil {
		return fmt.Errorf("soft delete project: %w", err)
	}
	if cmdTag.RowsAffected() == 0 {
		return fmt.Errorf("project not found")
	}
	return tx.Commit(ctx)
}

func (r *BoardRepository) ListProjects(ctx context.Context, boardID uuid.UUID) ([]*domain.Project, error) {
	rows, err := r.db.Query(ctx, `
        SELECT id, board_id, goal_id, name, description, status, created_by, owner_id, created_at, updated_at
        FROM projects
        WHERE board_id = $1 AND deleted_at IS NULL
        ORDER BY created_at DESC
    `, boardID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var projects []*domain.Project
	for rows.Next() {
		p := &domain.Project{}
		if err := rows.Scan(&p.ID, &p.BoardID, &p.GoalID, &p.Name, &p.Description, &p.Status, &p.CreatedBy, &p.OwnerID, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		projects = append(projects, p)
	}
	return projects, nil
}

func (r *BoardRepository) GetProject(ctx context.Context, projectID uuid.UUID) (*domain.Project, error) {
	p := &domain.Project{}
	err := r.db.QueryRow(ctx, `
        SELECT id, board_id, goal_id, name, description, status, created_by, owner_id, created_at, updated_at
        FROM projects
        WHERE id = $1 AND deleted_at IS NULL
    `, projectID).Scan(&p.ID, &p.BoardID, &p.GoalID, &p.Name, &p.Description, &p.Status, &p.CreatedBy, &p.OwnerID, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return p, nil
}

func (r *BoardRepository) DeleteSprint(ctx context.Context, boardID, sprintID uuid.UUID, userID int64) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	now := time.Now()

	if _, err := tx.Exec(ctx, `
		WITH changed AS (UPDATE tasks SET sprint_id=NULL,updated_at=$2 WHERE sprint_id=$1 AND deleted_at IS NULL RETURNING id)
 INSERT INTO task_history(id,task_id,user_id,action,field_name,old_value,created_at)
 SELECT gen_random_uuid(),id,$3,'updated','sprint_id',$1::text,NOW() FROM changed
	`, sprintID, now, userID); err != nil {
		return fmt.Errorf("clear sprint from tasks: %w", err)
	}

	cmdTag, err := tx.Exec(ctx, `
		UPDATE sprints
		SET deleted_at = $3, updated_at = $3
		WHERE id = $1 AND board_id = $2 AND deleted_at IS NULL
	`, sprintID, boardID, now)
	if err != nil {
		return fmt.Errorf("soft delete sprint: %w", err)
	}

	if cmdTag.RowsAffected() == 0 {
		return fmt.Errorf("sprint not found")
	}

	return tx.Commit(ctx)
}

func (r *BoardRepository) ListSprints(ctx context.Context, boardID uuid.UUID) ([]*domain.Sprint, error) {
	rows, err := r.db.Query(ctx, `
        SELECT id, board_id, goal_id, project_id, name, status, start_date, end_date, created_at, updated_at
        FROM sprints WHERE board_id=$1 AND deleted_at IS NULL
        ORDER BY created_at DESC
    `, boardID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sprints []*domain.Sprint
	for rows.Next() {
		s := &domain.Sprint{}
		if err := rows.Scan(&s.ID, &s.BoardID, &s.GoalID, &s.ProjectID, &s.Name, &s.Status, &s.StartDate, &s.EndDate, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		sprints = append(sprints, s)
	}
	return sprints, nil
}

func (r *BoardRepository) GetSprint(ctx context.Context, sprintID uuid.UUID) (*domain.Sprint, error) {
	s := &domain.Sprint{}
	err := r.db.QueryRow(ctx, `
        SELECT id, board_id, goal_id, project_id, name, status, start_date, end_date, created_at, updated_at
        FROM sprints
        WHERE id = $1 AND deleted_at IS NULL
    `, sprintID).Scan(&s.ID, &s.BoardID, &s.GoalID, &s.ProjectID, &s.Name, &s.Status, &s.StartDate, &s.EndDate, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return s, nil
}

func (r *BoardRepository) ListUserSprints(ctx context.Context, userID int64) ([]*domain.Sprint, error) {
	rows, err := r.db.Query(ctx, `
		SELECT s.id, s.board_id, s.goal_id, s.project_id, s.name, s.status, s.start_date, s.end_date, s.created_at, s.updated_at
		FROM sprints s
		JOIN boards b ON s.board_id = b.id
		WHERE s.deleted_at IS NULL AND b.deleted_at IS NULL
		  AND (
				b.owner_id = $1
				OR EXISTS (SELECT 1 FROM board_members bm WHERE bm.board_id = b.id AND bm.user_id = $1)
				OR `+kanbanExplicitBoardAccessExists(`b.id`, `$1`)+`
				OR `+kanbanLeadershipBoardAccessExists(`b.id`, `$1`)+`
		  )
		ORDER BY s.created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sprints []*domain.Sprint
	for rows.Next() {
		s := &domain.Sprint{}
		if err := rows.Scan(&s.ID, &s.BoardID, &s.GoalID, &s.ProjectID, &s.Name, &s.Status, &s.StartDate, &s.EndDate, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		sprints = append(sprints, s)
	}
	return sprints, nil
}

func (r *BoardRepository) ListColumns(ctx context.Context, boardID uuid.UUID) ([]*domain.KanbanColumn, error) {
	rows, err := r.db.Query(ctx, `
			  SELECT id, board_id, key, name, position, is_system, created_at, updated_at
			  FROM kanban_columns WHERE board_id=$1 ORDER BY position
		   `, boardID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cols []*domain.KanbanColumn
	for rows.Next() {
		c := &domain.KanbanColumn{}
		if err := rows.Scan(&c.ID, &c.BoardID, &c.Key, &c.Name, &c.Position, &c.IsSystem, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		cols = append(cols, c)
	}
	return cols, nil
}

func (r *BoardRepository) GetColumn(ctx context.Context, columnID uuid.UUID) (*domain.KanbanColumn, error) {
	c := &domain.KanbanColumn{}
	err := r.db.QueryRow(ctx, `
        SELECT id, board_id, user_id, key, name, position, is_system, created_at, updated_at 
        FROM kanban_columns 
        WHERE id=$1
    `, columnID).Scan(
		&c.ID, &c.BoardID, &c.UserID, &c.Key, &c.Name, &c.Position, &c.IsSystem, &c.CreatedAt, &c.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return c, nil
}

func (r *BoardRepository) CreateColumn(ctx context.Context, c *domain.KanbanColumn) error {
	if c.ID == uuid.Nil {
		c.ID = uuid.New()
	}
	now := time.Now()
	c.CreatedAt = now
	c.UpdatedAt = now

	// FIX: Added 'user_id' to the INSERT statement
	err := r.db.QueryRow(ctx, `
       INSERT INTO kanban_columns(id, board_id, user_id, key, name, position, is_system, created_at, updated_at)
       VALUES ($1, $2, $3, $4, $5, (SELECT COALESCE(MAX(position), 0) + 10 FROM kanban_columns WHERE board_id=$2), $6, $7, $8)
       RETURNING position
    `, c.ID, c.BoardID, c.UserID, c.Key, c.Name, c.IsSystem, c.CreatedAt, c.UpdatedAt).Scan(&c.Position)
	return err
}

func (r *BoardRepository) DeleteColumn(ctx context.Context, columnID uuid.UUID) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Permanently delete soft-deleted tasks in this column first, to avoid foreign key violations.
	_, err = tx.Exec(ctx, `DELETE FROM tasks WHERE column_id = $1 AND deleted_at IS NOT NULL`, columnID)
	if err != nil {
		return fmt.Errorf("delete soft-deleted tasks: %w", err)
	}

	cmdTag, err := tx.Exec(ctx, `DELETE FROM kanban_columns WHERE id=$1`, columnID)
	if err != nil {
		return err
	}

	if cmdTag.RowsAffected() == 0 {
		return fmt.Errorf("column not found")
	}

	return tx.Commit(ctx)
}

func (r *BoardRepository) UpdateColumn(ctx context.Context, columnID uuid.UUID, name, key, status string, userID int64) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var oldKey string
	if err = tx.QueryRow(ctx, `SELECT key FROM kanban_columns WHERE id=$1 FOR UPDATE`, columnID).Scan(&oldKey); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE kanban_columns SET name=$2,key=$3,updated_at=NOW() WHERE id=$1`, columnID, name, key); err != nil {
		return err
	}
	if oldKey != key {
		_, err = tx.Exec(ctx, `WITH old AS (SELECT id,status FROM tasks WHERE column_id=$1 AND deleted_at IS NULL AND status IS DISTINCT FROM $2 FOR UPDATE),
  changed AS (UPDATE tasks t SET status=$2,updated_at=NOW() FROM old WHERE t.id=old.id RETURNING t.id,old.status AS old_status,t.status AS new_status)
  INSERT INTO task_history(id,task_id,user_id,action,field_name,old_value,new_value,metadata,created_at)
  SELECT gen_random_uuid(),id,$3,'updated','status',old_status,new_status,jsonb_build_object('reason','column_key_changed','column_id',$1::text),NOW() FROM changed`, columnID, status, userID)
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (r *BoardRepository) MoveColumn(ctx context.Context, boardID, columnID uuid.UUID, newPosition int) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// 1. Fetch all columns to determine current order
	// We sort by position ASC, then created_at ASC to ensure deterministic stable order for ties or mess.
	rows, err := tx.Query(ctx, `
		SELECT id, position 
		FROM kanban_columns 
		WHERE board_id = $1 
		ORDER BY position ASC, created_at ASC
	`, boardID)
	if err != nil {
		return fmt.Errorf("list columns: %w", err)
	}

	type colInfo struct {
		ID  uuid.UUID
		Pos int
	}
	var cols []colInfo

	for rows.Next() {
		var c colInfo
		if err := rows.Scan(&c.ID, &c.Pos); err != nil {
			rows.Close()
			return fmt.Errorf("scan columns: %w", err)
		}
		cols = append(cols, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows err: %w", err)
	}

	// 2. Find and extract the column to be moved
	var movedCol colInfo
	var found bool
	var currentIdx int

	for i, c := range cols {
		if c.ID == columnID {
			movedCol = c
			currentIdx = i
			found = true
			break
		}
	}

	if !found {
		// Column not in this board or does not exist
		return fmt.Errorf("column not found")
	}

	// Remove the column from its current slice position
	// Use explicit slicing to avoid issues
	newCols := make([]colInfo, 0, len(cols))
	newCols = append(newCols, cols[:currentIdx]...)
	newCols = append(newCols, cols[currentIdx+1:]...)
	cols = newCols

	// 3. Insert at newPosition (treating newPosition as an index 0..N-1)
	// Clamp to bounds
	if newPosition < 0 {
		newPosition = 0
	}
	if newPosition > len(cols) {
		newPosition = len(cols)
	}

	// Insert: append prefix, then item, then suffix
	finalCols := make([]colInfo, 0, len(cols)+1)
	finalCols = append(finalCols, cols[:newPosition]...)
	finalCols = append(finalCols, movedCol)
	finalCols = append(finalCols, cols[newPosition:]...)

	// 4. Batch Update positions in DB
	// We normalize positions to 0, 1, 2... to ensure no gaps and correct order.
	for i, c := range finalCols {
		// Only update if the position is different to minimize DB writes
		if c.Pos != i {
			_, err := tx.Exec(ctx, `
				UPDATE kanban_columns 
				SET position = $1, updated_at = NOW() 
				WHERE id = $2
			`, i, c.ID)
			if err != nil {
				return fmt.Errorf("update pos %d: %w", i, err)
			}
		}
	}

	return tx.Commit(ctx)
}

func pgErrFields(err error) []zap.Field {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return []zap.Field{
			zap.String("pg_code", pgErr.Code),
			zap.String("pg_message", pgErr.Message),
			zap.String("pg_detail", pgErr.Detail),
			zap.String("pg_constraint", pgErr.ConstraintName),
			zap.String("pg_table", pgErr.TableName),
			zap.String("pg_column", pgErr.ColumnName),
		}
	}
	return []zap.Field{zap.Error(err)}
}

// Labels

func (r *BoardRepository) CreateLabel(ctx context.Context, l *domain.KanbanLabel) error {
	if l.ID == uuid.Nil {
		l.ID = uuid.New()
	}
	l.CreatedAt = time.Now()

	_, err := r.db.Exec(ctx, `
		INSERT INTO kanban_labels (id, board_id, name, color, created_at)
		VALUES ($1, $2, $3, $4, $5)
	`, l.ID, l.BoardID, l.Name, l.Color, l.CreatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrLabelExists
		}
		return err
	}
	return nil
}

func (r *BoardRepository) ListLabels(ctx context.Context, boardID uuid.UUID) ([]*domain.KanbanLabel, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, board_id, name, color, created_at
		FROM kanban_labels
		WHERE board_id = $1
		ORDER BY name
	`, boardID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var labels []*domain.KanbanLabel
	for rows.Next() {
		l := &domain.KanbanLabel{}
		if err := rows.Scan(&l.ID, &l.BoardID, &l.Name, &l.Color, &l.CreatedAt); err != nil {
			return nil, err
		}
		labels = append(labels, l)
	}
	return labels, nil
}

func (r *BoardRepository) DeleteLabel(ctx context.Context, labelID uuid.UUID, userID int64) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `WITH removed AS (DELETE FROM kanban_task_labels WHERE label_id=$1 RETURNING task_id,to_jsonb(kanban_task_labels)::text AS snapshot)
 INSERT INTO task_history(id,task_id,user_id,action,field_name,old_value,created_at)
 SELECT gen_random_uuid(),task_id,$2,'label_removed','label',snapshot,NOW() FROM removed`, labelID, userID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM kanban_labels WHERE id=$1`, labelID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *BoardRepository) GetLabel(ctx context.Context, labelID uuid.UUID) (*domain.KanbanLabel, error) {
	l := &domain.KanbanLabel{}
	err := r.db.QueryRow(ctx, `
		SELECT id, board_id, name, color, created_at
		FROM kanban_labels
		WHERE id = $1
	`, labelID).Scan(&l.ID, &l.BoardID, &l.Name, &l.Color, &l.CreatedAt)
	if err != nil {
		return nil, err
	}
	return l, nil
}
