package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/infrastructure/database"
	"github.com/west6ide/workspace-backend/internal/keycloak"
	"go.uber.org/zap"
)

type userRow struct {
	ID            int64
	KeycloakID    string
	IIN           string
	Email         string
	Username      string
	FirstName     string
	LastName      string
	AvatarURL     string
	Bio           string
	Position      string
	FullNameLocal string
	MinistryID    *int64
	StateBodyID   *int64
	DepartmentID  *int64
	DivisionID    *int64
	PrimePMID     *int64
	DeputyPMID    *int64
	IsActive      bool
	IsFirstLogin  bool
	CreatedAt     time.Time
	LastLoginAt   *time.Time
	LastSeenAt    *time.Time
	DeletedAt     *time.Time
}

type fkRef struct {
	Schema string
	Table  string
	Column string
}

type mergeOptions struct {
	PreferDuplicateHRData bool
	PreferDuplicateLogin  bool
}

func main() {
	time.Local = time.UTC
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	var (
		userIDsFlag    string
		primaryUserID  int64
		dryRun         bool
		deleteKeycloak bool
		opts           mergeOptions
	)

	flag.StringVar(&userIDsFlag, "user-ids", "", "comma separated user ids to merge")
	flag.Int64Var(&primaryUserID, "primary-user-id", 0, "optional primary user id; if omitted, oldest created account wins")
	flag.BoolVar(&dryRun, "dry-run", false, "print plan only, do not mutate DB or Keycloak")
	flag.BoolVar(&deleteKeycloak, "delete-keycloak", true, "delete duplicate users from Keycloak after successful DB merge")
	flag.BoolVar(&opts.PreferDuplicateHRData, "prefer-duplicate-hr-data", false, "prefer duplicate HR/IIN/profile data when merging into the primary user")
	flag.BoolVar(&opts.PreferDuplicateLogin, "prefer-duplicate-login", false, "replace primary email/username/keycloak_id with duplicate values")
	flag.Parse()

	if strings.TrimSpace(userIDsFlag) == "" {
		fmt.Fprintln(os.Stderr, "user-ids is required")
		os.Exit(2)
	}

	userIDs, err := parseIDs(userIDsFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid user-ids: %v\n", err)
		os.Exit(2)
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}

	logger, err := initLogger(cfg.Logger)
	if err != nil {
		fmt.Fprintf(os.Stderr, "init logger: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = logger.Sync() }()

	db, err := database.NewPostgresPool(ctx, cfg.Database, logger)
	if err != nil {
		logger.Fatal("connect database", zap.Error(err))
	}
	defer db.Close()

	users, err := loadUsers(ctx, db, userIDs)
	if err != nil {
		logger.Fatal("load users", zap.Error(err))
	}

	if len(users) < 2 {
		logger.Fatal("at least two active users are required for merge", zap.Int("loaded_users", len(users)))
	}

	primary, duplicates, err := choosePrimary(users, primaryUserID)
	if err != nil {
		logger.Fatal("choose primary", zap.Error(err))
	}

	allIDs := append([]int64{primary.ID}, duplicateIDs(duplicates)...)
	msgCounts, err := loadMessageCounts(ctx, db, allIDs)
	if err != nil {
		logger.Warn("failed to load message counts for logging", zap.Error(err))
	}

	logger.Info("merge plan",
		zap.Int64("primary_user_id", primary.ID),
		zap.Time("primary_created_at", primary.CreatedAt),
		zap.Int64s("duplicate_user_ids", duplicateIDs(duplicates)),
		zap.Bool("prefer_duplicate_hr_data", opts.PreferDuplicateHRData),
		zap.Bool("prefer_duplicate_login", opts.PreferDuplicateLogin),
		zap.Bool("dry_run", dryRun),
	)

	logger.Info("primary user details",
		zap.Int64("id", primary.ID),
		zap.String("username", primary.Username),
		zap.String("email", primary.Email),
		zap.Time("created_at", primary.CreatedAt),
		zap.Time("last_login_at", coalesceTime(primary.LastLoginAt)),
		zap.Time("last_seen_at", coalesceTime(primary.LastSeenAt)),
		zap.Int64("message_count", msgCounts[primary.ID]),
	)

	for _, dup := range duplicates {
		logger.Info("duplicate user details",
			zap.Int64("id", dup.ID),
			zap.String("username", dup.Username),
			zap.String("email", dup.Email),
			zap.Time("created_at", dup.CreatedAt),
			zap.Time("last_login_at", coalesceTime(dup.LastLoginAt)),
			zap.Time("last_seen_at", coalesceTime(dup.LastSeenAt)),
			zap.Int64("message_count", msgCounts[dup.ID]),
		)
	}

	if dryRun {
		return
	}

	tx, err := db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		logger.Fatal("begin tx", zap.Error(err))
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if err := lockUsers(ctx, tx, append([]int64{primary.ID}, duplicateIDs(duplicates)...)); err != nil {
		logger.Fatal("lock users", zap.Error(err))
	}

	if err := mergeUsers(ctx, tx, &primary, duplicates, opts, logger); err != nil {
		logger.Fatal("merge users", zap.Error(err))
	}

	if err := tx.Commit(ctx); err != nil {
		logger.Fatal("commit merge", zap.Error(err))
	}

	logger.Info("database merge committed",
		zap.Int64("primary_user_id", primary.ID),
		zap.Int64s("duplicate_user_ids", duplicateIDs(duplicates)),
	)

	if deleteKeycloak {
		var kc *keycloak.AdminClient
		if strings.TrimSpace(cfg.Keycloak.AdminClientID) != "" && strings.TrimSpace(cfg.Keycloak.AdminUser) != "" {
			kc = keycloak.NewAdminWithFallback(
				cfg.Keycloak.ServerURL,
				cfg.Keycloak.AdminClientRealm,
				cfg.Keycloak.AdminClientID,
				cfg.Keycloak.AdminClientSecret,
				cfg.Keycloak.AdminUser,
				cfg.Keycloak.AdminPass,
			)
		} else if strings.TrimSpace(cfg.Keycloak.AdminClientID) != "" {
			kc = keycloak.NewAdminWithClientCredentials(
				cfg.Keycloak.ServerURL,
				cfg.Keycloak.AdminClientRealm,
				cfg.Keycloak.AdminClientID,
				cfg.Keycloak.AdminClientSecret,
			)
		} else {
			kc = keycloak.NewAdmin(
				cfg.Keycloak.ServerURL,
				cfg.Keycloak.AdminRealm,
				cfg.Keycloak.AdminUser,
				cfg.Keycloak.AdminPass,
			)
		}
		if err := cleanupKeycloak(ctx, kc, cfg.Keycloak.Realm, primary.ID, duplicateIDs(duplicates), db, logger); err != nil {
			logger.Fatal("cleanup keycloak", zap.Error(err))
		}
	}
}

func initLogger(cfg config.LoggerConfig) (*zap.Logger, error) {
	return config.NewLogger(cfg)
}

func parseIDs(raw string) ([]int64, error) {
	parts := strings.Split(raw, ",")
	out := make([]int64, 0, len(parts))
	seen := make(map[int64]struct{}, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

func loadUsers(ctx context.Context, db *pgxpool.Pool, ids []int64) ([]userRow, error) {
	rows, err := db.Query(ctx, `
		SELECT id, COALESCE(keycloak_id, ''), COALESCE(iin, ''), COALESCE(email, ''), COALESCE(username, ''),
		       COALESCE(first_name, ''), COALESCE(last_name, ''), COALESCE(avatar_url, ''), COALESCE(bio, ''),
		       COALESCE(position, ''), COALESCE(full_name_local, ''), ministry_id, state_body_id, department_id,
		       division_id, prime_minister_id, deputy_prime_minister_id, is_active, is_first_login, created_at,
		       last_login_at, last_seen_at, deleted_at
		FROM users
		WHERE id = ANY($1)
	`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []userRow
	for rows.Next() {
		var u userRow
		if err := rows.Scan(
			&u.ID, &u.KeycloakID, &u.IIN, &u.Email, &u.Username,
			&u.FirstName, &u.LastName, &u.AvatarURL, &u.Bio,
			&u.Position, &u.FullNameLocal, &u.MinistryID, &u.StateBodyID, &u.DepartmentID,
			&u.DivisionID, &u.PrimePMID, &u.DeputyPMID, &u.IsActive, &u.IsFirstLogin, &u.CreatedAt,
			&u.LastLoginAt, &u.LastSeenAt, &u.DeletedAt,
		); err != nil {
			return nil, err
		}
		if u.DeletedAt == nil {
			users = append(users, u)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return users, nil
}

func choosePrimary(users []userRow, preferredID int64) (userRow, []userRow, error) {
	if preferredID != 0 {
		var primary userRow
		var duplicates []userRow
		found := false
		for _, u := range users {
			if u.ID == preferredID {
				primary = u
				found = true
			} else {
				duplicates = append(duplicates, u)
			}
		}
		if !found {
			return userRow{}, nil, fmt.Errorf("preferred primary user %d not found", preferredID)
		}
		return primary, duplicates, nil
	}

	sorted := append([]userRow(nil), users...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].CreatedAt.Equal(sorted[j].CreatedAt) {
			return sorted[i].ID < sorted[j].ID
		}
		return sorted[i].CreatedAt.Before(sorted[j].CreatedAt)
	})
	return sorted[0], sorted[1:], nil
}

func duplicateIDs(users []userRow) []int64 {
	out := make([]int64, 0, len(users))
	for _, u := range users {
		out = append(out, u.ID)
	}
	return out
}

func lockUsers(ctx context.Context, tx pgx.Tx, ids []int64) error {
	_, err := tx.Exec(ctx, `SELECT id FROM users WHERE id = ANY($1) FOR UPDATE`, ids)
	return err
}

func mergeUsers(ctx context.Context, tx pgx.Tx, primary *userRow, duplicates []userRow, opts mergeOptions, logger *zap.Logger) error {
	for _, dup := range duplicates {
		logger.Info("merging duplicate user",
			zap.Int64("primary_user_id", primary.ID),
			zap.Int64("duplicate_user_id", dup.ID),
		)

		// Clear unique constraints on duplicate user in the DB first, to avoid unique constraint violations
		// when updating the primary user with duplicate's values.
		_, err := tx.Exec(ctx, `
			UPDATE users
			SET iin = NULL,
			    keycloak_id = NULL,
			    email = SUBSTRING(email FROM 1 FOR 230) || '.dup-' || id::text,
			    username = SUBSTRING(username FROM 1 FOR 30) || '.dup-' || id::text,
			    updated_at = NOW()
			WHERE id = $1
		`, dup.ID)
		if err != nil {
			return fmt.Errorf("clear duplicate unique fields for duplicate %d: %w", dup.ID, err)
		}

		if err := mergePrimaryUserFields(ctx, tx, primary, dup, opts); err != nil {
			return fmt.Errorf("merge primary fields for duplicate %d: %w", dup.ID, err)
		}
		if err := mergeHRProfile(ctx, tx, primary.ID, dup.ID, opts); err != nil {
			return err
		}
		if err := mergeChatTheme(ctx, tx, primary.ID, dup.ID); err != nil {
			return err
		}
		if err := mergeGamification(ctx, tx, primary.ID, dup.ID); err != nil {
			return err
		}
		if err := mergeUnifiedAIChat(ctx, tx, primary.ID, dup.ID); err != nil {
			return err
		}
		if err := mergeKanbanColumns(ctx, tx, primary.ID, dup.ID); err != nil {
			return err
		}
		if err := moveDistinctConversationParticipants(ctx, tx, primary.ID, dup.ID); err != nil {
			return err
		}
		if err := moveDistinctMessageStatus(ctx, tx, primary.ID, dup.ID); err != nil {
			return err
		}
		if err := moveDistinctMessageMentions(ctx, tx, primary.ID, dup.ID); err != nil {
			return err
		}
		if err := moveDistinctMessageReactions(ctx, tx, primary.ID, dup.ID); err != nil {
			return err
		}
		if err := moveDistinctDeletedMessages(ctx, tx, primary.ID, dup.ID); err != nil {
			return err
		}
		if err := moveDistinctPermissions(ctx, tx, primary.ID, dup.ID); err != nil {
			return err
		}
		if err := moveDistinctUserDevices(ctx, tx, primary.ID, dup.ID); err != nil {
			return err
		}
		if err := moveDistinctQuickPin(ctx, tx, primary.ID, dup.ID); err != nil {
			return err
		}
		if err := moveDistinctNoteShares(ctx, tx, primary.ID, dup.ID); err != nil {
			return err
		}
		if err := moveDistinctPublicAppAccess(ctx, tx, primary.ID, dup.ID); err != nil {
			return err
		}
		if err := moveDistinctPublicAppFavorites(ctx, tx, primary.ID, dup.ID); err != nil {
			return err
		}
		if err := moveDistinctPublicAppRatings(ctx, tx, primary.ID, dup.ID); err != nil {
			return err
		}
		if err := moveDistinctEventAttendees(ctx, tx, primary.ID, dup.ID); err != nil {
			return err
		}
		if err := moveDistinctStateBodyKanbanAccess(ctx, tx, primary.ID, dup.ID); err != nil {
			return err
		}
		if err := moveDistinctStateBodyKanbanStatsAccess(ctx, tx, primary.ID, dup.ID); err != nil {
			return err
		}
		if err := moveDistinctUserReviewsAsReviewer(ctx, tx, primary.ID, dup.ID); err != nil {
			return err
		}
		if err := mergeUserKeyBackups(ctx, tx, primary.ID, dup.ID, opts, logger); err != nil {
			return err
		}
		if err := mergeUserPublicKeys(ctx, tx, primary.ID, dup.ID, logger); err != nil {
			return err
		}
		if err := mergeGroupMembers(ctx, tx, primary.ID, dup.ID); err != nil {
			return err
		}
		if err := mergeChannelMembers(ctx, tx, primary.ID, dup.ID); err != nil {
			return err
		}
		if err := mergeChannelPostViews(ctx, tx, primary.ID, dup.ID); err != nil {
			return err
		}
		if err := mergeChannelPostReactions(ctx, tx, primary.ID, dup.ID); err != nil {
			return err
		}
		if err := mergePublicAppUserPositions(ctx, tx, primary.ID, dup.ID); err != nil {
			return err
		}
		if err := mergeUserMailboxes(ctx, tx, primary.ID, dup.ID); err != nil {
			return err
		}
		if err := mergeAppleCalendarEventMappings(ctx, tx, primary.ID, dup.ID); err != nil {
			return err
		}
		if err := mergeGoogleCalendarEventMappings(ctx, tx, primary.ID, dup.ID); err != nil {
			return err
		}
		if err := mergeBoardMembers(ctx, tx, primary.ID, dup.ID); err != nil {
			return err
		}
		if err := mergeTaskAssignees(ctx, tx, primary.ID, dup.ID); err != nil {
			return err
		}
		if err := mergeCallParticipants(ctx, tx, primary.ID, dup.ID); err != nil {
			return err
		}
		if err := mergeUserRecentItems(ctx, tx, primary.ID, dup.ID); err != nil {
			return err
		}
		if err := updateTaskAttachmentsManual(ctx, tx, primary.ID, dup.ID); err != nil {
			return err
		}
		if err := updateBoardsManual(ctx, tx, primary.ID, dup.ID); err != nil {
			return err
		}

		if err := applyGenericFKUpdates(ctx, tx, primary.ID, dup.ID); err != nil {
			return err
		}

		if _, err := tx.Exec(ctx, `
			UPDATE users
			SET deleted_at = NOW(),
			    updated_at = NOW(),
			    is_active = FALSE,
			    merged_into_id = $1
			WHERE id = $2 AND deleted_at IS NULL
		`, primary.ID, dup.ID); err != nil {
			return fmt.Errorf("soft delete duplicate user %d: %w", dup.ID, err)
		}
	}
	return nil
}

func mergePrimaryUserFields(ctx context.Context, tx pgx.Tx, primary *userRow, dup userRow, opts mergeOptions) error {
	if primary.LastLoginAt == nil || (dup.LastLoginAt != nil && primary.LastLoginAt.Before(*dup.LastLoginAt)) {
		primary.LastLoginAt = dup.LastLoginAt
	}
	if primary.LastSeenAt == nil || (dup.LastSeenAt != nil && primary.LastSeenAt.Before(*dup.LastSeenAt)) {
		primary.LastSeenAt = dup.LastSeenAt
	}
	primary.IsActive = primary.IsActive || dup.IsActive
	primary.IsFirstLogin = primary.IsFirstLogin && dup.IsFirstLogin

	_, err := tx.Exec(ctx, `
		UPDATE users p
		SET keycloak_id = CASE
		        WHEN $16 AND NULLIF($2, '') IS NOT NULL THEN $2
		        ELSE COALESCE(NULLIF(p.keycloak_id, ''), NULLIF($2, ''))
		    END,
		    iin = CASE
		        WHEN $17 AND NULLIF($3, '') IS NOT NULL THEN $3
		        ELSE COALESCE(NULLIF(p.iin, ''), NULLIF($3, ''))
		    END,
		    email = CASE
		        WHEN $16 AND NULLIF($4, '') IS NOT NULL THEN $4
		        ELSE COALESCE(NULLIF(p.email, ''), NULLIF($4, ''))
		    END,
		    username = CASE
		        WHEN $16 AND NULLIF($5, '') IS NOT NULL THEN $5
		        ELSE COALESCE(NULLIF(p.username, ''), NULLIF($5, ''))
		    END,
		    first_name = COALESCE(NULLIF(p.first_name, ''), NULLIF($6, '')),
		    last_name = COALESCE(NULLIF(p.last_name, ''), NULLIF($7, '')),
		    avatar_url = COALESCE(NULLIF(p.avatar_url, ''), NULLIF($8, '')),
		    bio = COALESCE(NULLIF(p.bio, ''), NULLIF($9, '')),
		    position = COALESCE(NULLIF(p.position, ''), NULLIF($10, '')),
		    full_name_local = COALESCE(NULLIF(p.full_name_local, ''), NULLIF($11, '')),
		    ministry_id = COALESCE(p.ministry_id, $18),
		    state_body_id = COALESCE(p.state_body_id, $19),
		    department_id = COALESCE(p.department_id, $20),
		    division_id = COALESCE(p.division_id, $21),
		    prime_minister_id = COALESCE(p.prime_minister_id, $22),
		    deputy_prime_minister_id = COALESCE(p.deputy_prime_minister_id, $23),
		    is_active = $12,
		    is_first_login = $13,
		    last_login_at = $14::timestamptz,
		    last_seen_at = $15::timestamptz,
		    updated_at = NOW()
		WHERE p.id = $1
	`, primary.ID,
		dup.KeycloakID,
		dup.IIN,
		dup.Email,
		dup.Username,
		dup.FirstName,
		dup.LastName,
		dup.AvatarURL,
		dup.Bio,
		dup.Position,
		dup.FullNameLocal,
		primary.IsActive,
		primary.IsFirstLogin,
		primary.LastLoginAt,
		primary.LastSeenAt,
		opts.PreferDuplicateLogin,
		opts.PreferDuplicateHRData,
		dup.MinistryID,
		dup.StateBodyID,
		dup.DepartmentID,
		dup.DivisionID,
		dup.PrimePMID,
		dup.DeputyPMID,
	)
	return err
}

func mergeHRProfile(ctx context.Context, tx pgx.Tx, primaryID, dupID int64, opts mergeOptions) error {
	var primaryExists, dupExists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM user_hr_profiles WHERE user_id = $1)`, primaryID).Scan(&primaryExists); err != nil {
		return fmt.Errorf("check primary hr profile: %w", err)
	}
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM user_hr_profiles WHERE user_id = $1)`, dupID).Scan(&dupExists); err != nil {
		return fmt.Errorf("check duplicate hr profile: %w", err)
	}
	if !dupExists {
		return nil
	}
	if !primaryExists {
		_, err := tx.Exec(ctx, `UPDATE user_hr_profiles SET user_id = $1, updated_at = NOW() WHERE user_id = $2`, primaryID, dupID)
		return err
	}

	_, err := tx.Exec(ctx, `
		UPDATE user_hr_profiles p
		SET person_code = CASE
		        WHEN $3 AND NULLIF(d.person_code, '') IS NOT NULL THEN d.person_code
		        ELSE COALESCE(NULLIF(p.person_code, ''), NULLIF(d.person_code, ''))
		    END,
		    badge_code = COALESCE(NULLIF(p.badge_code, ''), NULLIF(d.badge_code, '')),
		    iin = CASE
		        WHEN $3 AND NULLIF(d.iin, '') IS NOT NULL THEN d.iin
		        ELSE COALESCE(NULLIF(p.iin, ''), NULLIF(d.iin, ''))
		    END,
		    last_name = COALESCE(NULLIF(p.last_name, ''), NULLIF(d.last_name, '')),
		    first_name = COALESCE(NULLIF(p.first_name, ''), NULLIF(d.first_name, '')),
		    middle_name = COALESCE(NULLIF(p.middle_name, ''), NULLIF(d.middle_name, '')),
		    photo = CASE
		        WHEN $3 AND NULLIF(d.photo, '') IS NOT NULL THEN d.photo
		        ELSE COALESCE(NULLIF(p.photo, ''), NULLIF(d.photo, ''))
		    END,
		    doc_type = CASE
		        WHEN $3 AND NULLIF(d.doc_type, '') IS NOT NULL THEN d.doc_type
		        ELSE COALESCE(NULLIF(p.doc_type, ''), NULLIF(d.doc_type, ''))
		    END,
		    person_status = CASE
		        WHEN $3 AND NULLIF(d.person_status, '') IS NOT NULL THEN d.person_status
		        ELSE COALESCE(NULLIF(p.person_status, ''), NULLIF(d.person_status, ''))
		    END,
		    last_order_number = CASE
		        WHEN $3 AND NULLIF(d.last_order_number, '') IS NOT NULL THEN d.last_order_number
		        ELSE COALESCE(NULLIF(p.last_order_number, ''), NULLIF(d.last_order_number, ''))
		    END,
		    last_order_date = CASE
		        WHEN $3 AND d.last_order_date IS NOT NULL THEN d.last_order_date
		        ELSE COALESCE(p.last_order_date, d.last_order_date)
		    END,
		    last_begin_date = CASE
		        WHEN $3 AND d.last_begin_date IS NOT NULL THEN d.last_begin_date
		        ELSE COALESCE(p.last_begin_date, d.last_begin_date)
		    END,
		    first_begin_date = CASE
		        WHEN $3 AND d.first_begin_date IS NOT NULL THEN d.first_begin_date
		        ELSE COALESCE(p.first_begin_date, d.first_begin_date)
		    END,
		    absence_status = COALESCE(NULLIF(p.absence_status, ''), NULLIF(d.absence_status, '')),
		    absence_order_number = COALESCE(NULLIF(p.absence_order_number, ''), NULLIF(d.absence_order_number, '')),
		    absence_order_date = COALESCE(p.absence_order_date, d.absence_order_date),
		    begin_absence_date = COALESCE(p.begin_absence_date, d.begin_absence_date),
		    end_absence_date = COALESCE(p.end_absence_date, d.end_absence_date),
		    card_type = CASE
		        WHEN $3 AND NULLIF(d.card_type, '') IS NOT NULL THEN d.card_type
		        ELSE COALESCE(NULLIF(p.card_type, ''), NULLIF(d.card_type, ''))
		    END,
		    bin = CASE
		        WHEN $3 AND NULLIF(d.bin, '') IS NOT NULL THEN d.bin
		        ELSE COALESCE(NULLIF(p.bin, ''), NULLIF(d.bin, ''))
		    END,
		    name_go = CASE
		        WHEN $3 AND NULLIF(d.name_go, '') IS NOT NULL THEN d.name_go
		        ELSE COALESCE(NULLIF(p.name_go, ''), NULLIF(d.name_go, ''))
		    END,
		    job_name = CASE
		        WHEN $3 AND NULLIF(d.job_name, '') IS NOT NULL THEN d.job_name
		        ELSE COALESCE(NULLIF(p.job_name, ''), NULLIF(d.job_name, ''))
		    END,
		    job_uid = CASE
		        WHEN $3 AND NULLIF(d.job_uid, '') IS NOT NULL THEN d.job_uid
		        ELSE COALESCE(NULLIF(p.job_uid, ''), NULLIF(d.job_uid, ''))
		    END,
		    sub_name = CASE
		        WHEN $3 AND NULLIF(d.sub_name, '') IS NOT NULL THEN d.sub_name
		        ELSE COALESCE(NULLIF(p.sub_name, ''), NULLIF(d.sub_name, ''))
		    END,
		    sub_uid = CASE
		        WHEN $3 AND NULLIF(d.sub_uid, '') IS NOT NULL THEN d.sub_uid
		        ELSE COALESCE(NULLIF(p.sub_uid, ''), NULLIF(d.sub_uid, ''))
		    END,
		    raw_payload = CASE
		        WHEN $3 AND d.raw_payload IS NOT NULL AND d.raw_payload <> '{}'::jsonb THEN d.raw_payload
		        WHEN p.raw_payload IS NULL OR p.raw_payload = '{}'::jsonb THEN d.raw_payload
		        ELSE p.raw_payload
		    END,
		    updated_at = NOW()
		FROM user_hr_profiles d
		WHERE p.user_id = $1 AND d.user_id = $2
	`, primaryID, dupID, opts.PreferDuplicateHRData)
	if err != nil {
		return fmt.Errorf("merge hr profile values: %w", err)
	}
	_, err = tx.Exec(ctx, `DELETE FROM user_hr_profiles WHERE user_id = $1`, dupID)
	return err
}

func mergeChatTheme(ctx context.Context, tx pgx.Tx, primaryID, dupID int64) error {
	var primaryExists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM chat_themes WHERE user_id = $1)`, primaryID).Scan(&primaryExists); err != nil {
		return err
	}
	if !primaryExists {
		_, err := tx.Exec(ctx, `UPDATE chat_themes SET user_id = $1, updated_at = NOW() WHERE user_id = $2`, primaryID, dupID)
		return err
	}
	_, err := tx.Exec(ctx, `DELETE FROM chat_themes WHERE user_id = $1`, dupID)
	return err
}

func mergeGamification(ctx context.Context, tx pgx.Tx, primaryID, dupID int64) error {
	var primaryExists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM user_gamification_scores WHERE user_id = $1)`, primaryID).Scan(&primaryExists); err != nil {
		return err
	}
	if !primaryExists {
		if _, err := tx.Exec(ctx, `UPDATE user_gamification_scores SET user_id = $1 WHERE user_id = $2`, primaryID, dupID); err != nil {
			return err
		}
	} else {
		if _, err := tx.Exec(ctx, `
			UPDATE user_gamification_scores p
			SET total_score = p.total_score + d.total_score,
			    level = GREATEST(p.level, d.level),
			    updated_at = NOW()
			FROM user_gamification_scores d
			WHERE p.user_id = $1 AND d.user_id = $2
		`, primaryID, dupID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM user_gamification_scores WHERE user_id = $1`, dupID); err != nil {
			return err
		}
	}
	_, err := tx.Exec(ctx, `UPDATE user_gamification_activities SET user_id = $1 WHERE user_id = $2`, primaryID, dupID)
	return err
}

func mergeUnifiedAIChat(ctx context.Context, tx pgx.Tx, primaryID, dupID int64) error {
	var primaryChatID string
	var dupChatID string
	primaryHasChat := true
	dupHasChat := true
	if err := tx.QueryRow(ctx, `SELECT id::text FROM unified_ai_chats WHERE user_id = $1`, primaryID).Scan(&primaryChatID); err != nil {
		if err == pgx.ErrNoRows {
			primaryHasChat = false
		} else {
			return err
		}
	}
	if err := tx.QueryRow(ctx, `SELECT id::text FROM unified_ai_chats WHERE user_id = $1`, dupID).Scan(&dupChatID); err != nil {
		if err == pgx.ErrNoRows {
			dupHasChat = false
		} else {
			return err
		}
	}
	if !dupHasChat {
		return nil
	}
	if !primaryHasChat {
		_, err := tx.Exec(ctx, `UPDATE unified_ai_chats SET user_id = $1, updated_at = NOW() WHERE user_id = $2`, primaryID, dupID)
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE unified_ai_messages SET chat_id = $1 WHERE chat_id = $2`, primaryChatID, dupChatID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `DELETE FROM unified_ai_chats WHERE user_id = $1`, dupID)
	return err
}

func mergeKanbanColumns(ctx context.Context, tx pgx.Tx, primaryID, dupID int64) error {
	type col struct {
		ID  string
		Key string
	}
	rows, err := tx.Query(ctx, `SELECT id::text, key FROM kanban_columns WHERE user_id = $1`, primaryID)
	if err != nil {
		return err
	}
	primaryCols := make(map[string]string)
	for rows.Next() {
		var c col
		if err := rows.Scan(&c.ID, &c.Key); err != nil {
			rows.Close()
			return err
		}
		primaryCols[c.Key] = c.ID
	}
	rows.Close()

	dupRows, err := tx.Query(ctx, `SELECT id::text, key FROM kanban_columns WHERE user_id = $1`, dupID)
	if err != nil {
		return err
	}
	var dupCols []col
	for dupRows.Next() {
		var c col
		if err := dupRows.Scan(&c.ID, &c.Key); err != nil {
			dupRows.Close()
			return err
		}
		dupCols = append(dupCols, c)
	}
	dupRows.Close()

	for _, c := range dupCols {
		if primaryColID, ok := primaryCols[c.Key]; ok {
			if _, err := tx.Exec(ctx, `UPDATE tasks SET column_id = $1 WHERE column_id = $2`, primaryColID, c.ID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `DELETE FROM kanban_columns WHERE id = $1`, c.ID); err != nil {
				return err
			}
			continue
		}
		if _, err := tx.Exec(ctx, `UPDATE kanban_columns SET user_id = $1, updated_at = NOW() WHERE id = $2`, primaryID, c.ID); err != nil {
			return err
		}
	}
	return nil
}

func moveDistinctConversationParticipants(ctx context.Context, tx pgx.Tx, primaryID, dupID int64) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO conversation_participants (
			conversation_id, user_id, joined_at, left_at, last_read_at, is_muted, is_blocked, created_at
		)
		SELECT conversation_id, $1, joined_at, left_at, last_read_at, is_muted, is_blocked, created_at
		FROM conversation_participants
		WHERE user_id = $2
		ON CONFLICT (conversation_id, user_id) DO NOTHING
	`, primaryID, dupID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `DELETE FROM conversation_participants WHERE user_id = $1`, dupID)
	return err
}

func moveDistinctMessageStatus(ctx context.Context, tx pgx.Tx, primaryID, dupID int64) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO message_status (id, message_id, user_id, delivered_at, read_at, created_at)
		SELECT gen_random_uuid(), message_id, $1, delivered_at, read_at, created_at
		FROM message_status
		WHERE user_id = $2
		ON CONFLICT (message_id, user_id) DO UPDATE
		SET delivered_at = COALESCE(message_status.delivered_at, EXCLUDED.delivered_at),
		    read_at = COALESCE(message_status.read_at, EXCLUDED.read_at)
	`, primaryID, dupID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `DELETE FROM message_status WHERE user_id = $1`, dupID)
	return err
}

