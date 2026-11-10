package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

type UserHRProfileRepository struct {
	db     *pgxpool.Pool
	logger *zap.Logger
}

func NewUserHRProfileRepository(db *pgxpool.Pool, logger *zap.Logger) *UserHRProfileRepository {
	return &UserHRProfileRepository{
		db:     db,
		logger: logger.With(zap.String("repository", "user_hr_profile")),
	}
}

func (r *UserHRProfileRepository) GetByPersonCode(ctx context.Context, personCode string) (*domain.UserHRProfile, error) {
	const query = `
		SELECT id, user_id, person_code
		FROM user_hr_profiles
		WHERE person_code = $1
	`

	profile := &domain.UserHRProfile{}
	err := r.db.QueryRow(ctx, query, personCode).Scan(
		&profile.ID, &profile.UserID, &profile.PersonCode,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("hr profile not found")
		}
		return nil, fmt.Errorf("get hr profile by person code: %w", err)
	}

	return profile, nil
}

func (r *UserHRProfileRepository) GetByIIN(ctx context.Context, iin string) (*domain.UserHRProfile, error) {
	const query = `
		SELECT id, user_id, iin
		FROM user_hr_profiles
		WHERE iin = $1
	`

	profile := &domain.UserHRProfile{}
	err := r.db.QueryRow(ctx, query, strings.TrimSpace(iin)).Scan(
		&profile.ID, &profile.UserID, &profile.IIN,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	return profile, nil
}

func (r *UserHRProfileRepository) GetByUserID(ctx context.Context, userID int64) (*domain.UserHRProfile, error) {
	const query = `
		SELECT
			id, user_id,
			COALESCE(person_code, ''), COALESCE(badge_code, ''), COALESCE(iin, ''),
			COALESCE(last_name, ''), COALESCE(first_name, ''), COALESCE(middle_name, ''), COALESCE(photo, ''),
			COALESCE(doc_type, ''), COALESCE(person_status, ''), COALESCE(last_order_number, ''),
			last_order_date, last_begin_date, first_begin_date,
			COALESCE(absence_status, ''), COALESCE(absence_order_number, ''), absence_order_date,
			begin_absence_date, end_absence_date, COALESCE(card_type, ''), COALESCE(bin, ''),
			COALESCE(name_go, ''), COALESCE(job_name, ''), COALESCE(job_uid, ''), COALESCE(sub_name, ''), COALESCE(sub_uid, ''),
			raw_payload, created_at, updated_at
		FROM user_hr_profiles
		WHERE user_id = $1
	`

	profile := &domain.UserHRProfile{}
	err := r.db.QueryRow(ctx, query, userID).Scan(
		&profile.ID, &profile.UserID, &profile.PersonCode, &profile.BadgeCode, &profile.IIN,
		&profile.LastName, &profile.FirstName, &profile.MiddleName, &profile.Photo,
		&profile.DocType, &profile.PersonStatus, &profile.LastOrderNumber,
		&profile.LastOrderDate, &profile.LastBeginDate, &profile.FirstBeginDate,
		&profile.AbsenceStatus, &profile.AbsenceOrderNumber, &profile.AbsenceOrderDate,
		&profile.BeginAbsenceDate, &profile.EndAbsenceDate, &profile.CardType, &profile.BIN,
		&profile.NameGO, &profile.JobName, &profile.JobUID, &profile.SubName, &profile.SubUID,
		&profile.RawPayload, &profile.CreatedAt, &profile.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("hr profile not found")
		}
		return nil, fmt.Errorf("get hr profile by user id: %w", err)
	}

	return profile, nil
}

