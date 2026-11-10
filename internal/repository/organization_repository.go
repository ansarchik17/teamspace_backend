package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

type StorageCapacityProvider interface {
	Capacity(context.Context) (domain.StorageCapacity, error)
}

func (r *OrganizationRepository) SetStorageCapacityProvider(provider StorageCapacityProvider) {
	r.capacity = provider
}

func (r *OrganizationRepository) SetAIAccountingEnabled(enabled bool) {
	r.aiAccountingEnabled = enabled
}

// SetAIQuotaTestSubjects is called only during server startup.
func (r *OrganizationRepository) SetAIQuotaTestSubjects(subjects domain.AIQuotaTestSubjects) {
	r.aiQuotaTestSubjects = append(domain.AIQuotaTestSubjects(nil), subjects...)
}

func (r *OrganizationRepository) SetEnforcementMode(mode string) {
	if mode == domain.AIEnforcementModeObserve {
		r.aiEnforcementMode = domain.AIEnforcementModeObserve
	} else {
		r.aiEnforcementMode = domain.AIEnforcementModeEnforced
	}
}

type OrganizationRepository struct {
	capacity            StorageCapacityProvider
	db                  organizationDB
	logger              *zap.Logger
	aiAccountingEnabled bool
	aiEnforcementMode   string
	aiQuotaTestSubjects domain.AIQuotaTestSubjects
}

// txOrganizationDB lets snapshot repositories run on an open transaction.
// Nested transactions with custom options are not supported there.
type txOrganizationDB struct{ pgx.Tx }

func (t txOrganizationDB) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	return nil, errors.New("nested transaction options are unsupported inside an organization snapshot")
}

type organizationDB interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Begin(context.Context) (pgx.Tx, error)
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
}

func NewOrganizationRepository(db *pgxpool.Pool, logger *zap.Logger) *OrganizationRepository {
	return &OrganizationRepository{
		db:                  db,
		logger:              logger.With(zap.String("repository", "organization")),
		aiAccountingEnabled: true,
		aiEnforcementMode:   domain.AIEnforcementModeEnforced,
	}
}

type organizationScope struct {
	ID      uuid.UUID
	Role    domain.Role
	OwnerID *int64
}

func (r *OrganizationRepository) CreateOrganization(ctx context.Context, userID int64, req domain.CreateOrganizationRequest) (*domain.Organization, error) {
	if err := r.ensurePlatformAdmin(ctx, userID); err != nil {
		return nil, err
	}
	var alreadyAssigned bool
	if err := r.db.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM users
			WHERE id = $1 AND organization_id IS NOT NULL AND deleted_at IS NULL
		)
	`, userID).Scan(&alreadyAssigned); err != nil {
		return nil, fmt.Errorf("check existing organization assignment: %w", err)
	}
	if alreadyAssigned {
		return nil, fmt.Errorf("organization already assigned; use the platform organization endpoint")
	}

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin organization creation: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := authorizeAdministrationLocked(ctx, tx, userID, nil, true); err != nil {
		return nil, err
	}

	var item domain.Organization
	if err := tx.QueryRow(ctx, `
		INSERT INTO organizations (name, slug, owner_id)
		VALUES ($1, $2, $3)
		RETURNING id, name, slug, status, created_at, updated_at
	`, req.Name, req.Slug, userID).Scan(&item.ID, &item.Name, &item.Slug, &item.Status, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return nil, fmt.Errorf("create organization: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET organization_id = $1 WHERE id = $2`, item.ID, userID); err != nil {
		return nil, fmt.Errorf("assign organization administrator: %w", err)
	}
	after, err := administrationSnapshot(ctx, tx, item.ID, "organization", item.ID.String())
	if err != nil {
		return nil, err
	}
	if err := writeAdministrationAudit(ctx, tx, item.ID, userID, "organization.create", "organization", item.ID.String(), nil, after); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit organization creation: %w", err)
	}
	return &item, nil
}

// ListPlatformOrganizations returns tenant metadata for platform
// administrators. It never exposes tenant-scoped resources and does not
// depend on the caller having an active organization.
func (r *OrganizationRepository) ListPlatformOrganizations(ctx context.Context, userID int64, filter domain.PlatformOrganizationFilter) (*domain.PlatformOrganizationList, error) {
	if err := r.ensurePlatformAdmin(ctx, userID); err != nil {
		return nil, err
	}

	where := " WHERE o.status <> 'deleted'"
	args := make([]any, 0, 2)
	argIndex := 1
	if filter.Query != "" {
		where += fmt.Sprintf(" AND (LOWER(o.name) LIKE LOWER($%d) OR LOWER(o.slug) LIKE LOWER($%d) OR LOWER(COALESCE(owner.email, '')) LIKE LOWER($%d))", argIndex, argIndex, argIndex)
		args = append(args, "%"+filter.Query+"%")
		argIndex++
	}
	if filter.Status != "" {
		where += fmt.Sprintf(" AND o.status = $%d", argIndex)
		args = append(args, filter.Status)
		argIndex++
	}

	var total int
	countQuery := `SELECT COUNT(*)::int FROM organizations o LEFT JOIN users owner ON owner.id = o.owner_id AND owner.deleted_at IS NULL` + where
	if err := r.db.QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, fmt.Errorf("count platform organizations: %w", err)
	}

	itemsQuery := `
		SELECT o.id, o.name, o.slug, o.status, o.created_at, o.updated_at,
		       o.owner_id,
		       COALESCE(NULLIF(TRIM(owner.full_name_local), ''), NULLIF(TRIM(CONCAT_WS(' ', owner.first_name, owner.last_name)), ''), owner.username, owner.email, ''),
		       COALESCE(owner.email, ''),
		       (SELECT COUNT(*)::int FROM users u WHERE u.organization_id = o.id AND u.deleted_at IS NULL),
		       (SELECT COUNT(*)::int FROM teams t WHERE t.organization_id = o.id AND t.deleted_at IS NULL)
		FROM organizations o
		LEFT JOIN users owner ON owner.id = o.owner_id AND owner.deleted_at IS NULL` + where + fmt.Sprintf(`
		ORDER BY o.created_at DESC, o.id
		LIMIT $%d OFFSET $%d`, argIndex, argIndex+1)
	args = append(args, filter.Limit, filter.Offset)

	rows, err := r.db.Query(ctx, itemsQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("list platform organizations: %w", err)
	}
	defer rows.Close()

	items := make([]domain.PlatformOrganization, 0)
	for rows.Next() {
		var item domain.PlatformOrganization
		var userCount, unitCount int
		if err := rows.Scan(
			&item.ID, &item.Name, &item.Slug, &item.Status, &item.CreatedAt, &item.UpdatedAt,
			&item.OwnerID, &item.OwnerName, &item.OwnerEmail, &userCount, &unitCount,
		); err != nil {
			return nil, fmt.Errorf("scan platform organization: %w", err)
		}
		item.UserCount = userCount
		item.UnitCount = unitCount
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate platform organizations: %w", err)
	}

	return &domain.PlatformOrganizationList{
		Items:  items,
		Total:  total,
		Limit:  filter.Limit,
		Offset: filter.Offset,
	}, nil
}

