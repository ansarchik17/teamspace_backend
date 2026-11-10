package repository

import (
	"context"
	"errors"
	"fmt"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/fileurl"
	"go.uber.org/zap"
)

type UserRepository struct {
	db     *pgxpool.Pool
	logger *zap.Logger
}

var ErrKeycloakUserNotLinked = errors.New("KEYCLOAK_USER_NOT_LINKED")

func NewUserRepository(db *pgxpool.Pool, logger *zap.Logger) *UserRepository {
	return &UserRepository{
		db:     db,
		logger: logger.With(zap.String("repository", "user")),
	}
}

func normalizeKeycloakEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func normalizeKeycloakUsername(username string) string {
	username = strings.ToLower(strings.TrimSpace(username))
	if username == "" {
		return ""
	}
	return strings.Join(strings.Fields(username), "")
}

func normalizeKeycloakIIN(iin string) string {
	if strings.TrimSpace(iin) == "" {
		return ""
	}
	var digits []rune
	for _, r := range iin {
		if unicode.IsDigit(r) {
			digits = append(digits, r)
		}
	}
	if len(digits) != 12 {
		return ""
	}
	return string(digits)
}

func keycloakAutoCreateUsersEnabled() bool {
	for _, key := range []string{"KEYCLOAK_AUTO_CREATE_USERS"} {
		value := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
		switch value {
		case "":
			continue
		case "0", "false", "no":
			return false
		case "1", "true", "yes":
			return true
		}
	}
	return true
}

func shouldReplaceIdentityField(current string, incoming string) bool {
	current = strings.TrimSpace(current)
	incoming = strings.TrimSpace(incoming)
	if incoming == "" {
		return false
	}
	if current == "" {
		return true
	}
	return current != incoming
}

func shouldReplaceIdentityPointer(current *string, incoming *string, normalizer func(string) string) bool {
	if incoming == nil {
		return false
	}
	incomingValue := normalizer(*incoming)
	if incomingValue == "" {
		return false
	}
	if current == nil {
		return true
	}
	currentValue := normalizer(*current)
	if currentValue == "" {
		return true
	}
	return currentValue != incomingValue
}

func containsInt64(values []int64, target int64) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// Create creates a new user (NO password hash stored)
func (r *UserRepository) Create(ctx context.Context, user *domain.User) error {
	return r.createUser(ctx, r.db, user)
}

type userRowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func (r *UserRepository) createUser(ctx context.Context, db userRowQuerier, user *domain.User) error {
	// При создании нового пользователя всегда ставим флаг первого входа в true
	user.IsFirstLogin = true

	query := `
       INSERT INTO users (
          keycloak_id, iin,
          email, username,
          first_name, last_name,
          avatar_url, bio, position,
          is_active, is_first_login, created_at, updated_at,
          role, availability_status, full_name_local,
          can_view_full_drive_activity,
          organization_id, team_id, manager_id
        )
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)
        RETURNING id, language
    `

	err := db.QueryRow(ctx, query,
		user.KeycloakID,
		user.IIN,
		user.Email,
		user.Username,
		user.FirstName,
		user.LastName,
		user.AvatarURL,
		user.Bio,
		user.Position,
		user.IsActive,
		user.IsFirstLogin, // $11
		user.CreatedAt,
		user.UpdatedAt,
		user.Role,
		user.AvailabilityStatus,
		user.FullNameLocal,
		user.CanViewFullDriveActivity,
		user.OrganizationID,
		user.TeamID,
		user.ManagerID,
	).Scan(&user.ID, &user.Language)

	if err != nil {
		r.logger.Error(config.LogEvent(config.LogOpCreate, "User create failed"),
			config.LogOp(config.LogOpCreate),
			zap.Error(err),
		)
		return fmt.Errorf("create user: %w", err)
	}

	r.logger.Info(config.LogEvent(config.LogOpCreate, "User created"),
		config.LogOp(config.LogOpCreate),
		zap.Int64("user_id", user.ID),
		zap.String("email", user.Email),
	)
	return nil
}

func (r *UserRepository) GetByID(ctx context.Context, id int64) (*domain.User, error) {
	query := `
       SELECT
          id, keycloak_id, iin,
          email, username, COALESCE(password_hash, '') AS password_hash,
          first_name, last_name,
          avatar_url, bio, position,
          is_active, is_first_login, last_login_at, last_seen_at,
          created_at, updated_at, deleted_at,
          role, availability_status, full_name_local,
          can_view_full_drive_activity, language,
          organization_id, team_id, manager_id,
          ministry_id, department_id
       FROM users
       WHERE id = $1 AND deleted_at IS NULL
    `

	u := &domain.User{}
	err := r.db.QueryRow(ctx, query, id).Scan(
		&u.ID, &u.KeycloakID, &u.IIN,
		&u.Email, &u.Username, &u.PasswordHash,
		&u.FirstName, &u.LastName,
		&u.AvatarURL, &u.Bio, &u.Position,
		&u.IsActive, &u.IsFirstLogin, &u.LastLoginAt, &u.LastSeenAt,
		&u.CreatedAt, &u.UpdatedAt, &u.DeletedAt,
		&u.Role, &u.AvailabilityStatus, &u.FullNameLocal,
		&u.CanViewFullDriveActivity, &u.Language,
		&u.OrganizationID, &u.TeamID, &u.ManagerID,
		&u.MinistryID, &u.DepartmentID,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("user not found")
		}
		r.logger.Error(config.LogEvent(config.LogOpRead, "User get by ID failed"),
			config.LogOp(config.LogOpRead),
			zap.Error(err),
			zap.Int64("user_id", id),
		)
		return nil, fmt.Errorf("get user by id: %w", err)
	}

	return u, nil
}

func (r *UserRepository) GetByIDs(ctx context.Context, ids []int64) ([]*domain.User, error) {
	if len(ids) == 0 {
		return []*domain.User{}, nil
	}

	query := `
       SELECT
          id, keycloak_id, iin,
          email, username, COALESCE(password_hash, '') AS password_hash,
          first_name, last_name,
          avatar_url, bio, position,
          is_active, is_first_login, last_login_at, last_seen_at,
          created_at, updated_at, deleted_at,
          role, availability_status, full_name_local,
          can_view_full_drive_activity, language,
          organization_id, team_id, manager_id,
          ministry_id, department_id
       FROM users
       WHERE id = ANY($1) AND deleted_at IS NULL
    `

	rows, err := r.db.Query(ctx, query, ids)
	if err != nil {
		r.logger.Error("Failed to get users by IDs", zap.Error(err))
		return nil, fmt.Errorf("get users by ids: %w", err)
	}
	defer rows.Close()

	var users []*domain.User
	for rows.Next() {
		u := &domain.User{}
		if err := rows.Scan(
			&u.ID, &u.KeycloakID, &u.IIN,
			&u.Email, &u.Username, &u.PasswordHash,
			&u.FirstName, &u.LastName,
			&u.AvatarURL, &u.Bio, &u.Position,
			&u.IsActive, &u.IsFirstLogin, &u.LastLoginAt, &u.LastSeenAt,
			&u.CreatedAt, &u.UpdatedAt, &u.DeletedAt,
			&u.Role, &u.AvailabilityStatus, &u.FullNameLocal,
			&u.CanViewFullDriveActivity, &u.Language,
			&u.OrganizationID, &u.TeamID, &u.ManagerID,
			&u.MinistryID, &u.DepartmentID,
		); err != nil {
			r.logger.Error("Failed to scan user", zap.Error(err))
			continue
		}
		users = append(users, u)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}

	return users, nil
}

