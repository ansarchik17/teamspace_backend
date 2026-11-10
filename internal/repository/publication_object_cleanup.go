package repository

import (
	"context"
	"net/url"
)

func (r *PublicationRepository) ProcessPublicationObjectCleanup(ctx context.Context, remove func(context.Context, string) error) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `SELECT object_name FROM publication_object_cleanup WHERE next_attempt_at<=NOW() ORDER BY next_attempt_at,object_name LIMIT 20 FOR UPDATE SKIP LOCKED`)
	if err != nil {
		return err
	}
	keys := []string{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return err
		}
		keys = append(keys, key)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, key := range keys {
		var referenced bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM publication_attachments WHERE strpos(file_url,$1)>0 OR strpos(file_url,$2)>0 OR strpos(COALESCE(thumbnail_url,''),$1)>0 OR strpos(COALESCE(thumbnail_url,''),$2)>0) OR EXISTS(SELECT 1 FROM publications WHERE strpos(COALESCE(cover_image_url,''),$1)>0 OR strpos(COALESCE(cover_image_url,''),$2)>0 OR strpos(content_doc::text,$1)>0 OR strpos(content_doc::text,$2)>0)`, key, url.QueryEscape(key)).Scan(&referenced)
		if err != nil {
			return err
		}
		if !referenced && remove(ctx, key) != nil {
			_, err = tx.Exec(ctx, `UPDATE publication_object_cleanup SET attempts=attempts+1,next_attempt_at=NOW()+INTERVAL '1 minute' WHERE object_name=$1`, key)
		} else {
			_, err = tx.Exec(ctx, `DELETE FROM publication_object_cleanup WHERE object_name=$1`, key)
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