func (r *OrganizationRepository) GetPlatformOrganization(ctx context.Context, userID int64, organizationID uuid.UUID) (*domain.PlatformOrganization, error) {
	if err := r.ensurePlatformAdmin(ctx, userID); err != nil {
		return nil, err
	}

	var item domain.PlatformOrganization
	var userCount, unitCount int
	err := r.db.QueryRow(ctx, `
		SELECT o.id, o.name, o.slug, o.status, o.created_at, o.updated_at,
		       o.owner_id,
		       COALESCE(NULLIF(TRIM(owner.full_name_local), ''), NULLIF(TRIM(CONCAT_WS(' ', owner.first_name, owner.last_name)), ''), owner.username, owner.email, ''),
		       COALESCE(owner.email, ''),
		       (SELECT COUNT(*)::int FROM users u WHERE u.organization_id = o.id AND u.deleted_at IS NULL),
		       (SELECT COUNT(*)::int FROM teams t WHERE t.organization_id = o.id AND t.deleted_at IS NULL)
		FROM organizations o
		LEFT JOIN users owner ON owner.id = o.owner_id AND owner.deleted_at IS NULL
		WHERE o.id = $1 AND o.status <> 'deleted'
	`, organizationID).Scan(
		&item.ID, &item.Name, &item.Slug, &item.Status, &item.CreatedAt, &item.UpdatedAt,
		&item.OwnerID, &item.OwnerName, &item.OwnerEmail, &userCount, &unitCount,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrOrganizationNotFound
		}
		return nil, fmt.Errorf("get platform organization: %w", err)
	}
	item.UserCount = userCount
	item.UnitCount = unitCount
	return &item, nil
}