func (r *UserRepository) GetFullDetailsByID(ctx context.Context, id int64) (*domain.UserDetailResponse, error) {
	query := `
       SELECT
          u.id, u.keycloak_id, u.iin,
          u.email, u.username,
          u.first_name, u.last_name,
          u.avatar_url, u.bio, u.position,
          u.is_active, u.is_first_login, u.last_login_at, u.last_seen_at,
          u.created_at,
          u.role, 
          u.availability_status, u.full_name_local,
          u.can_view_full_drive_activity, u.language,
          u.organization_id, u.team_id, u.manager_id,
          COALESCE(u.position, '') AS position_name
       FROM users u
       WHERE u.id = $1 AND u.deleted_at IS NULL
    `

	var u domain.UserDetailResponse

	err := r.db.QueryRow(ctx, query, id).Scan(
		&u.ID, &u.KeycloakID, &u.IIN,
		&u.Email, &u.Username,
		&u.FirstName, &u.LastName,
		&u.AvatarURL, &u.Bio, &u.Position,
		&u.IsActive, &u.IsFirstLogin, &u.LastLoginAt, &u.LastSeenAt,
		&u.CreatedAt,
		&u.Role,
		&u.AvailabilityStatus, &u.FullNameLocal,
		&u.CanViewFullDriveActivity, &u.Language,
		&u.OrganizationID, &u.TeamID, &u.ManagerID,
		&u.PositionName,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("user not found")
		}
		r.logger.Error("Failed to get user full details", zap.Error(err), zap.Int64("user_id", id))
		return nil, fmt.Errorf("get user details: %w", err)
	}

	u.FullName = u.FirstName + " " + u.LastName
	u.AvatarURL = fileurl.SignLegacyDownloadURL(u.AvatarURL)

	return &u, nil
}

func (r *UserRepository) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	email = strings.ToLower(strings.TrimSpace(email))

	query := `
       SELECT
          id, keycloak_id, iin,
          email, username, COALESCE(password_hash, '') AS password_hash,
          first_name, last_name,
          avatar_url, bio, position,
          is_active, is_first_login, last_login_at, last_seen_at,
          created_at, updated_at, deleted_at,
          role, availability_status, full_name_local,
          can_view_full_drive_activity, language,
          organization_id, team_id, manager_id,
          ministry_id, department_id
       FROM users
       WHERE email = $1 AND deleted_at IS NULL
    `

	u := &domain.User{}
	err := r.db.QueryRow(ctx, query, email).Scan(
		&u.ID, &u.KeycloakID, &u.IIN,
		&u.Email, &u.Username, &u.PasswordHash,
		&u.FirstName, &u.LastName,
		&u.AvatarURL, &u.Bio, &u.Position,
		&u.IsActive, &u.IsFirstLogin, &u.LastLoginAt, &u.LastSeenAt,
		&u.CreatedAt, &u.UpdatedAt, &u.DeletedAt,
		&u.Role, &u.AvailabilityStatus, &u.FullNameLocal,
		&u.CanViewFullDriveActivity, &u.Language,
		&u.OrganizationID, &u.TeamID, &u.ManagerID,
		&u.MinistryID, &u.DepartmentID,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("user not found")
		}
		r.logger.Error("Failed to get user by email", zap.Error(err), zap.String("email", email))
		return nil, fmt.Errorf("get user by email: %w", err)
	}

	return u, nil
}

func (r *UserRepository) GetByUsername(ctx context.Context, username string) (*domain.User, error) {
	query := `
       SELECT
          id, keycloak_id, iin,
          email, username, COALESCE(password_hash, '') AS password_hash,
          first_name, last_name,
          avatar_url, bio, position,
          is_active, is_first_login, last_login_at, last_seen_at,
          created_at, updated_at, deleted_at,
          role, availability_status, full_name_local,
          can_view_full_drive_activity, language,
          organization_id, team_id, manager_id
       FROM users
       WHERE username = $1 AND deleted_at IS NULL
    `

	u := &domain.User{}
	err := r.db.QueryRow(ctx, query, username).Scan(
		&u.ID, &u.KeycloakID, &u.IIN,
		&u.Email, &u.Username, &u.PasswordHash,
		&u.FirstName, &u.LastName,
		&u.AvatarURL, &u.Bio, &u.Position,
		&u.IsActive, &u.IsFirstLogin, &u.LastLoginAt, &u.LastSeenAt,
		&u.CreatedAt, &u.UpdatedAt, &u.DeletedAt,
		&u.Role, &u.AvailabilityStatus, &u.FullNameLocal,
		&u.CanViewFullDriveActivity, &u.Language,
		&u.OrganizationID, &u.TeamID, &u.ManagerID,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("user not found")
		}
		r.logger.Error("Failed to get user by username", zap.Error(err), zap.String("username", username))
		return nil, fmt.Errorf("get user by username: %w", err)
	}

	return u, nil
}