func (r *UserHRProfileRepository) GetUniqueValues(ctx context.Context, field string, filter HRProfileFilter) ([]string, error) {
	// Белый список разрешенных полей
	allowedFields := map[string]string{
		"job_name":      "hp.job_name",
		"sub_name":      "hp.sub_name",
		"name_go":       "hp.name_go",
		"person_status": "hp.person_status",
		"iin":           "hp.iin",
	}

	column, ok := allowedFields[field]
	if !ok {
		return nil, fmt.Errorf("invalid filter field: %s", field)
	}

	var conditions []string
	var args []any
	argID := 1

	conditions = append(conditions, fmt.Sprintf("%s IS NOT NULL AND %s != ''", column, column))
	conditions = append(conditions, "u.deleted_at IS NULL")
	if filter.ScopeMinistryID != nil {
		conditions = append(conditions, fmt.Sprintf("u.ministry_id = $%d", argID))
		args = append(args, *filter.ScopeMinistryID)
		argID++
	}
	if filter.ScopeUserID != nil {
		conditions = append(conditions, fmt.Sprintf("u.id = $%d", argID))
		args = append(args, *filter.ScopeUserID)
	}

	query := fmt.Sprintf(`
		SELECT DISTINCT COALESCE(%s, '')
		FROM user_hr_profiles hp
		JOIN users u ON u.id = hp.user_id
		WHERE %s
		ORDER BY 1
	`, column, strings.Join(conditions, " AND "))

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("get unique values for %s: %w", field, err)
	}
	defer rows.Close()

	var values []string
	for rows.Next() {
		var val string
		if err := rows.Scan(&val); err != nil {
			return nil, err
		}
		values = append(values, val)
	}
	return values, nil
}

func (r *UserHRProfileRepository) GetAllFilterValues(ctx context.Context, filter HRProfileFilter) (map[string][]string, error) {
	conditions := []string{"u.deleted_at IS NULL"}
	args := make([]any, 0, 2)
	argID := 1
	if filter.ScopeMinistryID != nil {
		conditions = append(conditions, fmt.Sprintf("u.ministry_id = $%d", argID))
		args = append(args, *filter.ScopeMinistryID)
		argID++
	}
	if filter.ScopeUserID != nil {
		conditions = append(conditions, fmt.Sprintf("u.id = $%d", argID))
		args = append(args, *filter.ScopeUserID)
	}

	query := fmt.Sprintf(`
		SELECT
			COALESCE(array_agg(DISTINCT hp.job_name ORDER BY hp.job_name)
				FILTER (WHERE hp.job_name IS NOT NULL AND hp.job_name <> ''), ARRAY[]::varchar[]),
			COALESCE(array_agg(DISTINCT hp.sub_name ORDER BY hp.sub_name)
				FILTER (WHERE hp.sub_name IS NOT NULL AND hp.sub_name <> ''), ARRAY[]::varchar[]),
			COALESCE(array_agg(DISTINCT hp.name_go ORDER BY hp.name_go)
				FILTER (WHERE hp.name_go IS NOT NULL AND hp.name_go <> ''), ARRAY[]::varchar[]),
			COALESCE(array_agg(DISTINCT hp.person_status ORDER BY hp.person_status)
				FILTER (WHERE hp.person_status IS NOT NULL AND hp.person_status <> ''), ARRAY[]::varchar[]),
			COALESCE(array_agg(DISTINCT hp.iin ORDER BY hp.iin)
				FILTER (WHERE hp.iin IS NOT NULL AND hp.iin <> ''), ARRAY[]::varchar[])
		FROM user_hr_profiles hp
		JOIN users u ON u.id = hp.user_id
		WHERE %s
	`, strings.Join(conditions, " AND "))

	var jobNames, subNames, organizationNames, personStatuses, iins []string
	if err := r.db.QueryRow(ctx, query, args...).Scan(
		&jobNames,
		&subNames,
		&organizationNames,
		&personStatuses,
		&iins,
	); err != nil {
		return nil, fmt.Errorf("get HR filter values: %w", err)
	}

	return map[string][]string{
		"job_name":      jobNames,
		"sub_name":      subNames,
		"name_go":       organizationNames,
		"person_status": personStatuses,
		"iin":           iins,
	}, nil
}

