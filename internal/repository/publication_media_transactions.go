package repository

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/west6ide/workspace-backend/internal/domain"
	"net/url"
	"strings"
)

func lockPublication(ctx context.Context, tx pgx.Tx, pubID uuid.UUID) error {
	var id uuid.UUID
	return tx.QueryRow(ctx, `SELECT id FROM publications WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, pubID).Scan(&id)
}

func (r *PublicationRepository) AddAttachments(ctx context.Context, pubID uuid.UUID, attachments []domain.PublicationAttachment) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockPublication(ctx, tx, pubID); err != nil {
		return err
	}
	var next int
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(order_index)+1,0) FROM publication_attachments WHERE publication_id=$1`, pubID).Scan(&next); err != nil {
		return err
	}
	for i := range attachments {
		attachments[i].OrderIndex = next + i
	}
	if err := r.insertAttachmentsTx(ctx, tx, pubID, attachments); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func ownedPublicationObject(ownerID int64, raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	key := raw
	if strings.HasSuffix(u.Path, "/api/v1/files/download") {
		key = u.Query().Get("object_name")
	} else if u.Scheme != "" || u.Host != "" {
		return ""
	}
	if strings.Contains(key, "..") || strings.ContainsAny(key, "\\\r\n\t?#") {
		return ""
	}
	for _, kind := range []string{"image", "pdf", "document", "video", "audio"} {
		if strings.HasPrefix(key, fmt.Sprintf("%s/%d/", kind, ownerID)) {
			return key
		}
	}
	return ""
}

func queueUnusedPublicationObject(ctx context.Context, tx pgx.Tx, ownerID int64, raw string) error {
	key := ownedPublicationObject(ownerID, raw)
	if key == "" {
		return nil
	}
	var referenced bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM publication_attachments WHERE file_url=$1 OR strpos(file_url,$2)>0 OR strpos(COALESCE(thumbnail_url,''),$2)>0 OR strpos(file_url,$1)>0 OR strpos(COALESCE(thumbnail_url,''),$1)>0) OR EXISTS(SELECT 1 FROM publications WHERE strpos(COALESCE(cover_image_url,''),$1)>0 OR strpos(COALESCE(cover_image_url,''),$2)>0 OR strpos(content_doc::text,$1)>0 OR strpos(content_doc::text,$2)>0)`, key, url.QueryEscape(key)).Scan(&referenced)
	if err != nil {
		return err
	}
	if referenced {
		return nil
	}
	_, err = tx.Exec(ctx, `INSERT INTO publication_object_cleanup(object_name) VALUES($1) ON CONFLICT DO NOTHING`, key)
	return err
}

func (r *PublicationRepository) QueuePublicationObjectCleanup(ctx context.Context, keys []string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, key := range keys {
		if _, err := tx.Exec(ctx, `INSERT INTO publication_object_cleanup(object_name) VALUES($1) ON CONFLICT DO NOTHING`, key); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (r *PublicationRepository) DeletePublicationAttachment(ctx context.Context, pubID, attachmentID uuid.UUID, ownerID int64) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockPublication(ctx, tx, pubID); err != nil {
		return err
	}
	var raw string
	var thumbnail *string
	err = tx.QueryRow(ctx, `DELETE FROM publication_attachments WHERE id=$1 AND publication_id=$2 RETURNING file_url,thumbnail_url`, attachmentID, pubID).Scan(&raw, &thumbnail)
	if err != nil {
		return fmt.Errorf("attachment not found: %w", err)
	}
	if err := queueUnusedPublicationObject(ctx, tx, ownerID, raw); err != nil {
		return err
	}
	if thumbnail != nil {
		if err := queueUnusedPublicationObject(ctx, tx, ownerID, *thumbnail); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