// ValidateOrganizationTeam ensures a selected subdivision belongs to the
// organization that will own the new user. The create flow uses this before
// provisioning the Keycloak account so invalid cross-organization links are
// rejected without creating a partially configured employee.
func (r *UserRepository) ValidateOrganizationTeam(ctx context.Context, organizationID, teamID uuid.UUID) error {
	var exists bool
	if err := r.db.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM teams
			WHERE id = $1
			  AND organization_id = $2
			  AND deleted_at IS NULL
		)
	`, teamID, organizationID).Scan(&exists); err != nil {
		return fmt.Errorf("validate organization team: %w", err)
	}
	if !exists {
		return fmt.Errorf("team does not belong to organization")
	}
	return nil
}

func (r *UserRepository) HasRegisteredMobileDevice(ctx context.Context, userID int64) (bool, error) {
	const query = `
		SELECT EXISTS (
			SELECT 1
			FROM user_devices
			WHERE user_id = $1
			  AND active = TRUE
			  AND logged_out_at IS NULL
			  AND NULLIF(TRIM(fcm_token), '') IS NOT NULL
		)
	`

	var exists bool
	if err := r.db.QueryRow(ctx, query, userID).Scan(&exists); err != nil {
		r.logger.Error("Failed to check registered mobile devices", zap.Error(err), zap.Int64("user_id", userID))
		return false, fmt.Errorf("check registered mobile devices: %w", err)
	}

	return exists, nil
}

func (r *UserRepository) GetByKeycloakID(ctx context.Context, kcID string) (*domain.User, error) {
	query := `
       SELECT
          id, keycloak_id, iin,
          email, username, COALESCE(password_hash, '') AS password_hash,
          first_name, last_name,
          avatar_url, bio, position,
          is_active, is_first_login, last_login_at, last_seen_at,
          created_at, updated_at, deleted_at,
          role, availability_status, full_name_local,
          can_view_full_drive_activity, language,
          organization_id, team_id, manager_id
       FROM users
       WHERE keycloak_id = $1 AND deleted_at IS NULL
    `

	u := &domain.User{}
	err := r.db.QueryRow(ctx, query, kcID).Scan(
		&u.ID, &u.KeycloakID, &u.IIN,
		&u.Email, &u.Username, &u.PasswordHash,
		&u.FirstName, &u.LastName,
		&u.AvatarURL, &u.Bio, &u.Position,
		&u.IsActive, &u.IsFirstLogin, &u.LastLoginAt, &u.LastSeenAt,
		&u.CreatedAt, &u.UpdatedAt, &u.DeletedAt,
		&u.Role, &u.AvailabilityStatus, &u.FullNameLocal,
		&u.CanViewFullDriveActivity, &u.Language,
		&u.OrganizationID, &u.TeamID, &u.ManagerID,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("user not found")
		}
		return nil, fmt.Errorf("get user by keycloak_id: %w", err)
	}

	return u, nil
}

func (r *UserRepository) GetByIIN(ctx context.Context, iin string) (*domain.User, error) {
	query := `
       SELECT
          id, keycloak_id, iin,
          email, username, COALESCE(password_hash, '') AS password_hash,
          first_name, last_name,
          avatar_url, bio, position,
          is_active, is_first_login, last_login_at, last_seen_at,
          created_at, updated_at, deleted_at,
          role, availability_status, full_name_local,
          can_view_full_drive_activity, language,
          organization_id, team_id, manager_id
       FROM users
       WHERE iin = $1 AND deleted_at IS NULL
    `

	u := &domain.User{}
	err := r.db.QueryRow(ctx, query, iin).Scan(
		&u.ID, &u.KeycloakID, &u.IIN,
		&u.Email, &u.Username, &u.PasswordHash,
		&u.FirstName, &u.LastName,
		&u.AvatarURL, &u.Bio, &u.Position,
		&u.IsActive, &u.IsFirstLogin, &u.LastLoginAt, &u.LastSeenAt,
		&u.CreatedAt, &u.UpdatedAt, &u.DeletedAt,
		&u.Role, &u.AvailabilityStatus, &u.FullNameLocal,
		&u.CanViewFullDriveActivity, &u.Language,
		&u.OrganizationID, &u.TeamID, &u.ManagerID,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("user not found")
		}
		return nil, fmt.Errorf("get user by iin: %w", err)
	}

	return u, nil
}

func (r *UserRepository) getByNormalizedEmail(ctx context.Context, email string, excludeIDs ...int64) (*domain.User, error) {
	email = normalizeKeycloakEmail(email)
	if email == "" {
		return nil, fmt.Errorf("user not found")
	}

	query := `
       SELECT
          id, keycloak_id, iin,
          email, username, COALESCE(password_hash, '') AS password_hash,
          first_name, last_name,
          avatar_url, bio, position,
          is_active, is_first_login, last_login_at, last_seen_at,
          created_at, updated_at, deleted_at,
          role, availability_status, full_name_local,
          can_view_full_drive_activity, language,
          organization_id, team_id, manager_id
       FROM users
       WHERE LOWER(TRIM(email)) = $1
         AND deleted_at IS NULL
    `
	args := []any{email}
	if len(excludeIDs) > 0 {
		query += ` AND id <> ALL($2)`
		args = append(args, excludeIDs)
	}
	query += ` ORDER BY id ASC LIMIT 1`

	u := &domain.User{}
	err := r.db.QueryRow(ctx, query, args...).Scan(
		&u.ID, &u.KeycloakID, &u.IIN,
		&u.Email, &u.Username, &u.PasswordHash,
		&u.FirstName, &u.LastName,
		&u.AvatarURL, &u.Bio, &u.Position,
		&u.IsActive, &u.IsFirstLogin, &u.LastLoginAt, &u.LastSeenAt,
		&u.CreatedAt, &u.UpdatedAt, &u.DeletedAt,
		&u.Role, &u.AvailabilityStatus, &u.FullNameLocal,
		&u.CanViewFullDriveActivity, &u.Language,
		&u.OrganizationID, &u.TeamID, &u.ManagerID,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("user not found")
		}
		return nil, fmt.Errorf("get user by normalized email: %w", err)
	}
	return u, nil
}

func (r *UserRepository) getByNormalizedUsername(ctx context.Context, username string, excludeIDs ...int64) (*domain.User, error) {
	username = normalizeKeycloakUsername(username)
	if username == "" {
		return nil, fmt.Errorf("user not found")
	}

	query := `
       SELECT
          id, keycloak_id, iin,
          email, username, COALESCE(password_hash, '') AS password_hash,
          first_name, last_name,
          avatar_url, bio, position,
          is_active, is_first_login, last_login_at, last_seen_at,
          created_at, updated_at, deleted_at,
          role, availability_status, full_name_local,
          can_view_full_drive_activity, language,
          organization_id, team_id, manager_id
       FROM users
       WHERE LOWER(REGEXP_REPLACE(TRIM(username), '\s+', '', 'g')) = $1
         AND deleted_at IS NULL
    `
	args := []any{username}
	if len(excludeIDs) > 0 {
		query += ` AND id <> ALL($2)`
		args = append(args, excludeIDs)
	}
	query += ` ORDER BY id ASC LIMIT 1`

	u := &domain.User{}
	err := r.db.QueryRow(ctx, query, args...).Scan(
		&u.ID, &u.KeycloakID, &u.IIN,
		&u.Email, &u.Username, &u.PasswordHash,
		&u.FirstName, &u.LastName,
		&u.AvatarURL, &u.Bio, &u.Position,
		&u.IsActive, &u.IsFirstLogin, &u.LastLoginAt, &u.LastSeenAt,
		&u.CreatedAt, &u.UpdatedAt, &u.DeletedAt,
		&u.Role, &u.AvailabilityStatus, &u.FullNameLocal,
		&u.CanViewFullDriveActivity, &u.Language,
		&u.OrganizationID, &u.TeamID, &u.ManagerID,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("user not found")
		}
		return nil, fmt.Errorf("get user by normalized username: %w", err)
	}
	return u, nil
}

func (r *UserRepository) getMergedIntoID(ctx context.Context, userID int64) (int64, error) {
	var mergedIntoID *int64
	if err := r.db.QueryRow(ctx, `SELECT merged_into_id FROM users WHERE id = $1 AND deleted_at IS NULL`, userID).Scan(&mergedIntoID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, fmt.Errorf("user not found")
		}
		return 0, fmt.Errorf("get merged_into_id: %w", err)
	}
	if mergedIntoID == nil || *mergedIntoID == 0 {
		return 0, nil
	}
	return *mergedIntoID, nil
}

func (r *UserRepository) syncKeycloakIdentityFields(existing *domain.User, incoming *domain.User) {
	if existing == nil || incoming == nil {
		return
	}

	existing.KeycloakID = incoming.KeycloakID
	if shouldReplaceIdentityPointer(existing.IIN, incoming.IIN, normalizeKeycloakIIN) {
		normalizedIIN := normalizeKeycloakIIN(*incoming.IIN)
		existing.IIN = &normalizedIIN
	}
	if shouldReplaceIdentityField(normalizeKeycloakEmail(existing.Email), normalizeKeycloakEmail(incoming.Email)) {
		existing.Email = normalizeKeycloakEmail(incoming.Email)
	}
	if shouldReplaceIdentityField(normalizeKeycloakUsername(existing.Username), normalizeKeycloakUsername(incoming.Username)) {
		existing.Username = strings.TrimSpace(incoming.Username)
	}

	existing.FirstName = incoming.FirstName
	existing.LastName = incoming.LastName
	existing.IsActive = incoming.IsActive
	existing.Role = incoming.Role
	if incoming.OrganizationID != nil {
		existing.OrganizationID = incoming.OrganizationID
	}
	if incoming.TeamID != nil {
		existing.TeamID = incoming.TeamID
	}
	if incoming.ManagerID != nil {
		existing.ManagerID = incoming.ManagerID
	}

	existing.UpdatedAt = time.Now()
}

func (r *UserRepository) persistLinkedKeycloakUser(ctx context.Context, existing *domain.User, incoming *domain.User, matchReason string) error {
	if !existing.IsActive {
		return apperrors.Forbidden("employee is inactive")
	}
	r.syncKeycloakIdentityFields(existing, incoming)
	if err := r.Update(ctx, existing); err != nil {
		return err
	}
	incoming.ID = existing.ID
	r.logger.Info("keycloak_user_linked_to_existing_user",
		zap.Int64("existing_user_id", existing.ID),
		zap.String("keycloak_sub", derefKeycloakValue(incoming.KeycloakID)),
		zap.String("email", normalizeKeycloakEmail(incoming.Email)),
		zap.String("iin", derefNormalizedKeycloakValue(incoming.IIN, normalizeKeycloakIIN)),
		zap.String("username", normalizeKeycloakUsername(incoming.Username)),
		zap.String("match_reason", matchReason),
		zap.Bool("was_created", false),
	)
	return nil
}

func derefKeycloakValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func derefNormalizedKeycloakValue(value *string, normalizer func(string) string) string {
	if value == nil {
		return ""
	}
	return normalizer(*value)
}

func (r *UserRepository) reassignKeycloakIdentity(ctx context.Context, sourceUserID int64, target *domain.User, incoming *domain.User, matchReason string) error {
	if sourceUserID == 0 || target == nil || incoming == nil || sourceUserID == target.ID {
		return r.persistLinkedKeycloakUser(ctx, target, incoming, matchReason)
	}

	now := time.Now()
	if _, err := r.db.Exec(ctx, `
		UPDATE users
		SET keycloak_id = NULL,
		    merged_into_id = $2,
		    updated_at = $3
		WHERE id = $1 AND deleted_at IS NULL
	`, sourceUserID, target.ID, now); err != nil {
		return fmt.Errorf("reassign keycloak identity source user: %w", err)
	}

	if err := r.persistLinkedKeycloakUser(ctx, target, incoming, matchReason); err != nil {
		return err
	}

	r.logger.Warn("keycloak_duplicate_user_relinked",
		zap.Int64("duplicate_user_id", sourceUserID),
		zap.Int64("canonical_user_id", target.ID),
		zap.String("keycloak_sub", derefKeycloakValue(incoming.KeycloakID)),
		zap.String("match_reason", matchReason),
	)
	return nil
}

func (r *UserRepository) findKeycloakMatchCandidate(ctx context.Context, incoming *domain.User, excludeIDs ...int64) (*domain.User, string, error) {
	if incoming == nil {
		return nil, "", fmt.Errorf("user is nil")
	}

	if incoming.IIN != nil {
		if normalizedIIN := normalizeKeycloakIIN(*incoming.IIN); normalizedIIN != "" {
			existingByIIN, err := r.GetByIIN(ctx, normalizedIIN)
			if err == nil && existingByIIN != nil {
				if !containsInt64(excludeIDs, existingByIIN.ID) {
					return existingByIIN, "iin", nil
				}
			}
		}
	}

	if email := normalizeKeycloakEmail(incoming.Email); email != "" {
		existingByEmail, err := r.getByNormalizedEmail(ctx, email, excludeIDs...)
		if err == nil && existingByEmail != nil {
			return existingByEmail, "email", nil
		}
	}

	if username := normalizeKeycloakUsername(incoming.Username); username != "" {
		existingByUsername, err := r.getByNormalizedUsername(ctx, username, excludeIDs...)
		if err == nil && existingByUsername != nil {
			return existingByUsername, "username", nil
		}
		if normalizedUsernameIIN := normalizeKeycloakIIN(username); normalizedUsernameIIN != "" {
			existingByUsernameIIN, err := r.GetByIIN(ctx, normalizedUsernameIIN)
			if err == nil && existingByUsernameIIN != nil {
				if !containsInt64(excludeIDs, existingByUsernameIIN.ID) {
					return existingByUsernameIIN, "username_iin", nil
				}
			}
		}
	}

	return nil, "", fmt.Errorf("user not found")
}

func (r *UserRepository) ResolveCanonicalUserID(ctx context.Context, userID int64) (int64, *domain.User, error) {
	if userID <= 0 {
		return userID, nil, fmt.Errorf("invalid user id")
	}

	current, err := r.GetByID(ctx, userID)
	if err != nil {
		return userID, nil, err
	}

	if mergedIntoID, err := r.getMergedIntoID(ctx, userID); err == nil && mergedIntoID > 0 {
		canonical, canonicalErr := r.GetByID(ctx, mergedIntoID)
		if canonicalErr == nil && canonical != nil {
			return canonical.ID, canonical, nil
		}
	}

	candidate, _, candidateErr := r.findKeycloakMatchCandidate(ctx, current, userID)
	if candidateErr == nil && candidate != nil {
		return candidate.ID, candidate, nil
	}

	return current.ID, current, nil
}

// Update updates user (NO password hash stored)
func (r *UserRepository) Update(ctx context.Context, user *domain.User) error {
	query := `
       UPDATE users 
       SET
          keycloak_id = $1,
          iin = $2,
          email = $3,
          username = $4,
          first_name = $5,
          last_name = $6,
          avatar_url = $7,
          bio = $8,
          position = $9,
          is_active = $10,
          updated_at = $11,
          role = $12,
          availability_status = $13,
          full_name_local = $14,
          can_view_full_drive_activity = $15,
          organization_id = $16,
          team_id = $17,
          manager_id = $18
       WHERE id = $19 AND deleted_at IS NULL
    `

	user.UpdatedAt = time.Now()

	res, err := r.db.Exec(ctx, query,
		user.KeycloakID,
		user.IIN,
		user.Email,
		user.Username,
		user.FirstName,
		user.LastName,
		user.AvatarURL,
		user.Bio,
		user.Position,
		user.IsActive,
		user.UpdatedAt,
		user.Role,
		user.AvailabilityStatus,
		user.FullNameLocal,
		user.CanViewFullDriveActivity,
		user.OrganizationID,
		user.TeamID,
		user.ManagerID,
		user.ID,
	)

	if err != nil {
		r.logger.Error(config.LogEvent(config.LogOpUpdate, "User update failed"),
			config.LogOp(config.LogOpUpdate),
			zap.Error(err),
			zap.Int64("user_id", user.ID),
		)
		return fmt.Errorf("update user: %w", err)
	}

	if res.RowsAffected() == 0 {
		return fmt.Errorf("user not found")
	}

	r.logger.Debug(config.LogEvent(config.LogOpUpdate, "User updated"),
		config.LogOp(config.LogOpUpdate),
		zap.Int64("user_id", user.ID),
	)
	return nil
}

// UpdateLastLogin updates the last login time AND sets is_first_login to FALSE
func (r *UserRepository) UpdateLastLogin(ctx context.Context, userID int64, loginAt time.Time) error {
	query := `
       UPDATE users 
       SET last_login_at = $1
       WHERE id = $2 AND deleted_at IS NULL
    `

	_, err := r.db.Exec(ctx, query, loginAt, userID)
	if err != nil {
		r.logger.Error("Failed to update last login", zap.Error(err), zap.Int64("user_id", userID))
		return fmt.Errorf("update last login: %w", err)
	}

	return nil
}

// UpdateLastSeen updates the last seen time
func (r *UserRepository) UpdateLastSeen(ctx context.Context, userID int64, seenAt time.Time) error {
	query := `
       UPDATE users 
       SET last_seen_at = $1
       WHERE id = $2 AND deleted_at IS NULL
    `

	_, err := r.db.Exec(ctx, query, seenAt, userID)
	if err != nil {
		r.logger.Error("Failed to update last seen", zap.Error(err), zap.Int64("user_id", userID))
		return fmt.Errorf("update last seen: %w", err)
	}

	return nil
}

// CompleteOnboarding sets is_first_login to FALSE.
// Call this after the frontend has shown the welcome animation.
func (r *UserRepository) CompleteOnboarding(ctx context.Context, userID int64) error {
	query := `
       UPDATE users 
       SET is_first_login = FALSE
       WHERE id = $1 AND deleted_at IS NULL
    `
	_, err := r.db.Exec(ctx, query, userID)
	if err != nil {
		r.logger.Error("Failed to complete onboarding", zap.Error(err), zap.Int64("user_id", userID))
		return fmt.Errorf("complete onboarding: %w", err)
	}
	return nil
}

// SetIsFirstLogin sets is_first_login to specified value in database.
func (r *UserRepository) SetIsFirstLogin(ctx context.Context, userID int64, isFirst bool) error {
	query := `
       UPDATE users 
       SET is_first_login = $1
       WHERE id = $2 AND deleted_at IS NULL
    `
	_, err := r.db.Exec(ctx, query, isFirst, userID)
	if err != nil {
		r.logger.Error("Failed to set is_first_login", zap.Error(err), zap.Bool("is_first_login", isFirst), zap.Int64("user_id", userID))
		return fmt.Errorf("set is_first_login: %w", err)
	}
	return nil
}

func (r *UserRepository) SearchVisibleTo(ctx context.Context, requesterID int64, q string, limit, offset int) ([]*domain.User, error) {
	requester, err := r.GetByID(ctx, requesterID)
	if err != nil {
		return nil, fmt.Errorf("get requester: %w", err)
	}
	if requester == nil || !requester.IsActive {
		return nil, fmt.Errorf("inactive directory requester")
	}
	return r.searchUserDirectory(ctx, q, limit, offset, requester)
}

func (r *UserRepository) searchUserDirectory(ctx context.Context, q string, limit, offset int, requester *domain.User) ([]*domain.User, error) {

	searchCondition, searchArgs, hasSearch := buildUserSearchCondition(q, 1, userSearchFields{
		// Each token is matched against the public calendar search document.
		// Keep this expression aligned with the calendar trigram index.
		documents: []string{
			"COALESCE(u.first_name, '') || ' ' || COALESCE(u.last_name, '') || ' ' || COALESCE(u.full_name_local, '') || ' ' || COALESCE(u.username, '') || ' ' || COALESCE(u.email, '')",
		},
	})

	query := `
       SELECT
          u.id, u.keycloak_id, u.iin,
          u.email, u.username,
          u.first_name,
          u.last_name,
          u.avatar_url, u.bio,
          u.is_active, u.is_first_login, u.created_at, u.full_name_local
       FROM users u
       WHERE u.deleted_at IS NULL
         AND u.is_active = true
    `

	args := make([]any, 0, len(searchArgs)+5)
	args = append(args, searchArgs...)
	if hasSearch {
		query += " AND " + searchCondition
	}
	if requester != nil && !domain.HasGlobalUserScope(requester) {
		if requester.OrganizationID != nil {
			query += fmt.Sprintf(" AND (u.id = $%d OR u.id = $%d OR u.organization_id = $%d)", len(args)+1, len(args)+2, len(args)+3)
			args = append(args, requester.ID, domain.SupportUserID, *requester.OrganizationID)
		} else if requester.MinistryID != nil {
			query += fmt.Sprintf(" AND (u.id = $%d OR u.id = $%d OR (u.organization_id IS NULL AND u.ministry_id = $%d))", len(args)+1, len(args)+2, len(args)+3)
			args = append(args, requester.ID, domain.SupportUserID, *requester.MinistryID)
		} else {
			query += fmt.Sprintf(" AND (u.id = $%d OR u.id = $%d)", len(args)+1, len(args)+2)
			args = append(args, requester.ID, domain.SupportUserID)
		}
	}

	if hasSearch {
		normalizedQuery := normalizeUserSearchQuery(q)
		exactQueryIndex := len(args) + 1
		prefixQueryIndex := len(args) + 2
		args = append(args, normalizedQuery, normalizedQuery+"%")
		query += fmt.Sprintf(`
       ORDER BY
         CASE
           WHEN LOWER(regexp_replace(TRIM(regexp_replace(COALESCE(concat_ws(' ', u.first_name, u.last_name, u.full_name_local), ''), '[‐‑‒–—―-]+', ' ', 'g')), '\s+', ' ', 'g')) = $%d THEN 0
           WHEN LOWER(regexp_replace(TRIM(regexp_replace(COALESCE(concat_ws(' ', u.first_name, u.last_name, u.full_name_local), ''), '[‐‑‒–—―-]+', ' ', 'g')), '\s+', ' ', 'g')) LIKE $%d THEN 1
           ELSE 2
         END,
         similarity(LOWER(regexp_replace(TRIM(regexp_replace(COALESCE(concat_ws(' ', u.first_name, u.last_name, u.full_name_local, u.username, u.email), ''), '[‐‑‒–—―-]+', ' ', 'g')), '\s+', ' ', 'g')), $%d) DESC,
         u.last_name,
         u.first_name,
		 u.id`, exactQueryIndex, prefixQueryIndex, exactQueryIndex)
	} else {
		query += ` ORDER BY u.last_name, u.first_name, u.id`
	}

	query += fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(args)+1, len(args)+2)
	args = append(args, limit, offset)

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		r.logger.Error("Failed to search visible users", zap.Error(err))
		return nil, fmt.Errorf("search visible users: %w", err)
	}
	defer rows.Close()

	var users []*domain.User
	for rows.Next() {
		u := &domain.User{}
		if err := rows.Scan(
			&u.ID, &u.KeycloakID, &u.IIN,
			&u.Email, &u.Username,
			&u.FirstName, &u.LastName,
			&u.AvatarURL, &u.Bio,
			&u.IsActive, &u.IsFirstLogin, &u.CreatedAt, &u.FullNameLocal,
		); err != nil {
			r.logger.Error("Failed to scan visible user", zap.Error(err))
			continue
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate visible users: %w", err)
	}

	return users, nil
}

func (r *UserRepository) SearchAllUsers(ctx context.Context, q string, limit, offset int) ([]*domain.User, error) {
	searchCondition, searchArgs, hasSearch := buildUserSearchCondition(q, 1, userSearchFields{
		nameParts: []string{"first_name", "last_name"},
		fields:    []string{"username", "email", "iin", "full_name_local"},
	})

	query := `
       SELECT
          id, keycloak_id, iin,
          email, username,
          first_name, last_name,
          avatar_url, bio,
          is_active, is_first_login, created_at, full_name_local
       FROM users
       WHERE deleted_at IS NULL
         AND is_active = true
         AND COALESCE(role, 'user') <> 'admin'
    `

	args := make([]any, 0, len(searchArgs)+2)
	args = append(args, searchArgs...)
	if hasSearch {
		query += " AND " + searchCondition
	}

	query += fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(args)+1, len(args)+2)
	args = append(args, limit, offset)

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		r.logger.Error("Failed to search all users", zap.Error(err))
		return nil, fmt.Errorf("search all users: %w", err)
	}
	defer rows.Close()

	var users []*domain.User
	for rows.Next() {
		u := &domain.User{}
		if err := rows.Scan(
			&u.ID, &u.KeycloakID, &u.IIN,
			&u.Email, &u.Username,
			&u.FirstName, &u.LastName,
			&u.AvatarURL, &u.Bio,
			&u.IsActive, &u.IsFirstLogin, &u.CreatedAt, &u.FullNameLocal,
		); err != nil {
			r.logger.Error("Failed to scan user", zap.Error(err))
			continue
		}
		users = append(users, u)
	}

	return users, nil
}

// GetIDsByUsernames returns a map of username -> user_id for the given usernames
func (r *UserRepository) GetIDsByUsernames(ctx context.Context, usernames []string) (map[string]int64, error) {
	if len(usernames) == 0 {
		return make(map[string]int64), nil
	}

	query := `
       SELECT username, id
       FROM users
       WHERE username = ANY($1) AND deleted_at IS NULL
    `

	rows, err := r.db.Query(ctx, query, usernames)
	if err != nil {
		r.logger.Error("Failed to get user IDs by usernames", zap.Error(err))
		return nil, fmt.Errorf("get user ids by usernames: %w", err)
	}
	defer rows.Close()

	result := make(map[string]int64)
	for rows.Next() {
		var username string
		var id int64
		if err := rows.Scan(&username, &id); err != nil {
			continue
		}
		result[username] = id
	}
	return result, nil
}

func (r *UserRepository) GetAll(ctx context.Context) ([]*domain.User, error) {
	query := `
       SELECT
          id, keycloak_id, iin,
          email, username,
          first_name, last_name,
          avatar_url, bio,
          is_active, is_first_login, last_login_at, last_seen_at,
          created_at, updated_at, deleted_at
       FROM users
       WHERE deleted_at IS NULL
       ORDER BY created_at DESC
    `

	rows, err := r.db.Query(ctx, query)
	if err != nil {
		r.logger.Error("Failed to get all users", zap.Error(err))
		return nil, fmt.Errorf("get all users: %w", err)
	}
	defer rows.Close()

	users := make([]*domain.User, 0, 32)

	for rows.Next() {
		u := &domain.User{Role: domain.RoleUser}
		if err := rows.Scan(
			&u.ID, &u.KeycloakID, &u.IIN,
			&u.Email, &u.Username,
			&u.FirstName, &u.LastName,
			&u.AvatarURL, &u.Bio,
			&u.IsActive, &u.IsFirstLogin, &u.LastLoginAt, &u.LastSeenAt,
			&u.CreatedAt, &u.UpdatedAt, &u.DeletedAt,
		); err != nil {
			r.logger.Error("Failed to scan user", zap.Error(err))
			continue
		}
		users = append(users, u)
	}

	if err := rows.Err(); err != nil {
		r.logger.Error("Rows iteration error", zap.Error(err))
		return nil, fmt.Errorf("get all users: %w", err)
	}

	return users, nil
}

func (r *UserRepository) GetPaginated(ctx context.Context, q string, limit, offset int, filters ...domain.UserFilters) ([]*domain.User, int, error) {
	return r.getPaginated(ctx, q, limit, offset, nil, filters...)
}

// GetPaginatedVisibleTo returns the directory visible to a requester. Global
// administrators are handled by the unscoped query; all other users are
// restricted to their organization before pagination is applied.
func (r *UserRepository) GetPaginatedVisibleTo(ctx context.Context, requesterID int64, q string, limit, offset int, filters ...domain.UserFilters) ([]*domain.User, int, error) {
	return r.getPaginated(ctx, q, limit, offset, &requesterID, filters...)
}

func (r *UserRepository) getPaginated(ctx context.Context, q string, limit, offset int, requesterID *int64, filters ...domain.UserFilters) ([]*domain.User, int, error) {
	if limit <= 0 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}

	searchCondition, searchArgs, hasSearch := buildUserSearchCondition(q, 1, userSearchFields{
		nameParts: []string{"u.first_name", "u.last_name"},
		fields:    []string{"u.username", "u.email", "u.iin", "u.full_name_local"},
	})

	baseQuery := `
		FROM users u
		WHERE u.deleted_at IS NULL
		  AND COALESCE(u.role, 'user') <> 'admin'
	`
	if requesterID != nil {
		requester, err := r.GetByID(ctx, *requesterID)
		if err != nil {
			return nil, 0, fmt.Errorf("get directory requester: %w", err)
		}
		if requester == nil {
			return nil, 0, fmt.Errorf("directory requester not found")
		}
		if !domain.HasGlobalUserScope(requester) {
			if requester.OrganizationID != nil {
				baseQuery += fmt.Sprintf(" AND u.organization_id = $%d", len(searchArgs)+1)
				searchArgs = append(searchArgs, *requester.OrganizationID)
			} else {
				baseQuery += fmt.Sprintf(" AND u.id = $%d", len(searchArgs)+1)
				searchArgs = append(searchArgs, requester.ID)
			}
		}
	}
	if hasSearch {
		baseQuery += " AND " + searchCondition
	}

	if len(filters) > 0 {
		f := filters[0]
		if f.PositionName != "" {
			baseQuery += fmt.Sprintf(" AND COALESCE(u.position, '') ILIKE $%d", len(searchArgs)+1)
			searchArgs = append(searchArgs, "%"+f.PositionName+"%")
		}
	}

	countQuery := "SELECT COUNT(*)" + baseQuery

	selectQuery := `
		SELECT
		  u.id, u.keycloak_id, u.iin,
		  u.email, u.username,
		  u.first_name, u.last_name,
		  u.avatar_url, u.bio, u.position,
		  u.is_active, u.is_first_login, u.last_login_at, u.last_seen_at,
		  u.created_at, u.updated_at, u.deleted_at,
		  NULL::bigint AS ministry_id, u.role, NULL::bigint AS state_body_id, NULL::bigint AS department_id, NULL::bigint AS division_id,
		  NULL::bigint AS prime_minister_id, NULL::bigint AS deputy_prime_minister_id, u.availability_status, u.full_name_local,
		  '' AS organization_name,
		  '' AS department_name,
		  COALESCE(u.position, '') AS position_name
	` + baseQuery
	selectQuery += fmt.Sprintf(" ORDER BY u.created_at DESC LIMIT $%d OFFSET $%d", len(searchArgs)+1, len(searchArgs)+2)

	var (
		users []*domain.User
		total int
	)

	countArgs := append([]any(nil), searchArgs...)
	if err := r.db.QueryRow(ctx, countQuery, countArgs...).Scan(&total); err != nil {
		r.logger.Error("Failed to count paginated users", zap.Error(err))
		return nil, 0, fmt.Errorf("count paginated users: %w", err)
	}

	if total == 0 {
		return []*domain.User{}, 0, nil
	}

	selectArgs := append(append([]any(nil), searchArgs...), limit, offset)
	rows, err := r.db.Query(ctx, selectQuery, selectArgs...)
	if err != nil {
		r.logger.Error("Failed to query paginated users", zap.Error(err))
		return nil, 0, fmt.Errorf("query paginated users: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		u := &domain.User{}
		if err := rows.Scan(
			&u.ID, &u.KeycloakID, &u.IIN,
			&u.Email, &u.Username,
			&u.FirstName, &u.LastName,
			&u.AvatarURL, &u.Bio, &u.Position,
			&u.IsActive, &u.IsFirstLogin, &u.LastLoginAt, &u.LastSeenAt,
			&u.CreatedAt, &u.UpdatedAt, &u.DeletedAt,
			&u.MinistryID, &u.Role, &u.StateBodyID, &u.DepartmentID, &u.DivisionID,
			&u.PrimeMinisterID, &u.DeputyPrimeMinisterID, &u.AvailabilityStatus, &u.FullNameLocal,
			&u.OrganizationName, &u.DepartmentName, &u.PositionName,
		); err != nil {
			r.logger.Error("Failed to scan paginated user", zap.Error(err))
			continue
		}
		users = append(users, u)
	}

	if err := rows.Err(); err != nil {
		r.logger.Error("Paginated users rows iteration error", zap.Error(err))
		return nil, 0, fmt.Errorf("query paginated users: %w", err)
	}

	return users, total, nil
}

func (r *UserRepository) GetAllVisibleTo(ctx context.Context, requesterID int64) ([]*domain.User, error) {
	requester, err := r.GetByID(ctx, requesterID)
	if err != nil {
		return nil, fmt.Errorf("get requester: %w", err)
	}

	query := `
       SELECT
          id, keycloak_id, iin,
          email, username,
          first_name, last_name,
          avatar_url, bio,
          is_active, is_first_login, last_login_at, last_seen_at,
          created_at, updated_at, deleted_at,
          ministry_id, role, state_body_id, department_id, division_id,
          prime_minister_id, deputy_prime_minister_id
       FROM users
       WHERE deleted_at IS NULL
         AND COALESCE(role, 'user') <> 'admin'
    `

	args := make([]interface{}, 0, 3)
	if !domain.HasGlobalUserScope(requester) {
		if requester.OrganizationID != nil {
			query += fmt.Sprintf(" AND (id = $%d OR id = $%d OR organization_id = $%d)", len(args)+1, len(args)+2, len(args)+3)
			args = append(args, requester.ID, domain.SupportUserID, *requester.OrganizationID)
		} else if requester.MinistryID != nil {
			query += ` AND (id = $1 OR id = $2 OR (organization_id IS NULL AND ministry_id = $3))`
			args = append(args, requesterID, domain.SupportUserID, *requester.MinistryID)
		} else {
			query += ` AND (id = $1 OR id = $2)`
			args = append(args, requesterID, domain.SupportUserID)
		}
	}

	query += ` ORDER BY created_at DESC`

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		r.logger.Error("Failed to get visible users", zap.Error(err))
		return nil, fmt.Errorf("get visible users: %w", err)
	}
	defer rows.Close()

	users := make([]*domain.User, 0, 32)
	for rows.Next() {
		u := &domain.User{}
		if err := rows.Scan(
			&u.ID, &u.KeycloakID, &u.IIN,
			&u.Email, &u.Username,
			&u.FirstName, &u.LastName,
			&u.AvatarURL, &u.Bio,
			&u.IsActive, &u.IsFirstLogin, &u.LastLoginAt, &u.LastSeenAt,
			&u.CreatedAt, &u.UpdatedAt, &u.DeletedAt,
			&u.MinistryID, &u.Role, &u.StateBodyID, &u.DepartmentID, &u.DivisionID,
			&u.PrimeMinisterID, &u.DeputyPrimeMinisterID,
		); err != nil {
			r.logger.Error("Failed to scan visible user", zap.Error(err))
			continue
		}
		users = append(users, u)
	}

	if err := rows.Err(); err != nil {
		r.logger.Error("Visible users rows iteration error", zap.Error(err))
		return nil, fmt.Errorf("get visible users: %w", err)
	}

	return users, nil
}

func (r *UserRepository) DemoteMinistryLeaders(ctx context.Context, ministryID int64, excludeUserID int64) ([]string, error) {
	return nil, nil
}

func (r *UserRepository) DemoteStateBodyLeaders(ctx context.Context, sbID int64, excludeUserID int64) ([]string, error) {
	return nil, nil
}

func (r *UserRepository) DemoteDepartmentLeaders(ctx context.Context, deptID int64, excludeUserID int64) ([]string, error) {
	return nil, nil
}

func (r *UserRepository) DemoteDivisionLeaders(ctx context.Context, divID int64, excludeUserID int64) ([]string, error) {
	return nil, nil
}

func (r *UserRepository) UpsertFromKeycloak(ctx context.Context, u *domain.User) error {
	if u == nil {
		return fmt.Errorf("user is nil")
	}
	if u.KeycloakID == nil || *u.KeycloakID == "" {
		return fmt.Errorf("keycloak_id is empty")
	}

	normalizedEmail := normalizeKeycloakEmail(u.Email)
	normalizedUsername := normalizeKeycloakUsername(u.Username)
	normalizedIIN := ""
	if u.IIN != nil {
		normalizedIIN = normalizeKeycloakIIN(*u.IIN)
		if normalizedIIN == "" {
			u.IIN = nil
		} else {
			u.IIN = &normalizedIIN
		}
	}
	u.Email = normalizedEmail
	u.Username = strings.TrimSpace(u.Username)

	kcID := strings.TrimSpace(*u.KeycloakID)
	*u.KeycloakID = kcID
	// A deleted identity is a tombstone, never a candidate for SSO relinking.
	// The unique Keycloak index and deletion trigger also cover concurrent sync.
	var deleted bool
	if err := r.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE keycloak_id=$1 AND deleted_at IS NOT NULL)`, kcID).Scan(&deleted); err != nil {
		return err
	}
	if deleted {
		return ErrKeycloakUserNotLinked
	}

	existingByKeycloakID, err := r.GetByKeycloakID(ctx, kcID)
	if err == nil && existingByKeycloakID != nil {
		canonicalCandidate, matchReason, candidateErr := r.findKeycloakMatchCandidate(ctx, u, existingByKeycloakID.ID)
		if candidateErr == nil && canonicalCandidate != nil && canonicalCandidate.ID != existingByKeycloakID.ID {
			return r.reassignKeycloakIdentity(ctx, existingByKeycloakID.ID, canonicalCandidate, u, matchReason)
		}
		return r.persistLinkedKeycloakUser(ctx, existingByKeycloakID, u, "keycloak_id")
	}

	existingCandidate, matchReason, matchErr := r.findKeycloakMatchCandidate(ctx, u)
	if matchErr == nil && existingCandidate != nil {
		return r.persistLinkedKeycloakUser(ctx, existingCandidate, u, matchReason)
	}

	if !keycloakAutoCreateUsersEnabled() {
		r.logger.Warn("keycloak_user_not_linked",
			zap.String("keycloak_sub", kcID),
			zap.String("email", normalizedEmail),
			zap.String("iin", normalizedIIN),
			zap.String("username", normalizedUsername),
			zap.String("reason", "no_existing_match"),
		)
		return ErrKeycloakUserNotLinked
	}

	if u.CreatedAt.IsZero() {
		u.CreatedAt = time.Now()
	}
	u.UpdatedAt = time.Now()

	if err := r.Create(ctx, u); err != nil {
		return err
	}

	r.logger.Info("keycloak_user_created_new_local_user",
		zap.Int64("new_user_id", u.ID),
		zap.String("keycloak_sub", kcID),
		zap.String("email", normalizedEmail),
		zap.String("iin", normalizedIIN),
		zap.String("username", normalizedUsername),
		zap.String("reason", "no_existing_match"),
	)
	return nil
}

