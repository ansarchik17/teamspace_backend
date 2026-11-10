package repository

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func (r *OrganizationRepository) ContactDirectoryConfig(ctx context.Context, actor int64) (*domain.DirectoryConfig, []byte, error) {
	org, err := r.ContactImportScope(ctx, actor)
	if err != nil {
		return nil, nil, err
	}
	var settings []byte
	var password []byte
	config := &domain.DirectoryConfig{}
	err = r.db.QueryRow(ctx, `SELECT settings,encrypted_password,revision,last_sync_at,last_sync_count FROM employee_directory_configs WHERE organization_id=$1`, org).Scan(&settings, &password, &config.Revision, &config.LastSyncAt, &config.LastSyncCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, apperrors.NotFound("directory connector")
	}
	if err != nil {
		return nil, nil, err
	}
	if err = json.Unmarshal(settings, &config.DirectorySettings); err != nil {
		return nil, nil, err
	}
	config.HasBindPassword = len(password) > 0
	return config, password, nil
}

func (r *OrganizationRepository) SaveContactDirectoryConfig(ctx context.Context, actor int64, settings domain.DirectorySettings, password []byte) (*domain.DirectoryConfig, error) {
	org, err := r.ContactImportScope(ctx, actor)
	if err != nil {
		return nil, err
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,1005))`, org.String()); err != nil {
		return nil, err
	}
	scoped := &OrganizationRepository{db: txOrganizationDB{tx}, logger: r.logger}
	current, err := scoped.ContactImportScope(ctx, actor)
	if err != nil {
		return nil, err
	}
	if current != org {
		return nil, apperrors.Conflict("organization changed")
	}
	old, oldPassword, err := scoped.ContactDirectoryConfig(ctx, actor)
	if err != nil && apperrors.ToAppError(err).StatusCode != 404 {
		return nil, err
	}
	if old != nil {
		var linked bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM employee_directory_links WHERE organization_id=$1 AND source='ldap')`, org).Scan(&linked)
		if err != nil {
			return nil, err
		}
		if linked && (old.URL != settings.URL || old.BaseDN != settings.BaseDN || old.Type != settings.Type || old.Attributes["external_id"] != settings.Attributes["external_id"]) {
			return nil, apperrors.Conflict("cannot change directory identity after sync; use the existing directory")
		}
		if password == nil {
			password = oldPassword
		}
	}
	if len(password) == 0 {
		return nil, apperrors.Validation("bind_password is required for a new connector")
	}
	encoded, err := json.Marshal(settings)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO employee_directory_configs(organization_id,settings,encrypted_password) VALUES($1,$2,$3) ON CONFLICT(organization_id) DO UPDATE SET settings=$2,encrypted_password=$3,revision=employee_directory_configs.revision+1,updated_at=NOW()`, org, string(encoded), password)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	config, _, err := r.ContactDirectoryConfig(ctx, actor)
	return config, err
}
