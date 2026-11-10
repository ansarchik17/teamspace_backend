package service

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

func TestNWS004TargetsNormalization(t *testing.T) {
	id := int64(15)
	ids, err := normalizePublicationTargets(&id, []int64{16, 15, 16}, nil)
	require.NoError(t, err)
	require.Equal(t, []int64{16, 15}, ids)
	for _, values := range [][]int64{nil, {0}, {-1}, make([]int64, 101)} {
		_, err := normalizePublicationTargets(nil, values, nil)
		require.Error(t, err)
	}
	ids, err = normalizePublicationTargets(nil, nil, &id)
	require.NoError(t, err)
	require.Equal(t, []int64{15}, ids)
	pub := &domain.Publication{Status: "published", PublishedAt: time.Now().Add(-time.Hour), TargetAudience: "unknown", AuthorID: 1}
	require.False(t, publicationVisible(pub, &domain.User{ID: 2}, time.Now()))
}

func TestNWS004AudienceConsistencyAndPermissionsIntegration(t *testing.T) {
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
	_, err := pool.Exec(ctx, `UPDATE users SET ministry_id=10,department_id=15;
	 UPDATE users SET role='admin' WHERE id=1;
	 INSERT INTO users(id,username,email,role,ministry_id,department_id) VALUES
	 (3,'target','target@example.test','user',20,16),(4,'outside','outside@example.test','user',30,99);
 UPDATE users SET organization_id='11111111-1111-4111-8111-111111111111' WHERE id IN (3,4);`)
	require.NoError(t, err)
	repo := repository.NewPublicationRepository(pool, zap.NewNop())
	svc := NewPublicationService(repo, repository.NewUserRepository(pool, zap.NewNop()), zap.NewNop())
	id := int64(15)
	req := domain.CreatePublicationRequest{SourceType: "user", Type: "news", Title: "Targeted", TargetAudience: "department", Importance: "normal", TargetDepartmentID: &id, TargetDepartmentIDs: []int64{16, 15, 16}}
	pub, err := svc.CreatePublication(ctx, 1, req)
	require.NoError(t, err)
	require.Nil(t, pub.TargetDepartmentID)
	require.ElementsMatch(t, []int64{15, 16}, pub.TargetDepartmentIDs)
	// Exercise historical rows with both single and multi-select fields populated.
	_, err = pool.Exec(ctx, `UPDATE publications SET target_department_id=15,target_department_ids=ARRAY[16]::bigint[],is_banner=true WHERE id=$1`, pub.ID)
	require.NoError(t, err)
	stored, err := repo.GetByID(ctx, pub.ID, 1)
	require.NoError(t, err)
	for _, userID := range []int64{2, 3} {
		_, err = svc.GetPublication(ctx, pub.ID, userID)
		require.NoError(t, err)
		list, err := svc.ListPublications(ctx, userID, "", "", nil, nil, nil, "", "published", 1, 20)
		require.NoError(t, err)
		require.Len(t, list, 1)
		banners, err := svc.ListBanners(ctx, userID)
		require.NoError(t, err)
		require.Len(t, banners, 1)
	}
	_, err = svc.GetPublication(ctx, pub.ID, 4)
	require.ErrorIs(t, err, ErrForbiddenView)
	list, err := svc.ListPublications(ctx, 4, "", "", nil, nil, nil, "", "published", 1, 20)
	require.NoError(t, err)
	require.Empty(t, list)
	recipients, err := repo.GetTargetAudienceRecipients(ctx, stored)
	require.NoError(t, err)
	require.Len(t, recipients, 3)
	require.NoError(t, repo.Acknowledge(ctx, pub.ID, 2))
	require.NoError(t, repo.Acknowledge(ctx, pub.ID, 3))
	stats, err := repo.GetBroadcastStats(ctx, pub.ID)
	require.NoError(t, err)
	require.Equal(t, 3, stats.TotalTargetUsers)
	require.Equal(t, 2, stats.AcknowledgedCount)
	// Ministry/branch routing must use the same union too.
	branch := &domain.Publication{AuthorID: 1, TargetAudience: "branch", TargetMinistryID: func() *int64 { n := int64(10); return &n }(), TargetMinistryIDs: []int64{20}}
	recipients, err = repo.GetTargetAudienceRecipients(ctx, branch)
	require.NoError(t, err)
	require.Len(t, recipients, 3)
	_, err = svc.CreatePublication(ctx, 2, req)
	require.Error(t, err)
	require.Equal(t, apperrors.ErrCodeForbidden, apperrors.ToAppError(err).Code)
	req.TargetDepartmentID = nil
	req.TargetDepartmentIDs = []int64{99999}
	_, err = svc.CreatePublication(ctx, 1, req)
	require.Error(t, err, "unknown targets must be rejected")
	req.TargetDepartmentIDs = []int64{15}
	ownPub, err := svc.CreatePublication(ctx, 2, req)
	require.NoError(t, err, "user can publish to own department")
	_, err = svc.UpdatePublication(ctx, ownPub.ID, 2, domain.UpdatePublicationRequest{
		SourceType: "user", Type: "news", Title: "Changed", TargetAudience: "department", Importance: "normal", TargetDepartmentIDs: []int64{16},
	})
	require.Error(t, err, "non-admin cannot retarget own post to another department")
	updated, err := svc.UpdatePublication(ctx, pub.ID, 1, domain.UpdatePublicationRequest{
		SourceType: "user", Type: "news", Title: "Text edit", TargetAudience: "department", Importance: "normal",
	})
	require.NoError(t, err)
	require.ElementsMatch(t, []int64{15, 16}, updated.TargetDepartmentIDs, "text edit preserves audience")
	_, err = svc.UpdatePublication(ctx, pub.ID, 1, domain.UpdatePublicationRequest{
		SourceType: "user", Type: "news", Title: "Changed", TargetAudience: "department", Importance: "normal", TargetDepartmentIDs: []int64{99999},
	})
	require.Error(t, err, "update must validate targets too")
}