func (r *UserRepository) UpdateStatus(ctx context.Context, userID int64, status string) error {
	query := `
		UPDATE users
		SET availability_status = $1, updated_at = now()
		WHERE id = $2 AND deleted_at IS NULL
	`
	res, err := r.db.Exec(ctx, query, status, userID)
	if err != nil {
		return fmt.Errorf("update status: %w", err)
	}
	if res.RowsAffected() == 0 {
		return fmt.Errorf("user not found")
	}
	return nil
}

// GetByKeycloakIDs returns users by keycloak IDs
func (r *UserRepository) GetByKeycloakIDs(ctx context.Context, kcIDs []string) ([]*domain.User, error) {
	if len(kcIDs) == 0 {
		return []*domain.User{}, nil
	}

	query := `
		SELECT
		  id, keycloak_id, iin,
		  email, username,
		  first_name, last_name,
		  avatar_url, bio, position,
		  is_active, is_first_login, last_login_at, last_seen_at,
		  created_at, updated_at, deleted_at,
		  ministry_id, role, state_body_id, department_id, division_id,
		  prime_minister_id, deputy_prime_minister_id, availability_status, full_name_local,
		  organization_id, team_id, manager_id
		FROM users
		WHERE deleted_at IS NULL
		  AND keycloak_id = ANY($1)
		  AND is_active = true
	`

	rows, err := r.db.Query(ctx, query, kcIDs)
	if err != nil {
		return nil, fmt.Errorf("get by keycloak_ids: %w", err)
	}
	defer rows.Close()

	var res []*domain.User
	for rows.Next() {
		u := &domain.User{}
		if err := rows.Scan(
			&u.ID, &u.KeycloakID, &u.IIN,
			&u.Email, &u.Username,
			&u.FirstName, &u.LastName,
			&u.AvatarURL, &u.Bio, &u.Position,
			&u.IsActive, &u.IsFirstLogin, &u.LastLoginAt, &u.LastSeenAt,
			&u.CreatedAt, &u.UpdatedAt, &u.DeletedAt,
			&u.MinistryID, &u.Role, &u.StateBodyID, &u.DepartmentID, &u.DivisionID,
			&u.PrimeMinisterID, &u.DeputyPrimeMinisterID, &u.AvailabilityStatus, &u.FullNameLocal,
			&u.OrganizationID, &u.TeamID, &u.ManagerID,
		); err != nil {
			r.logger.Error("Failed to scan user", zap.Error(err))
			continue
		}
		res = append(res, u)
	}
	return res, nil
}