func moveDistinctMessageMentions(ctx context.Context, tx pgx.Tx, primaryID, dupID int64) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO message_mentions (message_id, user_id)
		SELECT message_id, $1 FROM message_mentions WHERE user_id = $2
		ON CONFLICT (message_id, user_id) DO NOTHING
	`, primaryID, dupID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `DELETE FROM message_mentions WHERE user_id = $1`, dupID)
	return err
}

func moveDistinctMessageReactions(ctx context.Context, tx pgx.Tx, primaryID, dupID int64) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO message_reactions (id, message_id, user_id, reaction, created_at)
		SELECT gen_random_uuid(), message_id, $1, reaction, created_at
		FROM message_reactions
		WHERE user_id = $2
		ON CONFLICT (message_id, user_id, reaction) DO NOTHING
	`, primaryID, dupID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `DELETE FROM message_reactions WHERE user_id = $1`, dupID)
	return err
}

func moveDistinctDeletedMessages(ctx context.Context, tx pgx.Tx, primaryID, dupID int64) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO deleted_messages (user_id, message_id, deleted_at)
		SELECT $1, message_id, deleted_at
		FROM deleted_messages
		WHERE user_id = $2
		ON CONFLICT (user_id, message_id) DO NOTHING
	`, primaryID, dupID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `DELETE FROM deleted_messages WHERE user_id = $1`, dupID)
	return err
}

func moveDistinctPermissions(ctx context.Context, tx pgx.Tx, primaryID, dupID int64) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO permissions (resource_type, resource_id, user_id, role, created_at)
		SELECT resource_type, resource_id, $1, role, created_at
		FROM permissions
		WHERE user_id = $2
		ON CONFLICT (resource_type, resource_id, user_id) DO NOTHING
	`, primaryID, dupID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `DELETE FROM permissions WHERE user_id = $1`, dupID)
	return err
}

