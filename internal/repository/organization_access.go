package repository

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"strconv"
	"strings"
)

// orgID is accepted only on platform routes. Tenant routes always derive it from the actor.
func (r *OrganizationRepository) accessScope(ctx context.Context, actor int64, orgID *uuid.UUID) (organizationScope, error) {
	var scope organizationScope
	var err error
	if orgID != nil {
		scope, err = r.platformOrganizationScope(ctx, actor, *orgID)
	} else {
		scope, err = r.currentScope(ctx, actor)
	}
	if err != nil {
		return scope, err
	}
	if err = authorizeAdministration(ctx, r.db, actor, scope.OwnerID, orgID != nil); err != nil {
		return scope, err
	}
	return scope, nil
}
func (r *OrganizationRepository) OrganizationAccess(ctx context.Context, actor int64, orgID *uuid.UUID) (*domain.OrganizationAccess, error) {
	s, err := r.accessScope(ctx, actor, orgID)
	if err != nil {
		return nil, err
	}
	out := &domain.OrganizationAccess{MemberPositions: map[string]string{}, Positions: []domain.AccessPosition{}, Catalog: domain.AccessCatalog(), Apps: map[string]domain.AccessApp{}}
	for app := range domain.AccessCatalog() {
		out.Apps[app] = domain.AccessApp{}
	}
	rows, err := r.db.Query(ctx, `SELECT p.id,p.name,p.revision,(SELECT count(*) FROM organization_access_assignments a JOIN users u ON u.id=a.user_id AND u.organization_id=a.organization_id WHERE a.organization_id=p.organization_id AND a.position_id=p.id AND u.deleted_at IS NULL) FROM organization_access_positions p WHERE p.organization_id=$1 ORDER BY p.name`, s.ID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var p domain.AccessPosition
		if err = rows.Scan(&p.ID, &p.Name, &p.Revision, &p.UserCount); err != nil {
			rows.Close()
			return nil, err
		}
		p.Policies = map[string]domain.AccessPolicy{}
		for app := range domain.AccessCatalog() {
			p.Policies[app] = domain.AccessPolicy{Permissions: []string{}}
		}
		out.Positions = append(out.Positions, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = r.db.Query(ctx, `SELECT position_id,application,permissions,revision FROM organization_position_permissions WHERE organization_id=$1`, s.ID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id uuid.UUID
		var app string
		var p domain.AccessPolicy
		p.Configured = true
		if err = rows.Scan(&id, &app, &p.Permissions, &p.Revision); err != nil {
			rows.Close()
			return nil, err
		}
		for i := range out.Positions {
			if out.Positions[i].ID == id {
				out.Positions[i].Policies[app] = p
			}
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = r.db.Query(ctx, `SELECT catalog.application,COALESCE(a.enabled,false),COALESCE(a.revision,0),COALESCE(m.available,false),COALESCE(m.enabled,false) FROM (VALUES ('iform'),('odo'),('sd')) catalog(application) LEFT JOIN organization_access_apps a ON a.organization_id=$1 AND a.application=catalog.application LEFT JOIN organization_modules m ON m.organization_id=$1 AND m.application=catalog.application`, s.ID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var app string
		var a domain.AccessApp
		if err = rows.Scan(&app, &a.Enabled, &a.Revision, &a.Available, &a.ModuleEnabled); err != nil {
			rows.Close()
			return nil, err
		}
		out.Apps[app] = a
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = r.db.Query(ctx, `SELECT a.user_id,a.position_id FROM organization_access_assignments a JOIN users u ON u.id=a.user_id AND u.organization_id=a.organization_id WHERE a.organization_id=$1 AND u.deleted_at IS NULL`, s.ID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var uid int64
		var pid uuid.UUID
		if err = rows.Scan(&uid, &pid); err != nil {
			rows.Close()
			return nil, err
		}
		out.MemberPositions[strconv.FormatInt(uid, 10)] = pid.String()
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	err = r.db.QueryRow(ctx, `SELECT external_company_id FROM organization_app_links WHERE organization_id=$1 AND application='sd'`, s.ID).Scan(&out.SDCompanyID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	if err == nil && out.SDCompanyID != nil {
		var root bool
		if err = r.db.QueryRow(ctx, `SELECT is_tenant_root FROM organization_app_links WHERE organization_id=$1 AND application='sd'`, s.ID).Scan(&root); err == nil && !root {
			out.Catalog["sd"] = domain.SDClientPermissions(out.Catalog["sd"])
		}
	}
	out.Dependencies = domain.AccessDependencies(out.Catalog)
	out.Unavailable = domain.AccessUnavailablePermissions()
	return out, err
}

// All mutations share one organization lock, atomic audit and compare-and-swap revisions.
func (r *OrganizationRepository) ChangeOrganizationAccess(ctx context.Context, actor int64, orgID *uuid.UUID, action string, data AccessChange) error {
	s, err := r.accessScope(ctx, actor, orgID)
	if err != nil {
		return err
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var status string
	var owner *int64
	if err = tx.QueryRow(ctx, `SELECT status,owner_id FROM organizations WHERE id=$1 FOR UPDATE`, s.ID).Scan(&status, &owner); err != nil {
		return err
	}
	if s.Role != domain.RoleSuperAdmin && (owner == nil || *owner != actor) {
		return apperrors.Forbidden("Владелец компании изменился")
	}
	if status != "active" {
		return apperrors.Conflict("Компания приостановлена")
	}
	var active bool
	var role domain.Role
	if err = tx.QueryRow(ctx, `SELECT is_active,role FROM users WHERE id=$1 AND deleted_at IS NULL FOR SHARE`, actor).Scan(&active, &role); err != nil {
		return err
	}
	if !active || (orgID != nil && role != domain.RoleSuperAdmin) || (role != domain.RoleSuperAdmin && (owner == nil || *owner != actor)) {
		return apperrors.Forbidden("Доступ администратора отозван")
	}
	before, err := accessAuditSnapshot(ctx, tx, s.ID, action, data)
	if err != nil {
		return err
	}
	switch action {
	case "position":
		name := strings.TrimSpace(data.Name)
		if name == "" || len([]rune(name)) > 255 {
			return apperrors.Validation("Укажите должность длиной до 255 символов")
		}
		if data.ID == uuid.Nil {
			data.ID = uuid.New()
			_, err = tx.Exec(ctx, `INSERT INTO organization_access_positions(id,organization_id,name,position_key) VALUES($1,$2,$3,$4)`, data.ID, s.ID, name, domain.AccessPositionKey(name))
			if err == nil && data.UserID != 0 {
				// Creating a position with its first employee is one atomic operation.
				err = assignOrganizationAccessPosition(ctx, tx, s.ID, data.UserID, data.ID)
			}
		} else {
			var oldKey string
			err = tx.QueryRow(ctx, `SELECT position_key FROM organization_access_positions WHERE id=$1 AND organization_id=$2 AND revision=$3`, data.ID, s.ID, data.Revision).Scan(&oldKey)
			if errors.Is(err, pgx.ErrNoRows) {
				return apperrors.Conflict("Должность изменена. Обновите страницу")
			}
			if err != nil {
				return err
			}
			_, err = tx.Exec(ctx, `UPDATE organization_access_positions SET name=$3,position_key=$4,revision=revision+1 WHERE id=$1 AND organization_id=$2`, data.ID, s.ID, name, domain.AccessPositionKey(name))
			if err == nil {
				_, err = tx.Exec(ctx, `UPDATE users u SET position=$3 WHERE u.organization_id=$1 AND u.deleted_at IS NULL AND (LOWER(BTRIM(u.position))=$2 OR EXISTS(SELECT 1 FROM organization_access_assignments a WHERE a.user_id=u.id AND a.organization_id=$1 AND a.position_id=$4))`, s.ID, oldKey, name, data.ID)
				if err == nil {
					_, err = tx.Exec(ctx, `UPDATE organization_growy_position_roles SET position_key=$3,position_name=$4,updated_at=now() WHERE organization_id=$1 AND position_key=$2`, s.ID, oldKey, domain.AccessPositionKey(name), name)
				}

			}
		}
	case "delete":
		var count int
		err = tx.QueryRow(ctx, `SELECT count(*) FROM organization_access_assignments a JOIN users u ON u.id=a.user_id AND u.organization_id=a.organization_id WHERE a.position_id=$1 AND a.organization_id=$2 AND u.deleted_at IS NULL`, data.ID, s.ID).Scan(&count)
		if err != nil {
			return err
		}
		if count > 0 {
			return apperrors.Conflict("Сначала переведите сотрудников на другую должность")
		}
		result, e := tx.Exec(ctx, `DELETE FROM organization_access_positions WHERE id=$1 AND organization_id=$2 AND revision=$3`, data.ID, s.ID, data.Revision)
		err = e
		if err == nil && result.RowsAffected() != 1 {
			return apperrors.Conflict("Должность изменена. Обновите страницу")
		}
	case "policy":
		perms, e := domain.ValidateAccessPermissions(data.Application, data.Permissions)
		if e == nil && data.Application == "sd" {
			var root bool
			linkErr := tx.QueryRow(ctx, `SELECT is_tenant_root FROM organization_app_links WHERE organization_id=$1 AND application='sd'`, s.ID).Scan(&root)
			if linkErr != nil && !errors.Is(linkErr, pgx.ErrNoRows) {
				return linkErr
			}
			if linkErr == nil && !root {
				allowed := map[string]bool{}
				for _, p := range domain.SDClientPermissions(domain.AccessCatalog()["sd"]) {
					allowed[p.Code] = true
				}
				for _, code := range perms {
					if !allowed[code] {
						return apperrors.Validation("Это право недоступно клиентской компании СД")
					}
				}
			}
		}
		if e != nil {
			if errors.Is(e, domain.ErrAccessPermissionUnavailable) {
				return apperrors.New(apperrors.ErrCodeAccessPermissionUnavailable, e.Error(), 400)
			}
			if errors.Is(e, domain.ErrAccessDependencies) {
				return apperrors.New(apperrors.ErrCodeAccessDependencies, e.Error(), 400)
			}
			return apperrors.Validation(e.Error())
		}
		var exists bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM organization_access_positions WHERE id=$1 AND organization_id=$2)`, data.ID, s.ID).Scan(&exists)
		if err != nil {
			return err
		}
		if !exists {
			return apperrors.NotFound("Должность")
		}
		result, e := tx.Exec(ctx, `INSERT INTO organization_position_permissions(organization_id,position_id,application,permissions,revision) SELECT $1,$2,$3,$4,1 WHERE $5::bigint=0
   ON CONFLICT(organization_id,position_id,application) DO NOTHING`, s.ID, data.ID, data.Application, perms, data.Revision)
		if e != nil {
			return e
		}
		if data.Revision > 0 {
			result, err = tx.Exec(ctx, `UPDATE organization_position_permissions SET permissions=$4,revision=revision+1 WHERE organization_id=$1 AND position_id=$2 AND application=$3 AND revision=$5`, s.ID, data.ID, data.Application, perms, data.Revision)
		}
		if err == nil && result.RowsAffected() != 1 {
			return apperrors.Conflict("Права уже изменены другим администратором. Обновите страницу")
		}
	case "application":
		if _, ok := domain.AccessCatalog()[data.Application]; !ok {
			return apperrors.Validation("Неизвестное приложение")
		}
		if data.Enabled {
			if e := requireOrganizationModule(ctx, tx, s.ID, data.Application); e != nil {
				return e
			}
		}
		if data.Application == "sd" && data.Enabled {
			var linked bool
			if e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM organization_app_links WHERE organization_id=$1 AND application='sd')`, s.ID).Scan(&linked); e != nil {
				return e
			}
			if !linked {
				return apperrors.Conflict("Системный администратор должен связать компанию с СД")
			}
		}
		result, e := tx.Exec(ctx, `INSERT INTO organization_access_apps(organization_id,application,enabled,revision) SELECT $1,$2,$3,1 WHERE $4::bigint=0 ON CONFLICT DO NOTHING`, s.ID, data.Application, data.Enabled, data.Revision)
		if e != nil {
			return e
		}
		if data.Revision > 0 {
			result, err = tx.Exec(ctx, `UPDATE organization_access_apps SET enabled=$3,revision=revision+1 WHERE organization_id=$1 AND application=$2 AND revision=$4`, s.ID, data.Application, data.Enabled, data.Revision)
		}
		if err == nil && result.RowsAffected() != 1 {
			return apperrors.Conflict("Режим доступа изменился. Обновите страницу")
		}
	case "unassign":
		result, e := tx.Exec(ctx, `UPDATE users SET position=NULL WHERE id=$1 AND organization_id=$2 AND deleted_at IS NULL`, data.UserID, s.ID)
		err = e
		if err == nil && result.RowsAffected() != 1 {
			return apperrors.NotFound("Сотрудник компании")
		}
		if err == nil {
			_, err = tx.Exec(ctx, `DELETE FROM organization_access_assignments WHERE user_id=$1 AND organization_id=$2`, data.UserID, s.ID)
		}
	case "member":
		err = assignOrganizationAccessPosition(ctx, tx, s.ID, data.UserID, data.ID)

	case "link":
		var enabled bool
		if e := tx.QueryRow(ctx, `SELECT COALESCE((SELECT enabled FROM organization_access_apps WHERE organization_id=$1 AND application='sd'),false)`, s.ID).Scan(&enabled); e != nil {
			return e
		}
		if enabled {
			return apperrors.Conflict("Перед изменением связи отключите управление правами СД")
		}

		if orgID == nil || role != domain.RoleSuperAdmin {
			return apperrors.Forbidden("Связь с СД настраивает системный администратор")
		}
		if data.CompanyID == uuid.Nil {
			return apperrors.Validation("Укажите ID компании СД")
		}
		_, err = tx.Exec(ctx, `INSERT INTO organization_app_links(organization_id,application,external_company_id,is_tenant_root) VALUES($1,'sd',$2,$3) ON CONFLICT(organization_id,application) DO UPDATE SET external_company_id=EXCLUDED.external_company_id,is_tenant_root=EXCLUDED.is_tenant_root`, s.ID, data.CompanyID, data.CompanyTenantRoot)
		if err == nil && !data.CompanyTenantRoot {
			codes := []string{}
			for _, p := range domain.SDClientPermissions(domain.AccessCatalog()["sd"]) {
				codes = append(codes, p.Code)
			}
			_, err = tx.Exec(ctx, `UPDATE organization_position_permissions SET permissions=ARRAY(SELECT code FROM unnest(permissions) code WHERE code=ANY($2::text[])),revision=revision+1 WHERE organization_id=$1 AND application='sd'`, s.ID, codes)
		}

	default:
		return apperrors.Validation("Неизвестное действие")
	}
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			if action == "position" {
				return apperrors.Conflict("Должность с таким названием уже существует")
			}
			return apperrors.Conflict("Компания СД уже связана с другой компанией Workspace")
		}
		return err
	}
	details, _ := json.Marshal(data)
	_, err = tx.Exec(ctx, `INSERT INTO organization_access_audit(organization_id,actor_id,action,details) VALUES($1,$2,$3,$4)`, s.ID, actor, action, details)
	if err != nil {
		return err
	}
	after, err := accessAuditSnapshot(ctx, tx, s.ID, action, data)
	if err != nil {
		return err
	}
	objectID := data.ID.String()
	if action == "member" || action == "unassign" {
		objectID = strconv.FormatInt(data.UserID, 10)
	}
	if action == "application" || action == "link" {
		objectID = data.Application
	}
	if action == "link" {
		objectID = "sd"
	}
	if action == "policy" {
		objectID += ":" + data.Application
	}
	if err := writeAdministrationAudit(ctx, tx, s.ID, actor, "access."+action, "access", objectID, before, after); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type AccessChange struct {
	CompanyTenantRoot bool      `json:"-"`
	ID                uuid.UUID `json:"id"`
	Name              string    `json:"name"`
	Application       string    `json:"application"`
	Permissions       []string  `json:"permissions"`
	Revision          int64     `json:"revision"`
	Enabled           bool      `json:"enabled"`
	UserID            int64     `json:"user_id"`
	CompanyID         uuid.UUID `json:"company_id"`
}

func assignOrganizationAccessPosition(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, userID int64, positionID uuid.UUID) error {
	result, err := tx.Exec(ctx, `UPDATE users SET position=p.name FROM organization_access_positions p WHERE users.id=$1 AND users.organization_id=$2 AND users.deleted_at IS NULL AND p.id=$3 AND p.organization_id=$2`, userID, organizationID, positionID)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return apperrors.NotFound("Сотрудник или должность компании")
	}
	_, err = tx.Exec(ctx, `INSERT INTO organization_access_assignments(organization_id,user_id,position_id) VALUES($1,$2,$3) ON CONFLICT(user_id) DO UPDATE SET organization_id=EXCLUDED.organization_id,position_id=EXCLUDED.position_id`, organizationID, userID, positionID)
	return err
}

func (r *OrganizationRepository) ResolveOwnAccess(ctx context.Context, actor int64, app string) (domain.ResolvedAccess, error) {
	s, err := r.currentScope(ctx, actor)
	if err != nil {
		return domain.ResolvedAccess{}, err
	}
	return r.resolveAccess(ctx, s.ID, app, `u.id=$3`, actor)
}
func (r *OrganizationRepository) ResolveSDExternalAccess(ctx context.Context, companyID uuid.UUID, subject string) (domain.ResolvedAccess, error) {
	var orgID uuid.UUID
	err := r.db.QueryRow(ctx, `SELECT organization_id FROM organization_app_links WHERE application='sd' AND external_company_id=$1`, companyID).Scan(&orgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ResolvedAccess{Permissions: []string{}}, nil
	}
	if err != nil {
		return domain.ResolvedAccess{}, err
	}
	if strings.HasPrefix(subject, "workspace-user:") {
		id, err := strconv.ParseInt(strings.TrimPrefix(subject, "workspace-user:"), 10, 64)
		if err != nil || id <= 0 {
			return domain.ResolvedAccess{}, apperrors.Validation("Некорректный идентификатор Workspace")
		}
		return r.resolveAccess(ctx, orgID, "sd", `u.id=$3`, id)
	}
	return r.resolveAccess(ctx, orgID, "sd", `u.keycloak_id::text=$3`, subject)
}
func (r *OrganizationRepository) resolveAccess(ctx context.Context, orgID uuid.UUID, app, predicate string, identity any) (domain.ResolvedAccess, error) {
	out := domain.ResolvedAccess{Linked: true, OrganizationID: orgID.String(), Permissions: []string{}}
	if _, ok := domain.AccessCatalog()[app]; !ok {
		return out, apperrors.Validation("Неизвестное приложение")
	}
	var active bool
	err := r.db.QueryRow(ctx, `SELECT COALESCE(a.enabled,false),o.status='active',COALESCE(m.available,false),COALESCE(m.enabled,false) FROM organizations o LEFT JOIN organization_access_apps a ON a.organization_id=o.id AND a.application=$2 LEFT JOIN organization_modules m ON m.organization_id=o.id AND m.application=$2 WHERE o.id=$1`, orgID, app).Scan(&out.Managed, &active, &out.ModuleAvailable, &out.ModuleEnabled)
	if err != nil {
		return out, err
	}
	out.OrganizationActive = active
	if !active || !out.ModuleAvailable || !out.ModuleEnabled {
		// Prevent consumers falling back to local permissions after denial.
		out.Managed = true
		return out, nil
	}
	// Identity is matched inside the linked tenant, never globally and never by company name.
	var owner bool
	err = r.db.QueryRow(ctx, `SELECT COALESCE(pos.name,u.position,''),COALESCE(p.permissions,'{}'::text[]),COALESCE(u.team_id::text,''),COALESCE(u.avatar_url,''),ARRAY[u.role::text],
 (u.id=(SELECT owner_id FROM organizations WHERE id=$1)) IS TRUE FROM users u
 LEFT JOIN organization_access_assignments assignment ON assignment.user_id=u.id AND assignment.organization_id=u.organization_id
 LEFT JOIN organization_access_positions pos ON pos.organization_id=assignment.organization_id AND pos.id=assignment.position_id
 LEFT JOIN organization_position_permissions p ON p.organization_id=u.organization_id AND p.position_id=pos.id AND p.application=$2
 WHERE u.organization_id=$1 AND u.deleted_at IS NULL AND workspace_user_can_login(u.id) AND `+predicate, orgID, app, identity).Scan(&out.Position, &out.Permissions, &out.DepartmentID, &out.AvatarURL, &out.Roles, &owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	out.Active = err == nil
	if err == nil && owner && out.Managed {
		out.Permissions, err = r.ownerAccessPermissions(ctx, orgID, app)
	}
	if out.Managed {
		// Platform administration never becomes a service administrator role.
		out.Roles = []string{"user"}
	}
	return out, err
}

func (r *OrganizationRepository) ownerAccessPermissions(ctx context.Context, org uuid.UUID, app string) ([]string, error) {
	root := false
	if app == "sd" {
		if err := r.db.QueryRow(ctx, `SELECT COALESCE((SELECT is_tenant_root FROM organization_app_links WHERE organization_id=$1 AND application='sd'),false)`, org).Scan(&root); err != nil {
			return nil, err
		}
	}
	return domain.OwnerAccessPermissions(app, root), nil
}

func (r *OrganizationRepository) AuthorizeAccessPlatform(ctx context.Context, actor int64, org uuid.UUID) error {
	_, err := r.accessScope(ctx, actor, &org)
	return err
}

func (r *OrganizationRepository) ResolveSDIdentity(ctx context.Context, subject string) (domain.SDWorkspaceIdentity, error) {
	out := domain.SDWorkspaceIdentity{}
	rows, err := r.db.Query(ctx, `SELECT u.id,l.external_company_id,(workspace_user_can_login(u.id) AND u.deleted_at IS NULL AND o.status='active' AND EXISTS(SELECT 1 FROM organization_modules m WHERE m.organization_id=o.id AND m.application='sd' AND m.available AND m.enabled)) FROM users u JOIN organizations o ON o.id=u.organization_id JOIN organization_app_links l ON l.organization_id=o.id AND l.application='sd' WHERE u.keycloak_id::text=$1`, subject)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		count++
		if count > 1 {
			return out, apperrors.Conflict("Идентификатор входа связан с несколькими компаниями")
		}
		if err = rows.Scan(&out.UserID, &out.CompanyID, &out.Active); err != nil {
			return out, err
		}
		out.Linked = true
	}
	return out, rows.Err()
}

func (r *OrganizationRepository) SDHandoffProfile(ctx context.Context, actor int64) (domain.SDHandoffProfile, error) {
	var out domain.SDHandoffProfile
	var companyID, organizationID *uuid.UUID
	var userActive, organizationActive bool
	err := r.db.QueryRow(ctx, `SELECT u.id,COALESCE(u.keycloak_id::text,''),l.external_company_id,
 COALESCE(u.username,''),COALESCE(u.email,''),COALESCE(u.first_name,''),COALESCE(u.last_name,''),
 (workspace_user_can_login(u.id) AND u.deleted_at IS NULL),o.id,COALESCE(o.status='active',false)
 FROM users u LEFT JOIN organizations o ON o.id=u.organization_id
 LEFT JOIN organization_app_links l ON l.organization_id=o.id AND l.application='sd'
 WHERE u.id=$1`, actor).Scan(
		&out.UserID, &out.Subject, &companyID, &out.Username, &out.Email, &out.FirstName, &out.LastName,
		&userActive, &organizationID, &organizationActive)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, apperrors.Forbidden("Учётная запись Workspace не найдена").WithUserMessage("Учётная запись Workspace не найдена. Выполните вход повторно.")
	}
	if err != nil {
		return out, err
	}
	if !userActive {
		return out, apperrors.Forbidden("Учётная запись Workspace отключена").WithUserMessage("Учётная запись Workspace отключена. Обратитесь к администратору.")
	}
	if organizationID == nil {
		return out, apperrors.Conflict("Пользователь Workspace не назначен в компанию. Администратор должен назначить компанию в карточке сотрудника")
	}
	if !organizationActive {
		return out, apperrors.Forbidden("Компания Workspace отключена").WithUserMessage("Компания Workspace отключена. Обратитесь к системному администратору.")
	}
	if err := requireOrganizationModule(ctx, r.db, *organizationID, "sd"); err != nil {
		return out, err
	}
	if companyID == nil || *companyID == uuid.Nil {
		message := "Пространство компании SenimDesk ещё не подготовлено. Повторите открытие из Workspace"
		return out, apperrors.New("SD_COMPANY_NOT_LINKED", message, 409).WithUserMessage(message).WithDetail("organization_id", organizationID.String())
	}
	out.CompanyID = *companyID
	return out, nil
}

// ListMembersForApplication never accepts a tenant ID from the caller. Policies
// and identities are selected together inside the actor's current company.
func (r *OrganizationRepository) ListMembersForApplication(ctx context.Context, actor int64, app string) ([]domain.OrganizationMember, error) {
	app = strings.TrimSpace(app)
	if app == "" {
		return r.ListMembers(ctx, actor)
	}
	if _, ok := domain.AccessCatalog()[app]; !ok {
		return nil, apperrors.Validation("Неизвестное приложение")
	}
	scope, err := r.currentScope(ctx, actor)
	if err != nil {
		return nil, err
	}
	members, err := r.listMembersForOrganization(ctx, scope.ID)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.Query(ctx, `SELECT u.id, COALESCE(a.enabled,false), o.status='active',COALESCE(module.available,false),COALESCE(module.enabled,false),
 COALESCE(pos.name,u.position,''), COALESCE(p.permissions,'{}'::text[])
 FROM users u JOIN organizations o ON o.id=u.organization_id
 LEFT JOIN organization_access_apps a ON a.organization_id=o.id AND a.application=$2
 LEFT JOIN organization_modules module ON module.organization_id=o.id AND module.application=$2
 LEFT JOIN organization_access_assignments assignment ON assignment.user_id=u.id AND assignment.organization_id=o.id
 LEFT JOIN organization_access_positions pos ON pos.organization_id=assignment.organization_id AND pos.id=assignment.position_id
 LEFT JOIN organization_position_permissions p ON p.organization_id=o.id AND p.position_id=pos.id AND p.application=$2
 WHERE u.organization_id=$1 AND u.deleted_at IS NULL`, scope.ID, app)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	policies := map[int64]domain.ResolvedAccess{}
	for rows.Next() {
		var id int64
		policy := domain.ResolvedAccess{Linked: true, OrganizationID: scope.ID.String(), Permissions: []string{}}
		if err := rows.Scan(&id, &policy.Managed, &policy.OrganizationActive, &policy.ModuleAvailable, &policy.ModuleEnabled, &policy.Position, &policy.Permissions); err != nil {
			return nil, err
		}
		policies[id] = policy
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var ownerPermissions []string
	if scope.OwnerID != nil {
		ownerPermissions, err = r.ownerAccessPermissions(ctx, scope.ID, app)
		if err != nil {
			return nil, err
		}
	}
	for i := range members {
		policy, ok := policies[members[i].ID]
		if !ok {
			return nil, errors.New("organization member policy is missing")
		}
		policy.Active = members[i].IsActive && policy.OrganizationActive && policy.ModuleAvailable && policy.ModuleEnabled
		if policy.Active && policy.Managed && scope.OwnerID != nil && members[i].ID == *scope.OwnerID {
			policy.Permissions = append([]string(nil), ownerPermissions...)
		}
		if !policy.ModuleAvailable || !policy.ModuleEnabled || !policy.OrganizationActive {
			policy.Managed = true
			policy.Permissions = []string{}
		}
		members[i].Access = &policy
		position := policy.Position
		members[i].Position = &position
	}
	return members, nil
}