// GetNeverLoggedIn returns users who have never logged in
func (r *UserRepository) GetNeverLoggedIn(ctx context.Context) ([]*domain.User, error) {
	query := `
       SELECT
          id, keycloak_id, iin,
          email, username,
          first_name, last_name,
          avatar_url, bio, position,
          is_active, is_first_login, last_login_at, last_seen_at,
          created_at, updated_at, deleted_at,
          role, availability_status, full_name_local,
          can_view_full_drive_activity, language,
          organization_id, team_id, manager_id
       FROM users
       WHERE last_login_at IS NULL AND deleted_at IS NULL
       ORDER BY created_at DESC
    `

	rows, err := r.db.Query(ctx, query)
	if err != nil {
		r.logger.Error("Failed to get never logged in users", zap.Error(err))
		return nil, fmt.Errorf("get never logged in users: %w", err)
	}
	defer rows.Close()

	var users []*domain.User

	for rows.Next() {
		u := &domain.User{}
		if err := rows.Scan(
			&u.ID, &u.KeycloakID, &u.IIN,
			&u.Email, &u.Username,
			&u.FirstName, &u.LastName,
			&u.AvatarURL, &u.Bio, &u.Position,
			&u.IsActive, &u.IsFirstLogin, &u.LastLoginAt, &u.LastSeenAt,
			&u.CreatedAt, &u.UpdatedAt, &u.DeletedAt,
			&u.Role, &u.AvailabilityStatus, &u.FullNameLocal,
			&u.CanViewFullDriveActivity, &u.Language,
			&u.OrganizationID, &u.TeamID, &u.ManagerID,
		); err != nil {
			r.logger.Error("Failed to scan user", zap.Error(err))
			continue
		}
		users = append(users, u)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}

	return users, nil
}

