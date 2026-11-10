-- +goose Up
-- +goose StatementBegin
WITH livekit_small_events AS (
    SELECT
        e.id,
        COALESCE(NULLIF(e.livekit_room_name, ''), NULLIF(e.meeting_room_id, '')) AS room_name,
        COALESCE(NULLIF(substring(COALESCE(e.meeting_url, '') FROM '^(https?://[^/]+)'), ''), '') AS origin
    FROM events e
    LEFT JOIN event_attendees ea ON ea.event_id = e.id
    WHERE e.deleted_at IS NULL
      AND e.is_online = TRUE
      AND e.meeting_provider = 'livekit'
    GROUP BY e.id, e.livekit_room_name, e.meeting_room_id, e.meeting_url
    HAVING 1 + COUNT(DISTINCT ea.user_id) <= 5
)
UPDATE events e
SET meeting_url = CASE
        WHEN l.origin <> '' THEN l.origin || '/web/calls/calendar/' || l.room_name
        ELSE '/web/calls/calendar/' || l.room_name
    END,
    updated_at = NOW()
FROM livekit_small_events l
WHERE e.id = l.id
  AND COALESCE(l.room_name, '') <> '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