type HRProfileFilter struct {
	Query           string
	JobName         string
	SubName         string
	NameGO          string
	PersonStatus    string
	IIN             string
	ScopeMinistryID *int64
	ScopeUserID     *int64
	CursorUpdatedAt *time.Time
	CursorID        *int64
}

func buildHRProfileSummaryResponse(user *domain.User, profile *domain.UserHRProfileSummary, departmentName, organizationName string) *domain.UserHRProfileSummaryResponse {
	userResp := user.ToResponse()

	if profile == nil {
		profile = &domain.UserHRProfileSummary{
			UserID:    user.ID,
			IIN:       derefString(user.IIN),
			LastName:  user.LastName,
			FirstName: user.FirstName,
			JobName:   derefString(user.Position),
			SubName:   strings.TrimSpace(departmentName),
			NameGO:    strings.TrimSpace(organizationName),
			CreatedAt: user.CreatedAt,
			UpdatedAt: user.UpdatedAt,
		}
	}

	if profile.JobName != "" {
		userResp.PositionName = profile.JobName
	}
	if profile.SubName != "" {
		userResp.DepartmentName = profile.SubName
	} else if strings.TrimSpace(departmentName) != "" {
		userResp.DepartmentName = strings.TrimSpace(departmentName)
	}
	if profile.NameGO != "" {
		userResp.OrganizationName = profile.NameGO
	} else if strings.TrimSpace(organizationName) != "" {
		userResp.OrganizationName = strings.TrimSpace(organizationName)
	}

	return &domain.UserHRProfileSummaryResponse{
		User:      userResp,
		HRProfile: profile,
	}
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func (r *UserHRProfileRepository) List(ctx context.Context, filter HRProfileFilter, limit, offset int) ([]*domain.UserHRProfileSummaryResponse, int, error) {
	if limit <= 0 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	var conditions []string
	var args []any
	argID := 1

	// Базовое условие - не удален
	conditions = append(conditions, "u.deleted_at IS NULL")

	if searchCondition, searchArgs, ok := buildUserSearchCondition(filter.Query, argID, userSearchFields{
		nameParts: []string{"hp.first_name", "hp.last_name", "hp.middle_name"},
		fields: []string{
			"u.first_name",
			"u.last_name",
			"u.full_name_local",
			"u.email",
			"u.username",
			"hp.iin",
			"hp.person_code",
			"hp.badge_code",
			"hp.job_name",
			"hp.sub_name",
			"hp.name_go",
		},
	}); ok {
		conditions = append(conditions, searchCondition)
		args = append(args, searchArgs...)
		argID += len(searchArgs)
	}

	// Точечные фильтры
	if filter.JobName != "" {
		conditions = append(conditions, fmt.Sprintf("hp.job_name = $%d", argID))
		args = append(args, filter.JobName)
		argID++
	}
	if filter.SubName != "" {
		conditions = append(conditions, fmt.Sprintf("hp.sub_name = $%d", argID))
		args = append(args, filter.SubName)
		argID++
	}
	if filter.NameGO != "" {
		conditions = append(conditions, fmt.Sprintf("hp.name_go = $%d", argID))
		args = append(args, filter.NameGO)
		argID++
	}
	if filter.PersonStatus != "" {
		conditions = append(conditions, fmt.Sprintf("hp.person_status = $%d", argID))
		args = append(args, filter.PersonStatus)
		argID++
	}
	if filter.IIN != "" {
		conditions = append(conditions, fmt.Sprintf("hp.iin = $%d", argID))
		args = append(args, filter.IIN)
		argID++
	}
	if filter.ScopeMinistryID != nil {
		conditions = append(conditions, fmt.Sprintf("u.ministry_id = $%d", argID))
		args = append(args, *filter.ScopeMinistryID)
		argID++
	}
	if filter.ScopeUserID != nil {
		conditions = append(conditions, fmt.Sprintf("u.id = $%d", argID))
		args = append(args, *filter.ScopeUserID)
		argID++
	}

	countWhereClause := ""
	if len(conditions) > 0 {
		countWhereClause = "WHERE " + strings.Join(conditions, " AND ")
	}

	countFrom := "users u"
	needsHRJoinForCount := strings.TrimSpace(filter.Query) != "" ||
		filter.JobName != "" ||
		filter.SubName != "" ||
		filter.NameGO != "" ||
		filter.PersonStatus != "" ||
		filter.IIN != ""
	if needsHRJoinForCount {
		countFrom += " LEFT JOIN user_hr_profiles hp ON u.id = hp.user_id"
	}
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM %s %s", countFrom, countWhereClause)
	countArgs := append([]any(nil), args...)

	if filter.CursorUpdatedAt != nil && filter.CursorID != nil {
		conditions = append(conditions, fmt.Sprintf("(COALESCE(hp.updated_at, u.updated_at), COALESCE(hp.id, u.id)) < ($%d, $%d)", argID, argID+1))
		args = append(args, *filter.CursorUpdatedAt, *filter.CursorID)
		argID += 2
	}

	whereClause := ""
	if len(conditions) > 0 {
		whereClause = "WHERE " + strings.Join(conditions, " AND ")
	}

	baseQuery := fmt.Sprintf(`
		SELECT
			u.id, u.keycloak_id, u.iin,
			u.email, u.username,
			u.first_name, u.last_name,
			u.avatar_url, u.bio, u.position,
			u.is_active, u.is_first_login, u.last_login_at, u.last_seen_at,
			u.created_at, u.updated_at, u.deleted_at,
			u.role, u.prime_minister_id, u.deputy_prime_minister_id,
			u.ministry_id, u.state_body_id, u.department_id, u.division_id,
			u.availability_status, u.full_name_local,
			COALESCE(div.name, dep.name, sb.name, ''),
			COALESCE(sb.name, m.name, ''),
			hp.id IS NOT NULL,
			COALESCE(hp.id, 0), COALESCE(hp.user_id, u.id), COALESCE(hp.person_code, ''), COALESCE(hp.badge_code, ''), COALESCE(hp.iin, ''),
			COALESCE(hp.last_name, ''), COALESCE(hp.first_name, ''), COALESCE(hp.middle_name, ''),
			COALESCE(hp.doc_type, ''), COALESCE(hp.person_status, ''), COALESCE(hp.last_order_number, ''),
			hp.last_order_date, hp.last_begin_date, hp.first_begin_date,
			COALESCE(hp.absence_status, ''), COALESCE(hp.absence_order_number, ''), hp.absence_order_date,
			hp.begin_absence_date, hp.end_absence_date, COALESCE(hp.card_type, ''), COALESCE(hp.bin, ''),
			COALESCE(hp.name_go, ''), COALESCE(hp.job_name, ''), COALESCE(hp.job_uid, ''), COALESCE(hp.sub_name, ''), COALESCE(hp.sub_uid, ''),
			COALESCE(hp.created_at, u.created_at), COALESCE(hp.updated_at, u.updated_at)
		FROM users u
		LEFT JOIN user_hr_profiles hp ON u.id = hp.user_id
		LEFT JOIN ministries m ON m.id = u.ministry_id
		LEFT JOIN state_bodies sb ON sb.id = u.state_body_id
		LEFT JOIN departments dep ON dep.id = u.department_id
		LEFT JOIN divisions div ON div.id = u.division_id
		%s
		ORDER BY COALESCE(hp.updated_at, u.updated_at) DESC, COALESCE(hp.id, u.id) DESC
		LIMIT $%d OFFSET $%d
	`, whereClause, argID, argID+1)

	dbOffset := offset
	if filter.CursorUpdatedAt != nil && filter.CursorID != nil {
		dbOffset = 0
	}

	args = append(args, limit, dbOffset)

	var (
		result []*domain.UserHRProfileSummaryResponse
		total  int
	)
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		if err := r.db.QueryRow(gctx, countQuery, countArgs...).Scan(&total); err != nil {
			return fmt.Errorf("count hr profiles: %w", err)
		}
		return nil
	})
	g.Go(func() error {
		rows, err := r.db.Query(gctx, baseQuery, args...)
		if err != nil {
			return fmt.Errorf("list hr profiles: %w", err)
		}
		defer rows.Close()

		localResult := make([]*domain.UserHRProfileSummaryResponse, 0, limit)
		for rows.Next() {
			u := &domain.User{}
			hp := &domain.UserHRProfileSummary{}
			var departmentName string
			var organizationName string
			var hasProfile bool
			if err := rows.Scan(
				&u.ID, &u.KeycloakID, &u.IIN,
				&u.Email, &u.Username,
				&u.FirstName, &u.LastName,
				&u.AvatarURL, &u.Bio, &u.Position,
				&u.IsActive, &u.IsFirstLogin, &u.LastLoginAt, &u.LastSeenAt,
				&u.CreatedAt, &u.UpdatedAt, &u.DeletedAt,
				&u.Role, &u.PrimeMinisterID, &u.DeputyPrimeMinisterID,
				&u.MinistryID, &u.StateBodyID, &u.DepartmentID, &u.DivisionID,
				&u.AvailabilityStatus, &u.FullNameLocal,
				&departmentName, &organizationName,
				&hasProfile,
				&hp.ID, &hp.UserID, &hp.PersonCode, &hp.BadgeCode, &hp.IIN,
				&hp.LastName, &hp.FirstName, &hp.MiddleName,
				&hp.DocType, &hp.PersonStatus, &hp.LastOrderNumber,
				&hp.LastOrderDate, &hp.LastBeginDate, &hp.FirstBeginDate,
				&hp.AbsenceStatus, &hp.AbsenceOrderNumber, &hp.AbsenceOrderDate,
				&hp.BeginAbsenceDate, &hp.EndAbsenceDate, &hp.CardType, &hp.BIN,
				&hp.NameGO, &hp.JobName, &hp.JobUID, &hp.SubName, &hp.SubUID,
				&hp.CreatedAt, &hp.UpdatedAt,
			); err != nil {
				return fmt.Errorf("scan hr profile list: %w", err)
			}

			if !hasProfile {
				hp = nil
			}

			localResult = append(localResult, buildHRProfileSummaryResponse(u, hp, departmentName, organizationName))
		}

		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate hr profile list: %w", err)
		}

		result = localResult
		return nil
	})
	if err := g.Wait(); err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return []*domain.UserHRProfileSummaryResponse{}, 0, nil
	}

	return result, total, nil
}