// CreatePlatformOrganization creates a tenant without changing the
// platform administrator's organization_id. Customer ownership is assigned
// later by the onboarding/invitation flow.
func (r *OrganizationRepository) CreatePlatformOrganization(ctx context.Context, userID int64, req domain.CreateOrganizationRequest) (*domain.PlatformOrganization, error) {
	if err := r.ensurePlatformAdmin(ctx, userID); err != nil {
		return nil, err
	}

	var err error
	req, err = req.Normalize()
	if err != nil {
		return nil, err
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err := authorizeAdministrationLocked(ctx, tx, userID, nil, true); err != nil {
		return nil, err
	}
	var organizationID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO organizations (name, slug, status)
		VALUES ($1, $2, 'active')
		RETURNING id
	`, req.Name, req.Slug).Scan(&organizationID); err != nil {
		return nil, fmt.Errorf("create platform organization: %w", err)
	}
	after, err := administrationSnapshot(ctx, tx, organizationID, "organization", organizationID.String())
	if err != nil {
		return nil, err
	}
	if err := writeAdministrationAudit(ctx, tx, organizationID, userID, "organization.create", "organization", organizationID.String(), nil, after); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return r.GetPlatformOrganization(ctx, userID, organizationID)
}

// ListPlatformOrganizationOwnerCandidates returns active users that are not
// yet assigned to a customer organization. Platform administrators are
// deliberately excluded because they belong to the platform itself.
func (r *OrganizationRepository) ListPlatformOrganizationOwnerCandidates(ctx context.Context, userID int64, organizationID *uuid.UUID, query string, limit int) ([]domain.OrganizationOwnerCandidate, error) {
	if err := r.ensurePlatformAdmin(ctx, userID); err != nil {
		return nil, err
	}

	rows, err := r.db.Query(ctx, `
		WITH candidates AS (
			SELECT u.id,
			       COALESCE(
				       NULLIF(TRIM(u.full_name_local), ''),
				       NULLIF(TRIM(CONCAT_WS(' ', u.first_name, u.last_name)), ''),
				       NULLIF(TRIM(u.username), ''),
				       u.email,
				       ''
			       ) AS full_name,
			       u.email,
			       u.username
			FROM users u
			WHERE u.deleted_at IS NULL
			  AND u.is_active = TRUE
			  AND COALESCE(u.role, 'user') <> 'admin'
			  AND ($2::uuid IS NULL OR u.organization_id IS NULL OR u.organization_id = $2::uuid)
		)
		SELECT id, full_name, email, username
		FROM candidates
		WHERE $1 = ''
		   OR full_name ILIKE '%' || $1 || '%'
		   OR COALESCE(email, '') ILIKE '%' || $1 || '%'
		   OR COALESCE(username, '') ILIKE '%' || $1 || '%'
		ORDER BY full_name, id
		LIMIT $3
	`, strings.TrimSpace(query), organizationID, limit)
	if err != nil {
		return nil, fmt.Errorf("list organization owner candidates: %w", err)
	}
	defer rows.Close()

	candidates := make([]domain.OrganizationOwnerCandidate, 0)
	for rows.Next() {
		var candidate domain.OrganizationOwnerCandidate
		if err := rows.Scan(&candidate.ID, &candidate.FullName, &candidate.Email, &candidate.Username); err != nil {
			return nil, fmt.Errorf("scan organization owner candidate: %w", err)
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate organization owner candidates: %w", err)
	}
	return candidates, nil
}

// AssignPlatformOrganizationOwner assigns an existing local user to a
// customer organization and makes that user its owner atomically.
func (r *OrganizationRepository) AssignPlatformOrganizationOwner(ctx context.Context, userID int64, organizationID uuid.UUID, targetUserID int64) (*domain.PlatformOrganization, error) {
	if err := r.ensurePlatformAdmin(ctx, userID); err != nil {
		return nil, err
	}
	if targetUserID <= 0 {
		return nil, fmt.Errorf("organization owner user id is invalid")
	}

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin organization owner assignment: %w", err)
	}
	defer tx.Rollback(ctx)

	var lockedOrganizationID uuid.UUID
	if err := tx.QueryRow(ctx, `
		SELECT id
		FROM organizations
		WHERE id = $1 AND status <> 'deleted'
		FOR UPDATE
	`, organizationID).Scan(&lockedOrganizationID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("organization not found")
		}
		return nil, fmt.Errorf("lock organization for owner assignment: %w", err)
	}
	if err := authorizeAdministrationLocked(ctx, tx, userID, nil, true); err != nil {
		return nil, err
	}

	before, err := administrationSnapshot(ctx, tx, organizationID, "organization", organizationID.String())
	if err != nil {
		return nil, err
	}
	var assignedOrganization *uuid.UUID
	var targetRole domain.Role
	var isActive bool
	if err := tx.QueryRow(ctx, `
		SELECT organization_id, role, is_active
		FROM users
		WHERE id = $1 AND deleted_at IS NULL
		FOR UPDATE
	`, targetUserID).Scan(&assignedOrganization, &targetRole, &isActive); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("organization owner user not found")
		}
		return nil, fmt.Errorf("resolve organization owner user: %w", err)
	}
	if !isActive {
		return nil, fmt.Errorf("organization owner user must be active")
	}
	if targetRole == domain.RoleSuperAdmin {
		return nil, fmt.Errorf("platform administrator cannot be a customer organization owner")
	}
	if assignedOrganization != nil && *assignedOrganization != organizationID {
		return nil, fmt.Errorf("user is already assigned to another organization")
	}

	if _, err := tx.Exec(ctx, `
		UPDATE users
		SET team_id = CASE WHEN organization_id = $1 THEN team_id ELSE NULL END,
		    manager_id = CASE WHEN organization_id = $1 THEN manager_id ELSE NULL END,
		    organization_id = $1, updated_at = NOW()
		WHERE id = $2
	`, organizationID, targetUserID); err != nil {
		return nil, fmt.Errorf("assign organization owner user: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE organizations
		SET owner_id = $2, updated_at = NOW()
		WHERE id = $1
	`, organizationID, targetUserID); err != nil {
		return nil, fmt.Errorf("update organization owner: %w", err)
	}
	after, err := administrationSnapshot(ctx, tx, organizationID, "organization", organizationID.String())
	if err != nil {
		return nil, err
	}
	if err := writeAdministrationAudit(ctx, tx, organizationID, userID, "organization.owner.assign", "organization", organizationID.String(), before, after); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit organization owner assignment: %w", err)
	}

	return r.GetPlatformOrganization(ctx, userID, organizationID)
}

