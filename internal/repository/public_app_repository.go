package repository

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

type PublicAppRepository struct {
	db     *pgxpool.Pool
	logger *zap.Logger
}

func NewPublicAppRepository(db *pgxpool.Pool, logger *zap.Logger) *PublicAppRepository {
	return &PublicAppRepository{
		db:     db,
		logger: logger.With(zap.String("repository", "public_app")),
	}
}

type publicAppScanner interface {
	Scan(dest ...any) error
}

func scanPublicApp(scanner publicAppScanner, app *domain.PublicApp) error {
	if err := scanner.Scan(
		&app.ID,
		&app.AppName,
		&app.Link,
		&app.AppPhoto,
		&app.WebView,
		&app.Position,
		&app.IsGlobal,
		&app.IsPrivate,
		&app.Category,
		&app.Tags,
		&app.CreatedAt,
		&app.UpdatedAt,
		&app.IsFavorite,
		&app.AverageRating,
		&app.RatingCount,
		&app.MyRating,
		&app.FavoriteCount,
		&app.OrganizationIDs,
	); err != nil {
		return err
	}

	if app.Tags == nil {
		app.Tags = []string{}
	}
	if app.OrganizationIDs == nil {
		app.OrganizationIDs = []int64{}
	}

	return nil
}

func publicAppRatingJoin(userIDPlaceholder string) string {
	return fmt.Sprintf(`
		LEFT JOIN (
			SELECT app_id,
			       ROUND(AVG(rating)::numeric, 2)::DOUBLE PRECISION AS average_rating,
			       COUNT(*)::INT AS rating_count
			FROM public_app_ratings
			GROUP BY app_id
		) pr ON pr.app_id = pa.id
		LEFT JOIN public_app_ratings ur ON ur.app_id = pa.id AND ur.user_id = %s
	`, userIDPlaceholder)
}

func publicAppFavoriteCountJoin() string {
	return `
		LEFT JOIN (
			SELECT app_id,
			       COUNT(DISTINCT user_id)::INT AS favorite_count
			FROM public_app_favorites
			GROUP BY app_id
		) pf ON pf.app_id = pa.id
	`
}

func (r *PublicAppRepository) Create(ctx context.Context, app *domain.PublicApp) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		r.logger.Error("Failed to start transaction for create", zap.Error(err))
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	// First, determine position (max + 1)
	var maxPos int
	_ = tx.QueryRow(ctx, "SELECT COALESCE(MAX(position), 0) FROM public_apps").Scan(&maxPos)
	app.Position = maxPos + 1

	query := `INSERT INTO public_apps (app_name, link, app_photo, web_view, position, is_global, is_private, category, tags, created_at, updated_at) 
			  VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) RETURNING id`

	err = tx.QueryRow(ctx, query, app.AppName, app.Link, app.AppPhoto, app.WebView, app.Position, app.IsGlobal, app.IsPrivate, app.Category, app.Tags, app.CreatedAt, app.UpdatedAt).Scan(&app.ID)
	if err != nil {
		r.logger.Error("Failed to create public app in tx", zap.Error(err))
		return fmt.Errorf("create public app in tx: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		r.logger.Error("Failed to commit create transaction", zap.Error(err))
		return fmt.Errorf("commit create: %w", err)
	}
	return nil
}

func (r *PublicAppRepository) GetAll(ctx context.Context, userID int64) ([]domain.PublicApp, error) {
	return r.FindAll(ctx, userID, "", nil, "position")
}

func (r *PublicAppRepository) GetByID(ctx context.Context, id int64, userID int64) (*domain.PublicApp, error) {
	query := `
		SELECT pa.id, pa.app_name, pa.link, pa.app_photo, pa.web_view,
		       COALESCE(up.position, pa.position) AS position,
		       pa.is_global, pa.is_private,
		       COALESCE(pa.category, '') AS category,
		       COALESCE(pa.tags, ARRAY[]::TEXT[]) AS tags,
		       pa.created_at, pa.updated_at,
		       (pa.is_global OR EXISTS(SELECT 1 FROM public_app_favorites paf WHERE paf.app_id = pa.id AND paf.user_id = $1)) AS is_favorite,
		       COALESCE(pr.average_rating, 0)::DOUBLE PRECISION AS average_rating,
		       COALESCE(pr.rating_count, 0)::INT AS rating_count,
		       ur.rating AS my_rating,
		       COALESCE(pf.favorite_count, 0)::INT AS favorite_count,
		       ARRAY[]::BIGINT[] AS organization_ids
		FROM public_apps pa
		LEFT JOIN public_app_user_positions up ON up.app_id = pa.id AND up.user_id = $1
	` + publicAppRatingJoin("$1") + `
	` + publicAppFavoriteCountJoin() + `
		WHERE pa.id = $2
		  AND ($1 = 0 
		       OR EXISTS(SELECT 1 FROM users u WHERE u.id = $1 AND u.role = 'admin')
		       OR (pa.is_private = FALSE OR EXISTS(SELECT 1 FROM public_app_access paa WHERE paa.app_id = pa.id AND paa.user_id = $1))
		      )
	`

	var app domain.PublicApp
	err := scanPublicApp(r.db.QueryRow(ctx, query, userID, id), &app)
	if err != nil {
		return nil, fmt.Errorf("get public app using id %d: %w", id, err)
	}
	return &app, nil
}

