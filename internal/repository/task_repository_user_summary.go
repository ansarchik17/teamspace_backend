package repository

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func (r *TaskRepository) GetUserTaskBoardsSummary(ctx context.Context, userID int64) ([]domain.UserTaskBoardsSummaryItem, error) {
	return r.GetUserTaskBoardsSummaryFiltered(ctx, userID, nil, nil)
}

func userTaskSummaryReferenceTime(dateFrom, dateTo *time.Time) time.Time {
	referenceTime := time.Now()
	if dateFrom != nil && dateFrom.After(referenceTime) {
		referenceTime = *dateFrom
	}
	if dateFrom == nil && dateTo != nil && dateTo.After(referenceTime) {
		referenceTime = *dateTo
	}
	return referenceTime
}

func (r *TaskRepository) GetUserTaskBoardsSummaryFiltered(
	ctx context.Context,
	userID int64,
	dateFrom *time.Time,
	dateTo *time.Time,
) ([]domain.UserTaskBoardsSummaryItem, error) {
	query := `
		SELECT
			b.id,
			b.name,
			c.id,
			COALESCE(c.key, ''),
			COALESCE(c.name, ''),
			COALESCE(c.position, 0),
			COUNT(DISTINCT t.id)::int AS task_count,
			COUNT(DISTINCT t.id) FILTER (
				WHERE t.due_at IS NOT NULL
				  AND t.due_at < NOW()
				  AND COALESCE(c.key, '') <> 'DONE'
			)::int AS overdue_count
		FROM boards b
		JOIN tasks t
			ON t.board_id = b.id
		   AND t.deleted_at IS NULL
		JOIN task_assignees ta
			ON ta.task_id = t.id
		   AND ta.user_id = $1
		LEFT JOIN kanban_columns c
			ON c.id = t.column_id
		WHERE b.deleted_at IS NULL
		  AND ` + kanbanBoardVisibilityCondition("b", "$1") + `
	`

	args := []any{userID}
	conditions := make([]string, 0, 4)
	nextArg := 2

	if dateFrom != nil || dateTo != nil {
		referenceTime := userTaskSummaryReferenceTime(dateFrom, dateTo)
		conditions = append(conditions,
			"UPPER(COALESCE(c.key, '')) <> 'DONE'",
			"UPPER(COALESCE(t.status, '')) NOT IN ('ГОТОВО', 'DONE', 'COMPLETED', 'CLOSED', 'ARCHIVED')",
			fmt.Sprintf("(t.due_at IS NULL OR t.due_at >= $%d)", nextArg),
		)
		args = append(args, referenceTime)
	}
	if len(conditions) > 0 {
		query += " AND " + strings.Join(conditions, " AND ")
	}
	query += `
		GROUP BY b.id, b.name, c.id, c.key, c.name, c.position
		ORDER BY b.name ASC, c.position ASC, c.name ASC
	`

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("get user task boards summary: %w", err)
	}
	defer rows.Close()

	boardMap := make(map[uuid.UUID]*domain.UserTaskBoardsSummaryItem)
	columnOrder := make(map[uuid.UUID]map[uuid.UUID]int)
	boardOrder := make([]uuid.UUID, 0)

	for rows.Next() {
		var (
			boardID      uuid.UUID
			boardName    string
			columnID     uuid.UUID
			columnKey    string
			columnName   string
			columnPos    int
			taskCount    int
			overdueCount int
		)

		if err := rows.Scan(
			&boardID,
			&boardName,
			&columnID,
			&columnKey,
			&columnName,
			&columnPos,
			&taskCount,
			&overdueCount,
		); err != nil {
			return nil, fmt.Errorf("scan user task boards summary: %w", err)
		}

		boardItem, ok := boardMap[boardID]
		if !ok {
			boardItem = &domain.UserTaskBoardsSummaryItem{
				BoardID:   boardID,
				BoardName: boardName,
			}
			boardMap[boardID] = boardItem
			boardOrder = append(boardOrder, boardID)
			columnOrder[boardID] = make(map[uuid.UUID]int)
		}

		boardItem.TaskCount += taskCount
		boardItem.OverdueCount += overdueCount

		switch columnKey {
		case domain.ColTODO:
			boardItem.TodoCount += taskCount
		case domain.ColInProgress:
			boardItem.InProgressCount += taskCount
		case domain.ColTest:
			boardItem.ReviewCount += taskCount
		case domain.ColDone:
			boardItem.DoneCount += taskCount
		case domain.ColBlock:
			boardItem.BlockedCount += taskCount
		}

		boardItem.Columns = append(boardItem.Columns, domain.UserTaskBoardsSummaryColumn{
			ColumnID:     columnID,
			ColumnKey:    columnKey,
			ColumnName:   columnName,
			TaskCount:    taskCount,
			OverdueCount: overdueCount,
		})
		columnOrder[boardID][columnID] = columnPos
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate user task boards summary: %w", err)
	}

	items := make([]domain.UserTaskBoardsSummaryItem, 0, len(boardOrder))
	for _, boardID := range boardOrder {
		item := boardMap[boardID]
		sort.SliceStable(item.Columns, func(i, j int) bool {
			left := columnOrder[boardID][item.Columns[i].ColumnID]
			right := columnOrder[boardID][item.Columns[j].ColumnID]
			if left == right {
				return item.Columns[i].ColumnName < item.Columns[j].ColumnName
			}
			return left < right
		})
		items = append(items, *item)
	}

	sort.SliceStable(items, func(i, j int) bool {
		if items[i].TaskCount == items[j].TaskCount {
			return items[i].BoardName < items[j].BoardName
		}
		return items[i].TaskCount > items[j].TaskCount
	})

	return items, nil
}
