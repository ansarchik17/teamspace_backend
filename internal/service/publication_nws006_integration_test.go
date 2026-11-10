package service

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

func TestNWS006BroadcastQueueAndBannersIntegration(t *testing.T) {
	_, pool := roomIntegrationService(t)
	ctx := context.Background()
	for _, name := range []string{
		"20260630000100_create_publications.sql",
		"20260701000200_create_publication_views.sql",
		"20260929001000_publications_news_cover_status_rich.sql",
		"20260929001100_publications_media_gallery_pdf_video.sql",
		"20260929001200_publication_object_cleanup.sql",
		"20260929001300_publications_reactions_threads_moderation.sql",
		"20260930001400_publications_pinned_banners_broadcasts.sql",
		"20260930001500_publication_audience_scope.sql",
		"20261001000100_publications_drafts_scheduled_archive.sql",
		"20261001000200_publications_target_audience_departments.sql",
	} {
		data, err := os.ReadFile("../../db/migrations/" + name)
		require.NoError(t, err)
		_, err = pool.Exec(ctx, strings.Split(string(data), "-- +goose Down")[0])
		require.NoError(t, err, name)
	}
	repo := repository.NewPublicationRepository(pool, zap.NewNop())
	pubID := uuid.New()
	_, err := pool.Exec(ctx, `INSERT INTO publications(id,author_id,title,status,importance,target_audience,is_banner,requires_acknowledgement,published_at,scheduled_at) VALUES ($1,1,'Alert','scheduled','urgent','all',true,true,NOW()-INTERVAL '1 minute',NOW()-INTERVAL '1 minute')`, pubID)
	require.NoError(t, err)
	ids, err := repo.PublishDueWithIDs(ctx)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{pubID}, ids)
	jobs, err := repo.PendingBroadcasts(ctx)
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	require.Equal(t, pubID, jobs[0].PublicationID)
	visible, err := repo.ListBanners(ctx, 2, nil, nil)
	require.NoError(t, err)
	require.Len(t, visible, 1)
	require.NoError(t, repo.Acknowledge(ctx, pubID, 2))
	visible, err = repo.ListBanners(ctx, 2, nil, nil)
	require.NoError(t, err)
	require.Empty(t, visible)
	recipients, err := repo.GetTargetAudienceRecipients(ctx, &domain.Publication{AuthorID: 1, TargetAudience: "all", SourceType: "department", DepartmentID: func() *int64 { n := int64(42); return &n }()})
	require.NoError(t, err)
	require.Len(t, recipients, 2)
	release, acquired, err := repo.AcquireBroadcastLock(ctx, pubID)
	require.NoError(t, err)
	require.True(t, acquired)
	defer release()
	_, acquired, err = repo.AcquireBroadcastLock(ctx, pubID)
	require.NoError(t, err)
	require.False(t, acquired)
	require.NoError(t, repo.RecordBroadcastDelivery(ctx, pubID, 2, "email"))
	sent, err := repo.HasBroadcastDelivery(ctx, pubID, 2, "email")
	require.NoError(t, err)
	require.True(t, sent)
	require.NoError(t, repo.RetryQueuedBroadcast(ctx, pubID, context.DeadlineExceeded))
	jobs, err = repo.PendingBroadcasts(ctx)
	require.NoError(t, err)
	require.Empty(t, jobs)
	var nextAttempt time.Time
	require.NoError(t, pool.QueryRow(ctx, `SELECT next_attempt_at FROM publication_broadcast_queue WHERE publication_id=$1`, pubID).Scan(&nextAttempt))
	require.True(t, nextAttempt.After(time.Now()))
}