func (r *PublicAppRepository) Update(ctx context.Context, app *domain.PublicApp) error {
	// Start a transaction for safe reordering
	tx, err := r.db.Begin(ctx)
	if err != nil {
		r.logger.Error("Failed to start transaction for update", zap.Error(err))
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	// 1. Get current (old) position
	var oldPosition int
	err = tx.QueryRow(ctx, "SELECT position FROM public_apps WHERE id = $1 FOR UPDATE", app.ID).Scan(&oldPosition)
	if err != nil {
		r.logger.Error("Failed to fetch old position", zap.Int64("id", app.ID), zap.Error(err))
		return fmt.Errorf("fetch old position: %w", err)
	}

	newPosition := app.Position

	// 2. Perform shifting logic if position has changed
	if newPosition != oldPosition {
		if newPosition < oldPosition {
			// Shifting forward: items between new and old position move up (+1)
			_, err = tx.Exec(ctx,
				"UPDATE public_apps SET position = position + 1 WHERE position >= $1 AND position < $2 AND id != $3",
				newPosition, oldPosition, app.ID)
		} else {
			// Shifting backward: items between old and new position move down (-1)
			_, err = tx.Exec(ctx,
				"UPDATE public_apps SET position = position - 1 WHERE position <= $1 AND position > $2 AND id != $3",
				newPosition, oldPosition, app.ID)
		}

		if err != nil {
			r.logger.Error("Failed to shift positions during update", zap.Error(err))
			return fmt.Errorf("shift positions: %w", err)
		}
	}

	// 3. Update the app itself
	query := `UPDATE public_apps SET app_name = $1, link = $2, app_photo = $3, web_view = $4, position = $5, is_global = $6, is_private = $7, category = $8, tags = $9, updated_at = $10 WHERE id = $11`
	_, err = tx.Exec(ctx, query, app.AppName, app.Link, app.AppPhoto, app.WebView, newPosition, app.IsGlobal, app.IsPrivate, app.Category, app.Tags, app.UpdatedAt, app.ID)
	if err != nil {
		r.logger.Error("Failed to update public app in tx", zap.Error(err))
		return fmt.Errorf("update public app in tx: %w", err)
	}

	// 5. Commit transaction
	if err := tx.Commit(ctx); err != nil {
		r.logger.Error("Failed to commit public app update transaction", zap.Error(err))
		return fmt.Errorf("commit update: %w", err)
	}

	return nil
}

func (r *PublicAppRepository) Delete(ctx context.Context, id int64) error {
	query := `DELETE FROM public_apps WHERE id = $1`

	_, err := r.db.Exec(ctx, query, id)
	if err != nil {
		r.logger.Error("Failed to delete public app", zap.Error(err))
		return fmt.Errorf("delete public app: %w", err)
	}
	return nil
}
func (r *PublicAppRepository) AddFavorite(ctx context.Context, userID, appID int64) error {
	query := `INSERT INTO public_app_favorites (user_id, app_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`
	_, err := r.db.Exec(ctx, query, userID, appID)
	if err != nil {
		r.logger.Error("Failed to add favorite app", zap.Error(err))
		return fmt.Errorf("add favorite app: %w", err)
	}
	return nil
}

func (r *PublicAppRepository) RemoveFavorite(ctx context.Context, userID, appID int64) error {
	query := `DELETE FROM public_app_favorites WHERE user_id = $1 AND app_id = $2`
	_, err := r.db.Exec(ctx, query, userID, appID)
	if err != nil {
		r.logger.Error("Failed to remove favorite app", zap.Error(err))
		return fmt.Errorf("remove favorite app: %w", err)
	}
	return nil
}

func (r *PublicAppRepository) GetFavorites(ctx context.Context, userID int64) ([]domain.PublicApp, error) {
	// Favorites should include user-selected favorites AND global apps
	// We use the new position column in public_app_favorites
	query := `
		SELECT pa.id, pa.app_name, pa.link, pa.app_photo, pa.web_view,
		       COALESCE(paf.position, pa.position) AS position,
		       pa.is_global, pa.is_private,
		       COALESCE(pa.category, '') AS category,
		       COALESCE(pa.tags, ARRAY[]::TEXT[]) AS tags,
		       pa.created_at, pa.updated_at,
		       TRUE AS is_favorite,
		       COALESCE(pr.average_rating, 0)::DOUBLE PRECISION AS average_rating,
		       COALESCE(pr.rating_count, 0)::INT AS rating_count,
		       ur.rating AS my_rating,
		       COALESCE(pf.favorite_count, 0)::INT AS favorite_count,
		       ARRAY[]::BIGINT[] AS organization_ids
		FROM public_apps pa
		LEFT JOIN public_app_favorites paf ON pa.id = paf.app_id AND paf.user_id = $1
	` + publicAppRatingJoin("$1") + `
	` + publicAppFavoriteCountJoin() + `
		WHERE (paf.user_id = $1 OR pa.is_global = TRUE)
		AND ($1 = 0 
		     OR EXISTS(SELECT 1 FROM users u WHERE u.id = $1 AND u.role = 'admin')
		     OR (pa.is_private = FALSE OR EXISTS(SELECT 1 FROM public_app_access paa WHERE paa.app_id = pa.id AND paa.user_id = $1))
		    )
		ORDER BY paf.position ASC NULLS LAST, pa.id DESC
	`

	rows, err := r.db.Query(ctx, query, userID)
	if err != nil {
		r.logger.Error("Failed to list favorite apps", zap.Error(err))
		return nil, fmt.Errorf("list favorite apps: %w", err)
	}
	defer rows.Close()

	var apps []domain.PublicApp
	for rows.Next() {
		var app domain.PublicApp
		if err := scanPublicApp(rows, &app); err != nil {
			r.logger.Error("Failed to scan favorite app", zap.Error(err))
			continue
		}
		apps = append(apps, app)
	}
	return apps, nil
}

func (r *PublicAppRepository) FindAll(ctx context.Context, userID int64, nameQuery string, downloaded *bool, sort string) ([]domain.PublicApp, error) {
	query := `
		SELECT pa.id, pa.app_name, pa.link, pa.app_photo, pa.web_view,
		       COALESCE(up.position, pa.position) AS position,
		       pa.is_global, pa.is_private,
		       COALESCE(pa.category, '') AS category,
		       COALESCE(pa.tags, ARRAY[]::TEXT[]) AS tags,
		       pa.created_at, pa.updated_at,
		       (pa.is_global OR EXISTS(SELECT 1 FROM public_app_favorites paf WHERE paf.app_id = pa.id AND paf.user_id = $1)) AS is_favorite,
		       COALESCE(pr.average_rating, 0)::DOUBLE PRECISION AS average_rating,
		       COALESCE(pr.rating_count, 0)::INT AS rating_count,
		       ur.rating AS my_rating,
		       COALESCE(pf.favorite_count, 0)::INT AS favorite_count,
		       ARRAY[]::BIGINT[] AS organization_ids
		FROM public_apps pa
		LEFT JOIN public_app_user_positions up ON up.app_id = pa.id AND up.user_id = $1
	` + publicAppRatingJoin("$1") + `
	` + publicAppFavoriteCountJoin() + `
		WHERE ($1 = 0 
		       OR EXISTS(SELECT 1 FROM users u WHERE u.id = $1 AND u.role = 'admin')
		       OR (pa.is_private = FALSE OR EXISTS(SELECT 1 FROM public_app_access paa WHERE paa.app_id = pa.id AND paa.user_id = $1))
		      )
	`

	args := []interface{}{userID}
	argIdx := 2

	if nameQuery != "" {
		query += fmt.Sprintf(" AND (pa.app_name ILIKE $%d OR EXISTS (SELECT 1 FROM unnest(COALESCE(pa.tags, ARRAY[]::TEXT[])) AS tag WHERE tag ILIKE $%d))", argIdx, argIdx)
		args = append(args, "%"+nameQuery+"%")
	}

	if downloaded != nil {
		if *downloaded {
			query += " AND (pa.is_global OR EXISTS(SELECT 1 FROM public_app_favorites paf WHERE paf.app_id = pa.id AND paf.user_id = $1))"
		} else {
			query += " AND NOT (pa.is_global OR EXISTS(SELECT 1 FROM public_app_favorites paf WHERE paf.app_id = pa.id AND paf.user_id = $1))"
		}
	}

	switch sort {
	case "rating":
		query += " ORDER BY average_rating DESC, rating_count DESC, position ASC, pa.id DESC"
	default:
		query += " ORDER BY position ASC, pa.id DESC"
	}

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		r.logger.Error("Failed to list public apps with favorites", zap.Error(err))
		return nil, fmt.Errorf("list public apps with favorites: %w", err)
	}
	defer rows.Close()

	var apps []domain.PublicApp
	for rows.Next() {
		var app domain.PublicApp
		if err := scanPublicApp(rows, &app); err != nil {
			r.logger.Error("Failed to scan public app", zap.Error(err))
			continue
		}
		apps = append(apps, app)
	}
	return apps, nil
}