func moveDistinctUserDevices(ctx context.Context, tx pgx.Tx, primaryID, dupID int64) error {
	// 1. Update last_used_at on primary's active devices to be the greatest of primary's and duplicate's active devices for matching device_ids
	_, err := tx.Exec(ctx, `
		UPDATE user_devices pri
		SET last_used_at = GREATEST(pri.last_used_at, dup.last_used_at),
		    updated_at = NOW()
		FROM user_devices dup
		WHERE pri.user_id = $1
		  AND pri.active = TRUE
		  AND pri.device_id IS NOT NULL AND TRIM(pri.device_id) <> ''
		  AND dup.user_id = $2
		  AND dup.active = TRUE
		  AND dup.device_id = pri.device_id
	`, primaryID, dupID)
	if err != nil {
		return fmt.Errorf("update primary user devices last_used_at: %w", err)
	}

	// 2. Deactivate duplicate's active devices that conflict with primary's active devices on device_id
	_, err = tx.Exec(ctx, `
		UPDATE user_devices dup
		SET active = FALSE,
		    logged_out_at = COALESCE(dup.logged_out_at, NOW()),
		    updated_at = NOW()
		FROM user_devices pri
		WHERE dup.user_id = $2
		  AND dup.active = TRUE
		  AND dup.device_id IS NOT NULL AND TRIM(dup.device_id) <> ''
		  AND pri.user_id = $1
		  AND pri.active = TRUE
		  AND pri.device_id = dup.device_id
	`, primaryID, dupID)
	if err != nil {
		return fmt.Errorf("deactivate conflicting duplicate user devices: %w", err)
	}

	// 3. Reassign duplicate's devices to primary user
	_, err = tx.Exec(ctx, `
		UPDATE user_devices
		SET user_id = $1,
		    updated_at = NOW()
		WHERE user_id = $2
	`, primaryID, dupID)
	if err != nil {
		return fmt.Errorf("reassign duplicate user devices: %w", err)
	}

	return nil
}