func (r *UserHRProfileRepository) Upsert(ctx context.Context, profile *domain.UserHRProfile) error {
	if profile == nil {
		return fmt.Errorf("hr profile is nil")
	}

	now := time.Now()
	if profile.CreatedAt.IsZero() {
		profile.CreatedAt = now
	}
	profile.UpdatedAt = now

	const query = `
		INSERT INTO user_hr_profiles (
			user_id, person_code, badge_code, iin,
			last_name, first_name, middle_name, photo,
			doc_type, person_status, last_order_number,
			last_order_date, last_begin_date, first_begin_date,
			absence_status, absence_order_number, absence_order_date,
			begin_absence_date, end_absence_date, card_type, bin,
			name_go, job_name, job_uid, sub_name, sub_uid,
			raw_payload, created_at, updated_at
		)
		VALUES (
			$1,$2,$3,$4,
			$5,$6,$7,$8,
			$9,$10,$11,
			$12,$13,$14,
			$15,$16,$17,
			$18,$19,$20,$21,
			$22,$23,$24,$25,$26,
			$27,$28,$29
		)
		ON CONFLICT (user_id) DO UPDATE SET
			person_code = EXCLUDED.person_code,
			badge_code = EXCLUDED.badge_code,
			iin = EXCLUDED.iin,
			last_name = EXCLUDED.last_name,
			first_name = EXCLUDED.first_name,
			middle_name = EXCLUDED.middle_name,
			photo = EXCLUDED.photo,
			doc_type = EXCLUDED.doc_type,
			person_status = EXCLUDED.person_status,
			last_order_number = EXCLUDED.last_order_number,
			last_order_date = EXCLUDED.last_order_date,
			last_begin_date = EXCLUDED.last_begin_date,
			first_begin_date = EXCLUDED.first_begin_date,
			absence_status = EXCLUDED.absence_status,
			absence_order_number = EXCLUDED.absence_order_number,
			absence_order_date = EXCLUDED.absence_order_date,
			begin_absence_date = EXCLUDED.begin_absence_date,
			end_absence_date = EXCLUDED.end_absence_date,
			card_type = EXCLUDED.card_type,
			bin = EXCLUDED.bin,
			name_go = EXCLUDED.name_go,
			job_name = EXCLUDED.job_name,
			job_uid = EXCLUDED.job_uid,
			sub_name = EXCLUDED.sub_name,
			sub_uid = EXCLUDED.sub_uid,
			raw_payload = EXCLUDED.raw_payload,
			updated_at = EXCLUDED.updated_at
		RETURNING id
	`

	if err := r.db.QueryRow(ctx, query,
		profile.UserID, emptyToNil(profile.PersonCode), emptyToNil(profile.BadgeCode), emptyToNil(profile.IIN),
		emptyToNil(profile.LastName), emptyToNil(profile.FirstName), emptyToNil(profile.MiddleName), emptyToNil(profile.Photo),
		emptyToNil(profile.DocType), emptyToNil(profile.PersonStatus), emptyToNil(profile.LastOrderNumber),
		profile.LastOrderDate, profile.LastBeginDate, profile.FirstBeginDate,
		emptyToNil(profile.AbsenceStatus), emptyToNil(profile.AbsenceOrderNumber), profile.AbsenceOrderDate,
		profile.BeginAbsenceDate, profile.EndAbsenceDate, emptyToNil(profile.CardType), emptyToNil(profile.BIN),
		emptyToNil(profile.NameGO), emptyToNil(profile.JobName), emptyToNil(profile.JobUID), emptyToNil(profile.SubName), emptyToNil(profile.SubUID),
		profile.RawPayload, profile.CreatedAt, profile.UpdatedAt,
	).Scan(&profile.ID); err != nil {
		return fmt.Errorf("upsert hr profile: %w", err)
	}

	return nil
}

func emptyToNil(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func (r *UserHRProfileRepository) GetManagementVacations(ctx context.Context) ([]*domain.ManagementVacationResponse, error) {
	const query = `
		SELECT 
			u.id, 
			u.first_name, 
			u.last_name, 
			u.avatar_url, 
			COALESCE(NULLIF(TRIM(u.position), ''), '')
		FROM users u
		WHERE u.deleted_at IS NULL
		  AND u.is_active = true
		  AND u.availability_status = 'vacation'
		ORDER BY u.last_name ASC
	`

	rows, err := r.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query management vacations: %w", err)
	}
	defer rows.Close()

	result := make([]*domain.ManagementVacationResponse, 0)
	for rows.Next() {
		item := &domain.ManagementVacationResponse{}
		if err := rows.Scan(
			&item.UserID, &item.FirstName, &item.LastName, &item.AvatarURL, &item.Position,
		); err != nil {
			return nil, fmt.Errorf("scan management vacation: %w", err)
		}
		result = append(result, item)
	}
	return result, nil
}
