package repository

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func (r *OrganizationRepository) ContactImportScope(ctx context.Context, actor int64) (uuid.UUID, error) {
	scope, err := r.currentScope(ctx, actor)
	if err != nil {
		return uuid.Nil, err
	}
	if err = authorizeAdministration(ctx, r.db, actor, scope.OwnerID, false); err != nil {
		return uuid.Nil, err
	}
	var active bool
	if err = r.db.QueryRow(ctx, `SELECT is_active FROM users WHERE id=$1`, actor).Scan(&active); err != nil {
		return uuid.Nil, err
	}
	if !active {
		return uuid.Nil, apperrors.Forbidden("inactive administrator")
	}
	return scope.ID, nil
}

// ApplyContactImport is all-or-nothing, with the same transaction path for preview.
// It deliberately preserves login identity, roles, passwords, and avatars of existing users.
func (r *OrganizationRepository) ApplyContactImport(ctx context.Context, actor int64, source string, revision int64, rows []domain.ContactImportRow, dryRun bool) (*domain.ContactImportResult, error) {
	org, err := r.ContactImportScope(ctx, actor)
	if err != nil {
		return nil, err
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	for _, salt := range []int{1005, 1002, 1003} {
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,$2))`, org.String(), salt); err != nil {
			return nil, err
		}
	}
	scoped := &OrganizationRepository{db: txOrganizationDB{tx}, logger: r.logger}
	current, err := scoped.ContactImportScope(ctx, actor)
	if err != nil {
		return nil, err
	}
	if current != org {
		return nil, apperrors.Conflict("organization changed during import")
	}
	if source == "ldap" {
		var currentRevision int64
		err = tx.QueryRow(ctx, `SELECT revision FROM employee_directory_configs WHERE organization_id=$1`, org).Scan(&currentRevision)
		if err != nil {
			return nil, err
		}
		if currentRevision != revision {
			return nil, apperrors.Conflict("directory configuration changed; retry sync")
		}
	}
	emailKeys := []string{}
	for _, row := range rows {
		emailKeys = append(emailKeys, strings.ToLower(row.Email))
	}
	sort.Strings(emailKeys)
	for _, email := range emailKeys {
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,1006))`, email); err != nil {
			return nil, err
		}
	}
	result := &domain.ContactImportResult{DryRun: dryRun, Total: len(rows), Issues: []domain.ContactImportIssue{}}
	fail := func(row int, field, message string) (*domain.ContactImportResult, error) {
		result.Issues = append(result.Issues, domain.ContactImportIssue{Row: row, Field: field, Message: message})
		return result, nil
	}
	databaseFailure := func(row int, field string, e error) (*domain.ContactImportResult, error) {
		var pg *pgconn.PgError
		if errors.As(e, &pg) && (strings.HasPrefix(pg.Code, "23") || pg.Code == "40P01") {
			return fail(row, field, "conflicting identity, assignment or hierarchy; no rows applied")
		}
		return nil, e
	}
	ids := make([]int64, len(rows))
	seenUsers := map[int64]bool{}
	for i, row := range rows {
		var id int64
		err = tx.QueryRow(ctx, `SELECT u.id FROM employee_directory_links l JOIN users u ON u.id=l.user_id WHERE l.organization_id=$1 AND l.source=$2 AND l.external_id=$3 AND u.organization_id=$1 AND u.deleted_at IS NULL`, org, source, row.ExternalID).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			var stale bool
			err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM employee_directory_links WHERE organization_id=$1 AND source=$2 AND external_id=$3)`, org, source, row.ExternalID).Scan(&stale)
			if err != nil {
				return nil, err
			}
			if stale {
				return fail(row.Row, "external_id", "linked employee was deleted or transferred; resolve mapping first")
			}
			candidates, e := tx.Query(ctx, `SELECT id FROM users WHERE organization_id=$1 AND deleted_at IS NULL AND (lower(username)=lower($2) OR lower(email)=lower($3)) ORDER BY id`, org, row.Username, row.Email)
			if e != nil {
				return nil, e
			}
			found := []int64{}
			for candidates.Next() {
				var candidate int64
				if e = candidates.Scan(&candidate); e != nil {
					candidates.Close()
					return nil, e
				}
				found = append(found, candidate)
			}
			e = candidates.Err()
			candidates.Close()
			if e != nil {
				return nil, e
			}
			if len(found) > 1 {
				return fail(row.Row, "identity", "ambiguous existing employee")
			}
			if len(found) == 1 {
				id = found[0]
			}
		} else if err != nil {
			return nil, err
		}
		if id == 0 {
			var collision bool
			err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE lower(email)=lower($1))`, row.Email).Scan(&collision)
			if err != nil {
				return nil, err
			}
			if collision {
				return fail(row.Row, "email", "identity is already reserved")
			}
			active := true
			if row.IsActive != nil {
				active = *row.IsActive
			}
			err = tx.QueryRow(ctx, `INSERT INTO users(email,username,first_name,last_name,full_name_local,organization_id,role,is_active,is_first_login,created_at,updated_at)
   VALUES($1,$2,$3,$4,$3||' '||$4,$5,'user',$6,TRUE,NOW(),NOW()) RETURNING id`, row.Email, row.Username, row.FirstName, row.LastName, org, active).Scan(&id)
			if err != nil {
				return databaseFailure(row.Row, "identity", err)
			}
			result.Created++
		} else {
			var role string
			err = tx.QueryRow(ctx, `SELECT role FROM users WHERE id=$1 AND organization_id=$2 AND deleted_at IS NULL FOR UPDATE`, id, org).Scan(&role)
			if errors.Is(err, pgx.ErrNoRows) {
				return fail(row.Row, "identity", "employee was deleted or transferred during import")
			}
			if err != nil {
				return nil, err
			}
			if row.IsActive != nil && !*row.IsActive && (role == string(domain.RoleSuperAdmin) || (scopeOwner(ctx, tx, org, id))) {
				return fail(row.Row, "is_active", "cannot deactivate organization owner or administrator")
			}
			result.Updated++
		}
		if seenUsers[id] {
			return fail(row.Row, "identity", "multiple rows match the same employee")
		}
		seenUsers[id] = true
		ids[i] = id
		_, err = tx.Exec(ctx, `INSERT INTO employee_directory_links(organization_id,source,external_id,user_id) VALUES($1,$2,$3,$4) ON CONFLICT(organization_id,source,external_id) DO UPDATE SET updated_at=NOW()`, org, source, row.ExternalID, id)
		if err != nil {
			return databaseFailure(row.Row, "external_id", err)
		}
		// Detach explicitly managed relationships first so a valid reorganization is independent of row order.
		_, err = tx.Exec(ctx, `UPDATE users SET first_name=$2,last_name=$3,full_name_local=$2||' '||$3,
   position=CASE WHEN $4::text IS NULL THEN position ELSE NULLIF($4::text,'') END,
   is_active=COALESCE($5,is_active),manager_id=CASE WHEN $6 THEN NULL ELSE manager_id END,updated_at=NOW() WHERE id=$1`, id, row.FirstName, row.LastName, row.Position, row.IsActive, row.ManagerUsername != nil)
		if err != nil {
			return databaseFailure(row.Row, "profile", err)
		}
		work := row.WorkEmail
		if work == nil {
			email := row.Email
			work = &email
		}
		_, err = tx.Exec(ctx, `INSERT INTO employee_contacts(user_id,work_email,phone) VALUES($1,NULLIF($2::text,''),NULLIF($3::text,'')) ON CONFLICT(user_id) DO UPDATE SET work_email=NULLIF($2::text,''),phone=CASE WHEN $3::text IS NULL THEN employee_contacts.phone ELSE NULLIF($3::text,'') END,updated_at=NOW()`, id, work, row.Phone)
		if err != nil {
			return databaseFailure(row.Row, "contacts", err)
		}
	}
	// Resolve units and managers after every employee exists.
	for i, row := range rows {
		if row.Department != nil {
			var unit *uuid.UUID
			if *row.Department != "" {
				matches, e := tx.Query(ctx, `SELECT id FROM teams WHERE organization_id=$1 AND deleted_at IS NULL AND lower(name)=lower($2)`, org, *row.Department)
				if e != nil {
					return nil, e
				}
				found := []uuid.UUID{}
				for matches.Next() {
					var candidate uuid.UUID
					if e = matches.Scan(&candidate); e != nil {
						matches.Close()
						return nil, e
					}
					found = append(found, candidate)
				}
				e = matches.Err()
				matches.Close()
				if e != nil {
					return nil, e
				}
				if len(found) != 1 {
					return fail(row.Row, "department", "department must match exactly one existing active unit")
				}
				unit = &found[0]
			}
			_, err = tx.Exec(ctx, `UPDATE users SET team_id=$2 WHERE id=$1`, ids[i], unit)
			if err != nil {
				return databaseFailure(row.Row, "department", err)
			}
		}
		if row.ManagerUsername != nil && *row.ManagerUsername != "" {
			// Prefer the input's external username, which may have changed since the login identity was created.
			var manager int64
			for j, candidate := range rows {
				if strings.EqualFold(candidate.Username, *row.ManagerUsername) {
					manager = ids[j]
					break
				}
			}
			if manager == 0 {
				var count int
				err = tx.QueryRow(ctx, `SELECT count(*),COALESCE(min(id),0) FROM users WHERE organization_id=$1 AND deleted_at IS NULL AND is_active=TRUE AND lower(username)=lower($2)`, org, *row.ManagerUsername).Scan(&count, &manager)
				if err != nil {
					return nil, err
				}
				if count != 1 {
					return fail(row.Row, "manager_username", "manager must match exactly one active employee")
				}
			}
			_, err = tx.Exec(ctx, `UPDATE users SET manager_id=$2 WHERE id=$1`, ids[i], manager)
			if err != nil {
				return databaseFailure(row.Row, "manager_username", err)
			}
		}
	}
	if dryRun {
		return result, nil
	}
	if source == "ldap" {
		_, err = tx.Exec(ctx, `UPDATE employee_directory_configs SET last_sync_at=NOW(),last_sync_count=$2,revision=revision+1 WHERE organization_id=$1`, org, len(rows))
		if err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit contact import: %w", err)
	}
	result.Applied = true
	return result, nil
}

func scopeOwner(ctx context.Context, tx pgx.Tx, org uuid.UUID, id int64) bool {
	var owner bool
	err := tx.QueryRow(ctx, `SELECT owner_id=$2 FROM organizations WHERE id=$1`, org, id).Scan(&owner)
	return err == nil && owner
}