func moveDistinctQuickPin(ctx context.Context, tx pgx.Tx, primaryID, dupID int64) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO quick_pin_credential (
			user_id, device_id, pin_hash, encrypted_session_secret, expires_at, active, created_at, updated_at
		)
		SELECT $1, device_id, pin_hash, encrypted_session_secret, expires_at, active, created_at, updated_at
		FROM quick_pin_credential
		WHERE user_id = $2
		ON CONFLICT (user_id, device_id) DO UPDATE
		SET pin_hash = EXCLUDED.pin_hash,
		    encrypted_session_secret = EXCLUDED.encrypted_session_secret,
		    expires_at = GREATEST(quick_pin_credential.expires_at, EXCLUDED.expires_at),
		    active = quick_pin_credential.active OR EXCLUDED.active,
		    updated_at = GREATEST(quick_pin_credential.updated_at, EXCLUDED.updated_at)
	`, primaryID, dupID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `DELETE FROM quick_pin_credential WHERE user_id = $1`, dupID)
	return err
}

func moveDistinctNoteShares(ctx context.Context, tx pgx.Tx, primaryID, dupID int64) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO note_shares (note_id, user_id, granted_by, created_at)
		SELECT note_id, $1, granted_by, created_at
		FROM note_shares
		WHERE user_id = $2
		ON CONFLICT (note_id, user_id) DO NOTHING
	`, primaryID, dupID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE note_shares SET granted_by = $1 WHERE granted_by = $2`, primaryID, dupID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `DELETE FROM note_shares WHERE user_id = $1`, dupID)
	return err
}

func moveDistinctPublicAppAccess(ctx context.Context, tx pgx.Tx, primaryID, dupID int64) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO public_app_access (app_id, user_id, created_at)
		SELECT app_id, $1, created_at
		FROM public_app_access
		WHERE user_id = $2
		ON CONFLICT (app_id, user_id) DO NOTHING
	`, primaryID, dupID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `DELETE FROM public_app_access WHERE user_id = $1`, dupID)
	return err
}