// ShareConversation checks if two users share at least one active conversation
func (r *UserRepository) ShareConversation(ctx context.Context, user1ID, user2ID int64) (bool, error) {
	query := `
		SELECT EXISTS (
			SELECT 1 
			FROM conversation_participants cp1
			JOIN conversation_participants cp2 ON cp1.conversation_id = cp2.conversation_id
			WHERE cp1.user_id = $1 
			  AND cp2.user_id = $2
			  AND cp1.left_at IS NULL
			  AND cp2.left_at IS NULL
		)
	`
	var exists bool
	err := r.db.QueryRow(ctx, query, user1ID, user2ID).Scan(&exists)
	if err != nil {
		r.logger.Error("Failed to check share conversation", zap.Error(err), zap.Int64("user1_id", user1ID), zap.Int64("user2_id", user2ID))
		return false, fmt.Errorf("check share conversation: %w", err)
	}
	return exists, nil
}

func (r *UserRepository) SharedConversationUserIDs(ctx context.Context, requesterID int64, candidateIDs []int64) (map[int64]bool, error) {
	if len(candidateIDs) == 0 {
		return map[int64]bool{}, nil
	}

	query := `
		SELECT DISTINCT cp2.user_id
		FROM conversation_participants cp1
		JOIN conversation_participants cp2 ON cp1.conversation_id = cp2.conversation_id
		WHERE cp1.user_id = $1
		  AND cp2.user_id = ANY($2)
		  AND cp1.left_at IS NULL
		  AND cp2.left_at IS NULL
	`

	rows, err := r.db.Query(ctx, query, requesterID, candidateIDs)
	if err != nil {
		return nil, fmt.Errorf("check shared conversations in batch: %w", err)
	}
	defer rows.Close()

	result := make(map[int64]bool, len(candidateIDs))
	for rows.Next() {
		var userID int64
		if err := rows.Scan(&userID); err != nil {
			return nil, err
		}
		result[userID] = true
	}

	return result, rows.Err()
}

