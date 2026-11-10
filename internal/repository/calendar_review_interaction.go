package repository

import (
	"context"
)

func (r *CalendarRepository) HasAnyInteractionBetweenUsers(ctx context.Context, user1ID, user2ID int64) (bool, error) {
	const q = `
SELECT EXISTS (
  SELECT 1
  FROM events e
  LEFT JOIN event_attendees a1
    ON a1.event_id = e.id AND a1.user_id = $1
  LEFT JOIN event_attendees a2
    ON a2.event_id = e.id AND a2.user_id = $2
  WHERE
    ( (e.user_id = $1 OR a1.user_id IS NOT NULL)
      AND
      (e.user_id = $2 OR a2.user_id IS NOT NULL) )
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