func moveDistinctPublicAppFavorites(ctx context.Context, tx pgx.Tx, primaryID, dupID int64) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO public_app_favorites (user_id, app_id, created_at)
		SELECT $1, app_id, created_at
		FROM public_app_favorites
		WHERE user_id = $2
		ON CONFLICT (user_id, app_id) DO NOTHING
	`, primaryID, dupID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `DELETE FROM public_app_favorites WHERE user_id = $1`, dupID)
	return err
}

func moveDistinctPublicAppRatings(ctx context.Context, tx pgx.Tx, primaryID, dupID int64) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO public_app_ratings (app_id, user_id, rating, created_at, updated_at)
		SELECT app_id, $1, rating, created_at, updated_at
		FROM public_app_ratings
		WHERE user_id = $2
		ON CONFLICT (app_id, user_id) DO UPDATE
		SET rating = EXCLUDED.rating,
		    updated_at = GREATEST(public_app_ratings.updated_at, EXCLUDED.updated_at)
	`, primaryID, dupID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `DELETE FROM public_app_ratings WHERE user_id = $1`, dupID)
	return err
}

func moveDistinctEventAttendees(ctx context.Context, tx pgx.Tx, primaryID, dupID int64) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO event_attendees (event_id, user_id, status, created_at)
		SELECT event_id, $1, status, created_at
		FROM event_attendees
		WHERE user_id = $2
		ON CONFLICT (event_id, user_id) DO NOTHING
	`, primaryID, dupID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `DELETE FROM event_attendees WHERE user_id = $1`, dupID)
	return err
}

func moveDistinctStateBodyKanbanAccess(ctx context.Context, tx pgx.Tx, primaryID, dupID int64) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO state_body_kanban_access (state_body_id, user_id, granted_by, created_at)
		SELECT state_body_id, $1, granted_by, created_at
		FROM state_body_kanban_access
		WHERE user_id = $2
		ON CONFLICT (state_body_id, user_id) DO NOTHING
	`, primaryID, dupID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE state_body_kanban_access SET granted_by = $1 WHERE granted_by = $2`, primaryID, dupID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `DELETE FROM state_body_kanban_access WHERE user_id = $1`, dupID)
	return err
}

func moveDistinctStateBodyKanbanStatsAccess(ctx context.Context, tx pgx.Tx, primaryID, dupID int64) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO state_body_kanban_stats_access (state_body_id, user_id, granted_by, created_at)
		SELECT state_body_id, $1, granted_by, created_at
		FROM state_body_kanban_stats_access
		WHERE user_id = $2
		ON CONFLICT (state_body_id, user_id) DO NOTHING
	`, primaryID, dupID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE state_body_kanban_stats_access SET granted_by = $1 WHERE granted_by = $2`, primaryID, dupID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `DELETE FROM state_body_kanban_stats_access WHERE user_id = $1`, dupID)
	return err
}

func moveDistinctUserReviewsAsReviewer(ctx context.Context, tx pgx.Tx, primaryID, dupID int64) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO user_reviews (
			id, reviewer_id, target_user_id, data_fluency, project_management,
			digital_proficiency, crisis_communication, service_design_thinking, created_at
		)
		SELECT id, $1, target_user_id, data_fluency, project_management,
		       digital_proficiency, crisis_communication, service_design_thinking, created_at
		FROM user_reviews
		WHERE reviewer_id = $2
		ON CONFLICT DO NOTHING
	`, primaryID, dupID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO user_reviews (
			id, reviewer_id, target_user_id, data_fluency, project_management,
			digital_proficiency, crisis_communication, service_design_thinking, created_at
		)
		SELECT id, reviewer_id, $1, data_fluency, project_management,
		       digital_proficiency, crisis_communication, service_design_thinking, created_at
		FROM user_reviews
		WHERE target_user_id = $2
		ON CONFLICT DO NOTHING
	`, primaryID, dupID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM user_reviews WHERE reviewer_id = $1`, dupID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `DELETE FROM user_reviews WHERE target_user_id = $1`, dupID)
	return err
}

