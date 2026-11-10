package service

import (
	"bytes"
	"context"
	"encoding/csv"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

func TestNWS007SafeCSVCell(t *testing.T) {
	for _, value := range []string{"=1+1", "+1", "-1", "@SUM(A1)", "  =1", "\ttext", "\rtext", "\ntext"} {
		require.Equal(t, "'"+value, safeCSVCell(value))
	}
	for _, value := range []string{"", "Иван Иванов", "normal@example.test", " ordinary"} {
		require.Equal(t, value, safeCSVCell(value))
	}
}

func TestNWS007AcknowledgementAndReadersIntegration(t *testing.T) {
	_, pool := roomIntegrationService(t)
	ctx := context.Background()
	for _, name := range []string{
		"20260630000100_create_publications.sql", "20260701000200_create_publication_views.sql",
		"20260929001000_publications_news_cover_status_rich.sql", "20260929001100_publications_media_gallery_pdf_video.sql",
		"20260929001200_publication_object_cleanup.sql", "20260929001300_publications_reactions_threads_moderation.sql",
		"20260930001400_publications_pinned_banners_broadcasts.sql", "20260930001500_publication_audience_scope.sql",
		"20261001000100_publications_drafts_scheduled_archive.sql", "20261001000200_publications_target_audience_departments.sql",
	} {
		data, err := os.ReadFile("../../db/migrations/" + name)
		require.NoError(t, err)
		_, err = pool.Exec(ctx, strings.Split(string(data), "-- +goose Down")[0])
		require.NoError(t, err)
	}
	repo := repository.NewPublicationRepository(pool, zap.NewNop())
	svc := NewPublicationService(repo, repository.NewUserRepository(pool, zap.NewNop()), zap.NewNop())
	pubID := uuid.New()
	_, err := pool.Exec(ctx, `INSERT INTO publications(id,author_id,title,status,target_audience,is_banner,requires_acknowledgement,published_at)
		VALUES ($1,1,'Readers','published','all',true,true,NOW());
	`, pubID)
	require.NoError(t, err)
	first, err := svc.AcknowledgePublication(ctx, pubID, 2)
	require.NoError(t, err)
	again, err := svc.AcknowledgePublication(ctx, pubID, 2)
	require.NoError(t, err)
	require.True(t, first.AcknowledgedAt.Equal(again.AcknowledgedAt))
	stats, err := svc.GetPublicationAnalytics(ctx, pubID, 1)
	require.NoError(t, err)
	require.Equal(t, 1, stats.ViewsCount)
	require.Equal(t, 1, stats.AcknowledgedCount)
	_, err = svc.ListPublicationReaders(ctx, pubID, 2, domain.PublicationReadersFilter{})
	require.ErrorIs(t, err, ErrForbiddenAction)
	_, err = pool.Exec(ctx, `UPDATE users SET first_name='=1+1' WHERE id=2`)
	require.NoError(t, err)
	data, _, _, err := svc.ExportPublicationReaders(ctx, pubID, 1, domain.PublicationReadersExportFilter{Format: "csv", Status: "acknowledged"})
	require.NoError(t, err)
	records, err := csv.NewReader(bytes.NewReader(data[3:])).ReadAll()
	require.NoError(t, err)
	require.Len(t, records, 2)
	require.True(t, strings.HasPrefix(records[1][1], "'=1+1"))
	// Malformed legacy targeting must fail closed, including for the author.
	_, err = pool.Exec(ctx, `UPDATE publications SET target_audience='department',target_department_id=NULL,target_department_ids='{}' WHERE id=$1`, pubID)
	require.NoError(t, err)
	_, err = svc.ListPublicationReaders(ctx, pubID, 1, domain.PublicationReadersFilter{})
	require.Error(t, err)
	_, _, _, err = svc.ExportPublicationReaders(ctx, pubID, 1, domain.PublicationReadersExportFilter{Format: "csv"})
	require.Error(t, err)
	// Simulate a failed side effect and verify that acknowledgement is rolled back.
	require.NoError(t, repo.Unacknowledge(ctx, pubID, 2))
	_, err = pool.Exec(ctx, `ALTER TABLE publication_banner_dismissals ADD CONSTRAINT fail_dismissal CHECK (user_id <> 1)`)
	require.NoError(t, err)
	_, err = repo.AcknowledgeWithView(ctx, pubID, 1, true)
	require.Error(t, err)
	var count int
	require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM publication_acknowledgements WHERE publication_id=$1 AND user_id=1`, pubID).Scan(&count))
	require.Zero(t, count)
	require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM publication_views WHERE publication_id=$1 AND user_id=1`, pubID).Scan(&count))
	require.Zero(t, count)
}