func (r *UserRepository) GetPrimaryMailboxLogin(ctx context.Context, userID int64) (string, error) {
	const query = `
		SELECT mailbox_login
		FROM user_mailboxes
		WHERE user_id = $1
		  AND is_primary = TRUE
		LIMIT 1
	`

	var mailboxLogin string
	err := r.db.QueryRow(ctx, query, userID).Scan(&mailboxLogin)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("get primary mailbox login: %w", err)
	}

	return strings.ToLower(strings.TrimSpace(mailboxLogin)), nil
}

func (r *UserRepository) UpsertPrimaryMailboxLogin(ctx context.Context, userID int64, mailboxLogin string) error {
	mailboxLogin = strings.ToLower(strings.TrimSpace(mailboxLogin))
	if mailboxLogin == "" {
		return fmt.Errorf("mailbox login is required")
	}

	const query = `
		INSERT INTO user_mailboxes (user_id, mailbox_login, is_primary, created_at, updated_at)
		VALUES ($1, $2, TRUE, NOW(), NOW())
		ON CONFLICT (user_id) WHERE is_primary = TRUE
		DO UPDATE SET
			mailbox_login = EXCLUDED.mailbox_login,
			updated_at = NOW()
	`

	if _, err := r.db.Exec(ctx, query, userID, mailboxLogin); err != nil {
		return fmt.Errorf("upsert primary mailbox login: %w", err)
	}

	return nil
}

