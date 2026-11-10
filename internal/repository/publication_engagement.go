package repository

import (
	"context"
	"github.com/google/uuid"
)

// Keep legacy likes and emoji reactions atomic, including old alias records.
func (r *PublicationRepository) setLikeReaction(ctx context.Context, pubID uuid.UUID, userID int64, add bool) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Serialize competing add/remove requests for the same publication.
	if _, err = tx.Exec(ctx, `SELECT id FROM publications WHERE id=$1 FOR UPDATE`, pubID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM publication_reactions WHERE publication_id=$1 AND user_id=$2 AND reaction IN ('like','👍')`, pubID, userID); err != nil {
		return err
	}
	if add {
		_, err = tx.Exec(ctx, `INSERT INTO publication_reactions(publication_id,user_id,reaction) VALUES($1,$2,'👍')`, pubID, userID)
		if err == nil {
			_, err = tx.Exec(ctx, `INSERT INTO publication_likes(publication_id,user_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, pubID, userID)
		}
	} else {
		_, err = tx.Exec(ctx, `DELETE FROM publication_likes WHERE publication_id=$1 AND user_id=$2`, pubID, userID)
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