func (r *OrganizationRepository) UpdatePlatformOrganization(ctx context.Context, userID int64, organizationID uuid.UUID, req domain.UpdatePlatformOrganizationRequest) (*domain.PlatformOrganization, error) {
	if err := r.ensurePlatformAdmin(ctx, userID); err != nil {
		return nil, err
	}
	if err := req.Validate(); err != nil {
		return nil, err
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var lockedID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM organizations WHERE id=$1 AND status<>'deleted' FOR UPDATE`, organizationID).Scan(&lockedID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrOrganizationNotFound
		}
		return nil, err
	}
	if err := authorizeAdministrationLocked(ctx, tx, userID, nil, true); err != nil {
		return nil, err
	}

	before, err := administrationSnapshot(ctx, tx, organizationID, "organization", organizationID.String())
	if err != nil {
		return nil, err
	}
	var name, status *string
	if req.Name != nil {
		value := strings.TrimSpace(*req.Name)
		name = &value
	}
	if req.Status != nil {
		value := strings.ToLower(strings.TrimSpace(*req.Status))
		status = &value
	}

	result, err := tx.Exec(ctx, `
		UPDATE organizations
		SET name = COALESCE($2, name),
		    status = COALESCE($3, status),
		    updated_at = NOW()
		WHERE id = $1
	`, organizationID, name, status)
	if err != nil {
		return nil, fmt.Errorf("update platform organization: %w", err)
	}
	if result.RowsAffected() == 0 {
		return nil, fmt.Errorf("organization not found")
	}
	after, err := administrationSnapshot(ctx, tx, organizationID, "organization", organizationID.String())
	if err != nil {
		return nil, err
	}
	if err := writeAdministrationAudit(ctx, tx, organizationID, userID, "organization.update", "organization", organizationID.String(), before, after); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return r.GetPlatformOrganization(ctx, userID, organizationID)
}

func (r *OrganizationRepository) ensurePlatformAdmin(ctx context.Context, userID int64) error {
	return authorizeAdministration(ctx, r.db, userID, nil, true)
}

func (r *OrganizationRepository) platformOrganizationScope(ctx context.Context, userID int64, organizationID uuid.UUID) (organizationScope, error) {
	if err := r.ensurePlatformAdmin(ctx, userID); err != nil {
		return organizationScope{}, err
	}
	var exists bool
	if err := r.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM organizations WHERE id = $1 AND status<>'deleted')`, organizationID).Scan(&exists); err != nil {
		return organizationScope{}, fmt.Errorf("resolve platform organization scope: %w", err)
	}
	if !exists {
		return organizationScope{}, fmt.Errorf("organization not found")
	}
	return organizationScope{ID: organizationID, Role: domain.RoleSuperAdmin}, nil
}

func (r *OrganizationRepository) CurrentOrganization(ctx context.Context, userID int64) (*domain.Organization, error) {
	scope, err := r.currentScope(ctx, userID)
	if err != nil {
		return nil, err
	}
	var item domain.Organization
	if err := r.db.QueryRow(ctx, `
		SELECT o.id, o.name, o.slug, o.status, o.owner_id,
		       COALESCE(NULLIF(TRIM(owner.full_name_local), ''), NULLIF(TRIM(CONCAT_WS(' ', owner.first_name, owner.last_name)), ''), owner.username, owner.email, ''),
		       COALESCE(owner.email, ''), o.created_at, o.updated_at
		FROM organizations o
		LEFT JOIN users owner ON owner.id = o.owner_id AND owner.deleted_at IS NULL
		WHERE o.id = $1 AND o.status = 'active'
	`, scope.ID).Scan(
		&item.ID, &item.Name, &item.Slug, &item.Status,
		&item.OwnerID, &item.OwnerName, &item.OwnerEmail,
		&item.CreatedAt, &item.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("get current organization: %w", err)
	}
	item.CanManageStructure = scope.Role == domain.RoleSuperAdmin || (scope.OwnerID != nil && *scope.OwnerID == userID)
	return &item, nil
}

// OrganizationSlugByUserID returns the active tenant slug assigned to a user.
// The slug is also the canonical tenant subdomain under TENANT_BASE_DOMAIN.
func (r *OrganizationRepository) OrganizationSlugByUserID(ctx context.Context, userID int64) (string, error) {
	var slug string
	if err := r.db.QueryRow(ctx, `
		SELECT o.slug
		FROM users u
		JOIN organizations o ON o.id = u.organization_id AND o.status = 'active'
		WHERE u.id = $1 AND u.deleted_at IS NULL
	`, userID).Scan(&slug); err != nil {
		return "", fmt.Errorf("resolve organization slug for user: %w", err)
	}
	return strings.ToLower(strings.TrimSpace(slug)), nil
}

func (r *OrganizationRepository) GrowyHandoffDetailsByUserID(ctx context.Context, userID int64) (*domain.GrowyHandoffDetails, error) {
	return r.growyHandoffDetailsForModule(ctx, userID, "growy")
}

func (r *OrganizationRepository) GrowyCRMHandoffDetailsByUserID(ctx context.Context, userID int64) (*domain.GrowyHandoffDetails, error) {
	return r.growyHandoffDetailsForModule(ctx, userID, "crm")
}

// Both deployments share position roles, but company module grants are independent.
func (r *OrganizationRepository) growyHandoffDetailsForModule(ctx context.Context, userID int64, application string) (*domain.GrowyHandoffDetails, error) {
	var details domain.GrowyHandoffDetails
	err := r.db.QueryRow(ctx, `
		SELECT o.id, o.name, o.slug, COALESCE(BTRIM(u.position), ''),
		       CASE WHEN o.owner_id = u.id THEN 'tenant_admin'
	            WHEN NULLIF(BTRIM(u.position), '') IS NULL THEN 'employee'
		            ELSE COALESCE(mapping.growy_role, 'employee') END,
		       CASE WHEN o.owner_id = u.id THEN ARRAY['tenant_admin']::TEXT[] ||
		            array_remove(COALESCE(mapping.growy_roles, ARRAY[]::TEXT[]), 'tenant_admin')
		            WHEN NULLIF(BTRIM(u.position), '') IS NULL THEN ARRAY['employee']::TEXT[]
		            ELSE COALESCE(mapping.growy_roles, ARRAY['employee']::TEXT[]) END
		FROM users u
	JOIN organizations o ON o.id = u.organization_id AND o.status = 'active'
	JOIN organization_modules module ON module.organization_id=o.id AND module.application=$2 AND module.available AND module.enabled
	LEFT JOIN organization_growy_position_roles mapping
	  ON mapping.organization_id = o.id
	 AND mapping.position_key = LOWER(BTRIM(u.position))
	WHERE u.id = $1 AND u.deleted_at IS NULL AND u.is_active = TRUE
	`, userID, application).Scan(&details.OrganizationID, &details.OrganizationName, &details.OrganizationSlug, &details.Position, &details.GrowyRole, &details.GrowyRoles)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrOrganizationNotFound
		}
		return nil, fmt.Errorf("resolve Growy handoff company and role: %w", err)
	}
	return &details, nil
}

