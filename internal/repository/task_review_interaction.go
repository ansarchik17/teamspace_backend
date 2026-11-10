package repository

import (
	"context"
)

func (r *TaskRepository) HasAnyInteractionBetweenUsers(ctx context.Context, user1ID, user2ID int64) (bool, error) {
	const q = `
SELECT EXISTS (
    SELECT 1
    FROM task_assignees a1
    JOIN task_assignees a2 ON a2.task_id = a1.task_id
    WHERE a1.user_id = $1 AND a2.user_id = $2

    UNION ALL

    SELECT 1
    FROM tasks t
    JOIN task_assignees a ON a.task_id = t.id
    WHERE (t.created_by = $1 AND a.user_id = $2)
       OR (t.created_by = $2 AND a.user_id = $1)
    LIMIT 1
)
`
	var ok bool
	err := r.db.QueryRow(ctx, q, user1ID, user2ID).Scan(&ok)
	if err != nil {
		return false, err
	}
	return ok, nil
}
