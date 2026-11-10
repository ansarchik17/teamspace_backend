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

type OrganizationIdentityProviderRepository struct {
	db                  organizationDB
	logger              *zap.Logger
	currentOrganization *uuid.UUID
}

func NewOrganizationIdentityProviderRepository(db *pgxpool.Pool, logger *zap.Logger) *OrganizationIdentityProviderRepository {
	return &OrganizationIdentityProviderRepository{
		db:     db,
		logger: logger.With(zap.String("repository", "organization_identity_provider")),
	}
}

func (r *OrganizationIdentityProviderRepository) List(ctx context.Context, userID int64, organizationID uuid.UUID) ([]domain.OrganizationIdentityProvider, error) {
	if err := r.ensureScope(ctx, userID, organizationID); err != nil {
		return nil, err
	}
	rows, err := r.db.Query(ctx, `
		SELECT id, organization_id, provider_type, alias, display_name, enabled,
		       settings, secret_set, status, COALESCE(last_error, ''),
		       last_tested_at, created_at, updated_at
		FROM organization_identity_providers
		WHERE organization_id = $1
		ORDER BY created_at ASC
	`, organizationID)
	if err != nil {
		return nil, fmt.Errorf("list organization identity providers: %w", err)
	}
	defer rows.Close()

	items := make([]domain.OrganizationIdentityProvider, 0)
	for rows.Next() {
		item, err := scanOrganizationIdentityProvider(rows)
		if err != nil {
			return nil, fmt.Errorf("scan organization identity provider: %w", err)
		}
		items = append(items, *item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate organization identity providers: %w", err)
	}
	return items, nil
}

func (r *OrganizationIdentityProviderRepository) Get(ctx context.Context, userID int64, organizationID, providerID uuid.UUID) (*domain.OrganizationIdentityProvider, error) {
	if err := r.ensureScope(ctx, userID, organizationID); err != nil {
		return nil, err
	}
	item, err := r.get(ctx, organizationID, providerID)
	if err != nil {
		return nil, err
	}
	return item, nil
}

func (r *OrganizationIdentityProviderRepository) Create(ctx context.Context, userID int64, organizationID uuid.UUID, providerType, alias, displayName string, settings map[string]string, secretSet, enabled bool) (*domain.OrganizationIdentityProvider, error) {
	return auditOrganizationMutation(ctx, &OrganizationRepository{db: r.db, logger: r.logger}, userID, organizationID, r.currentOrganization == nil, "identity_provider.create", "identity_provider", "", func(inner *OrganizationRepository) (*domain.OrganizationIdentityProvider, error) {
		return (&OrganizationIdentityProviderRepository{db: inner.db, logger: r.logger, currentOrganization: r.currentOrganization}).create(ctx, userID, organizationID, providerType, alias, displayName, settings, secretSet, enabled)
	})
}

func (r *OrganizationIdentityProviderRepository) create(ctx context.Context, userID int64, organizationID uuid.UUID, providerType, alias, displayName string, settings map[string]string, secretSet, enabled bool) (*domain.OrganizationIdentityProvider, error) {
	if err := r.ensureScope(ctx, userID, organizationID); err != nil {
		return nil, err
	}
	settingsJSON, err := json.Marshal(settings)
	if err != nil {
		return nil, fmt.Errorf("marshal identity provider settings: %w", err)
	}
	var id uuid.UUID
	if err := r.db.QueryRow(ctx, `
		INSERT INTO organization_identity_providers
		    (organization_id, provider_type, alias, display_name, enabled, settings, secret_set, status)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, $8)
		RETURNING id
	`, organizationID, providerType, alias, displayName, enabled, string(settingsJSON), secretSet, providerStatus(enabled, false)).Scan(&id); err != nil {
		return nil, fmt.Errorf("create organization identity provider: %w", err)
	}
	return r.get(ctx, organizationID, id)
}

func (r *OrganizationIdentityProviderRepository) Update(ctx context.Context, userID int64, organizationID, providerID uuid.UUID, displayName *string, settings map[string]string, secretSet *bool, enabled *bool, status, lastError *string, testedAt *time.Time) (*domain.OrganizationIdentityProvider, error) {
	return auditOrganizationMutation(ctx, &OrganizationRepository{db: r.db, logger: r.logger}, userID, organizationID, r.currentOrganization == nil, "identity_provider.update", "identity_provider", providerID.String(), func(inner *OrganizationRepository) (*domain.OrganizationIdentityProvider, error) {
		return (&OrganizationIdentityProviderRepository{db: inner.db, logger: r.logger, currentOrganization: r.currentOrganization}).update(ctx, userID, organizationID, providerID, displayName, settings, secretSet, enabled, status, lastError, testedAt)
	})
}

func (r *OrganizationIdentityProviderRepository) update(ctx context.Context, userID int64, organizationID, providerID uuid.UUID, displayName *string, settings map[string]string, secretSet *bool, enabled *bool, status, lastError *string, testedAt *time.Time) (*domain.OrganizationIdentityProvider, error) {
	if err := r.ensureScope(ctx, userID, organizationID); err != nil {
		return nil, err
	}
	var settingsJSON any
	if settings != nil {
		payload, err := json.Marshal(settings)
		if err != nil {
			return nil, fmt.Errorf("marshal identity provider settings: %w", err)
		}
		settingsJSON = string(payload)
	}
	if _, err := r.db.Exec(ctx, `
		UPDATE organization_identity_providers
		SET display_name = COALESCE($3, display_name),
		    settings = COALESCE($4::jsonb, settings),
		    secret_set = COALESCE($5, secret_set),
		    enabled = COALESCE($6, enabled),
		    status = COALESCE($7, status),
		    last_error = CASE WHEN $8::text IS NULL THEN last_error ELSE $8 END,
		    last_tested_at = CASE WHEN $9::timestamptz IS NULL THEN last_tested_at ELSE $9 END,
		    updated_at = NOW()
		WHERE id = $1 AND organization_id = $2
	`, providerID, organizationID, displayName, settingsJSON, secretSet, enabled, status, lastError, testedAt); err != nil {
		return nil, fmt.Errorf("update organization identity provider: %w", err)
	}
	return r.get(ctx, organizationID, providerID)
}

func (r *OrganizationIdentityProviderRepository) Delete(ctx context.Context, userID int64, organizationID, providerID uuid.UUID) error {
	return auditOrganizationMutationError(ctx, &OrganizationRepository{db: r.db, logger: r.logger}, userID, organizationID, r.currentOrganization == nil, "identity_provider.delete", "identity_provider", providerID.String(), func(inner *OrganizationRepository) error {
		return (&OrganizationIdentityProviderRepository{db: inner.db, logger: r.logger, currentOrganization: r.currentOrganization}).delete(ctx, userID, organizationID, providerID)
	})
}

func (r *OrganizationIdentityProviderRepository) delete(ctx context.Context, userID int64, organizationID, providerID uuid.UUID) error {
	if err := r.ensureScope(ctx, userID, organizationID); err != nil {
		return err
	}
	result, err := r.db.Exec(ctx, `
		DELETE FROM organization_identity_providers
		WHERE id = $1 AND organization_id = $2
	`, providerID, organizationID)
	if err != nil {
		return fmt.Errorf("delete organization identity provider: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("identity provider not found")
	}
	return nil
}

func (r *OrganizationIdentityProviderRepository) OrganizationSlug(ctx context.Context, userID int64, organizationID uuid.UUID) (string, error) {
	if err := r.ensureScope(ctx, userID, organizationID); err != nil {
		return "", err
	}
	var slug string
	if err := r.db.QueryRow(ctx, `SELECT slug FROM organizations WHERE id = $1`, organizationID).Scan(&slug); err != nil {
		return "", fmt.Errorf("get organization slug: %w", err)
	}
	return slug, nil
}

// ResolveEnabledAliasByOrganizationSlug is read-only and intentionally does
// not require an authenticated Workspace user: it is used before login to
// select the configured customer broker provider.
func (r *OrganizationIdentityProviderRepository) ResolveEnabledAliasByOrganizationSlug(ctx context.Context, slug string) (string, error) {
	var alias string
	err := r.db.QueryRow(ctx, `
		SELECT p.alias
		FROM organization_identity_providers p
		JOIN organizations o ON o.id = p.organization_id
		WHERE lower(o.slug) = lower($1)
		  AND o.status = 'active'
		  AND p.enabled = TRUE
		  AND p.status = 'active'
		ORDER BY p.created_at ASC
		LIMIT 1
	`, strings.TrimSpace(slug)).Scan(&alias)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("organization identity provider not found")
		}
		return "", fmt.Errorf("resolve organization identity provider: %w", err)
	}
	return alias, nil
}