func (r *OrganizationRepository) currentScope(ctx context.Context, userID int64) (organizationScope, error) {
	var scope organizationScope
	if err := requireActiveAdministrationActor(ctx, r.db, userID); err != nil {
		return scope, err
	}
	if err := r.db.QueryRow(ctx, `
		SELECT o.id, u.role, o.owner_id
		FROM users u
		JOIN LATERAL (
			SELECT candidate.id, candidate.owner_id
			FROM organizations candidate
			WHERE candidate.status = 'active'
			  AND (
				candidate.id = u.organization_id
				OR (u.organization_id IS NULL AND candidate.owner_id = u.id)
			  )
			ORDER BY CASE WHEN candidate.id = u.organization_id THEN 0 ELSE 1 END,
			         candidate.updated_at DESC,
			         candidate.id
			LIMIT 1
		) o ON TRUE
		WHERE u.id = $1 AND u.deleted_at IS NULL
	`, userID).Scan(&scope.ID, &scope.Role, &scope.OwnerID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return scope, domain.ErrOrganizationNotFound
		}
		return scope, fmt.Errorf("resolve organization scope: %w", err)
	}
	return scope, nil
}

func (r *OrganizationRepository) ListUnits(ctx context.Context, userID int64) ([]domain.OrganizationUnit, error) {
	scope, err := r.currentScope(ctx, userID)
	if err != nil {
		return nil, err
	}
	return r.listUnitsForOrganization(ctx, scope.ID)
}

func (r *OrganizationRepository) ListPlatformUnits(ctx context.Context, userID int64, organizationID uuid.UUID) ([]domain.OrganizationUnit, error) {
	scope, err := r.platformOrganizationScope(ctx, userID, organizationID)
	if err != nil {
		return nil, err
	}
	return r.listUnitsForOrganization(ctx, scope.ID)
}