// Reorder updates the positions of apps for a specific user
func (r *PublicAppRepository) Reorder(ctx context.Context, userID int64, appIDs []int64) error {
	if len(appIDs) == 0 {
		return nil
	}

	// Start a transaction
	tx, err := r.db.Begin(ctx)
	if err != nil {
		r.logger.Error("Failed to start transaction", zap.Error(err))
		return fmt.Errorf("start transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	// Upsert position for each app for this user
	// We are now storing positions in public_app_user_positions specifically for the "All Apps" view
	query := `
		INSERT INTO public_app_user_positions (user_id, app_id, position, updated_at) 
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (user_id, app_id) DO UPDATE SET position = EXCLUDED.position, updated_at = NOW()
	`
	for i, appID := range appIDs {
		position := i + 1
		_, err := tx.Exec(ctx, query, userID, appID, position)
		if err != nil {
			r.logger.Error("Failed to update user app position", zap.Int64("user_id", userID), zap.Int64("app_id", appID), zap.Error(err))
			return fmt.Errorf("update user app position: %w", err)
		}
	}

	return tx.Commit(ctx)
}

func (r *PublicAppRepository) ReorderFavorites(ctx context.Context, userID int64, appIDs []int64) error {
	if len(appIDs) == 0 {
		return nil
	}

	tx, err := r.db.Begin(ctx)
	if err != nil {
		r.logger.Error("Failed to start transaction", zap.Error(err))
		return fmt.Errorf("start transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	/*
	 Logic for favorites reorder:
	 We need to update the `position` column in `public_app_favorites`.
	 BUT, some apps in the list might be global apps that the user hasn't explicitly favorited yet (no row in public_app_favorites).
	 If a user reorders a global app, we MUST create a row in public_app_favorites to store its position.
	 Creating a row also means `is_favorite` becomes true, which is consistent.
	*/

	query := `
		INSERT INTO public_app_favorites (user_id, app_id, position) 
		VALUES ($1, $2, $3)
		ON CONFLICT (user_id, app_id) DO UPDATE SET position = EXCLUDED.position
	`

	for i, appID := range appIDs {
		position := i + 1
		_, err := tx.Exec(ctx, query, userID, appID, position)
		if err != nil {
			r.logger.Error("Failed to update favorite app position", zap.Int64("user_id", userID), zap.Int64("app_id", appID), zap.Error(err))
			return fmt.Errorf("update favorite app position: %w", err)
		}
	}

	return tx.Commit(ctx)
}

func (r *PublicAppRepository) GrantAccess(ctx context.Context, appID int64, userID int64) error {
	query := `INSERT INTO public_app_access (app_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`
	_, err := r.db.Exec(ctx, query, appID, userID)
	if err != nil {
		r.logger.Error("Failed to grant app access", zap.Error(err))
		return fmt.Errorf("grant app access: %w", err)
	}
	return nil
}

func (r *PublicAppRepository) RevokeAccess(ctx context.Context, appID int64, userID int64) error {
	query := `DELETE FROM public_app_access WHERE app_id = $1 AND user_id = $2`
	_, err := r.db.Exec(ctx, query, appID, userID)
	if err != nil {
		r.logger.Error("Failed to revoke app access", zap.Error(err))
		return fmt.Errorf("revoke app access: %w", err)
	}
	return nil
}

func (r *PublicAppRepository) UpsertRating(ctx context.Context, userID, appID int64, rating int) error {
	query := `
		INSERT INTO public_app_ratings (app_id, user_id, rating, created_at, updated_at)
		VALUES ($1, $2, $3, NOW(), NOW())
		ON CONFLICT (app_id, user_id)
		DO UPDATE SET rating = EXCLUDED.rating, updated_at = NOW()
	`

	_, err := r.db.Exec(ctx, query, appID, userID, rating)
	if err != nil {
		r.logger.Error("Failed to upsert public app rating", zap.Error(err))
		return fmt.Errorf("upsert public app rating: %w", err)
	}

	return nil
}

func (r *PublicAppRepository) RemoveRating(ctx context.Context, userID, appID int64) error {
	query := `DELETE FROM public_app_ratings WHERE app_id = $1 AND user_id = $2`

	_, err := r.db.Exec(ctx, query, appID, userID)
	if err != nil {
		r.logger.Error("Failed to remove public app rating", zap.Error(err))
		return fmt.Errorf("remove public app rating: %w", err)
	}

	return nil
}