func (r *OrganizationIdentityProviderRepository) get(ctx context.Context, organizationID, providerID uuid.UUID) (*domain.OrganizationIdentityProvider, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, organization_id, provider_type, alias, display_name, enabled,
		       settings, secret_set, status, COALESCE(last_error, ''),
		       last_tested_at, created_at, updated_at
		FROM organization_identity_providers
		WHERE id = $1 AND organization_id = $2
	`, providerID, organizationID)
	item, err := scanOrganizationIdentityProvider(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("identity provider not found")
		}
		return nil, fmt.Errorf("get organization identity provider: %w", err)
	}
	return item, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanOrganizationIdentityProvider(row rowScanner) (*domain.OrganizationIdentityProvider, error) {
	var (
		item        domain.OrganizationIdentityProvider
		settingsRaw []byte
	)
	if err := row.Scan(
		&item.ID,
		&item.OrganizationID,
		&item.ProviderType,
		&item.Alias,
		&item.DisplayName,
		&item.Enabled,
		&settingsRaw,
		&item.SecretSet,
		&item.Status,
		&item.LastError,
		&item.LastTestedAt,
		&item.CreatedAt,
		&item.UpdatedAt,
	); err != nil {
		return nil, err
	}
	item.Settings = map[string]string{}
	if len(settingsRaw) > 0 {
		if err := json.Unmarshal(settingsRaw, &item.Settings); err != nil {
			return nil, err
		}
	}
	return &item, nil
}

func (r *OrganizationIdentityProviderRepository) ensurePlatformScope(ctx context.Context, userID int64, organizationID uuid.UUID) error {
	if err := authorizeAdministration(ctx, r.db, userID, nil, true); err != nil {
		return err
	}
	var exists bool
	if err := r.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM organizations WHERE id = $1)`, organizationID).Scan(&exists); err != nil {
		return fmt.Errorf("resolve organization: %w", err)
	}
	if !exists {
		return fmt.Errorf("organization not found")
	}
	return nil
}

func providerStatus(enabled, hasError bool) string {
	if hasError {
		return "error"
	}
	if enabled {
		return "active"
	}
	return "disabled"
}