func (r *UserRepository) SoftDelete(ctx context.Context, id int64) error {
	query := `UPDATE users SET deleted_at = NOW(), is_active = FALSE, updated_at = NOW() WHERE id = $1`
	_, err := r.db.Exec(ctx, query, id)
	if err != nil {
		r.logger.Error("Failed to soft delete user", zap.Error(err), zap.Int64("user_id", id))
		return fmt.Errorf("soft delete user: %w", err)
	}
	return nil
}

func (r *UserRepository) UpdateRole(ctx context.Context, id int64, role domain.Role) error {
	query := `UPDATE users SET role = $1, updated_at = NOW() WHERE id = $2`
	_, err := r.db.Exec(ctx, query, role, id)
	if err != nil {
		r.logger.Error("Failed to update user role", zap.Error(err), zap.Int64("user_id", id), zap.String("role", string(role)))
		return fmt.Errorf("update user role: %w", err)
	}
	return nil
}

func (r *UserRepository) UpdateMinistryID(ctx context.Context, id int64, ministryID int64) error {
	query := `UPDATE users SET ministry_id = $1, updated_at = NOW() WHERE id = $2`
	_, err := r.db.Exec(ctx, query, ministryID, id)
	if err != nil {
		r.logger.Error("Failed to update user ministry id", zap.Error(err), zap.Int64("user_id", id), zap.Int64("ministry_id", ministryID))
		return fmt.Errorf("update user ministry id: %w", err)
	}
	return nil
}