func applyGenericFKUpdates(ctx context.Context, tx pgx.Tx, primaryID, dupID int64) error {
	refs, err := loadGenericFKRefs(ctx, tx)
	if err != nil {
		return err
	}
	for _, ref := range refs {
		query := fmt.Sprintf(
			`UPDATE %s SET %s = $1 WHERE %s = $2`,
			quoteQualified(ref.Schema, ref.Table),
			quoteIdent(ref.Column),
			quoteIdent(ref.Column),
		)
		if _, err := tx.Exec(ctx, query, primaryID, dupID); err != nil {
			return fmt.Errorf("generic fk update %s.%s.%s: %w", ref.Schema, ref.Table, ref.Column, err)
		}
	}
	return nil
}

func loadGenericFKRefs(ctx context.Context, tx pgx.Tx) ([]fkRef, error) {
	rows, err := tx.Query(ctx, `
		SELECT ns.nspname, cls.relname, attr.attname
		FROM pg_constraint con
		JOIN pg_class cls ON cls.oid = con.conrelid
		JOIN pg_namespace ns ON ns.oid = cls.relnamespace
		JOIN pg_attribute attr ON attr.attrelid = con.conrelid AND attr.attnum = con.conkey[1]
		WHERE con.contype = 'f'
		  AND con.confrelid = 'public.users'::regclass
		  AND array_length(con.conkey, 1) = 1
		  AND NOT (
		    (ns.nspname = 'public' AND cls.relname = 'users') OR
		    (ns.nspname = 'public' AND cls.relname = 'user_hr_profiles') OR
		    (ns.nspname = 'public' AND cls.relname = 'chat_themes') OR
		    (ns.nspname = 'public' AND cls.relname = 'user_key_backups') OR
		    (ns.nspname = 'public' AND cls.relname = 'user_public_keys') OR
		    (ns.nspname = 'public' AND cls.relname = 'unified_ai_chats') OR
		    (ns.nspname = 'public' AND cls.relname = 'user_gamification_scores') OR
		    (ns.nspname = 'public' AND cls.relname = 'conversation_participants') OR
		    (ns.nspname = 'public' AND cls.relname = 'message_status') OR
		    (ns.nspname = 'public' AND cls.relname = 'message_mentions') OR
		    (ns.nspname = 'public' AND cls.relname = 'message_reactions') OR
		    (ns.nspname = 'public' AND cls.relname = 'deleted_messages') OR
		    (ns.nspname = 'public' AND cls.relname = 'permissions') OR
		    (ns.nspname = 'public' AND cls.relname = 'user_devices') OR
		    (ns.nspname = 'public' AND cls.relname = 'quick_pin_credential') OR
		    (ns.nspname = 'public' AND cls.relname = 'note_shares') OR
		    (ns.nspname = 'public' AND cls.relname = 'public_app_access') OR
		    (ns.nspname = 'public' AND cls.relname = 'public_app_favorites') OR
		    (ns.nspname = 'public' AND cls.relname = 'public_app_ratings') OR
		    (ns.nspname = 'public' AND cls.relname = 'event_attendees') OR
		    (ns.nspname = 'public' AND cls.relname = 'state_body_kanban_access') OR
		    (ns.nspname = 'public' AND cls.relname = 'state_body_kanban_stats_access') OR
		    (ns.nspname = 'public' AND cls.relname = 'user_reviews') OR
		    (ns.nspname = 'public' AND cls.relname = 'kanban_columns') OR
		    (ns.nspname = 'public' AND cls.relname = 'group_members') OR
		    (ns.nspname = 'public' AND cls.relname = 'channel_members') OR
		    (ns.nspname = 'public' AND cls.relname = 'channel_post_views') OR
		    (ns.nspname = 'public' AND cls.relname = 'channel_post_reactions') OR
		    (ns.nspname = 'public' AND cls.relname = 'public_app_user_positions') OR
		    (ns.nspname = 'public' AND cls.relname = 'user_mailboxes') OR
		    (ns.nspname = 'public' AND cls.relname = 'apple_calendar_event_mappings') OR
		    (ns.nspname = 'public' AND cls.relname = 'google_calendar_event_mappings')
		  )
		ORDER BY ns.nspname, cls.relname, attr.attname
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var refs []fkRef
	for rows.Next() {
		var ref fkRef
		if err := rows.Scan(&ref.Schema, &ref.Table, &ref.Column); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}

func cleanupKeycloak(ctx context.Context, kc *keycloak.AdminClient, realm string, primaryID int64, dupIDs []int64, db *pgxpool.Pool, logger *zap.Logger) error {
	var primaryKeycloakID string
	if err := db.QueryRow(ctx, `SELECT COALESCE(keycloak_id, '') FROM users WHERE id = $1`, primaryID).Scan(&primaryKeycloakID); err != nil {
		return err
	}

	rows, err := db.Query(ctx, `SELECT id, COALESCE(keycloak_id, '') FROM users WHERE id = ANY($1)`, dupIDs)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var userID int64
		var kcID string
		if err := rows.Scan(&userID, &kcID); err != nil {
			return err
		}
		if kcID == "" || kcID == primaryKeycloakID {
			continue
		}
		if err := kc.LogoutUser(ctx, realm, kcID); err != nil {
			logger.Warn("keycloak logout before delete failed",
				zap.Int64("user_id", userID),
				zap.String("keycloak_id", kcID),
				zap.Error(err),
			)
		}
		if err := kc.DeleteUser(ctx, realm, kcID); err != nil {
			return fmt.Errorf("delete keycloak user %s for duplicate user %d: %w", kcID, userID, err)
		}
		logger.Info("keycloak duplicate deleted",
			zap.Int64("user_id", userID),
			zap.String("keycloak_id", kcID),
		)
	}
	return rows.Err()
}

func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

func quoteQualified(schema, table string) string {
	return quoteIdent(schema) + "." + quoteIdent(table)
}

func loadMessageCounts(ctx context.Context, db *pgxpool.Pool, ids []int64) (map[int64]int64, error) {
	rows, err := db.Query(ctx, `
		SELECT sender_id, COUNT(*) 
		FROM messages 
		WHERE sender_id = ANY($1) 
		GROUP BY sender_id
	`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := make(map[int64]int64)
	for rows.Next() {
		var senderID, count int64
		if err := rows.Scan(&senderID, &count); err != nil {
			return nil, err
		}
		counts[senderID] = count
	}
	return counts, rows.Err()
}

func coalesceTime(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

func mergeUserKeyBackups(ctx context.Context, tx pgx.Tx, primaryID, dupID int64, opts mergeOptions, logger *zap.Logger) error {
	var primaryExists, dupExists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM user_key_backups WHERE user_id = $1)`, primaryID).Scan(&primaryExists); err != nil {
		return fmt.Errorf("check primary key backup: %w", err)
	}
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM user_key_backups WHERE user_id = $1)`, dupID).Scan(&dupExists); err != nil {
		return fmt.Errorf("check duplicate key backup: %w", err)
	}

	if !dupExists {
		return nil
	}

	if !primaryExists {
		_, err := tx.Exec(ctx, `UPDATE user_key_backups SET user_id = $1, updated_at = NOW() WHERE user_id = $2`, primaryID, dupID)
		if err != nil {
			return fmt.Errorf("move key backup from %d to %d: %w", dupID, primaryID, err)
		}
		logger.Info("moved key backup from duplicate to primary", zap.Int64("from_user_id", dupID), zap.Int64("to_user_id", primaryID))
		return nil
	}

	if opts.PreferDuplicateLogin {
		_, err := tx.Exec(ctx, `DELETE FROM user_key_backups WHERE user_id = $1`, primaryID)
		if err != nil {
			return fmt.Errorf("delete primary key backup: %w", err)
		}
		_, err = tx.Exec(ctx, `UPDATE user_key_backups SET user_id = $1, updated_at = NOW() WHERE user_id = $2`, primaryID, dupID)
		if err != nil {
			return fmt.Errorf("move duplicate key backup: %w", err)
		}
		logger.Info("replaced primary key backup with duplicate key backup (prefer duplicate login is active)", zap.Int64("primary_id", primaryID), zap.Int64("duplicate_id", dupID))
	} else {
		_, err := tx.Exec(ctx, `DELETE FROM user_key_backups WHERE user_id = $1`, dupID)
		if err != nil {
			return fmt.Errorf("delete duplicate key backup: %w", err)
		}
		logger.Info("kept primary key backup and deleted duplicate key backup", zap.Int64("primary_id", primaryID), zap.Int64("duplicate_id", dupID))
	}

	return nil
}

func mergeUserPublicKeys(ctx context.Context, tx pgx.Tx, primaryID, dupID int64, logger *zap.Logger) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO user_public_keys (
			user_id, device_id, public_key, device_name, platform, status, trusted_at, revoked_at, last_seen_at, created_at
		)
		SELECT $1, device_id, public_key, device_name, platform, status, trusted_at, revoked_at, last_seen_at, created_at
		FROM user_public_keys
		WHERE user_id = $2
		ON CONFLICT (user_id, device_id) DO UPDATE SET
			public_key = CASE 
				WHEN (EXCLUDED.status = 'trusted' AND user_public_keys.status <> 'trusted') OR 
				     (EXCLUDED.status = user_public_keys.status AND COALESCE(EXCLUDED.last_seen_at, EXCLUDED.created_at) > COALESCE(user_public_keys.last_seen_at, user_public_keys.created_at)) 
				THEN EXCLUDED.public_key 
				ELSE user_public_keys.public_key 
			END,
			device_name = CASE 
				WHEN (EXCLUDED.status = 'trusted' AND user_public_keys.status <> 'trusted') OR 
				     (EXCLUDED.status = user_public_keys.status AND COALESCE(EXCLUDED.last_seen_at, EXCLUDED.created_at) > COALESCE(user_public_keys.last_seen_at, user_public_keys.created_at)) 
				THEN COALESCE(EXCLUDED.device_name, user_public_keys.device_name) 
				ELSE COALESCE(user_public_keys.device_name, EXCLUDED.device_name) 
			END,
			platform = CASE 
				WHEN (EXCLUDED.status = 'trusted' AND user_public_keys.status <> 'trusted') OR 
				     (EXCLUDED.status = user_public_keys.status AND COALESCE(EXCLUDED.last_seen_at, EXCLUDED.created_at) > COALESCE(user_public_keys.last_seen_at, user_public_keys.created_at)) 
				THEN COALESCE(EXCLUDED.platform, user_public_keys.platform) 
				ELSE COALESCE(user_public_keys.platform, EXCLUDED.platform) 
			END,
			status = CASE 
				WHEN (EXCLUDED.status = 'trusted' AND user_public_keys.status <> 'trusted') OR 
				     (EXCLUDED.status = user_public_keys.status AND COALESCE(EXCLUDED.last_seen_at, EXCLUDED.created_at) > COALESCE(user_public_keys.last_seen_at, user_public_keys.created_at)) 
				THEN EXCLUDED.status 
				ELSE user_public_keys.status 
			END,
			trusted_at = CASE 
				WHEN (EXCLUDED.status = 'trusted' AND user_public_keys.status <> 'trusted') OR 
				     (EXCLUDED.status = user_public_keys.status AND COALESCE(EXCLUDED.last_seen_at, EXCLUDED.created_at) > COALESCE(user_public_keys.last_seen_at, user_public_keys.created_at)) 
				THEN EXCLUDED.trusted_at 
				ELSE user_public_keys.trusted_at 
			END,
			revoked_at = CASE 
				WHEN (EXCLUDED.status = 'trusted' AND user_public_keys.status <> 'trusted') OR 
				     (EXCLUDED.status = user_public_keys.status AND COALESCE(EXCLUDED.last_seen_at, EXCLUDED.created_at) > COALESCE(user_public_keys.last_seen_at, user_public_keys.created_at)) 
				THEN EXCLUDED.revoked_at 
				ELSE user_public_keys.revoked_at 
			END,
			last_seen_at = GREATEST(user_public_keys.last_seen_at, EXCLUDED.last_seen_at),
			created_at = LEAST(user_public_keys.created_at, EXCLUDED.created_at)
	`, primaryID, dupID)
	if err != nil {
		return fmt.Errorf("merge public keys conflict resolution: %w", err)
	}

	_, err = tx.Exec(ctx, `DELETE FROM user_public_keys WHERE user_id = $1`, dupID)
	if err != nil {
		return fmt.Errorf("delete duplicate public keys: %w", err)
	}

	logger.Info("merged public keys with conflict resolution", zap.Int64("primary_id", primaryID), zap.Int64("duplicate_id", dupID))
	return nil
}