func (r *OrganizationRepository) listUnitsForOrganization(ctx context.Context, organizationID uuid.UUID) ([]domain.OrganizationUnit, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, organization_id, parent_team_id, name, unit_type,
		       description, manager_id, display_order, created_at, updated_at
		FROM teams
		WHERE organization_id = $1 AND deleted_at IS NULL
		ORDER BY display_order, name, id
	`, organizationID)
	if err != nil {
		return nil, fmt.Errorf("list organization units: %w", err)
	}
	defer rows.Close()

	units := make([]domain.OrganizationUnit, 0)
	for rows.Next() {
		var item domain.OrganizationUnit
		if err := rows.Scan(
			&item.ID, &item.OrganizationID, &item.ParentUnitID, &item.Name,
			&item.UnitType, &item.Description, &item.ManagerID, &item.DisplayOrder,
			&item.CreatedAt, &item.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan organization unit: %w", err)
		}
		units = append(units, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate organization units: %w", err)
	}
	return units, nil
}

func (r *OrganizationRepository) ListMembers(ctx context.Context, userID int64) ([]domain.OrganizationMember, error) {
	scope, err := r.currentScope(ctx, userID)
	if err != nil {
		return nil, err
	}
	return r.listMembersForOrganization(ctx, scope.ID)
}

func (r *OrganizationRepository) ListPlatformMembers(ctx context.Context, userID int64, organizationID uuid.UUID) ([]domain.OrganizationMember, error) {
	scope, err := r.platformOrganizationScope(ctx, userID, organizationID)
	if err != nil {
		return nil, err
	}
	return r.listMembersForOrganization(ctx, scope.ID)
}

func (r *OrganizationRepository) ListGrowyPositionRoles(ctx context.Context, userID int64) ([]domain.GrowyPositionRole, error) {
	scope, err := r.currentScope(ctx, userID)
	if err != nil {
		return nil, err
	}
	if err := authorizeAdministration(ctx, r.db, userID, scope.OwnerID, false); err != nil {
		return nil, err
	}
	return r.listGrowyPositionRolesForOrganization(ctx, scope.ID)
}

func (r *OrganizationRepository) ListPlatformGrowyPositionRoles(ctx context.Context, userID int64, organizationID uuid.UUID) ([]domain.GrowyPositionRole, error) {
	scope, err := r.platformOrganizationScope(ctx, userID, organizationID)
	if err != nil {
		return nil, err
	}
	return r.listGrowyPositionRolesForOrganization(ctx, scope.ID)
}

func (r *OrganizationRepository) listGrowyPositionRolesForOrganization(ctx context.Context, organizationID uuid.UUID) ([]domain.GrowyPositionRole, error) {
	rows, err := r.db.Query(ctx, `
		SELECT MIN(BTRIM(u.position)) AS position_name, COALESCE(mapping.growy_role, 'employee'),
		       COALESCE(mapping.growy_roles, ARRAY['employee']::TEXT[]), COUNT(*)::int
		FROM users u
	LEFT JOIN organization_growy_position_roles mapping
	  ON mapping.organization_id = u.organization_id
	 AND mapping.position_key = LOWER(BTRIM(u.position))
		WHERE u.organization_id = $1 AND u.deleted_at IS NULL AND u.is_active = TRUE
		  AND NULLIF(BTRIM(u.position), '') IS NOT NULL
		GROUP BY LOWER(BTRIM(u.position)), mapping.growy_role, mapping.growy_roles
		ORDER BY LOWER(MIN(BTRIM(u.position))), MIN(BTRIM(u.position))
	`, organizationID)
	if err != nil {
		return nil, fmt.Errorf("list Growy job title roles: %w", err)
	}
	defer rows.Close()

	items := make([]domain.GrowyPositionRole, 0)
	for rows.Next() {
		var item domain.GrowyPositionRole
		if err := rows.Scan(&item.Position, &item.GrowyRole, &item.GrowyRoles, &item.UserCount); err != nil {
			return nil, fmt.Errorf("scan Growy job title role: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Growy job title roles: %w", err)
	}
	return items, nil
}

func (r *OrganizationRepository) SaveGrowyPositionRoles(ctx context.Context, userID int64, mappings []domain.GrowyPositionRole) error {
	scope, err := r.currentScope(ctx, userID)
	if err != nil {
		return err
	}
	if err := authorizeAdministration(ctx, r.db, userID, scope.OwnerID, false); err != nil {
		return err
	}
	return auditOrganizationMutationError(ctx, r, userID, scope.ID, false, "growy.roles.update", "growy_roles", "", func(inner *OrganizationRepository) error {
		return inner.saveGrowyPositionRolesForOrganization(ctx, scope.ID, mappings)
	})
}

func (r *OrganizationRepository) SavePlatformGrowyPositionRoles(ctx context.Context, userID int64, organizationID uuid.UUID, mappings []domain.GrowyPositionRole) error {
	scope, err := r.platformOrganizationScope(ctx, userID, organizationID)
	if err != nil {
		return err
	}
	return auditOrganizationMutationError(ctx, r, userID, scope.ID, true, "growy.roles.update", "growy_roles", "", func(inner *OrganizationRepository) error {
		return inner.saveGrowyPositionRolesForOrganization(ctx, scope.ID, mappings)
	})
}

func (r *OrganizationRepository) saveGrowyPositionRolesForOrganization(ctx context.Context, organizationID uuid.UUID, mappings []domain.GrowyPositionRole) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin save Growy job title roles: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `DELETE FROM organization_growy_position_roles WHERE organization_id = $1`, organizationID); err != nil {
		return fmt.Errorf("replace Growy job title roles: %w", err)
	}
	for _, item := range mappings {
		result, err := tx.Exec(ctx, `
			INSERT INTO organization_growy_position_roles
			    (organization_id, position_key, position_name, growy_role, growy_roles, updated_at)
			SELECT $1, LOWER(BTRIM($2)), BTRIM($2), $3, $4::TEXT[], NOW()
			WHERE EXISTS (
			    SELECT 1 FROM users
			    WHERE organization_id = $1 AND deleted_at IS NULL AND is_active = TRUE
			      AND LOWER(BTRIM(position)) = LOWER(BTRIM($2))
			)
			ON CONFLICT (organization_id, position_key)
			DO UPDATE SET position_name = EXCLUDED.position_name,
			              growy_role = EXCLUDED.growy_role,
			              growy_roles = EXCLUDED.growy_roles,
			              updated_at = NOW()
		`, organizationID, item.Position, item.GrowyRole, item.GrowyRoles)
		if err != nil {
			return fmt.Errorf("save Growy job title role: %w", err)
		}
		if result.RowsAffected() != 1 {
			return fmt.Errorf("job title is not assigned to an active user in this organization")
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit Growy job title roles: %w", err)
	}
	return nil
}

func (r *OrganizationRepository) listMembersForOrganization(ctx context.Context, organizationID uuid.UUID) ([]domain.OrganizationMember, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, keycloak_id, COALESCE(NULLIF(full_name_local, ''), CONCAT(first_name, ' ', last_name)),
		       email, username, position, avatar_url, availability_status,
		       team_id, manager_id, role, is_active, COALESCE(first_name,''), COALESCE(last_name,'')
		FROM users
		WHERE organization_id = $1 AND deleted_at IS NULL
		ORDER BY first_name, last_name, id
	`, organizationID)
	if err != nil {
		return nil, fmt.Errorf("list organization members: %w", err)
	}
	defer rows.Close()

	members := make([]domain.OrganizationMember, 0)
	for rows.Next() {
		var member domain.OrganizationMember
		var role domain.Role
		if err := rows.Scan(
			&member.ID,
			&member.KeycloakID,
			&member.FullName,
			&member.Email,
			&member.Username,
			&member.Position,
			&member.AvatarURL,
			&member.AvailabilityStatus,
			&member.UnitID,
			&member.ManagerID,
			&role,
			&member.IsActive,
			&member.FirstName,
			&member.LastName,
		); err != nil {
			return nil, fmt.Errorf("scan organization member: %w", err)
		}
		if normalizedRole := strings.TrimSpace(string(role)); normalizedRole != "" {
			member.Roles = []string{normalizedRole}
		} else {
			member.Roles = []string{}
		}
		members = append(members, member)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate organization members: %w", err)
	}
	return members, nil
}

func (r *OrganizationRepository) CreateUnit(ctx context.Context, userID int64, req domain.CreateOrganizationUnitRequest) (*domain.OrganizationUnit, error) {
	scope, err := r.currentScope(ctx, userID)
	if err != nil {
		return nil, err
	}
	if err := authorizeAdministration(ctx, r.db, userID, scope.OwnerID, false); err != nil {
		return nil, err
	}
	return auditOrganizationMutation(ctx, r, userID, scope.ID, false, "unit.create", "unit", "", func(inner *OrganizationRepository) (*domain.OrganizationUnit, error) {
		return inner.createUnitForOrganization(ctx, scope.ID, req)
	})
}

