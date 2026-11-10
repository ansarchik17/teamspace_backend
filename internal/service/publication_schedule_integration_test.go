package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

// Keep status VARCHAR, as in production: TEXT hides parameter inference errors.
func TestPublicationSetStatusVarcharIntegration(t *testing.T) {
	_, pool := roomIntegrationService(t)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `CREATE TABLE publications (
		id UUID PRIMARY KEY, status VARCHAR(50), published_at TIMESTAMPTZ,
		scheduled_at TIMESTAMPTZ, archive_at TIMESTAMPTZ, archived_at TIMESTAMPTZ,
		is_pinned BOOLEAN, is_banner BOOLEAN, updated_at TIMESTAMPTZ, deleted_at TIMESTAMPTZ,
		author_id BIGINT, importance TEXT, notification_sent_at TIMESTAMPTZ
	);
	CREATE TABLE publication_broadcast_queue (publication_id UUID UNIQUE, triggered_by BIGINT);`)
	require.NoError(t, err)
	id := uuid.New()
	_, err = pool.Exec(ctx, `INSERT INTO publications(id,status,is_pinned,is_banner,author_id,importance)
		VALUES ($1,'draft',true,true,1,'normal')`, id)
	require.NoError(t, err)
	repo := repository.NewPublicationRepository(pool, zap.NewNop())
	now := time.Now().UTC().Truncate(time.Microsecond)
	scheduled := now.Add(time.Hour)
	archive := now.Add(2 * time.Hour)
	for _, status := range []string{domain.PublicationStatusPublished, domain.PublicationStatusScheduled, domain.PublicationStatusDraft, domain.PublicationStatusArchived} {
		t.Run(status, func(t *testing.T) {
			var scheduledAt *time.Time
			if status == domain.PublicationStatusScheduled {
				scheduledAt = &scheduled
			}
			require.NoError(t, repo.SetStatus(ctx, id, status, now, scheduledAt, &archive))
			var actual string
			var pinned, banner bool
			var actualScheduled, archivedAt *time.Time
			require.NoError(t, pool.QueryRow(ctx, `SELECT status,is_pinned,is_banner,scheduled_at,archived_at FROM publications WHERE id=$1`, id).
				Scan(&actual, &pinned, &banner, &actualScheduled, &archivedAt))
			require.Equal(t, status, actual)
			if scheduledAt == nil {
				require.Nil(t, actualScheduled)
			} else {
				require.NotNil(t, actualScheduled)
				require.True(t, scheduledAt.Equal(*actualScheduled))
			}
			if status == domain.PublicationStatusArchived {
				require.False(t, pinned)
				require.False(t, banner)
				require.NotNil(t, archivedAt)
			} else {
				require.True(t, pinned)
				require.True(t, banner)
				require.Nil(t, archivedAt)
			}
		})
	}
}

func TestNWS001ScheduledPublishingIntegration(t *testing.T) {
	_, pool := roomIntegrationService(t)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `CREATE TABLE publications(id INT PRIMARY KEY,status TEXT,scheduled_at TIMESTAMPTZ,published_at TIMESTAMPTZ,updated_at TIMESTAMPTZ,deleted_at TIMESTAMPTZ);
 INSERT INTO publications VALUES
 (1,'scheduled',NOW()-INTERVAL '1 hour',NOW()-INTERVAL '1 hour',NOW(),NULL),
 (2,'scheduled',NOW()+INTERVAL '1 hour',NOW()+INTERVAL '1 hour',NOW(),NULL),
 (3,'draft',NULL,NOW(),NOW(),NULL),
 (4,'scheduled',NOW()-INTERVAL '1 hour',NOW()-INTERVAL '1 hour',NOW(),NOW());`)
	require.NoError(t, err)
	repo := repository.NewPublicationRepository(pool, zap.NewNop())
	require.NoError(t, repo.PublishDue(ctx))
	require.NoError(t, repo.PublishDue(ctx))
	for id, status := range map[int]string{1: "published", 2: "scheduled", 3: "draft", 4: "scheduled"} {
		var actual string
		require.NoError(t, pool.QueryRow(ctx, `SELECT status FROM publications WHERE id=$1`, id).Scan(&actual))
		require.Equal(t, status, actual)
	}
}
