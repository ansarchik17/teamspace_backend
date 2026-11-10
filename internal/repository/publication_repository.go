package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

type PublicationRepository struct {
	db     *pgxpool.Pool
	logger *zap.Logger
}

var ErrInvalidPublicationAttachmentOrder = errors.New("invalid publication attachment order")

func NewPublicationRepository(db *pgxpool.Pool, logger *zap.Logger) *PublicationRepository {
	return &PublicationRepository{
		db:     db,
		logger: logger.With(zap.String("repository", "publication")),
	}
}

// Create inserts a publication and its attachments in a transaction
func (r *PublicationRepository) Create(ctx context.Context, pub *domain.Publication, attachments []domain.PublicationAttachment) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		r.logger.Error("Failed to begin transaction", zap.Error(err))
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var contentDocJSON []byte
	if pub.ContentDoc != nil {
		contentDocJSON, _ = json.Marshal(pub.ContentDoc)
	}
	if len(contentDocJSON) == 0 {
		contentDocJSON = []byte("{}")
	}

	if pub.Status == "" {
		pub.Status = domain.PublicationStatusPublished
	}

	var bannerColor string
	if pub.BannerColor != nil && *pub.BannerColor != "" {
		bannerColor = *pub.BannerColor
	} else {
		bannerColor = domain.BannerColorUrgent
	}

	pubQuery := `
		INSERT INTO publications (
			id, author_id, approved_by_id, source_type,
			type, title, content, content_doc, content_html, cover_image_url, status,
			target_audience, target_ministry_id, target_department_id, target_ministry_ids, target_department_ids,
			ministry_id, state_body_id, department_id, importance,
			is_pinned, pinned_at, pinned_by, pinned_until,
			is_banner, banner_until, banner_action_text, banner_action_url, banner_color,
			allow_comments, requires_acknowledgement, scheduled_at, published_at,
			archive_at, archived_at,
			views_count, shares_count, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4,
			$5, $6, $7, $8, $9, $10, $11,
			$12, $13, $14, $15, $16,
			$17, $18, $19, $20,
			$21, $22, $23, $24,
			$25, $26, $27, $28, $29,
			$30, $31, $32, $33,
			$34, $35,
			0, 0, $36, $37
		)
	`
	_, err = tx.Exec(ctx, pubQuery,
		pub.ID, pub.AuthorID, pub.ApprovedByID, pub.SourceType,
		pub.Type, pub.Title, pub.Content, contentDocJSON, pub.ContentHTML, pub.CoverImageURL, pub.Status,
		pub.TargetAudience, pub.TargetMinistryID, pub.TargetDepartmentID, pub.TargetMinistryIDs, pub.TargetDepartmentIDs,
		pub.MinistryID, pub.StateBodyID, pub.DepartmentID, pub.Importance,
		pub.IsPinned, pub.PinnedAt, pub.PinnedByID, pub.PinnedUntil,
		pub.IsBanner, pub.BannerUntil, pub.BannerActionText, pub.BannerActionURL, bannerColor,
		pub.AllowComments, pub.RequiresAcknowledgement, pub.ScheduledAt, pub.PublishedAt,
		pub.ArchiveAt, pub.ArchivedAt,
		pub.CreatedAt, pub.UpdatedAt,
	)
	if err != nil {
		r.logger.Error("Failed to insert publication", zap.Error(err))
		return fmt.Errorf("insert publication: %w", err)
	}

	if err := r.insertAttachmentsTx(ctx, tx, pub.ID, attachments); err != nil {
		return err
	}
	if pub.Status == domain.PublicationStatusPublished && (pub.Importance == domain.PublicationImportanceImportant || pub.Importance == domain.PublicationImportanceUrgent) {
		if _, err := tx.Exec(ctx, `INSERT INTO publication_broadcast_queue (publication_id, triggered_by) VALUES ($1,$2) ON CONFLICT DO NOTHING`, pub.ID, pub.AuthorID); err != nil {
			return err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		r.logger.Error("Failed to commit transaction", zap.Error(err))
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

func (r *PublicationRepository) Update(ctx context.Context, pub *domain.Publication, attachments []domain.PublicationAttachment) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		r.logger.Error("Failed to begin transaction", zap.Error(err))
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var contentDocJSON []byte
	if pub.ContentDoc != nil {
		contentDocJSON, _ = json.Marshal(pub.ContentDoc)
	}
	if len(contentDocJSON) == 0 {
		contentDocJSON = []byte("{}")
	}

	if pub.Status == "" {
		pub.Status = domain.PublicationStatusPublished
	}

	var bannerColor string
	if pub.BannerColor != nil && *pub.BannerColor != "" {
		bannerColor = *pub.BannerColor
	} else {
		bannerColor = domain.BannerColorUrgent
	}

	pubQuery := `
		UPDATE publications
		SET
			source_type = $2,
			type = $3,
			title = $4,
			content = $5,
			content_doc = $6,
			content_html = $7,
			cover_image_url = $8,
			status = $9,
			target_audience = $10,
			target_ministry_id = $11,
			target_department_id = $12,
			target_ministry_ids = $13,
			target_department_ids = $14,
			ministry_id = $15,
			state_body_id = $16,
			department_id = $17,
			importance = $18,
			is_pinned = $19,
			pinned_at = $20,
			pinned_by = $21,
			pinned_until = $22,
			is_banner = $23,
			banner_until = $24,
			banner_action_text = $25,
			banner_action_url = $26,
			banner_color = $27,
			allow_comments = $28,
			requires_acknowledgement = $29,
			scheduled_at = $30,
			published_at = $31,
			archive_at = $32,
			archived_at = $33,
			updated_at = $34
		WHERE id = $1 AND deleted_at IS NULL
	`
	commandTag, err := tx.Exec(ctx, pubQuery,
		pub.ID, pub.SourceType, pub.Type, pub.Title, pub.Content,
		contentDocJSON, pub.ContentHTML, pub.CoverImageURL, pub.Status,
		pub.TargetAudience, pub.TargetMinistryID, pub.TargetDepartmentID, pub.TargetMinistryIDs, pub.TargetDepartmentIDs,
		pub.MinistryID, pub.StateBodyID, pub.DepartmentID, pub.Importance,
		pub.IsPinned, pub.PinnedAt, pub.PinnedByID, pub.PinnedUntil,
		pub.IsBanner, pub.BannerUntil, pub.BannerActionText, pub.BannerActionURL, bannerColor,
		pub.AllowComments, pub.RequiresAcknowledgement,
		pub.ScheduledAt, pub.PublishedAt,
		pub.ArchiveAt, pub.ArchivedAt, pub.UpdatedAt,
	)
	if err != nil {
		r.logger.Error("Failed to update publication", zap.Error(err))
		return fmt.Errorf("update publication: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		return fmt.Errorf("publication not found")
	}

	if attachments != nil {
		rows, err := tx.Query(ctx, `SELECT id,file_url,thumbnail_url FROM publication_attachments WHERE publication_id=$1`, pub.ID)
		if err != nil {
			return err
		}
		previousIDs := map[string]uuid.UUID{}
		oldURLs := []string{}
		for rows.Next() {
			var id uuid.UUID
			var raw string
			var thumb *string
			if err := rows.Scan(&id, &raw, &thumb); err != nil {
				rows.Close()
				return err
			}
			previousIDs[raw] = id
			oldURLs = append(oldURLs, raw)
			if thumb != nil {
				oldURLs = append(oldURLs, *thumb)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for i := range attachments {
			if attachments[i].ID == uuid.Nil {
				attachments[i].ID = previousIDs[attachments[i].FileURL]
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM publication_attachments WHERE publication_id = $1`, pub.ID); err != nil {
			r.logger.Error("Failed to replace publication attachments", zap.Error(err))
			return fmt.Errorf("delete publication attachments: %w", err)
		}

		if err := r.insertAttachmentsTx(ctx, tx, pub.ID, attachments); err != nil {
			return err
		}
		for _, raw := range oldURLs {
			if err := queueUnusedPublicationObject(ctx, tx, pub.AuthorID, raw); err != nil {
				return err
			}
		}
	}
	if pub.Status == domain.PublicationStatusPublished && (pub.Importance == domain.PublicationImportanceImportant || pub.Importance == domain.PublicationImportanceUrgent) {
		if _, err := tx.Exec(ctx, `INSERT INTO publication_broadcast_queue (publication_id, triggered_by) SELECT id, author_id FROM publications WHERE id=$1 AND notification_sent_at IS NULL ON CONFLICT DO NOTHING`, pub.ID); err != nil {
			return err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		r.logger.Error("Failed to commit transaction", zap.Error(err))
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

func (r *PublicationRepository) UpdateImported(ctx context.Context, pub *domain.Publication, attachments []domain.PublicationAttachment) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		r.logger.Error("Failed to begin transaction", zap.Error(err))
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var contentDocJSON []byte
	if pub.ContentDoc != nil {
		contentDocJSON, _ = json.Marshal(pub.ContentDoc)
	}
	if len(contentDocJSON) == 0 {
		contentDocJSON = []byte("{}")
	}

	if pub.Status == "" {
		pub.Status = domain.PublicationStatusPublished
	}

	pubQuery := `
		UPDATE publications
		SET
			approved_by_id = $2,
			source_type = $3,
			type = $4,
			title = $5,
			content = $6,
			content_doc = $7,
			content_html = $8,
			cover_image_url = $9,
			status = $10,
			target_audience = $11,
			importance = $12,
			allow_comments = $13,
			requires_acknowledgement = $14,
			published_at = $15,
			updated_at = $16
		WHERE id = $1 AND deleted_at IS NULL
	`
	commandTag, err := tx.Exec(ctx, pubQuery,
		pub.ID, pub.ApprovedByID, pub.SourceType, pub.Type, pub.Title, pub.Content,
		contentDocJSON, pub.ContentHTML, pub.CoverImageURL, pub.Status,
		pub.TargetAudience, pub.Importance, pub.AllowComments, pub.RequiresAcknowledgement,
		pub.PublishedAt, pub.UpdatedAt,
	)
	if err != nil {
		r.logger.Error("Failed to update imported publication", zap.Error(err))
		return fmt.Errorf("update imported publication: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		return fmt.Errorf("publication not found")
	}

	if _, err := tx.Exec(ctx, `DELETE FROM publication_attachments WHERE publication_id = $1`, pub.ID); err != nil {
		r.logger.Error("Failed to replace imported publication attachments", zap.Error(err))
		return fmt.Errorf("delete imported publication attachments: %w", err)
	}

	if err := r.insertAttachmentsTx(ctx, tx, pub.ID, attachments); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		r.logger.Error("Failed to commit transaction", zap.Error(err))
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

// GetByID retrieves a single publication with metrics and flags
func (r *PublicationRepository) GetByID(ctx context.Context, id uuid.UUID, currentUserID int64) (*domain.Publication, error) {
	query := `
		SELECT
			id, author_id, approved_by_id, source_type,
			type, title, content, content_doc, content_html, cover_image_url, status,
			target_audience, target_ministry_id, target_department_id, target_ministry_ids, target_department_ids,
			ministry_id, state_body_id, department_id, importance,
			is_pinned, pinned_at, pinned_by, pinned_until,
			is_banner, banner_until, banner_action_text, banner_action_url, banner_color,
			notification_sent_at,
			allow_comments, requires_acknowledgement, scheduled_at, published_at,
			archive_at, archived_at,
			(SELECT COUNT(*) FROM publication_views WHERE publication_id = publications.id) AS views_count, shares_count, created_at, updated_at,
			(SELECT COUNT(*) FROM publication_likes WHERE publication_id = publications.id) AS likes_count,
			(SELECT COUNT(*) FROM publication_comments WHERE publication_id = publications.id AND deleted_at IS NULL) AS comments_count,
			EXISTS(SELECT 1 FROM publication_likes WHERE publication_id = publications.id AND user_id = $2) AS is_liked,
			EXISTS(SELECT 1 FROM publication_acknowledgements WHERE publication_id = publications.id AND user_id = $2) AS is_acknowledged,
			EXISTS(SELECT 1 FROM publication_banner_dismissals WHERE publication_id = publications.id AND user_id = $2) AS is_banner_dismissed
		FROM publications
		WHERE id = $1 AND deleted_at IS NULL
		  AND ` + publicationOrganizationScope("publications.author_id", "$2", true)

	pub := &domain.Publication{}
	var contentDocRaw []byte
	err := r.db.QueryRow(ctx, query, id, currentUserID).Scan(
		&pub.ID, &pub.AuthorID, &pub.ApprovedByID, &pub.SourceType,
		&pub.Type, &pub.Title, &pub.Content, &contentDocRaw, &pub.ContentHTML, &pub.CoverImageURL, &pub.Status,
		&pub.TargetAudience, &pub.TargetMinistryID, &pub.TargetDepartmentID, &pub.TargetMinistryIDs, &pub.TargetDepartmentIDs,
		&pub.MinistryID, &pub.StateBodyID, &pub.DepartmentID, &pub.Importance,
		&pub.IsPinned, &pub.PinnedAt, &pub.PinnedByID, &pub.PinnedUntil,
		&pub.IsBanner, &pub.BannerUntil, &pub.BannerActionText, &pub.BannerActionURL, &pub.BannerColor,
		&pub.NotificationSentAt,
		&pub.AllowComments, &pub.RequiresAcknowledgement, &pub.ScheduledAt, &pub.PublishedAt,
		&pub.ArchiveAt, &pub.ArchivedAt,
		&pub.ViewsCount, &pub.SharesCount, &pub.CreatedAt, &pub.UpdatedAt,
		&pub.LikesCount, &pub.CommentsCount, &pub.IsLiked, &pub.IsAcknowledged, &pub.IsBannerDismissed,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("publication not found")
		}
		r.logger.Error("Failed to get publication by ID", zap.Error(err), zap.String("pub_id", id.String()))
		return nil, fmt.Errorf("get publication: %w", err)
	}

	if len(contentDocRaw) > 2 && string(contentDocRaw) != "{}" && string(contentDocRaw) != "null" {
		var doc domain.ContentDoc
		if err := json.Unmarshal(contentDocRaw, &doc); err == nil && len(doc.Blocks) > 0 {
			pub.ContentDoc = &doc
		}
	}

	// Fetch attachments
	attachments, err := r.GetAttachmentsByPubID(ctx, id)
	if err != nil {
		return nil, err
	}
	pub.Attachments = attachments

	return pub, nil
}

// GetAttachmentsByPubID retrieves attachments for a publication
func (r *PublicationRepository) GetAttachmentsByPubID(ctx context.Context, pubID uuid.UUID) ([]domain.PublicationAttachment, error) {
	query := `
		SELECT id, publication_id, file_url, file_name, file_type, file_size,
		       caption, order_index, mime_type, thumbnail_url, video_provider, video_embed_url, created_at
		FROM publication_attachments
		WHERE publication_id = $1
		ORDER BY order_index ASC, created_at ASC
	`
	rows, err := r.db.Query(ctx, query, pubID)
	if err != nil {
		r.logger.Error("Failed to query attachments", zap.Error(err), zap.String("pub_id", pubID.String()))
		return nil, fmt.Errorf("query attachments: %w", err)
	}
	defer rows.Close()

	var list []domain.PublicationAttachment
	for rows.Next() {
		var att domain.PublicationAttachment
		err := rows.Scan(
			&att.ID, &att.PublicationID, &att.FileURL, &att.FileName, &att.FileType, &att.FileSize,
			&att.Caption, &att.OrderIndex, &att.MimeType, &att.ThumbnailURL, &att.VideoProvider, &att.VideoEmbedURL, &att.CreatedAt,
		)
		if err != nil {
			r.logger.Error("Failed to scan attachment", zap.Error(err))
			continue
		}
		list = append(list, att)
	}
	return list, nil
}

func (r *PublicationRepository) insertAttachmentsTx(ctx context.Context, tx pgx.Tx, pubID uuid.UUID, attachments []domain.PublicationAttachment) error {
	if len(attachments) == 0 {
		return nil
	}
	attachQuery := `
		INSERT INTO publication_attachments (
			id, publication_id, file_url, file_name, file_type, file_size,
			caption, order_index, mime_type, thumbnail_url, video_provider, video_embed_url, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
	`
	for i := range attachments {
		var ownerID int64
		if err := tx.QueryRow(ctx, `SELECT author_id FROM publications WHERE id=$1`, pubID).Scan(&ownerID); err != nil {
			return err
		}
		mediaURLs := []string{attachments[i].FileURL}
		if attachments[i].ThumbnailURL != nil {
			mediaURLs = append(mediaURLs, *attachments[i].ThumbnailURL)
		}
		for _, raw := range mediaURLs {
			key := ownedPublicationObject(ownerID, raw)
			if key == "" {
				continue
			}
			var queued bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM publication_object_cleanup WHERE object_name=$1)`, key).Scan(&queued); err != nil {
				return err
			}
			if queued {
				return fmt.Errorf("attachment is pending deletion")
			}
		}
		attID := attachments[i].ID
		if attID == uuid.Nil {
			attID = uuid.New()
			attachments[i].ID = attID
		}
		attachments[i].PublicationID = pubID
		createdAt := attachments[i].CreatedAt
		if createdAt.IsZero() {
			createdAt = time.Now()
			attachments[i].CreatedAt = createdAt
		}
		orderIndex := attachments[i].OrderIndex
		if orderIndex == 0 {
			orderIndex = i
			attachments[i].OrderIndex = orderIndex
		}

		_, err := tx.Exec(ctx, attachQuery,
			attID, pubID, attachments[i].FileURL,
			attachments[i].FileName, attachments[i].FileType, attachments[i].FileSize,
			attachments[i].Caption, orderIndex, attachments[i].MimeType,
			attachments[i].ThumbnailURL, attachments[i].VideoProvider, attachments[i].VideoEmbedURL,
			createdAt,
		)
		if err != nil {
			r.logger.Error("Failed to insert attachment", zap.Error(err))
			return fmt.Errorf("insert attachment: %w", err)
		}
	}
	return nil
}

func (r *PublicationRepository) AddAttachment(ctx context.Context, att *domain.PublicationAttachment) error {
	if att.ID == uuid.Nil {
		att.ID = uuid.New()
	}
	if att.CreatedAt.IsZero() {
		att.CreatedAt = time.Now()
	}
	query := `
		INSERT INTO publication_attachments (
			id, publication_id, file_url, file_name, file_type, file_size,
			caption, order_index, mime_type, thumbnail_url, video_provider, video_embed_url, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
	`
	_, err := r.db.Exec(ctx, query,
		att.ID, att.PublicationID, att.FileURL, att.FileName, att.FileType, att.FileSize,
		att.Caption, att.OrderIndex, att.MimeType, att.ThumbnailURL, att.VideoProvider, att.VideoEmbedURL, att.CreatedAt,
	)
	if err != nil {
		r.logger.Error("Failed to add attachment", zap.Error(err), zap.String("pub_id", att.PublicationID.String()))
		return fmt.Errorf("add attachment: %w", err)
	}
	return nil
}

func (r *PublicationRepository) GetAttachmentByID(ctx context.Context, pubID, attachmentID uuid.UUID) (*domain.PublicationAttachment, error) {
	query := `
		SELECT id, publication_id, file_url, file_name, file_type, file_size,
		       caption, order_index, mime_type, thumbnail_url, video_provider, video_embed_url, created_at
		FROM publication_attachments
		WHERE id = $1 AND publication_id = $2
	`
	var att domain.PublicationAttachment
	err := r.db.QueryRow(ctx, query, attachmentID, pubID).Scan(
		&att.ID, &att.PublicationID, &att.FileURL, &att.FileName, &att.FileType, &att.FileSize,
		&att.Caption, &att.OrderIndex, &att.MimeType, &att.ThumbnailURL, &att.VideoProvider, &att.VideoEmbedURL, &att.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("attachment not found")
		}
		return nil, err
	}
	return &att, nil
}

func (r *PublicationRepository) DeleteAttachment(ctx context.Context, pubID, attachmentID uuid.UUID) error {
	query := `DELETE FROM publication_attachments WHERE id = $1 AND publication_id = $2`
	cmd, err := r.db.Exec(ctx, query, attachmentID, pubID)
	if err != nil {
		r.logger.Error("Failed to delete attachment", zap.Error(err), zap.String("attachment_id", attachmentID.String()))
		return fmt.Errorf("delete attachment: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return fmt.Errorf("attachment not found")
	}
	return nil
}

func (r *PublicationRepository) ReorderAttachments(ctx context.Context, pubID uuid.UUID, orderedIDs []uuid.UUID) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if len(orderedIDs) == 0 || len(orderedIDs) > 100 {
		return ErrInvalidPublicationAttachmentOrder
	}
	if err := lockPublication(ctx, tx, pubID); err != nil {
		return err
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM publication_attachments WHERE publication_id=$1`, pubID).Scan(&count); err != nil {
		return err
	}
	if count != len(orderedIDs) {
		return fmt.Errorf("%w: must include every attachment", ErrInvalidPublicationAttachmentOrder)
	}
	seen := map[uuid.UUID]bool{}

	query := `UPDATE publication_attachments SET order_index = $1 WHERE id = $2 AND publication_id = $3`
	for idx, id := range orderedIDs {
		if seen[id] {
			return fmt.Errorf("%w: duplicate attachment ID", ErrInvalidPublicationAttachmentOrder)
		}
		seen[id] = true
		cmd, err := tx.Exec(ctx, query, idx, id, pubID)
		if err != nil {
			return fmt.Errorf("update attachment order: %w", err)
		}
		if cmd.RowsAffected() != 1 {
			return fmt.Errorf("%w: attachment not found", ErrInvalidPublicationAttachmentOrder)
		}
	}
	return tx.Commit(ctx)
}

func (r *PublicationRepository) GetNextAttachmentOrderIndex(ctx context.Context, pubID uuid.UUID) (int, error) {
	query := `SELECT COALESCE(MAX(order_index) + 1, 0) FROM publication_attachments WHERE publication_id = $1`
	var nextIdx int
	err := r.db.QueryRow(ctx, query, pubID).Scan(&nextIdx)
	if err != nil {
		return 0, err
	}
	return nextIdx, nil
}

func (r *PublicationRepository) FindByAuthorTitleContent(ctx context.Context, authorID int64, title, content string) (*domain.Publication, error) {
	const query = `
		SELECT id, author_id, approved_by_id, source_type,
		       type, title, content, content_doc, content_html, cover_image_url, status,
		       target_audience, importance,
		       is_pinned, allow_comments, requires_acknowledgement, scheduled_at, published_at,
		       views_count, shares_count, created_at, updated_at
		FROM publications
		WHERE deleted_at IS NULL
		  AND author_id = $1
		  AND title = $2
		  AND COALESCE(content, '') = $3
		ORDER BY published_at DESC
		LIMIT 1
	`

	pub := &domain.Publication{}
	var contentDocRaw []byte
	err := r.db.QueryRow(ctx, query, authorID, title, content).Scan(
		&pub.ID, &pub.AuthorID, &pub.ApprovedByID, &pub.SourceType,
		&pub.Type, &pub.Title, &pub.Content, &contentDocRaw, &pub.ContentHTML, &pub.CoverImageURL, &pub.Status,
		&pub.TargetAudience, &pub.Importance,
		&pub.IsPinned, &pub.AllowComments, &pub.RequiresAcknowledgement, &pub.ScheduledAt, &pub.PublishedAt,
		&pub.ViewsCount, &pub.SharesCount, &pub.CreatedAt, &pub.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("find publication by author/title/content: %w", err)
	}

	if len(contentDocRaw) > 2 && string(contentDocRaw) != "{}" && string(contentDocRaw) != "null" {
		var doc domain.ContentDoc
		if err := json.Unmarshal(contentDocRaw, &doc); err == nil && len(doc.Blocks) > 0 {
			pub.ContentDoc = &doc
		}
	}

	return pub, nil
}

// List retrieves publications with filter support and visibility controls
func (r *PublicationRepository) List(
	ctx context.Context,
	currentUserID int64,
	isSuperAdmin bool,
	currentUserMinistryID *int64,
	currentUserDepartmentID *int64,
	pubType, sourceType string,
	ministryID, stateBodyID, departmentID *int64,
	importance string,
	status string,
	page, limit int,
) ([]domain.Publication, error) {
	var conditions []string
	var args []interface{}

	// $1 = currentUserID
	args = append(args, currentUserID)
	conditions = append(conditions, publicationOrganizationScope("publications.author_id", "$1", true))

	if isSuperAdmin {
		// SuperAdmin can inspect drafts and archives across all audiences.
	} else {
		var audConds []string
		audConds = append(audConds, "target_audience = 'all'", "author_id = $1")

		if currentUserMinistryID != nil {
			args = append(args, *currentUserMinistryID)
			midArg := len(args)
			audConds = append(audConds, fmt.Sprintf(
				"(target_audience IN ('ministry', 'branch') AND (target_ministry_id = $%d OR $%d = ANY(target_ministry_ids)))",
				midArg, midArg,
			))
		}

		if currentUserDepartmentID != nil {
			args = append(args, *currentUserDepartmentID)
			deptArg := len(args)
			audConds = append(audConds, fmt.Sprintf(
				"(target_audience = 'department' AND (target_department_id = $%d OR $%d = ANY(target_department_ids)))",
				deptArg, deptArg,
			))
		}

		conditions = append(conditions, "("+strings.Join(audConds, " OR ")+")")
	}

	if pubType != "" {
		args = append(args, pubType)
		conditions = append(conditions, fmt.Sprintf("type = $%d", len(args)))
	}
	if sourceType != "" {
		args = append(args, sourceType)
		conditions = append(conditions, fmt.Sprintf("source_type = $%d", len(args)))
	}
	if importance != "" {
		args = append(args, importance)
		conditions = append(conditions, fmt.Sprintf("importance = $%d", len(args)))
	}
	if ministryID != nil {
		args = append(args, *ministryID)
		mArg := len(args)
		conditions = append(conditions, fmt.Sprintf("(ministry_id = $%d OR target_ministry_id = $%d OR $%d = ANY(target_ministry_ids))", mArg, mArg, mArg))
	}
	if stateBodyID != nil {
		args = append(args, *stateBodyID)
		conditions = append(conditions, fmt.Sprintf("state_body_id = $%d", len(args)))
	}
	if departmentID != nil {
		args = append(args, *departmentID)
		dArg := len(args)
		conditions = append(conditions, fmt.Sprintf("(department_id = $%d OR target_department_id = $%d OR $%d = ANY(target_department_ids))", dArg, dArg, dArg))
	}

	switch status {
	case domain.PublicationStatusDraft:
		if isSuperAdmin {
			conditions = append(conditions, fmt.Sprintf("status = '%s'", domain.PublicationStatusDraft))
		} else {
			conditions = append(conditions, fmt.Sprintf("status = '%s' AND author_id = $1", domain.PublicationStatusDraft))
		}
	case domain.PublicationStatusScheduled:
		if isSuperAdmin {
			conditions = append(conditions, fmt.Sprintf("status = '%s'", domain.PublicationStatusScheduled))
		} else {
			conditions = append(conditions, fmt.Sprintf("status = '%s' AND author_id = $1", domain.PublicationStatusScheduled))
		}
	case domain.PublicationStatusArchived:
		if isSuperAdmin {
			conditions = append(conditions, fmt.Sprintf("status = '%s'", domain.PublicationStatusArchived))
		} else {
			conditions = append(conditions, fmt.Sprintf("status = '%s' AND author_id = $1", domain.PublicationStatusArchived))
		}
	case "all":
		if !isSuperAdmin {
			conditions = append(conditions, "((status IN ('published', 'scheduled') AND (scheduled_at IS NULL OR scheduled_at <= NOW()) AND published_at <= NOW() AND (archive_at IS NULL OR archive_at > NOW())) OR author_id = $1)")
		}
	default:
		conditions = append(conditions, "status = 'published' AND published_at <= NOW() AND (archive_at IS NULL OR archive_at > NOW())")
	}

	query := `
		SELECT 
			id, author_id, approved_by_id, source_type,
			type, title, content, content_doc, content_html, cover_image_url, status,
			target_audience, target_ministry_id, target_department_id, target_ministry_ids, target_department_ids,
			ministry_id, state_body_id, department_id, importance,
			is_pinned, pinned_at, pinned_by, pinned_until,
			is_banner, banner_until, banner_action_text, banner_action_url, banner_color,
			notification_sent_at,
			allow_comments, requires_acknowledgement, scheduled_at, published_at,
			archive_at, archived_at,
			(SELECT COUNT(*) FROM publication_views WHERE publication_id = publications.id) AS views_count, shares_count, created_at, updated_at,
			(SELECT COUNT(*) FROM publication_likes WHERE publication_id = publications.id) AS likes_count,
			(SELECT COUNT(*) FROM publication_comments WHERE publication_id = publications.id AND deleted_at IS NULL) AS comments_count,
			EXISTS(SELECT 1 FROM publication_likes WHERE publication_id = publications.id AND user_id = $1) AS is_liked,
			EXISTS(SELECT 1 FROM publication_acknowledgements WHERE publication_id = publications.id AND user_id = $1) AS is_acknowledged,
			EXISTS(SELECT 1 FROM publication_banner_dismissals WHERE publication_id = publications.id AND user_id = $1) AS is_banner_dismissed
		FROM publications
		WHERE deleted_at IS NULL
	`

	for _, cond := range conditions {
		query += " AND " + cond
	}

	if status == domain.PublicationStatusArchived {
		query += " ORDER BY COALESCE(archived_at, updated_at) DESC, id DESC"
	} else if status == domain.PublicationStatusDraft {
		query += " ORDER BY updated_at DESC, id DESC"
	} else if status == domain.PublicationStatusScheduled {
		query += " ORDER BY scheduled_at ASC, id DESC"
	} else {
		query += " ORDER BY (is_pinned AND (pinned_until IS NULL OR pinned_until > NOW())) DESC, published_at DESC NULLS LAST, created_at DESC, id DESC"
	}

	// Pagination
	offset := (page - 1) * limit
	args = append(args, limit, offset)
	query += fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(args)-1, len(args))

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		r.logger.Error("Failed to query publications list", zap.Error(err))
		return nil, fmt.Errorf("list publications: %w", err)
	}
	defer rows.Close()

	var publications []domain.Publication
	for rows.Next() {
		var pub domain.Publication
		var contentDocRaw []byte
		err := rows.Scan(
			&pub.ID, &pub.AuthorID, &pub.ApprovedByID, &pub.SourceType,
			&pub.Type, &pub.Title, &pub.Content, &contentDocRaw, &pub.ContentHTML, &pub.CoverImageURL, &pub.Status,
			&pub.TargetAudience, &pub.TargetMinistryID, &pub.TargetDepartmentID, &pub.TargetMinistryIDs, &pub.TargetDepartmentIDs,
			&pub.MinistryID, &pub.StateBodyID, &pub.DepartmentID, &pub.Importance,
			&pub.IsPinned, &pub.PinnedAt, &pub.PinnedByID, &pub.PinnedUntil,
			&pub.IsBanner, &pub.BannerUntil, &pub.BannerActionText, &pub.BannerActionURL, &pub.BannerColor,
			&pub.NotificationSentAt,
			&pub.AllowComments, &pub.RequiresAcknowledgement, &pub.ScheduledAt, &pub.PublishedAt,
			&pub.ArchiveAt, &pub.ArchivedAt,
			&pub.ViewsCount, &pub.SharesCount, &pub.CreatedAt, &pub.UpdatedAt,
			&pub.LikesCount, &pub.CommentsCount, &pub.IsLiked, &pub.IsAcknowledged, &pub.IsBannerDismissed,
		)
		if err != nil {
			r.logger.Error("Failed to scan publication row", zap.Error(err))
			continue
		}
		if len(contentDocRaw) > 2 && string(contentDocRaw) != "{}" && string(contentDocRaw) != "null" {
			var doc domain.ContentDoc
			if err := json.Unmarshal(contentDocRaw, &doc); err == nil && len(doc.Blocks) > 0 {
				pub.ContentDoc = &doc
			}
		}
		publications = append(publications, pub)
	}

	// Load attachments for each publication (since limit is small, this is acceptable,
	// or we can optimize in the future with batched loads).
	for i := range publications {
		attachments, err := r.GetAttachmentsByPubID(ctx, publications[i].ID)
		if err == nil {
			publications[i].Attachments = attachments
		}
	}

	return publications, nil
}

// Delete marks a publication as deleted
func (r *PublicationRepository) Delete(ctx context.Context, id uuid.UUID) error {
	query := `UPDATE publications SET deleted_at = NOW(), updated_at = NOW() WHERE id = $1`
	_, err := r.db.Exec(ctx, query, id)
	if err != nil {
		r.logger.Error("Failed to delete publication", zap.Error(err), zap.String("pub_id", id.String()))
		return fmt.Errorf("delete publication: %w", err)
	}
	return nil
}

// RecordView inserts a view record for the user if it doesn't exist
func (r *PublicationRepository) RecordView(ctx context.Context, pubID uuid.UUID, userID int64) error {
	query := `
		INSERT INTO publication_views (publication_id, user_id, viewed_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (publication_id, user_id) DO NOTHING
	`
	_, err := r.db.Exec(ctx, query, pubID, userID)
	return err
}

// IncrementShares increments shares_count
func (r *PublicationRepository) IncrementShares(ctx context.Context, id uuid.UUID) error {
	query := `UPDATE publications SET shares_count = shares_count + 1 WHERE id = $1`
	_, err := r.db.Exec(ctx, query, id)
	return err
}

// Like adds a user like
func (r *PublicationRepository) Like(ctx context.Context, pubID uuid.UUID, userID int64) error {
	query := `
		INSERT INTO publication_likes (publication_id, user_id, created_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (publication_id, user_id) DO NOTHING
	`
	_, err := r.db.Exec(ctx, query, pubID, userID)
	if err != nil {
		r.logger.Error("Failed to like publication", zap.Error(err), zap.String("pub_id", pubID.String()))
		return fmt.Errorf("like publication: %w", err)
	}
	return nil
}

// Unlike removes a user like
func (r *PublicationRepository) Unlike(ctx context.Context, pubID uuid.UUID, userID int64) error {
	query := `DELETE FROM publication_likes WHERE publication_id = $1 AND user_id = $2`
	_, err := r.db.Exec(ctx, query, pubID, userID)
	if err != nil {
		r.logger.Error("Failed to unlike publication", zap.Error(err), zap.String("pub_id", pubID.String()))
		return fmt.Errorf("unlike publication: %w", err)
	}
	return nil
}

// ListLikes returns users who liked a publication ordered by most recent like.
func (r *PublicationRepository) ListLikes(ctx context.Context, pubID uuid.UUID) ([]domain.PublicationLikeUser, error) {
	query := `
		SELECT
			u.id,
			u.first_name,
			u.last_name,
			TRIM(COALESCE(u.first_name, '') || ' ' || COALESCE(u.last_name, '')) AS full_name,
			u.avatar_url,
			u.position,
			pl.created_at
		FROM publication_likes pl
		JOIN users u ON u.id = pl.user_id
		WHERE pl.publication_id = $1
		ORDER BY pl.created_at DESC, u.id DESC
	`

	rows, err := r.db.Query(ctx, query, pubID)
	if err != nil {
		r.logger.Error("Failed to list publication likes", zap.Error(err), zap.String("pub_id", pubID.String()))
		return nil, fmt.Errorf("list publication likes: %w", err)
	}
	defer rows.Close()

	likes := make([]domain.PublicationLikeUser, 0)
	for rows.Next() {
		var like domain.PublicationLikeUser
		if err := rows.Scan(
			&like.ID,
			&like.FirstName,
			&like.LastName,
			&like.FullName,
			&like.AvatarURL,
			&like.Position,
			&like.LikedAt,
		); err != nil {
			r.logger.Error("Failed to scan publication like user", zap.Error(err), zap.String("pub_id", pubID.String()))
			continue
		}
		likes = append(likes, like)
	}

	return likes, nil
}

// ListViews returns users who viewed a publication ordered by most recent view.
func (r *PublicationRepository) ListViews(ctx context.Context, pubID uuid.UUID) ([]domain.PublicationViewUser, error) {
	query := `
		SELECT
			u.id,
			u.first_name,
			u.last_name,
			TRIM(COALESCE(u.first_name, '') || ' ' || COALESCE(u.last_name, '')) AS full_name,
			u.avatar_url,
			u.position,
			pv.viewed_at
		FROM publication_views pv
		JOIN users u ON u.id = pv.user_id
		WHERE pv.publication_id = $1
		ORDER BY pv.viewed_at DESC, u.id DESC
	`

	rows, err := r.db.Query(ctx, query, pubID)
	if err != nil {
		r.logger.Error("Failed to list publication views", zap.Error(err), zap.String("pub_id", pubID.String()))
		return nil, fmt.Errorf("list publication views: %w", err)
	}
	defer rows.Close()

	views := make([]domain.PublicationViewUser, 0)
	for rows.Next() {
		var view domain.PublicationViewUser
		if err := rows.Scan(
			&view.ID,
			&view.FirstName,
			&view.LastName,
			&view.FullName,
			&view.AvatarURL,
			&view.Position,
			&view.ViewedAt,
		); err != nil {
			r.logger.Error("Failed to scan publication view user", zap.Error(err), zap.String("pub_id", pubID.String()))
			continue
		}
		views = append(views, view)
	}

	return views, nil
}

// AddComment inserts a new comment
// AddComment inserts a new comment or reply
func (r *PublicationRepository) AddComment(ctx context.Context, comment *domain.PublicationComment) error {
	if comment.ID == uuid.Nil {
		comment.ID = uuid.New()
	}
	if comment.CreatedAt.IsZero() {
		comment.CreatedAt = time.Now()
	}
	if comment.UpdatedAt.IsZero() {
		comment.UpdatedAt = comment.CreatedAt
	}
	if comment.Status == "" {
		comment.Status = domain.CommentStatusActive
	}

	query := `
		INSERT INTO publication_comments (
			id, publication_id, author_id, parent_id, reply_to_user_id, content,
			is_pinned, pinned_at, pinned_by, status, is_hidden, moderation_reason,
			moderated_by, moderated_at, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
	`
	_, err := r.db.Exec(ctx, query,
		comment.ID, comment.PublicationID, comment.AuthorID, comment.ParentID, comment.ReplyToUserID, comment.Content,
		comment.IsPinned, comment.PinnedAt, comment.PinnedByID, comment.Status, comment.IsHidden, comment.ModerationReason,
		comment.ModeratedByID, comment.ModeratedAt, comment.CreatedAt, comment.UpdatedAt,
	)
	if err != nil {
		r.logger.Error("Failed to add comment", zap.Error(err), zap.String("pub_id", comment.PublicationID.String()))
		return fmt.Errorf("add comment: %w", err)
	}
	return nil
}

// DeleteComment soft deletes a comment
func (r *PublicationRepository) DeleteComment(ctx context.Context, id uuid.UUID) error {
	query := `UPDATE publication_comments SET deleted_at = NOW(), updated_at = NOW() WHERE id = $1 AND deleted_at IS NULL`
	cmd, err := r.db.Exec(ctx, query, id)
	if err != nil {
		r.logger.Error("Failed to delete comment", zap.Error(err), zap.String("comment_id", id.String()))
		return fmt.Errorf("delete comment: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return fmt.Errorf("comment not found")
	}
	return nil
}

// GetCommentByID retrieves a comment
func (r *PublicationRepository) GetCommentByID(ctx context.Context, id uuid.UUID) (*domain.PublicationComment, error) {
	query := `
		SELECT id, publication_id, author_id, parent_id, reply_to_user_id, content,
		       is_pinned, pinned_at, pinned_by, status, is_hidden, moderation_reason,
		       moderated_by, moderated_at, created_at, updated_at
		FROM publication_comments
		WHERE id = $1 AND deleted_at IS NULL
	`
	var comment domain.PublicationComment
	err := r.db.QueryRow(ctx, query, id).Scan(
		&comment.ID, &comment.PublicationID, &comment.AuthorID, &comment.ParentID, &comment.ReplyToUserID, &comment.Content,
		&comment.IsPinned, &comment.PinnedAt, &comment.PinnedByID, &comment.Status, &comment.IsHidden, &comment.ModerationReason,
		&comment.ModeratedByID, &comment.ModeratedAt, &comment.CreatedAt, &comment.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("comment not found")
		}
		return nil, err
	}
	return &comment, nil
}

// ListComments lists comments for a publication with optional inclusion of hidden comments
func (r *PublicationRepository) ListComments(ctx context.Context, pubID uuid.UUID, includeHidden bool) ([]domain.PublicationComment, error) {
	query := `
		SELECT id, publication_id, author_id, parent_id, reply_to_user_id, content,
		       is_pinned, pinned_at, pinned_by, status, is_hidden, moderation_reason,
		       moderated_by, moderated_at, created_at, updated_at
		FROM publication_comments
		WHERE publication_id = $1 AND deleted_at IS NULL
		ORDER BY is_pinned DESC, created_at ASC
	`
	rows, err := r.db.Query(ctx, query, pubID)
	if err != nil {
		r.logger.Error("Failed to query comments", zap.Error(err), zap.String("pub_id", pubID.String()))
		return nil, fmt.Errorf("query comments: %w", err)
	}
	defer rows.Close()

	var list []domain.PublicationComment
	for rows.Next() {
		var comment domain.PublicationComment
		err := rows.Scan(
			&comment.ID, &comment.PublicationID, &comment.AuthorID, &comment.ParentID, &comment.ReplyToUserID, &comment.Content,
			&comment.IsPinned, &comment.PinnedAt, &comment.PinnedByID, &comment.Status, &comment.IsHidden, &comment.ModerationReason,
			&comment.ModeratedByID, &comment.ModeratedAt, &comment.CreatedAt, &comment.UpdatedAt,
		)
		if err != nil {
			r.logger.Error("Failed to scan comment", zap.Error(err))
			continue
		}
		list = append(list, comment)
	}
	return list, nil
}

// ListReplies retrieves direct replies to a parent comment
func (r *PublicationRepository) ListReplies(ctx context.Context, pubID, parentID uuid.UUID, includeHidden bool) ([]domain.PublicationComment, error) {
	query := `
		SELECT id, publication_id, author_id, parent_id, reply_to_user_id, content,
		       is_pinned, pinned_at, pinned_by, status, is_hidden, moderation_reason,
		       moderated_by, moderated_at, created_at, updated_at
		FROM publication_comments
		WHERE publication_id = $1 AND parent_id = $2 AND deleted_at IS NULL
		ORDER BY created_at ASC
	`
	rows, err := r.db.Query(ctx, query, pubID, parentID)
	if err != nil {
		r.logger.Error("Failed to query comment replies", zap.Error(err), zap.String("parent_id", parentID.String()))
		return nil, fmt.Errorf("query replies: %w", err)
	}
	defer rows.Close()

	var list []domain.PublicationComment
	for rows.Next() {
		var comment domain.PublicationComment
		err := rows.Scan(
			&comment.ID, &comment.PublicationID, &comment.AuthorID, &comment.ParentID, &comment.ReplyToUserID, &comment.Content,
			&comment.IsPinned, &comment.PinnedAt, &comment.PinnedByID, &comment.Status, &comment.IsHidden, &comment.ModerationReason,
			&comment.ModeratedByID, &comment.ModeratedAt, &comment.CreatedAt, &comment.UpdatedAt,
		)
		if err != nil {
			r.logger.Error("Failed to scan reply", zap.Error(err))
			continue
		}
		list = append(list, comment)
	}
	return list, nil
}

// PinComment pins or unpins a comment
func (r *PublicationRepository) PinComment(ctx context.Context, commentID uuid.UUID, userID int64, pin bool) error {
	query := `
		UPDATE publication_comments
		SET is_pinned = $2,
		    pinned_at = CASE WHEN $2 THEN NOW() ELSE NULL END,
		    pinned_by = CASE WHEN $2 THEN $3::bigint ELSE NULL END,
		    updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL
	`
	cmd, err := r.db.Exec(ctx, query, commentID, pin, userID)
	if err != nil {
		r.logger.Error("Failed to pin comment", zap.Error(err), zap.String("comment_id", commentID.String()))
		return fmt.Errorf("pin comment: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return fmt.Errorf("comment not found")
	}
	return nil
}

// ModerateComment updates moderation status and reason of a comment
func (r *PublicationRepository) ModerateComment(ctx context.Context, commentID uuid.UUID, status string, isHidden bool, reason *string, moderatorID int64) error {
	query := `
		UPDATE publication_comments
		SET status = $2::varchar,
		    deleted_at = CASE WHEN $2::varchar = 'deleted_by_moderator' THEN NOW() ELSE deleted_at END,
		    is_hidden = $3,
		    moderation_reason = $4,
		    moderated_by = $5,
		    moderated_at = NOW(),
		    updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL
	`
	cmd, err := r.db.Exec(ctx, query, commentID, status, isHidden, reason, moderatorID)
	if err != nil {
		r.logger.Error("Failed to moderate comment", zap.Error(err), zap.String("comment_id", commentID.String()))
		return fmt.Errorf("moderate comment: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return fmt.Errorf("comment not found")
	}
	return nil
}

// SetCommentsAllowed closes or reopens discussion on publication
func (r *PublicationRepository) SetCommentsAllowed(ctx context.Context, pubID uuid.UUID, allow bool) error {
	query := `UPDATE publications SET allow_comments = $2, updated_at = NOW() WHERE id = $1 AND deleted_at IS NULL`
	cmd, err := r.db.Exec(ctx, query, pubID, allow)
	if err != nil {
		r.logger.Error("Failed to set allow_comments", zap.Error(err), zap.String("pub_id", pubID.String()))
		return fmt.Errorf("set allow_comments: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return fmt.Errorf("publication not found")
	}
	return nil
}

// AddReaction adds an emoji reaction to a publication
func (r *PublicationRepository) AddReaction(ctx context.Context, pubID uuid.UUID, userID int64, reaction string) error {
	if reaction == "👍" {
		return r.setLikeReaction(ctx, pubID, userID, true)
	}
	query := `
		INSERT INTO publication_reactions (publication_id, user_id, reaction, created_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (publication_id, user_id, reaction) DO NOTHING
	`
	_, err := r.db.Exec(ctx, query, pubID, userID, reaction)
	if err != nil {
		r.logger.Error("Failed to add publication reaction", zap.Error(err), zap.String("pub_id", pubID.String()))
		return fmt.Errorf("add publication reaction: %w", err)
	}
	return nil
}

// RemoveReaction removes an emoji reaction from a publication
func (r *PublicationRepository) RemoveReaction(ctx context.Context, pubID uuid.UUID, userID int64, reaction string) error {
	if reaction == "👍" {
		return r.setLikeReaction(ctx, pubID, userID, false)
	}
	query := `DELETE FROM publication_reactions WHERE publication_id = $1 AND user_id = $2 AND reaction = $3`
	_, err := r.db.Exec(ctx, query, pubID, userID, reaction)
	if err != nil {
		r.logger.Error("Failed to remove publication reaction", zap.Error(err), zap.String("pub_id", pubID.String()))
		return fmt.Errorf("remove publication reaction: %w", err)
	}
	return nil
}

// GetReactions returns aggregated reaction summaries and user's reacted emojis
func (r *PublicationRepository) GetReactions(ctx context.Context, pubID uuid.UUID, currentUserID int64) ([]domain.ReactionSummary, []string, error) {
	query := `
		WITH ranked AS (
		 SELECT *, count(*) OVER (PARTITION BY reaction) AS total,
		 bool_or(user_id=$2) OVER (PARTITION BY reaction) AS reacted,
		 row_number() OVER (PARTITION BY reaction ORDER BY created_at,user_id) AS rn
		 FROM publication_reactions WHERE publication_id=$1
		)
		SELECT pr.reaction, pr.user_id, pr.total, pr.reacted,
		       TRIM(COALESCE(u.first_name, '') || ' ' || COALESCE(u.last_name, '')) AS full_name,
		       u.avatar_url
		FROM ranked pr
		JOIN users u ON u.id = pr.user_id
		WHERE pr.rn <= 20
		ORDER BY pr.created_at ASC
	`
	rows, err := r.db.Query(ctx, query, pubID, currentUserID)
	if err != nil {
		r.logger.Error("Failed to get publication reactions", zap.Error(err), zap.String("pub_id", pubID.String()))
		return nil, nil, fmt.Errorf("get publication reactions: %w", err)
	}
	defer rows.Close()

	summaryMap := make(map[string]*domain.ReactionSummary)
	order := make([]string, 0)
	userReactions := make([]string, 0)

	for rows.Next() {
		var reaction, fullName string
		var userID int64
		var avatarURL *string
		var total int
		var reacted bool

		if err := rows.Scan(&reaction, &userID, &total, &reacted, &fullName, &avatarURL); err != nil {
			continue
		}

		s, exists := summaryMap[reaction]
		if !exists {
			s = &domain.ReactionSummary{
				Reaction: reaction,
				Count:    total,
				Users:    make([]domain.ReactionUser, 0),
				Reacted:  reacted,
			}
			summaryMap[reaction] = s
			order = append(order, reaction)
			if reacted {
				userReactions = append(userReactions, reaction)
			}
		}
		if len(s.Users) < 20 {
			s.Users = append(s.Users, domain.ReactionUser{
				ID:       userID,
				FullName: fullName,
				Avatar:   avatarURL,
			})
		}
	}

	result := make([]domain.ReactionSummary, 0, len(order))
	for _, reaction := range order {
		result = append(result, *summaryMap[reaction])
	}
	return result, userReactions, nil
}

// AddCommentReaction adds an emoji reaction to a comment
func (r *PublicationRepository) AddCommentReaction(ctx context.Context, commentID uuid.UUID, userID int64, reaction string) error {
	query := `
		INSERT INTO publication_comment_reactions (comment_id, user_id, reaction, created_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (comment_id, user_id, reaction) DO NOTHING
	`
	_, err := r.db.Exec(ctx, query, commentID, userID, reaction)
	if err != nil {
		r.logger.Error("Failed to add comment reaction", zap.Error(err), zap.String("comment_id", commentID.String()))
		return fmt.Errorf("add comment reaction: %w", err)
	}
	return nil
}

// RemoveCommentReaction removes an emoji reaction from a comment
func (r *PublicationRepository) RemoveCommentReaction(ctx context.Context, commentID uuid.UUID, userID int64, reaction string) error {
	query := `DELETE FROM publication_comment_reactions WHERE comment_id = $1 AND user_id = $2 AND reaction = $3`
	_, err := r.db.Exec(ctx, query, commentID, userID, reaction)
	if err != nil {
		r.logger.Error("Failed to remove comment reaction", zap.Error(err), zap.String("comment_id", commentID.String()))
		return fmt.Errorf("remove comment reaction: %w", err)
	}
	return nil
}

// GetCommentReactions loads reactions for a list of comment IDs
func (r *PublicationRepository) GetCommentReactions(ctx context.Context, commentIDs []uuid.UUID, currentUserID int64) (map[uuid.UUID][]domain.ReactionSummary, error) {
	result := make(map[uuid.UUID][]domain.ReactionSummary, len(commentIDs))
	if len(commentIDs) == 0 {
		return result, nil
	}

	query := `
		WITH ranked AS (
		 SELECT *, count(*) OVER (PARTITION BY comment_id,reaction) AS total,
		 bool_or(user_id=$2) OVER (PARTITION BY comment_id,reaction) AS reacted,
		 row_number() OVER (PARTITION BY comment_id,reaction ORDER BY created_at,user_id) AS rn
		 FROM publication_comment_reactions WHERE comment_id=ANY($1)
		)
		SELECT pcr.comment_id, pcr.reaction, pcr.user_id, pcr.total, pcr.reacted,
		       TRIM(COALESCE(u.first_name, '') || ' ' || COALESCE(u.last_name, '')) AS full_name,
		       u.avatar_url
		FROM ranked pcr
		JOIN users u ON u.id = pcr.user_id
		WHERE pcr.rn <= 20
		ORDER BY pcr.created_at ASC
	`
	rows, err := r.db.Query(ctx, query, commentIDs, currentUserID)
	if err != nil {
		r.logger.Error("Failed to query comment reactions", zap.Error(err))
		return nil, fmt.Errorf("get comment reactions: %w", err)
	}
	defer rows.Close()

	type reactionEntry struct {
		summaryMap map[string]*domain.ReactionSummary
		order      []string
	}
	commentsMap := make(map[uuid.UUID]*reactionEntry)

	for rows.Next() {
		var commentID uuid.UUID
		var reaction, fullName string
		var userID int64
		var avatarURL *string
		var total int
		var reacted bool

		if err := rows.Scan(&commentID, &reaction, &userID, &total, &reacted, &fullName, &avatarURL); err != nil {
			continue
		}

		entry, ok := commentsMap[commentID]
		if !ok {
			entry = &reactionEntry{
				summaryMap: make(map[string]*domain.ReactionSummary),
				order:      make([]string, 0),
			}
			commentsMap[commentID] = entry
		}

		s, exists := entry.summaryMap[reaction]
		if !exists {
			s = &domain.ReactionSummary{
				Reaction: reaction,
				Count:    total,
				Users:    make([]domain.ReactionUser, 0),
				Reacted:  reacted,
			}
			entry.summaryMap[reaction] = s
			entry.order = append(entry.order, reaction)
		}
		if len(s.Users) < 20 {
			s.Users = append(s.Users, domain.ReactionUser{
				ID:       userID,
				FullName: fullName,
				Avatar:   avatarURL,
			})
		}
	}

	for commentID, entry := range commentsMap {
		summaries := make([]domain.ReactionSummary, 0, len(entry.order))
		for _, rx := range entry.order {
			summaries = append(summaries, *entry.summaryMap[rx])
		}
		result[commentID] = summaries
	}

	return result, nil
}

// Acknowledge records that a user has read/acknowledged the publication
func (r *PublicationRepository) Acknowledge(ctx context.Context, pubID uuid.UUID, userID int64) error {
	query := `
		INSERT INTO publication_acknowledgements (publication_id, user_id, acknowledged_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (publication_id, user_id) DO NOTHING
	`
	_, err := r.db.Exec(ctx, query, pubID, userID)
	if err != nil {
		r.logger.Error("Failed to acknowledge publication", zap.Error(err), zap.String("pub_id", pubID.String()))
		return fmt.Errorf("acknowledge publication: %w", err)
	}
	return nil
}

// AcknowledgeWithView commits all acknowledgement side effects together and returns
// the original database timestamp on repeated requests.
func (r *PublicationRepository) AcknowledgeWithView(ctx context.Context, pubID uuid.UUID, userID int64, dismissBanner bool) (time.Time, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return time.Time{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `INSERT INTO publication_acknowledgements (publication_id,user_id)
		VALUES ($1,$2) ON CONFLICT (publication_id,user_id) DO NOTHING`, pubID, userID)
	if err != nil {
		return time.Time{}, fmt.Errorf("acknowledge publication: %w", err)
	}
	var acknowledgedAt time.Time
	if err = tx.QueryRow(ctx, `SELECT acknowledged_at FROM publication_acknowledgements WHERE publication_id=$1 AND user_id=$2`, pubID, userID).Scan(&acknowledgedAt); err != nil {
		return time.Time{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO publication_views (publication_id,user_id)
		VALUES ($1,$2) ON CONFLICT (publication_id,user_id) DO NOTHING`, pubID, userID); err != nil {
		return time.Time{}, fmt.Errorf("record acknowledgement view: %w", err)
	}
	if dismissBanner {
		if _, err = tx.Exec(ctx, `INSERT INTO publication_banner_dismissals (publication_id,user_id)
			VALUES ($1,$2) ON CONFLICT (publication_id,user_id) DO NOTHING`, pubID, userID); err != nil {
			return time.Time{}, fmt.Errorf("dismiss acknowledgement banner: %w", err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return time.Time{}, err
	}
	return acknowledgedAt, nil
}

// Unacknowledge removes user's acknowledgement of the publication.
func (r *PublicationRepository) Unacknowledge(ctx context.Context, pubID uuid.UUID, userID int64) error {
	query := `DELETE FROM publication_acknowledgements WHERE publication_id = $1 AND user_id = $2`
	_, err := r.db.Exec(ctx, query, pubID, userID)
	if err != nil {
		r.logger.Error("Failed to unacknowledge publication", zap.Error(err), zap.String("pub_id", pubID.String()))
		return fmt.Errorf("unacknowledge publication: %w", err)
	}
	return nil
}

// SetStatus updates publication status and publishing timestamps
func (r *PublicationRepository) SetStatus(ctx context.Context, id uuid.UUID, status string, publishedAt time.Time, scheduledAt *time.Time, archiveAt *time.Time) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var archivedAt *time.Time
	if status == domain.PublicationStatusArchived {
		now := time.Now()
		archivedAt = &now
	}

	query := `
		UPDATE publications
		SET status = $2::varchar,
		    published_at = $3,
		    scheduled_at = $4,
		    archive_at = COALESCE($5, archive_at),
		    archived_at = $6,
		    is_pinned = CASE WHEN $2::varchar = 'archived' THEN false ELSE is_pinned END,
		    is_banner = CASE WHEN $2::varchar = 'archived' THEN false ELSE is_banner END,
		    updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL
	`
	commandTag, err := tx.Exec(ctx, query, id, status, publishedAt, scheduledAt, archiveAt, archivedAt)
	if err != nil {
		r.logger.Error("Failed to set publication status", zap.Error(err), zap.String("pub_id", id.String()))
		return fmt.Errorf("set publication status: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		return fmt.Errorf("publication not found")
	}
	if status == domain.PublicationStatusPublished {
		if _, err := tx.Exec(ctx, `INSERT INTO publication_broadcast_queue (publication_id, triggered_by) SELECT id, author_id FROM publications WHERE id=$1 AND importance IN ('important','urgent') AND notification_sent_at IS NULL ON CONFLICT DO NOTHING`, id); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// Archive marks a publication as archived and clears pin/banner
func (r *PublicationRepository) Archive(ctx context.Context, id uuid.UUID, archivedAt time.Time) error {
	query := `
		UPDATE publications
		SET status = $2,
		    archived_at = $3,
		    is_pinned = false,
		    is_banner = false,
		    updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL
	`
	commandTag, err := r.db.Exec(ctx, query, id, domain.PublicationStatusArchived, archivedAt)
	if err != nil {
		r.logger.Error("Failed to archive publication", zap.Error(err), zap.String("pub_id", id.String()))
		return fmt.Errorf("archive publication: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		return fmt.Errorf("publication not found")
	}
	return nil
}

// Unarchive restores an archived publication back to published or draft
func (r *PublicationRepository) Unarchive(ctx context.Context, id uuid.UUID, targetStatus string) error {
	if targetStatus != domain.PublicationStatusDraft && targetStatus != domain.PublicationStatusPublished {
		targetStatus = domain.PublicationStatusPublished
	}
	query := `
		UPDATE publications
		SET status = $2,
		    archived_at = NULL,
		    archive_at = NULL,
		    scheduled_at = NULL,
		    published_at = CASE WHEN $2::varchar = 'published' THEN NOW() ELSE published_at END,
		    updated_at = NOW()
		WHERE id = $1 AND status = 'archived' AND deleted_at IS NULL
	`
	commandTag, err := r.db.Exec(ctx, query, id, targetStatus)
	if err != nil {
		r.logger.Error("Failed to unarchive publication", zap.Error(err), zap.String("pub_id", id.String()))
		return fmt.Errorf("unarchive publication: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		return fmt.Errorf("publication not found")
	}
	return nil
}

// ArchiveObsolete moves publications published before cutoff to archived status
func (r *PublicationRepository) ArchiveObsolete(ctx context.Context, cutoff time.Time, types []string) (int64, error) {
	query := `
		UPDATE publications
		SET status = $1,
		    archived_at = NOW(),
		    is_pinned = false,
		    is_banner = false,
		    updated_at = NOW()
		WHERE status = 'published'
		  AND published_at < $2
		  AND deleted_at IS NULL
	`
	args := []any{domain.PublicationStatusArchived, cutoff}
	if len(types) > 0 {
		query += " AND type = ANY($3)"
		args = append(args, types)
	}
	commandTag, err := r.db.Exec(ctx, query, args...)
	if err != nil {
		r.logger.Error("Failed to archive obsolete publications", zap.Error(err))
		return 0, fmt.Errorf("archive obsolete publications: %w", err)
	}
	return commandTag.RowsAffected(), nil
}

// SetCoverImage updates publication cover image URL
func (r *PublicationRepository) SetCoverImage(ctx context.Context, id uuid.UUID, coverImageURL *string) error {
	query := `
		UPDATE publications
		SET cover_image_url = $2,
		    updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL
	`
	commandTag, err := r.db.Exec(ctx, query, id, coverImageURL)
	if err != nil {
		r.logger.Error("Failed to set publication cover image", zap.Error(err), zap.String("pub_id", id.String()))
		return fmt.Errorf("set publication cover image: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		return fmt.Errorf("publication not found")
	}
	return nil
}

// PinPublication pins a publication to the top of feeds until specified time or indefinitely
func (r *PublicationRepository) PinPublication(ctx context.Context, id uuid.UUID, userID int64, pinnedUntil *time.Time) error {
	query := `
		UPDATE publications
		SET is_pinned = true,
		    pinned_at = NOW(),
		    pinned_by = $2,
		    pinned_until = $3,
		    updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL
	`
	cmd, err := r.db.Exec(ctx, query, id, userID, pinnedUntil)
	if err != nil {
		r.logger.Error("Failed to pin publication", zap.Error(err), zap.String("pub_id", id.String()))
		return fmt.Errorf("pin publication: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return fmt.Errorf("publication not found")
	}
	return nil
}

// UnpinPublication unpins a publication
func (r *PublicationRepository) UnpinPublication(ctx context.Context, id uuid.UUID) error {
	query := `
		UPDATE publications
		SET is_pinned = false,
		    pinned_at = NULL,
		    pinned_by = NULL,
		    pinned_until = NULL,
		    updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL
	`
	cmd, err := r.db.Exec(ctx, query, id)
	if err != nil {
		r.logger.Error("Failed to unpin publication", zap.Error(err), zap.String("pub_id", id.String()))
		return fmt.Errorf("unpin publication: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return fmt.Errorf("publication not found")
	}
	return nil
}

// SetBanner configures banner mode and action buttons for a publication
func (r *PublicationRepository) SetBanner(ctx context.Context, id uuid.UUID, isBanner bool, until *time.Time, actionText, actionURL, color *string) error {
	query := `
		UPDATE publications
		SET is_banner = $2,
		    banner_until = $3,
		    banner_action_text = $4,
		    banner_action_url = $5,
		    banner_color = COALESCE($6, banner_color, 'urgent'),
		    updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL
	`
	cmd, err := r.db.Exec(ctx, query, id, isBanner, until, actionText, actionURL, color)
	if err != nil {
		r.logger.Error("Failed to set publication banner", zap.Error(err), zap.String("pub_id", id.String()))
		return fmt.Errorf("set publication banner: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return fmt.Errorf("publication not found")
	}
	return nil
}

// DismissBanner records that a user closed a banner in their UI
func (r *PublicationRepository) DismissBanner(ctx context.Context, pubID uuid.UUID, userID int64) error {
	query := `
		INSERT INTO publication_banner_dismissals (publication_id, user_id, dismissed_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (publication_id, user_id) DO NOTHING
	`
	_, err := r.db.Exec(ctx, query, pubID, userID)
	if err != nil {
		r.logger.Error("Failed to dismiss publication banner", zap.Error(err), zap.String("pub_id", pubID.String()))
		return fmt.Errorf("dismiss banner: %w", err)
	}
	return nil
}

// ListBanners retrieves active banners visible to the user
func (r *PublicationRepository) ListBanners(ctx context.Context, userID int64, ministryID *int64, departmentID *int64) ([]domain.Publication, error) {
	query := `
		SELECT
			id, author_id, approved_by_id, source_type,
			type, title, content, content_doc, content_html, cover_image_url, status,
			target_audience, target_ministry_id, target_department_id, target_ministry_ids, target_department_ids,
			ministry_id, state_body_id, department_id, importance,
			is_pinned, pinned_at, pinned_by, pinned_until,
			is_banner, banner_until, banner_action_text, banner_action_url, banner_color,
			notification_sent_at,
			allow_comments, requires_acknowledgement, scheduled_at, published_at,
			archive_at, archived_at,
			(SELECT COUNT(*) FROM publication_views WHERE publication_id = publications.id) AS views_count, shares_count, created_at, updated_at,
			(SELECT COUNT(*) FROM publication_likes WHERE publication_id = publications.id) AS likes_count,
			(SELECT COUNT(*) FROM publication_comments WHERE publication_id = publications.id AND deleted_at IS NULL) AS comments_count,
			EXISTS(SELECT 1 FROM publication_likes WHERE publication_id = publications.id AND user_id = $1) AS is_liked,
			EXISTS(SELECT 1 FROM publication_acknowledgements WHERE publication_id = publications.id AND user_id = $1) AS is_acknowledged,
			EXISTS(SELECT 1 FROM publication_banner_dismissals WHERE publication_id = publications.id AND user_id = $1) AS is_banner_dismissed
		FROM publications
		WHERE deleted_at IS NULL
		  AND ` + publicationOrganizationScope("publications.author_id", "$1", true) + `
		  AND is_banner = true
		  AND status = 'published'
		  AND published_at <= NOW()
		  AND (archive_at IS NULL OR archive_at > NOW())
		  AND (banner_until IS NULL OR banner_until > NOW())
		  AND (
			target_audience = 'all'
			OR ($2::bigint IS NOT NULL AND target_audience IN ('ministry', 'branch') AND (target_ministry_id = $2 OR $2 = ANY(target_ministry_ids)))
			OR ($3::bigint IS NOT NULL AND target_audience = 'department' AND (target_department_id = $3 OR $3 = ANY(target_department_ids)))
		  )
		AND (
			(requires_acknowledgement = true AND NOT EXISTS (SELECT 1 FROM publication_acknowledgements WHERE publication_id = publications.id AND user_id = $1))
			OR (requires_acknowledgement = false AND NOT EXISTS (SELECT 1 FROM publication_banner_dismissals WHERE publication_id = publications.id AND user_id = $1))
		)
		ORDER BY
			CASE WHEN importance = 'urgent' THEN 1 WHEN importance = 'important' THEN 2 ELSE 3 END ASC,
			published_at DESC
	`
	rows, err := r.db.Query(ctx, query, userID, ministryID, departmentID)
	if err != nil {
		r.logger.Error("Failed to list publication banners", zap.Error(err))
		return nil, fmt.Errorf("list publication banners: %w", err)
	}
	defer rows.Close()

	var list []domain.Publication
	for rows.Next() {
		var pub domain.Publication
		var contentDocRaw []byte
		err := rows.Scan(
			&pub.ID, &pub.AuthorID, &pub.ApprovedByID, &pub.SourceType,
			&pub.Type, &pub.Title, &pub.Content, &contentDocRaw, &pub.ContentHTML, &pub.CoverImageURL, &pub.Status,
			&pub.TargetAudience, &pub.TargetMinistryID, &pub.TargetDepartmentID, &pub.TargetMinistryIDs, &pub.TargetDepartmentIDs,
			&pub.MinistryID, &pub.StateBodyID, &pub.DepartmentID, &pub.Importance,
			&pub.IsPinned, &pub.PinnedAt, &pub.PinnedByID, &pub.PinnedUntil,
			&pub.IsBanner, &pub.BannerUntil, &pub.BannerActionText, &pub.BannerActionURL, &pub.BannerColor,
			&pub.NotificationSentAt,
			&pub.AllowComments, &pub.RequiresAcknowledgement, &pub.ScheduledAt, &pub.PublishedAt,
			&pub.ArchiveAt, &pub.ArchivedAt,
			&pub.ViewsCount, &pub.SharesCount, &pub.CreatedAt, &pub.UpdatedAt,
			&pub.LikesCount, &pub.CommentsCount, &pub.IsLiked, &pub.IsAcknowledged, &pub.IsBannerDismissed,
		)
		if err != nil {
			return nil, err
		}
		if len(contentDocRaw) > 2 && string(contentDocRaw) != "{}" && string(contentDocRaw) != "null" {
			var doc domain.ContentDoc
			if err := json.Unmarshal(contentDocRaw, &doc); err == nil {
				pub.ContentDoc = &doc
			}
		}
		list = append(list, pub)
	}
	return list, nil
}

type TargetRecipient struct {
	Language  string
	ID        int64
	Email     string
	FirstName string
	LastName  string
}

// GetTargetAudienceRecipients retrieves active users matching audience targeting
func (r *PublicationRepository) GetTargetAudienceRecipients(ctx context.Context, pub *domain.Publication) ([]TargetRecipient, error) {
	query := `
		SELECT id, email, first_name, last_name, COALESCE(language, 'ru')
		FROM users
		WHERE is_active = true AND deleted_at IS NULL
	`
	var args []interface{}
	filter, targetArgs, err := audienceUserFilter(pub, "", 1)
	if err != nil {
		return nil, err
	}
	query += filter
	args = append(args, targetArgs...)

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		r.logger.Error("Failed to query target audience recipients", zap.Error(err))
		return nil, fmt.Errorf("get target recipients: %w", err)
	}
	defer rows.Close()

	var recipients []TargetRecipient
	for rows.Next() {
		var rec TargetRecipient
		if err := rows.Scan(&rec.ID, &rec.Email, &rec.FirstName, &rec.LastName, &rec.Language); err != nil {
			return nil, err
		}
		recipients = append(recipients, rec)
	}
	return recipients, rows.Err()
}

// RecordBroadcast records a push/email broadcast event
func (r *PublicationRepository) RecordBroadcast(ctx context.Context, pubID uuid.UUID, userID int64, importance, audience string, pushCount, emailCount int, status string, errMsg *string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	query := `
		INSERT INTO publication_broadcasts (
			publication_id, triggered_by, importance, target_audience,
			sent_push_count, sent_email_count, sent_at, status, error_message
		) VALUES (
			$1, $2, $3, $4, $5, $6, NOW(), $7, $8
		)
	`
	_, err = tx.Exec(ctx, query, pubID, userID, importance, audience, pushCount, emailCount, status, errMsg)
	if err != nil {
		r.logger.Error("Failed to record broadcast", zap.Error(err))
		return fmt.Errorf("record broadcast: %w", err)
	}

	if status == "completed" {
		if _, err := tx.Exec(ctx, `UPDATE publications SET notification_sent_at = NOW() WHERE id = $1`, pubID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// AcquireBroadcastLock serializes delivery across API requests and scheduler instances.
func (r *PublicationRepository) AcquireBroadcastLock(ctx context.Context, pubID uuid.UUID) (func(), bool, error) {
	conn, err := r.db.Acquire(ctx)
	if err != nil {
		return nil, false, err
	}
	var acquired bool
	err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1::text, 0))`, pubID.String()).Scan(&acquired)
	if err != nil || !acquired {
		conn.Release()
		return nil, false, err
	}
	return func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var released bool
		if err := conn.QueryRow(releaseCtx, `SELECT pg_advisory_unlock(hashtextextended($1::text, 0))`, pubID.String()).Scan(&released); err != nil || !released {
			conn.Conn().Close(context.Background())
		}
		conn.Release()
	}, true, nil
}

func (r *PublicationRepository) HasBroadcastDelivery(ctx context.Context, pubID uuid.UUID, userID int64, channel string) (bool, error) {
	var exists bool
	err := r.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM publication_broadcast_deliveries WHERE publication_id=$1 AND user_id=$2 AND channel=$3)`, pubID, userID, channel).Scan(&exists)
	return exists, err
}

func (r *PublicationRepository) RecordBroadcastDelivery(ctx context.Context, pubID uuid.UUID, userID int64, channel string) error {
	_, err := r.db.Exec(ctx, `INSERT INTO publication_broadcast_deliveries (publication_id,user_id,channel) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, pubID, userID, channel)
	return err
}

type PendingPublicationBroadcast struct {
	PublicationID uuid.UUID
	TriggeredBy   int64
}

func (r *PublicationRepository) PendingBroadcasts(ctx context.Context) ([]PendingPublicationBroadcast, error) {
	rows, err := r.db.Query(ctx, `SELECT q.publication_id,q.triggered_by FROM publication_broadcast_queue q JOIN publications p ON p.id=q.publication_id WHERE q.next_attempt_at <= NOW() AND p.status='published' AND p.published_at <= NOW() AND (p.scheduled_at IS NULL OR p.scheduled_at <= NOW()) AND (p.archive_at IS NULL OR p.archive_at > NOW()) AND p.deleted_at IS NULL ORDER BY q.next_attempt_at LIMIT 20`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var pending []PendingPublicationBroadcast
	for rows.Next() {
		var item PendingPublicationBroadcast
		if err := rows.Scan(&item.PublicationID, &item.TriggeredBy); err != nil {
			return nil, err
		}
		pending = append(pending, item)
	}
	return pending, rows.Err()
}

func (r *PublicationRepository) CompleteQueuedBroadcast(ctx context.Context, pubID uuid.UUID) error {
	_, err := r.db.Exec(ctx, `DELETE FROM publication_broadcast_queue WHERE publication_id=$1`, pubID)
	return err
}

func (r *PublicationRepository) RetryQueuedBroadcast(ctx context.Context, pubID uuid.UUID, cause error) error {
	_, err := r.db.Exec(ctx, `UPDATE publication_broadcast_queue SET attempts=attempts+1, last_error=$2, next_attempt_at=NOW()+LEAST(INTERVAL '1 hour', INTERVAL '1 minute' * POWER(2, LEAST(attempts, 6))) WHERE publication_id=$1`, pubID, cause.Error())
	return err
}

// GetBroadcastStats returns delivery and acknowledgement stats for a publication
func (r *PublicationRepository) GetBroadcastStats(ctx context.Context, pubID uuid.UUID) (*domain.PublicationBroadcastStatsResponse, error) {
	var authorID int64
	var targetAudience string
	var importance string
	var targetMinistryID *int64
	var targetDepartmentID *int64
	var targetMinistryIDs []int64
	var targetDepartmentIDs []int64
	err := r.db.QueryRow(ctx, `SELECT author_id, target_audience, importance, target_ministry_id, target_department_id, target_ministry_ids, target_department_ids FROM publications WHERE id = $1 AND deleted_at IS NULL`, pubID).
		Scan(&authorID, &targetAudience, &importance, &targetMinistryID, &targetDepartmentID, &targetMinistryIDs, &targetDepartmentIDs)
	if err != nil {
		return nil, fmt.Errorf("get publication for stats: %w", err)
	}

	targetCountQuery := `SELECT COUNT(*) FROM users WHERE is_active = true AND deleted_at IS NULL`
	var targetArgs []interface{}
	audience := &domain.Publication{AuthorID: authorID, TargetAudience: targetAudience, TargetMinistryID: targetMinistryID, TargetDepartmentID: targetDepartmentID, TargetMinistryIDs: targetMinistryIDs, TargetDepartmentIDs: targetDepartmentIDs}
	filter, targetArgs, err := audienceUserFilter(audience, "", 1)
	if err != nil {
		return nil, err
	}
	targetCountQuery += filter

	var totalTargetUsers int
	if err := r.db.QueryRow(ctx, targetCountQuery, targetArgs...).Scan(&totalTargetUsers); err != nil {
		return nil, err
	}

	var ackCount int
	ackCountQuery := `SELECT COUNT(*) FROM publication_acknowledgements a JOIN users u ON u.id=a.user_id WHERE a.publication_id=$1 AND u.is_active=true AND u.deleted_at IS NULL`
	ackArgs := []interface{}{pubID}
	filter, audienceArgs, err := audienceUserFilter(audience, "u.", 2)
	if err != nil {
		return nil, err
	}
	ackCountQuery += filter
	ackArgs = append(ackArgs, audienceArgs...)
	if err := r.db.QueryRow(ctx, ackCountQuery, ackArgs...).Scan(&ackCount); err != nil {
		return nil, err
	}

	rows, err := r.db.Query(ctx, `
		SELECT b.id, b.publication_id, COALESCE(b.triggered_by,0), COALESCE(u.first_name,''), COALESCE(u.last_name,''), u.avatar_url, u.position,
		       b.importance, b.target_audience, b.sent_push_count, b.sent_email_count, b.sent_at, b.status, b.error_message
		FROM publication_broadcasts b
		LEFT JOIN users u ON u.id = b.triggered_by
		WHERE b.publication_id = $1
		ORDER BY b.sent_at DESC
	`, pubID)
	if err != nil {
		return nil, fmt.Errorf("query broadcasts: %w", err)
	}
	defer rows.Close()

	var broadcasts []domain.BroadcastRecord
	var totalPush, totalEmail int
	var lastBroadcastAt *time.Time

	for rows.Next() {
		var rec domain.BroadcastRecord
		var u domain.UserMini
		err := rows.Scan(
			&rec.ID, &rec.PublicationID, &u.ID, &u.FirstName, &u.LastName, &u.AvatarURL, &u.Position,
			&rec.Importance, &rec.TargetAudience, &rec.SentPushCount, &rec.SentEmailCount, &rec.SentAt, &rec.Status, &rec.ErrorMessage,
		)
		if err != nil {
			return nil, err
		}
		u.FullName = u.FirstName + " " + u.LastName
		rec.TriggeredBy = u
		broadcasts = append(broadcasts, rec)
		totalPush += rec.SentPushCount
		totalEmail += rec.SentEmailCount
		if lastBroadcastAt == nil {
			t := rec.SentAt
			lastBroadcastAt = &t
		}
	}

	var ackPercent float64
	if totalTargetUsers > 0 {
		ackPercent = float64(ackCount) / float64(totalTargetUsers) * 100.0
		if ackPercent > 100.0 {
			ackPercent = 100.0
		}
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}
	return &domain.PublicationBroadcastStatsResponse{
		PublicationID:          pubID,
		Importance:             importance,
		TotalTargetUsers:       totalTargetUsers,
		AcknowledgedCount:      ackCount,
		AcknowledgementPercent: ackPercent,
		SentPushCount:          totalPush,
		SentEmailCount:         totalEmail,
		LastBroadcastAt:        lastBroadcastAt,
		Broadcasts:             broadcasts,
	}, nil
}

// ListAcknowledgements returns users who acknowledged reading the publication
func (r *PublicationRepository) ListAcknowledgements(ctx context.Context, pubID uuid.UUID, page, limit int) ([]domain.PublicationAcknowledgementUser, int, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 50
	}
	offset := (page - 1) * limit

	var total int
	err := r.db.QueryRow(ctx, `SELECT COUNT(*) FROM publication_acknowledgements WHERE publication_id = $1`, pubID).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	rows, err := r.db.Query(ctx, `
		SELECT a.user_id, u.first_name, u.last_name, u.avatar_url, u.position, a.acknowledged_at
		FROM publication_acknowledgements a
		JOIN users u ON u.id = a.user_id
		WHERE a.publication_id = $1
		ORDER BY a.acknowledged_at DESC
		LIMIT $2 OFFSET $3
	`, pubID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var acks []domain.PublicationAcknowledgementUser
	for rows.Next() {
		var ack domain.PublicationAcknowledgementUser
		err := rows.Scan(&ack.User.ID, &ack.User.FirstName, &ack.User.LastName, &ack.User.AvatarURL, &ack.User.Position, &ack.AcknowledgedAt)
		if err != nil {
			return nil, 0, err
		}
		ack.User.FullName = ack.User.FirstName + " " + ack.User.LastName
		acks = append(acks, ack)
	}

	return acks, total, nil
}

// GetPublicationAnalytics returns complete reading and acknowledgement statistics for a publication
func (r *PublicationRepository) GetPublicationAnalytics(ctx context.Context, pubID uuid.UUID) (*domain.PublicationAnalyticsResponse, error) {
	var (
		authorID                int64
		title                   string
		requiresAcknowledgement bool
		targetAudience          string
		targetMinistryID        *int64
		targetDepartmentID      *int64
		targetMinistryIDs       []int64
		targetDepartmentIDs     []int64
	)
	err := r.db.QueryRow(ctx, `
		SELECT author_id, title, requires_acknowledgement, target_audience, target_ministry_id, target_department_id, target_ministry_ids, target_department_ids
		FROM publications
		WHERE id = $1 AND deleted_at IS NULL
	`, pubID).Scan(&authorID, &title, &requiresAcknowledgement, &targetAudience, &targetMinistryID, &targetDepartmentID, &targetMinistryIDs, &targetDepartmentIDs)
	if err != nil {
		return nil, fmt.Errorf("get publication for analytics: %w", err)
	}

	audience := &domain.Publication{
		AuthorID:            authorID,
		TargetAudience:      targetAudience,
		TargetMinistryID:    targetMinistryID,
		TargetDepartmentID:  targetDepartmentID,
		TargetMinistryIDs:   targetMinistryIDs,
		TargetDepartmentIDs: targetDepartmentIDs,
	}

	// 1. Total targeted users
	targetCountQuery := `SELECT COUNT(*) FROM users WHERE is_active = true AND deleted_at IS NULL`
	filter, targetArgs, err := audienceUserFilter(audience, "", 1)
	if err != nil {
		return nil, err
	}
	targetCountQuery += filter

	var totalTargetUsers int
	if err := r.db.QueryRow(ctx, targetCountQuery, targetArgs...).Scan(&totalTargetUsers); err != nil {
		return nil, fmt.Errorf("count target users: %w", err)
	}

	// 2. Unique views count and min/max timestamps
	viewQuery := `
		SELECT COUNT(DISTINCT v.user_id), MIN(v.viewed_at), MAX(v.viewed_at)
		FROM publication_views v
		JOIN users u ON u.id = v.user_id
		WHERE v.publication_id = $1 AND u.is_active = true AND u.deleted_at IS NULL
	`
	viewArgs := []interface{}{pubID}
	filter, audienceArgs, err := audienceUserFilter(audience, "u.", 2)
	if err != nil {
		return nil, err
	}
	viewQuery += filter
	viewArgs = append(viewArgs, audienceArgs...)

	var (
		viewsCount  int
		firstViewAt *time.Time
		lastViewAt  *time.Time
	)
	if err := r.db.QueryRow(ctx, viewQuery, viewArgs...).Scan(&viewsCount, &firstViewAt, &lastViewAt); err != nil {
		return nil, fmt.Errorf("query view analytics: %w", err)
	}

	// 3. Unique acknowledgements count and min/max timestamps
	ackQuery := `
		SELECT COUNT(DISTINCT a.user_id), MIN(a.acknowledged_at), MAX(a.acknowledged_at)
		FROM publication_acknowledgements a
		JOIN users u ON u.id = a.user_id
		WHERE a.publication_id = $1 AND u.is_active = true AND u.deleted_at IS NULL
	`
	ackArgs := []interface{}{pubID}
	filter, ackAudArgs, err := audienceUserFilter(audience, "u.", 2)
	if err != nil {
		return nil, err
	}
	ackQuery += filter
	ackArgs = append(ackArgs, ackAudArgs...)

	var (
		ackCount   int
		firstAckAt *time.Time
		lastAckAt  *time.Time
	)
	if err := r.db.QueryRow(ctx, ackQuery, ackArgs...).Scan(&ackCount, &firstAckAt, &lastAckAt); err != nil {
		return nil, fmt.Errorf("query ack analytics: %w", err)
	}

	// 4. Department breakdown
	deptQuery := `
		SELECT
			u.department_id,
			COUNT(DISTINCT u.id) AS total_users,
			COUNT(DISTINCT v.user_id) AS views_count,
			COUNT(DISTINCT a.user_id) AS ack_count
		FROM users u
		LEFT JOIN publication_views v ON v.publication_id = $1 AND v.user_id = u.id
		LEFT JOIN publication_acknowledgements a ON a.publication_id = $1 AND a.user_id = u.id
		WHERE u.is_active = true AND u.deleted_at IS NULL AND u.department_id IS NOT NULL
	`
	deptArgs := []interface{}{pubID}
	filter, deptAudArgs, err := audienceUserFilter(audience, "u.", 2)
	if err == nil {
		deptQuery += filter
		deptArgs = append(deptArgs, deptAudArgs...)
	}
	deptQuery += ` GROUP BY u.department_id ORDER BY total_users DESC, views_count DESC`

	var deptStats []domain.PublicationDepartmentAnalytics
	rows, err := r.db.Query(ctx, deptQuery, deptArgs...)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var (
				deptID     int64
				totalUsers int
				vCount     int
				aCount     int
			)
			if err := rows.Scan(&deptID, &totalUsers, &vCount, &aCount); err == nil {
				var vPct, aPct float64
				if totalUsers > 0 {
					vPct = float64(vCount) / float64(totalUsers) * 100.0
					if vPct > 100.0 {
						vPct = 100.0
					}
					aPct = float64(aCount) / float64(totalUsers) * 100.0
					if aPct > 100.0 {
						aPct = 100.0
					}
				}
				deptStats = append(deptStats, domain.PublicationDepartmentAnalytics{
					DepartmentID:           deptID,
					DepartmentName:         fmt.Sprintf("Отдел #%d", deptID),
					TotalUsers:             totalUsers,
					ViewsCount:             vCount,
					ViewPercent:            vPct,
					AcknowledgedCount:      aCount,
					AcknowledgementPercent: aPct,
				})
			}
		}
	}

	// 5. Ministry breakdown
	minQuery := `
		SELECT
			u.ministry_id,
			COUNT(DISTINCT u.id) AS total_users,
			COUNT(DISTINCT v.user_id) AS views_count,
			COUNT(DISTINCT a.user_id) AS ack_count
		FROM users u
		LEFT JOIN publication_views v ON v.publication_id = $1 AND v.user_id = u.id
		LEFT JOIN publication_acknowledgements a ON a.publication_id = $1 AND a.user_id = u.id
		WHERE u.is_active = true AND u.deleted_at IS NULL AND u.ministry_id IS NOT NULL
	`
	minArgs := []interface{}{pubID}
	filter, minAudArgs, err := audienceUserFilter(audience, "u.", 2)
	if err == nil {
		minQuery += filter
		minArgs = append(minArgs, minAudArgs...)
	}
	minQuery += ` GROUP BY u.ministry_id ORDER BY total_users DESC, views_count DESC`

	var minStats []domain.PublicationMinistryAnalytics
	mRows, err := r.db.Query(ctx, minQuery, minArgs...)
	if err == nil {
		defer mRows.Close()
		for mRows.Next() {
			var (
				minID      int64
				totalUsers int
				vCount     int
				aCount     int
			)
			if err := mRows.Scan(&minID, &totalUsers, &vCount, &aCount); err == nil {
				var vPct, aPct float64
				if totalUsers > 0 {
					vPct = float64(vCount) / float64(totalUsers) * 100.0
					if vPct > 100.0 {
						vPct = 100.0
					}
					aPct = float64(aCount) / float64(totalUsers) * 100.0
					if aPct > 100.0 {
						aPct = 100.0
					}
				}
				minStats = append(minStats, domain.PublicationMinistryAnalytics{
					MinistryID:             minID,
					MinistryName:           fmt.Sprintf("Филиал #%d", minID),
					TotalUsers:             totalUsers,
					ViewsCount:             vCount,
					ViewPercent:            vPct,
					AcknowledgedCount:      aCount,
					AcknowledgementPercent: aPct,
				})
			}
		}
	}

	var viewPercent, ackPercent float64
	if totalTargetUsers > 0 {
		viewPercent = float64(viewsCount) / float64(totalTargetUsers) * 100.0
		if viewPercent > 100.0 {
			viewPercent = 100.0
		}
		ackPercent = float64(ackCount) / float64(totalTargetUsers) * 100.0
		if ackPercent > 100.0 {
			ackPercent = 100.0
		}
	}
	unreadCount := totalTargetUsers - viewsCount
	if unreadCount < 0 {
		unreadCount = 0
	}
	unackCount := totalTargetUsers - ackCount
	if unackCount < 0 {
		unackCount = 0
	}

	if deptStats == nil {
		deptStats = []domain.PublicationDepartmentAnalytics{}
	}
	if minStats == nil {
		minStats = []domain.PublicationMinistryAnalytics{}
	}

	return &domain.PublicationAnalyticsResponse{
		PublicationID:           pubID,
		Title:                   title,
		RequiresAcknowledgement: requiresAcknowledgement,
		TargetAudience:          targetAudience,
		TotalTargetUsers:        totalTargetUsers,
		ViewsCount:              viewsCount,
		ViewPercent:             viewPercent,
		AcknowledgedCount:       ackCount,
		AcknowledgementPercent:  ackPercent,
		UnreadCount:             unreadCount,
		UnacknowledgedCount:     unackCount,
		FirstViewAt:             firstViewAt,
		LastViewAt:              lastViewAt,
		FirstAcknowledgedAt:     firstAckAt,
		LastAcknowledgedAt:      lastAckAt,
		DepartmentStats:         deptStats,
		MinistryStats:           minStats,
	}, nil
}

// ListPublicationReaders returns paginated readership and acknowledgement status for target employees
func (r *PublicationRepository) ListPublicationReaders(ctx context.Context, pubID uuid.UUID, filter domain.PublicationReadersFilter) (*domain.PublicationReadersResponse, error) {
	var (
		authorID            int64
		targetAudience      string
		targetMinistryID    *int64
		targetDepartmentID  *int64
		targetMinistryIDs   []int64
		targetDepartmentIDs []int64
	)
	err := r.db.QueryRow(ctx, `
		SELECT author_id, target_audience, target_ministry_id, target_department_id, target_ministry_ids, target_department_ids
		FROM publications
		WHERE id = $1 AND deleted_at IS NULL
	`, pubID).Scan(&authorID, &targetAudience, &targetMinistryID, &targetDepartmentID, &targetMinistryIDs, &targetDepartmentIDs)
	if err != nil {
		return nil, fmt.Errorf("get publication for readers: %w", err)
	}

	audience := &domain.Publication{
		AuthorID:            authorID,
		TargetAudience:      targetAudience,
		TargetMinistryID:    targetMinistryID,
		TargetDepartmentID:  targetDepartmentID,
		TargetMinistryIDs:   targetMinistryIDs,
		TargetDepartmentIDs: targetDepartmentIDs,
	}

	args := []interface{}{pubID}
	where := []string{"u.is_active = true", "u.deleted_at IS NULL"}

	// Audience constraint:
	audFilter, audArgs, err := audienceUserFilter(audience, "u.", len(args)+1)
	if err != nil {
		return nil, err
	}
	if audFilter != "" {
		where = append(where, strings.TrimPrefix(strings.TrimSpace(audFilter), "AND "))
		args = append(args, audArgs...)
	}

	// Status filter:
	switch strings.ToLower(strings.TrimSpace(filter.Status)) {
	case "read", "viewed":
		where = append(where, "v.user_id IS NOT NULL")
	case "unread":
		where = append(where, "v.user_id IS NULL")
	case "acknowledged":
		where = append(where, "a.user_id IS NOT NULL")
	case "unacknowledged":
		where = append(where, "a.user_id IS NULL")
	}

	// Department filter:
	if filter.DepartmentID != nil {
		args = append(args, *filter.DepartmentID)
		where = append(where, fmt.Sprintf("u.department_id = $%d", len(args)))
	}

	// Ministry filter:
	if filter.MinistryID != nil {
		args = append(args, *filter.MinistryID)
		where = append(where, fmt.Sprintf("u.ministry_id = $%d", len(args)))
	}

	// Search filter:
	if s := strings.TrimSpace(filter.Search); s != "" {
		args = append(args, "%"+s+"%")
		sArg := len(args)
		where = append(where, fmt.Sprintf(
			"(u.first_name ILIKE $%d OR u.last_name ILIKE $%d OR (COALESCE(u.first_name, '') || ' ' || COALESCE(u.last_name, '')) ILIKE $%d OR u.email ILIKE $%d OR u.position ILIKE $%d)",
			sArg, sArg, sArg, sArg, sArg,
		))
	}

	whereClause := "WHERE " + strings.Join(where, " AND ")

	// Total count
	countQuery := fmt.Sprintf(`
		SELECT COUNT(DISTINCT u.id)
		FROM users u
		LEFT JOIN publication_views v ON v.publication_id = $1 AND v.user_id = u.id
		LEFT JOIN publication_acknowledgements a ON a.publication_id = $1 AND a.user_id = u.id
		%s
	`, whereClause)

	var total int
	if err := r.db.QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, fmt.Errorf("count publication readers: %w", err)
	}

	page := filter.Page
	if page < 1 {
		page = 1
	}
	limit := filter.Limit
	if limit < 1 {
		limit = 50
	} else if limit > 100000 {
		limit = 100000
	}
	offset := (page - 1) * limit

	query := fmt.Sprintf(`
		SELECT
			u.id,
			COALESCE(u.first_name, ''),
			COALESCE(u.last_name, ''),
			u.avatar_url,
			u.position,
			COALESCE(u.email, ''),
			u.department_id,
			u.ministry_id,
			(v.user_id IS NOT NULL) AS is_viewed,
			v.viewed_at,
			(a.user_id IS NOT NULL) AS is_acknowledged,
			a.acknowledged_at
		FROM users u
		LEFT JOIN publication_views v ON v.publication_id = $1 AND v.user_id = u.id
		LEFT JOIN publication_acknowledgements a ON a.publication_id = $1 AND a.user_id = u.id
		%s
		ORDER BY a.acknowledged_at DESC NULLS LAST, v.viewed_at DESC NULLS LAST, u.last_name ASC, u.first_name ASC
		LIMIT $%d OFFSET $%d
	`, whereClause, len(args)+1, len(args)+2)

	queryArgs := append(append([]interface{}{}, args...), limit, offset)
	rows, err := r.db.Query(ctx, query, queryArgs...)
	if err != nil {
		return nil, fmt.Errorf("query publication readers: %w", err)
	}
	defer rows.Close()

	readers := make([]domain.PublicationReaderItem, 0)
	for rows.Next() {
		var (
			item   domain.PublicationReaderItem
			deptID *int64
			minID  *int64
		)
		err := rows.Scan(
			&item.User.ID,
			&item.User.FirstName,
			&item.User.LastName,
			&item.User.AvatarURL,
			&item.User.Position,
			&item.Email,
			&deptID,
			&minID,
			&item.IsViewed,
			&item.ViewedAt,
			&item.IsAcknowledged,
			&item.AcknowledgedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan reader: %w", err)
		}
		item.User.FullName = strings.TrimSpace(item.User.FirstName + " " + item.User.LastName)
		item.DepartmentID = deptID
		if deptID != nil {
			dName := fmt.Sprintf("Отдел #%d", *deptID)
			item.DepartmentName = &dName
		}
		item.MinistryID = minID
		if minID != nil {
			mName := fmt.Sprintf("Филиал #%d", *minID)
			item.MinistryName = &mName
		}
		readers = append(readers, item)
	}

	return &domain.PublicationReadersResponse{
		PublicationID: pubID,
		TotalCount:    total,
		Page:          page,
		Limit:         limit,
		Readers:       readers,
	}, rows.Err()
}