func (r *OrganizationRepository) CreatePlatformUnit(ctx context.Context, userID int64, organizationID uuid.UUID, req domain.CreateOrganizationUnitRequest) (*domain.OrganizationUnit, error) {
	scope, err := r.platformOrganizationScope(ctx, userID, organizationID)
	if err != nil {
		return nil, err
	}
	return auditOrganizationMutation(ctx, r, userID, scope.ID, true, "unit.create", "unit", "", func(inner *OrganizationRepository) (*domain.OrganizationUnit, error) {
		return inner.createUnitForOrganization(ctx, scope.ID, req)
	})
}

func (r *OrganizationRepository) createUnitForOrganization(ctx context.Context, organizationID uuid.UUID, req domain.CreateOrganizationUnitRequest) (*domain.OrganizationUnit, error) {
	if err := r.ensureParent(ctx, organizationID, req.ParentUnitID, nil); err != nil {
		return nil, err
	}
	if err := r.ensureManager(ctx, organizationID, req.ManagerID); err != nil {
		return nil, err
	}

	var item domain.OrganizationUnit
	var err error
	err = r.db.QueryRow(ctx, `
		INSERT INTO teams (organization_id, parent_team_id, name, kind, unit_type,
		                   description, manager_id, display_order)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, organization_id, parent_team_id, name, unit_type,
		          description, manager_id, display_order, created_at, updated_at
	`, organizationID, req.ParentUnitID, req.Name, req.UnitType, req.UnitType,
		req.Description, req.ManagerID, req.DisplayOrder).Scan(
		&item.ID, &item.OrganizationID, &item.ParentUnitID, &item.Name,
		&item.UnitType, &item.Description, &item.ManagerID, &item.DisplayOrder,
		&item.CreatedAt, &item.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create organization unit: %w", err)
	}
	return &item, nil
}

func (r *OrganizationRepository) UpdateUnit(ctx context.Context, userID int64, unitID uuid.UUID, req domain.UpdateOrganizationUnitRequest) (*domain.OrganizationUnit, error) {
	scope, err := r.currentScope(ctx, userID)
	if err != nil {
		return nil, err
	}
	if err := authorizeAdministration(ctx, r.db, userID, scope.OwnerID, false); err != nil {
		return nil, err
	}
	return auditOrganizationMutation(ctx, r, userID, scope.ID, false, "unit.update", "unit", unitID.String(), func(inner *OrganizationRepository) (*domain.OrganizationUnit, error) {
		return inner.updateUnitForOrganization(ctx, scope.ID, unitID, req)
	})
}

func (r *OrganizationRepository) UpdatePlatformUnit(ctx context.Context, userID int64, organizationID, unitID uuid.UUID, req domain.UpdateOrganizationUnitRequest) (*domain.OrganizationUnit, error) {
	scope, err := r.platformOrganizationScope(ctx, userID, organizationID)
	if err != nil {
		return nil, err
	}
	return auditOrganizationMutation(ctx, r, userID, scope.ID, true, "unit.update", "unit", unitID.String(), func(inner *OrganizationRepository) (*domain.OrganizationUnit, error) {
		return inner.updateUnitForOrganization(ctx, scope.ID, unitID, req)
	})
}

func (r *OrganizationRepository) updateUnitForOrganization(ctx context.Context, organizationID, unitID uuid.UUID, req domain.UpdateOrganizationUnitRequest) (*domain.OrganizationUnit, error) {
	if !req.ClearParent {
		if err := r.ensureParent(ctx, organizationID, req.ParentUnitID, &unitID); err != nil {
			return nil, err
		}
	}
	if err := r.ensureManager(ctx, organizationID, req.ManagerID); err != nil {
		return nil, err
	}

	var name, unitType *string
	if req.Name != nil {
		value := strings.TrimSpace(*req.Name)
		name = &value
	}
	if req.UnitType != nil {
		value := strings.TrimSpace(*req.UnitType)
		unitType = &value
	}
	parentChanged := req.ParentUnitID != nil || req.ClearParent
	managerChanged := req.ManagerID != nil || req.ClearManager
	var displayOrder *int
	if req.DisplayOrder != nil {
		displayOrder = req.DisplayOrder
	}

	var item domain.OrganizationUnit
	var err error
	err = r.db.QueryRow(ctx, `
		UPDATE teams
		SET parent_team_id = CASE WHEN $3 THEN $4 ELSE parent_team_id END,
		    name = COALESCE($5, name),
		    unit_type = COALESCE($6, unit_type),
		    kind = COALESCE($6, kind),
		    description = COALESCE($7, description),
		    manager_id = CASE WHEN $8 THEN $9 ELSE manager_id END,
		    display_order = COALESCE($10, display_order),
		    updated_at = NOW()
		WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL
		RETURNING id, organization_id, parent_team_id, name, unit_type,
		          description, manager_id, display_order, created_at, updated_at
	`, unitID, organizationID, parentChanged, req.ParentUnitID, name, unitType,
		req.Description, managerChanged, req.ManagerID, displayOrder).Scan(
		&item.ID, &item.OrganizationID, &item.ParentUnitID, &item.Name,
		&item.UnitType, &item.Description, &item.ManagerID, &item.DisplayOrder,
		&item.CreatedAt, &item.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("organization unit not found")
		}
		return nil, fmt.Errorf("update organization unit: %w", err)
	}
	return &item, nil
}

func (r *OrganizationRepository) ArchiveUnit(ctx context.Context, userID int64, unitID uuid.UUID) error {
	scope, err := r.currentScope(ctx, userID)
	if err != nil {
		return err
	}
	if err := authorizeAdministration(ctx, r.db, userID, scope.OwnerID, false); err != nil {
		return err
	}
	return auditOrganizationMutationError(ctx, r, userID, scope.ID, false, "unit.archive", "unit", unitID.String(), func(inner *OrganizationRepository) error {
		return inner.archiveUnitForOrganization(ctx, scope.ID, unitID)
	})
}

