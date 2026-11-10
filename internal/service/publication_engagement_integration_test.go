package service

import (
	"context"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
	"os"
	"strings"
	"testing"
)

func TestNWS005EngagementIntegration(t *testing.T) {
	_, pool := roomIntegrationService(t)
	ctx := context.Background()
	for _, name := range []string{"20260630000100_create_publications.sql", "20260701000200_create_publication_views.sql", "20260929001000_publications_news_cover_status_rich.sql", "20260929001100_publications_media_gallery_pdf_video.sql", "20260929001200_publication_object_cleanup.sql", "20260929001300_publications_reactions_threads_moderation.sql", "20260930001400_publications_pinned_banners_broadcasts.sql", "20260930001500_publication_audience_scope.sql", "20261001000100_publications_drafts_scheduled_archive.sql", "20261001000200_publications_target_audience_departments.sql"} {
		data, err := os.ReadFile("../../db/migrations/" + name)
		require.NoError(t, err)
		_, err = pool.Exec(ctx, strings.Split(string(data), "-- +goose Down")[0])
		require.NoError(t, err)
	}
	repo := repository.NewPublicationRepository(pool, zap.NewNop())
	svc := NewPublicationService(repo, repository.NewUserRepository(pool, zap.NewNop()), zap.NewNop())
	pub := uuid.New()
	_, err := pool.Exec(ctx, `INSERT INTO publications(id,author_id,title,status) VALUES($1,1,'Engagement','published')`, pub)
	require.NoError(t, err)
	root, child, grandchild := uuid.New(), uuid.New(), uuid.New()
	_, err = pool.Exec(ctx, `INSERT INTO publication_comments(id,publication_id,author_id,content,parent_id) VALUES($1,$4,2,'root',NULL),($2,$4,2,'child',$1),($3,$4,2,'grandchild',$2)`, root, child, grandchild, pub)
	require.NoError(t, err)
	_, err = svc.PinComment(ctx, pub, root, 1, true)
	require.NoError(t, err)
	_, err = svc.PinComment(ctx, pub, root, 2, true)
	require.ErrorIs(t, err, ErrForbiddenAction)
	tree, err := svc.ListComments(ctx, pub, 2)
	require.NoError(t, err)
	require.Len(t, tree, 1)
	require.Len(t, tree[0].Replies, 1)
	require.Len(t, tree[0].Replies[0].Replies, 1)
	reason := "spam"
	_, err = svc.ModerateComment(ctx, pub, root, 1, domain.ModerateCommentRequest{Action: domain.ModerationActionHide, Reason: &reason})
	require.NoError(t, err)
	tree, err = svc.ListComments(ctx, pub, 2)
	require.NoError(t, err)
	require.Equal(t, "[Комментарий скрыт модератором]", tree[0].Content)
	require.Nil(t, tree[0].ModerationReason)
	require.Len(t, tree[0].Replies, 1)
	_, err = svc.AddCommentReaction(ctx, pub, root, 2, "🔥")
	require.ErrorIs(t, err, ErrForbiddenAction)
	_, err = svc.AddReaction(ctx, pub, 2, "arbitrary")
	require.Error(t, err)
	_, err = svc.AddReaction(ctx, pub, 2, "🔥")
	require.NoError(t, err)
	feed, err := svc.ListPublications(ctx, 2, "", "", nil, nil, nil, "", "", 1, 20)
	require.NoError(t, err)
	require.Len(t, feed, 1)
	require.Contains(t, feed[0].UserReactions, "🔥")
	_, err = svc.AddReaction(ctx, pub, 2, "like")
	require.NoError(t, err)
	_, err = svc.AddReaction(ctx, pub, 2, "👍")
	require.NoError(t, err)
	_, err = svc.RemoveReaction(ctx, pub, 2, "like")
	require.NoError(t, err)
	var count int
	err = pool.QueryRow(ctx, `SELECT count(*) FROM publication_likes WHERE publication_id=$1`, pub).Scan(&count)
	require.NoError(t, err)
	require.Zero(t, count)
	require.NoError(t, svc.LikePublication(ctx, pub, 2))
	reactions, _, err := repo.GetReactions(ctx, pub, 2)
	require.NoError(t, err)
	require.Len(t, reactions, 2)
	_, err = pool.Exec(ctx, `INSERT INTO users(id,role,username,email) SELECT n,'user','preview-'||n,'preview-'||n||'@test' FROM generate_series(10,34) n`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO publication_reactions(publication_id,user_id,reaction) SELECT $1,n,'🎉' FROM generate_series(10,34) n`, pub)
	require.NoError(t, err)
	preview, reacted, err := repo.GetReactions(ctx, pub, 34)
	require.NoError(t, err)
	require.Contains(t, reacted, "🎉")
	for _, summary := range preview {
		if summary.Reaction == "🎉" {
			require.Equal(t, 25, summary.Count)
			require.Len(t, summary.Users, 20)
			require.True(t, summary.Reacted, "viewer outside preview must still be marked")
		}
	}
	_, err = svc.ModerateComment(ctx, pub, root, 1, domain.ModerateCommentRequest{Action: domain.ModerationActionDelete, Reason: &reason})
	require.NoError(t, err)
	var status string
	var moderator int64
	var storedReason string
	var deleted bool
	err = pool.QueryRow(ctx, `SELECT status,moderated_by,moderation_reason,deleted_at IS NOT NULL FROM publication_comments WHERE id=$1`, root).Scan(&status, &moderator, &storedReason, &deleted)
	require.NoError(t, err)
	require.Equal(t, domain.CommentStatusDeletedByModerator, status)
	require.EqualValues(t, 1, moderator)
	require.Equal(t, reason, storedReason)
	require.True(t, deleted)
	tree, err = svc.ListComments(ctx, pub, 2)
	require.NoError(t, err)
	require.Len(t, tree, 1)
	require.Len(t, tree[0].Replies, 1)
}

func TestNWS005ReactionVocabulary(t *testing.T) {
	require.Equal(t, "👍", canonicalReaction(" like "))
	require.Equal(t, "🔥", canonicalReaction("🔥"))
	require.Empty(t, canonicalReaction("not-an-emoji"))
	require.Empty(t, canonicalReaction(strings.Repeat("🔥", 100)))
}