func mergeGroupMembers(ctx context.Context, tx pgx.Tx, primaryID, dupID int64) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO group_members (group_id, user_id, role, joined_at, left_at, is_muted, created_at)
		SELECT group_id, $1, role, joined_at, left_at, is_muted, created_at
		FROM group_members
		WHERE user_id = $2
		ON CONFLICT (group_id, user_id) DO UPDATE SET
			role = CASE
				WHEN EXCLUDED.role = 'owner' OR group_members.role = 'owner' THEN 'owner'
				WHEN EXCLUDED.role = 'admin' OR group_members.role = 'admin' THEN 'admin'
				ELSE 'member'
			END,
			joined_at = LEAST(group_members.joined_at, EXCLUDED.joined_at),
			created_at = LEAST(group_members.created_at, EXCLUDED.created_at)
	`, primaryID, dupID)
	if err != nil {
		return fmt.Errorf("merge group memberships: %w", err)
	}
	_, err = tx.Exec(ctx, `DELETE FROM group_members WHERE user_id = $1`, dupID)
	return err
}

func mergeChannelMembers(ctx context.Context, tx pgx.Tx, primaryID, dupID int64) error {
	_, err := tx.Exec(ctx, `
		UPDATE channels c
		SET member_count = GREATEST(0, c.member_count - 1)
		FROM channel_members cm1
		JOIN channel_members cm2 ON cm1.channel_id = cm2.channel_id
		WHERE cm1.user_id = $1 AND cm1.left_at IS NULL
		  AND cm2.user_id = $2 AND cm2.left_at IS NULL
		  AND c.id = cm1.channel_id
	`, primaryID, dupID)
	if err != nil {
		return fmt.Errorf("decrement channel member counts: %w", err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO channel_members (channel_id, user_id, role, is_muted, joined_at, left_at, created_at)
		SELECT channel_id, $1, role, is_muted, joined_at, left_at, created_at
		FROM channel_members
		WHERE user_id = $2
		ON CONFLICT (channel_id, user_id) DO UPDATE SET
			left_at = CASE
				WHEN EXCLUDED.left_at IS NULL OR channel_members.left_at IS NULL THEN NULL
				ELSE GREATEST(channel_members.left_at, EXCLUDED.left_at)
			END,
			joined_at = LEAST(channel_members.joined_at, EXCLUDED.joined_at)
	`, primaryID, dupID)
	if err != nil {
		return fmt.Errorf("merge channel memberships: %w", err)
	}

	_, err = tx.Exec(ctx, `DELETE FROM channel_members WHERE user_id = $1`, dupID)
	return err
}

func mergeChannelPostViews(ctx context.Context, tx pgx.Tx, primaryID, dupID int64) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO channel_post_views (post_id, user_id, viewed_at)
		SELECT post_id, $1, viewed_at
		FROM channel_post_views
		WHERE user_id = $2
		ON CONFLICT (post_id, user_id) DO UPDATE SET
			viewed_at = LEAST(channel_post_views.viewed_at, EXCLUDED.viewed_at)
	`, primaryID, dupID)
	if err != nil {
		return fmt.Errorf("merge post views: %w", err)
	}
	_, err = tx.Exec(ctx, `DELETE FROM channel_post_views WHERE user_id = $1`, dupID)
	return err
}

func mergeChannelPostReactions(ctx context.Context, tx pgx.Tx, primaryID, dupID int64) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO channel_post_reactions (post_id, user_id, reaction, created_at)
		SELECT post_id, $1, reaction, created_at
		FROM channel_post_reactions
		WHERE user_id = $2
		ON CONFLICT (post_id, user_id, reaction) DO NOTHING
	`, primaryID, dupID)
	if err != nil {
		return fmt.Errorf("merge post reactions: %w", err)
	}
	_, err = tx.Exec(ctx, `DELETE FROM channel_post_reactions WHERE user_id = $1`, dupID)
	return err
}