func (r *OrganizationRepository) ArchivePlatformUnit(ctx context.Context, userID int64, organizationID, unitID uuid.UUID) error {
	scope, err := r.platformOrganizationScope(ctx, userID, organizationID)
	if err != nil {
		return err
	}
	return auditOrganizationMutationError(ctx, r, userID, scope.ID, true, "unit.archive", "unit", unitID.String(), func(inner *OrganizationRepository) error {
		return inner.archiveUnitForOrganization(ctx, scope.ID, unitID)
	})
}

func (r *OrganizationRepository) archiveUnitForOrganization(ctx context.Context, organizationID, unitID uuid.UUID) error {
	var hasChildren, hasUsers bool
	if err := r.db.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM teams WHERE parent_team_id = $1 AND deleted_at IS NULL),
		       EXISTS(SELECT 1 FROM users WHERE team_id = $1 AND deleted_at IS NULL)
		FROM teams
		WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL
	`, unitID, organizationID).Scan(&hasChildren, &hasUsers); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("organization unit not found")
		}
		return fmt.Errorf("check organization unit dependencies: %w", err)
	}
	if hasChildren || hasUsers {
		return fmt.Errorf("organization unit must be empty before archiving")
	}
	result, err := r.db.Exec(ctx, `
		UPDATE teams SET deleted_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL
	`, unitID, organizationID)
	if err != nil {
		return fmt.Errorf("archive organization unit: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("organization unit not found")
	}
	return nil
}

func (r *OrganizationRepository) AssignUser(ctx context.Context, actorID, targetUserID int64, req domain.AssignUserToUnitRequest) error {
	scope, err := r.currentScope(ctx, actorID)
	if err != nil {
		return err
	}
	if err := authorizeAdministration(ctx, r.db, actorID, scope.OwnerID, false); err != nil {
		return err
	}
	return auditOrganizationMutationError(ctx, r, actorID, scope.ID, false, "user.assignment", "user_assignment", fmt.Sprint(targetUserID), func(inner *OrganizationRepository) error {
		return inner.assignUserForOrganization(ctx, scope.ID, targetUserID, req)
	})
}

func (r *OrganizationRepository) AssignPlatformUser(ctx context.Context, userID int64, organizationID uuid.UUID, targetUserID int64, req domain.AssignUserToUnitRequest) error {
	scope, err := r.platformOrganizationScope(ctx, userID, organizationID)
	if err != nil {
		return err
	}
	return auditOrganizationMutationError(ctx, r, userID, scope.ID, true, "user.assignment", "user_assignment", fmt.Sprint(targetUserID), func(inner *OrganizationRepository) error {
		return inner.assignUserForOrganization(ctx, scope.ID, targetUserID, req)
	})
}

func (r *OrganizationRepository) assignUserForOrganization(ctx context.Context, organizationID uuid.UUID, targetUserID int64, req domain.AssignUserToUnitRequest) error {
	if req.UnitID != nil {
		if err := r.ensureParent(ctx, organizationID, req.UnitID, nil); err != nil {
			return err
		}
	}
	if err := r.ensureManager(ctx, organizationID, req.ManagerID); err != nil {
		return err
	}
	result, err := r.db.Exec(ctx, `
		UPDATE users
		SET team_id = $3, manager_id = $4
		WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL
	`, targetUserID, organizationID, req.UnitID, req.ManagerID)
	if err != nil {
		return fmt.Errorf("assign user to organization unit: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("user is outside the current organization")
	}
	return nil
}

func (r *OrganizationRepository) ensureParent(ctx context.Context, organizationID uuid.UUID, parentID *uuid.UUID, selfID *uuid.UUID) error {
	if parentID == nil {
		return nil
	}
	if selfID != nil && *parentID == *selfID {
		return fmt.Errorf("organization unit cannot be its own parent")
	}
	var exists bool
	if err := r.db.QueryRow(ctx, `
		SELECT EXISTS(
			WITH RECURSIVE descendants AS (
				SELECT id FROM teams WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL
				UNION
				SELECT t.id FROM teams t JOIN descendants d ON t.parent_team_id = d.id
				WHERE t.organization_id = $2 AND t.deleted_at IS NULL
			)
			SELECT 1 FROM descendants WHERE id = $3
		)
	`, func() uuid.UUID {
		if selfID != nil {
			return *selfID
		}
		return uuid.Nil
	}(), organizationID, *parentID).Scan(&exists); err != nil {
		return fmt.Errorf("validate organization unit parent: %w", err)
	}
	if selfID != nil && exists {
		return fmt.Errorf("organization unit cannot be moved inside its own subtree")
	}
	var parentExists bool
	if err := r.db.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM teams WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL)
	`, *parentID, organizationID).Scan(&parentExists); err != nil {
		return fmt.Errorf("validate organization unit parent: %w", err)
	}
	if !parentExists {
		return fmt.Errorf("parent organization unit not found")
	}
	return nil
}

func (r *OrganizationRepository) ensureManager(ctx context.Context, organizationID uuid.UUID, managerID *int64) error {
	if managerID == nil {
		return nil
	}
	var exists bool
	if err := r.db.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM users WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL AND is_active=TRUE)
	`, *managerID, organizationID).Scan(&exists); err != nil {
		return fmt.Errorf("validate unit manager: %w", err)
	}
	if !exists {
		return fmt.Errorf("unit manager is outside the current organization or inactive")
	}
	return nil
}
