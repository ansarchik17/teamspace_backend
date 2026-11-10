package service

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

func TestPublicationOrganizationIsolationIntegration(t *testing.T) {
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
		require.NoError(t, err, name)
	}
	otherCompany := uuid.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO organizations(id,name) VALUES($1,'Other company');
	`, otherCompany)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO users(id,username,email,role,organization_id) VALUES
		(3,'other-admin','admin@other.test','admin',$1),
		(4,'other-member','member@other.test','user',$1),
		(5,'unassigned','legacy@example.test','user',NULL),
		(6,'platform-admin','platform@example.test','admin',NULL);`, otherCompany)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE users SET ministry_id=10,department_id=15`)
	require.NoError(t, err)
	repo := repository.NewPublicationRepository(pool, zap.NewNop())
	svc := NewPublicationService(repo, repository.NewUserRepository(pool, zap.NewNop()), zap.NewNop())
	create := func(author int64, title, status string) *domain.PublicationResponse {
		t.Helper()
		pub, err := svc.CreatePublication(ctx, author, domain.CreatePublicationRequest{
			SourceType: "user", Type: "news", Title: title, Content: title,
			TargetAudience: "all", Importance: "normal", Status: status,
		})
		require.NoError(t, err)
		return pub
	}
	own := create(1, "Own company", domain.PublicationStatusPublished)
	foreign := create(3, "Other company", domain.PublicationStatusPublished)
	legacy := create(5, "Unassigned author", domain.PublicationStatusPublished)
	ownDraft := create(2, "Own draft", domain.PublicationStatusDraft)
	create(4, "Other company draft", domain.PublicationStatusDraft)
	_, err = pool.Exec(ctx, `UPDATE publications SET is_banner=true WHERE status='published'`)
	require.NoError(t, err)

	for _, reader := range []int64{1, 2} {
		feed, err := svc.ListPublications(ctx, reader, "", "", nil, nil, nil, "", "published", 1, 20)
		require.NoError(t, err)
		require.Len(t, feed, 1)
		require.Equal(t, own.ID, feed[0].ID)
		for _, hidden := range []uuid.UUID{foreign.ID, legacy.ID} {
			_, err = svc.GetPublication(ctx, hidden, reader)
			require.ErrorContains(t, err, "not found", "direct links must respect company membership")
			_, err = svc.AcknowledgePublication(ctx, hidden, reader)
			require.Error(t, err, "foreign publications must not accept interactions")
		}
		banners, err := svc.ListBanners(ctx, reader)
		require.NoError(t, err)
		require.Len(t, banners, 1)
		require.Equal(t, own.ID, banners[0].ID)
	}
	feed, err := svc.ListPublications(ctx, 4, "", "", nil, nil, nil, "", "published", 1, 20)
	require.NoError(t, err)
	require.Len(t, feed, 1)
	require.Equal(t, foreign.ID, feed[0].ID)
	drafts, err := svc.ListPublications(ctx, 2, "", "", nil, nil, nil, "", "draft", 1, 20)
	require.NoError(t, err)
	require.Len(t, drafts, 1)
	require.Equal(t, ownDraft.ID, drafts[0].ID)
	_, err = svc.GetPublication(ctx, foreign.ID, 6)
	require.NoError(t, err, "platform administration retains its explicit global scope")

	stored, err := repo.GetByID(ctx, own.ID, 1)
	require.NoError(t, err)
	recipients, err := repo.GetTargetAudienceRecipients(ctx, stored)
	require.NoError(t, err)
	var recipientIDs []int64
	for _, recipient := range recipients {
		recipientIDs = append(recipientIDs, recipient.ID)
	}
	require.ElementsMatch(t, []int64{1, 2}, recipientIDs, "all means all members of the author's company")
	stats, err := repo.GetBroadcastStats(ctx, own.ID)
	require.NoError(t, err)
	require.Equal(t, 2, stats.TotalTargetUsers)
	analytics, err := repo.GetPublicationAnalytics(ctx, own.ID)
	require.NoError(t, err)
	require.Equal(t, 2, analytics.TotalTargetUsers)
	readers, err := repo.ListPublicationReaders(ctx, own.ID, domain.PublicationReadersFilter{})
	require.NoError(t, err)
	require.Len(t, readers.Readers, 2)
}