func mergePublicAppUserPositions(ctx context.Context, tx pgx.Tx, primaryID, dupID int64) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO public_app_user_positions (user_id, app_id, position, created_at, updated_at)
		SELECT $1, app_id, position, created_at, updated_at
		FROM public_app_user_positions
		WHERE user_id = $2
		ON CONFLICT (user_id, app_id) DO UPDATE SET
			position = LEAST(public_app_user_positions.position, EXCLUDED.position),
			updated_at = GREATEST(public_app_user_positions.updated_at, EXCLUDED.updated_at)
	`, primaryID, dupID)
	if err != nil {
		return fmt.Errorf("merge public app user positions: %w", err)
	}
	_, err = tx.Exec(ctx, `DELETE FROM public_app_user_positions WHERE user_id = $1`, dupID)
	return err
}

func mergeUserMailboxes(ctx context.Context, tx pgx.Tx, primaryID, dupID int64) error {
	_, err := tx.Exec(ctx, `
		UPDATE user_mailboxes
		SET is_primary = FALSE,
		    updated_at = NOW()
		WHERE user_id = $1
	`, dupID)
	if err != nil {
		return fmt.Errorf("deactivate duplicate mailbox primary flag: %w", err)
	}

	var primaryHasPrimary bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM user_mailboxes WHERE user_id = $1 AND is_primary = TRUE)`, primaryID).Scan(&primaryHasPrimary)
	if err != nil {
		return fmt.Errorf("check primary mailbox: %w", err)
	}

	if !primaryHasPrimary {
		_, err = tx.Exec(ctx, `
			UPDATE user_mailboxes
			SET is_primary = TRUE
			WHERE id = (
				SELECT id FROM user_mailboxes WHERE user_id = $1 ORDER BY is_primary DESC, created_at ASC LIMIT 1
			)
		`, dupID)
		if err != nil {
			return fmt.Errorf("restore primary mailbox on duplicate: %w", err)
		}
	}

	_, err = tx.Exec(ctx, `
		UPDATE user_mailboxes
		SET user_id = $1,
		    updated_at = NOW()
		WHERE user_id = $2
	`, primaryID, dupID)
	if err != nil {
		return fmt.Errorf("reassign mailboxes: %w", err)
	}

	return nil
}

func mergeAppleCalendarEventMappings(ctx context.Context, tx pgx.Tx, primaryID, dupID int64) error {
	_, err := tx.Exec(ctx, `
		DELETE FROM apple_calendar_event_mappings dup
		USING apple_calendar_event_mappings pri
		WHERE dup.user_id = $2
		  AND pri.user_id = $1
		  AND (dup.event_id = pri.event_id OR dup.apple_event_id = pri.apple_event_id)
	`, primaryID, dupID)
	if err != nil {
		return fmt.Errorf("delete conflicting apple calendar mappings: %w", err)
	}

	_, err = tx.Exec(ctx, `
		UPDATE apple_calendar_event_mappings
		SET user_id = $1,
		    updated_at = NOW()
		WHERE user_id = $2
	`, primaryID, dupID)
	if err != nil {
		return fmt.Errorf("reassign apple calendar mappings: %w", err)
	}
	return nil
}

func mergeGoogleCalendarEventMappings(ctx context.Context, tx pgx.Tx, primaryID, dupID int64) error {
	_, err := tx.Exec(ctx, `
		DELETE FROM google_calendar_event_mappings dup
		USING google_calendar_event_mappings pri
		WHERE dup.user_id = $2
		  AND pri.user_id = $1
		  AND (dup.event_id = pri.event_id OR dup.google_event_id = pri.google_event_id)
	`, primaryID, dupID)
	if err != nil {
		return fmt.Errorf("delete conflicting google calendar mappings: %w", err)
	}

	_, err = tx.Exec(ctx, `
		UPDATE google_calendar_event_mappings
		SET user_id = $1,
		    updated_at = NOW()
		WHERE user_id = $2
	`, primaryID, dupID)
	if err != nil {
		return fmt.Errorf("reassign google calendar mappings: %w", err)
	}
	return nil
}

func mergeBoardMembers(ctx context.Context, tx pgx.Tx, primaryID, dupID int64) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO board_members (board_id, user_id, role, joined_at)
		SELECT board_id, $1, role, joined_at
		FROM board_members
		WHERE user_id = $2
		ON CONFLICT (board_id, user_id) DO UPDATE SET
			role = CASE
				WHEN EXCLUDED.role = 'owner' OR board_members.role = 'owner' THEN 'owner'
				WHEN EXCLUDED.role = 'admin' OR board_members.role = 'admin' THEN 'admin'
				ELSE 'member'
			END,
			joined_at = LEAST(board_members.joined_at, EXCLUDED.joined_at)
	`, primaryID, dupID)
	if err != nil {
		return fmt.Errorf("merge board members: %w", err)
	}
	_, err = tx.Exec(ctx, `DELETE FROM board_members WHERE user_id = $1`, dupID)
	return err
}

func mergeTaskAssignees(ctx context.Context, tx pgx.Tx, primaryID, dupID int64) error {
	var hasAssignedAt bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM information_schema.columns
			WHERE table_schema = 'public'
			  AND table_name = 'task_assignees'
			  AND column_name = 'assigned_at'
		)
	`).Scan(&hasAssignedAt); err != nil {
		return fmt.Errorf("inspect task_assignees columns: %w", err)
	}

	timestampColumn := "created_at"
	insertColumn := "created_at"
	if hasAssignedAt {
		timestampColumn = "assigned_at"
		insertColumn = "assigned_at"
	}

	query := fmt.Sprintf(`
		INSERT INTO task_assignees (task_id, user_id, %s)
		SELECT task_id, $1, %s
		FROM task_assignees
		WHERE user_id = $2
		ON CONFLICT (task_id, user_id) DO NOTHING
	`, quoteIdent(insertColumn), quoteIdent(timestampColumn))

	_, err := tx.Exec(ctx, query, primaryID, dupID)
	if err != nil {
		return fmt.Errorf("merge task assignees: %w", err)
	}
	_, err = tx.Exec(ctx, `DELETE FROM task_assignees WHERE user_id = $1`, dupID)
	return err
}

func mergeCallParticipants(ctx context.Context, tx pgx.Tx, primaryID, dupID int64) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO call_participants (id, call_id, user_id, status, joined_at, left_at, role)
		SELECT gen_random_uuid(), call_id, $1, status, joined_at, left_at, role
		FROM call_participants
		WHERE user_id = $2
		ON CONFLICT (call_id, user_id) DO UPDATE SET
			status = CASE
				WHEN EXCLUDED.status = 'joined' OR call_participants.status = 'joined' THEN 'joined'
				ELSE 'left'
			END,
			left_at = CASE
				WHEN EXCLUDED.status = 'joined' OR call_participants.status = 'joined' THEN NULL
				ELSE COALESCE(call_participants.left_at, EXCLUDED.left_at)
			END
	`, primaryID, dupID)
	if err != nil {
		return fmt.Errorf("merge call participants: %w", err)
	}
	_, err = tx.Exec(ctx, `DELETE FROM call_participants WHERE user_id = $1`, dupID)
	return err
}

func mergeUserRecentItems(ctx context.Context, tx pgx.Tx, primaryID, dupID int64) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO user_recent_items (user_id, resource_type, resource_id, last_accessed_at)
		SELECT $1, resource_type, resource_id, last_accessed_at
		FROM user_recent_items
		WHERE user_id = $2
		ON CONFLICT (user_id, resource_type, resource_id) DO UPDATE SET
			last_accessed_at = GREATEST(user_recent_items.last_accessed_at, EXCLUDED.last_accessed_at)
	`, primaryID, dupID)
	if err != nil {
		return fmt.Errorf("merge user recent items: %w", err)
	}
	_, err = tx.Exec(ctx, `DELETE FROM user_recent_items WHERE user_id = $1`, dupID)
	return err
}

func updateTaskAttachmentsManual(ctx context.Context, tx pgx.Tx, primaryID, dupID int64) error {
	_, err := tx.Exec(ctx, `
		UPDATE task_attachments
		SET uploaded_by = $1
		WHERE uploaded_by = $2
	`, primaryID, dupID)
	if err != nil {
		return fmt.Errorf("manually update task attachments: %w", err)
	}
	return nil
}

func updateBoardsManual(ctx context.Context, tx pgx.Tx, primaryID, dupID int64) error {
	_, err := tx.Exec(ctx, `
		UPDATE boards
		SET owner_id = $1
		WHERE owner_id = $2
	`, primaryID, dupID)
	if err != nil {
		return fmt.Errorf("manually update boards owner_id: %w", err)
	}
	return nil
}
