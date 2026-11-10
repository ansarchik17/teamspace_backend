package main

import (
	"context"
	"fmt"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"database/sql"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/redis/go-redis/v9"
	agentcore "github.com/west6ide/workspace-backend/internal/agent"
	"github.com/west6ide/workspace-backend/internal/alem"
	"github.com/west6ide/workspace-backend/internal/auth"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/documentsearch"
	"github.com/west6ide/workspace-backend/internal/events"
	"github.com/west6ide/workspace-backend/internal/handler"
	"github.com/west6ide/workspace-backend/internal/infrastructure/database"
	"github.com/west6ide/workspace-backend/internal/keycloak"
	"github.com/west6ide/workspace-backend/internal/metrics"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"github.com/west6ide/workspace-backend/internal/repository"
	"github.com/west6ide/workspace-backend/internal/server"
	"github.com/west6ide/workspace-backend/internal/service"
	"github.com/west6ide/workspace-backend/internal/telegramguard"
	"github.com/west6ide/workspace-backend/internal/telemetry"
	"github.com/west6ide/workspace-backend/internal/websocket"
	boardws "github.com/west6ide/workspace-backend/internal/whiteboard"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

// @title Messenger API
// @version 2.0
// @description Modern messenger API with JWT authentication, real-time messaging, and file sharing
// @contact.name API Support

// @BasePath /api/v1
// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
func main() {
	// Set local timezone to UTC globally
	time.Local = time.UTC

	// Create context for cancellation
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	// Initialize logger
	logger, err := initLogger(cfg.Logger)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = logger.Sync() }()
	defer zap.RedirectStdLog(logger)()

	logger.Info("Starting Messenger API",
		zap.String("version", cfg.App.Version),
		zap.String("env", cfg.App.Env),
	)

	eventPublisher := events.NewNoopPublisher()
	if cfg.RabbitMQ.Enabled {
		rabbitPublisher, err := events.NewRabbitMQPublisher(cfg.RabbitMQ)
		if err != nil {
			logger.Warn("RabbitMQ publisher initialization failed, continuing with noop publisher", zap.Error(err))
		} else {
			eventPublisher = rabbitPublisher
			logger.Info("RabbitMQ publisher initialized successfully",
				zap.String("exchange", cfg.RabbitMQ.Exchange),
				zap.String("producer", cfg.RabbitMQ.Producer),
			)
		}
	} else {
		logger.Info("RabbitMQ publisher disabled by configuration")
	}
	defer func() {
		if err := eventPublisher.Close(); err != nil {
			logger.Warn("Failed to close RabbitMQ publisher", zap.Error(err))
		}
	}()

	// Initialize database
	db, err := database.NewPostgresPool(ctx, cfg.Database, logger)
	if err != nil {
		logger.Fatal("Failed to connect to database", zap.Error(err))
	}
	defer db.Close()

	// Run migrations
	migrationDSN := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s timezone=UTC",
		cfg.Database.Host, cfg.Database.Port, cfg.Database.User, cfg.Database.Password, cfg.Database.Database, cfg.Database.SSLMode,
	)

	migrationDB, err := sql.Open("pgx", migrationDSN)
	if err != nil {
		logger.Fatal("Failed to open database for migrations", zap.Error(err))
	}

	// Check connection
	if err := migrationDB.Ping(); err != nil {
		logger.Fatal("Failed to ping database for migrations", zap.Error(err))
	}

	if err := goose.SetDialect("postgres"); err != nil {
		logger.Fatal("Failed to set goose dialect", zap.Error(err))
	}

	if err := goose.Up(migrationDB, "db/migrations"); err != nil {
		logger.Fatal("Failed to key migrations", zap.Error(err))
	}

	// Close migration connection as we don't need it anymore
	if err := migrationDB.Close(); err != nil {
		logger.Error("Failed to close migration connection", zap.Error(err))
	}

	logger.Info("Migrations ran successfully")

	// Initialize Redis
	var redisClient *redis.Client
	rdb := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%s", cfg.Redis.Host, cfg.Redis.Port),
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
		PoolSize: cfg.Redis.PoolSize,
	})

	if err := rdb.Ping(ctx).Err(); err != nil {
		logger.Warn("Redis connection failed, running without cache", zap.Error(err))
		_ = rdb.Close() // Close the failed client
	} else {
		logger.Info("Connected to Redis successfully")
		redisClient = rdb
		defer redisClient.Close()
	}

	// Initialize repositories
	userRepo := repository.NewUserRepository(db, logger)
	userHRProfileRepo := repository.NewUserHRProfileRepository(db, logger)
	chatRepo := repository.NewChatRepository(db, redisClient, logger)
	messageRepo := repository.NewMessageRepository(db, logger)
	messageSyncRepo := repository.NewMessageSyncRepository(db, logger)
	messageSyncService := service.NewMessageSyncService(messageSyncRepo, logger)
	groupRepo := repository.NewGroupRepository(db, logger)
	pinnedRepo := repository.NewPinnedRepository(db, logger)
	deviceRepo := repository.NewDeviceRepository(db, logger)
	boardRepo := repository.NewBoardRepository(db, logger)
	taskRepo := repository.NewTaskRepository(db, logger)
	calendarRepo := repository.NewCalendarRepository(db, logger)
	quickPinRepo := repository.NewQuickPinRepository(db, logger)
	driveRepo := repository.NewDriveRepository(db, logger)
	recentRepo := repository.NewRecentRepository(db, logger)
	publicAppRepo := repository.NewPublicAppRepository(db, logger)
	aiRepo := repository.NewAIRepository(db, logger)
	unifiedAIRepo := repository.NewUnifiedAIRepository(db, logger)
	folderRepo := repository.NewFolderRepository(db, logger)
	reviewRepo := repository.NewReviewRepository(db, logger)
	notifRepo := repository.NewNotificationRepository(db, logger)
	callRepo := repository.NewCallHistoryRepository(db, logger)
	recordingRepo := repository.NewCallRecordingRepository(db, logger)
	personalRecordingRepo := repository.NewPersonalCallRecordingRepository(db, logger)
	statusRepo := repository.NewStatusRepository(db, logger)
	reactionRepo := repository.NewReactionRepository(db, logger)
	e2eeRepo := repository.NewE2EERepository(db, logger)
	notebookRepo := repository.NewNotebookRepository(db, logger)
	noteRepo := repository.NewNoteRepository(db, logger)
	googleCalendarRepo := repository.NewGoogleCalendarRepository(db)
	appleCalendarRepo := repository.NewAppleCalendarRepository(db)
	emailLogRepo := repository.NewEmailLogRepository(db, logger)
	emailMappingRepo := repository.NewEmailMappingRepository(db, logger)
	loginLogRepo := repository.NewLoginLogRepository(db, logger)
	gamificationRepo := repository.NewGamificationRepository(db)
	chatThemeRepo := repository.NewChatThemeRepository(db, logger)
	dashboardRepo := repository.NewDashboardRepository(db, logger)
	channelRepo := repository.NewChannelRepository(db, logger)
	userQRRepo := repository.NewUserQRRepository(db, logger)
	profileSectionRepo := repository.NewProfileSectionRepository(db, logger)
	agentRepo := repository.NewAgentRepository(db, logger)
	whiteboardRepo := repository.NewWhiteboardRepository(db, logger)
	pubRepo := repository.NewPublicationRepository(db, logger)
	// Initialize token storage and manager
	var tokenStorage auth.TokenStorage
	if redisClient != nil {
		tokenStorage = auth.NewRedisTokenStorage(redisClient)
	}
	tokenManager := auth.NewTokenManager(&cfg.JWT, tokenStorage)

	logger.Info("JWT token manager initialized",
		zap.Duration("access_token_expiry", cfg.JWT.AccessTokenExpiry),
		zap.Duration("refresh_token_expiry", cfg.JWT.RefreshTokenExpiry),
	)

	callTelemetry := telemetry.NewCallTelemetry(cfg.Metrics.Enabled)

	// Initialize WebSocket hub
	wsHub := websocket.NewHub(logger, userRepo, callTelemetry)
	wsHub.SyncHandler = messageSyncService
	metricsCollector := metrics.NewCollector(cfg.App.Name, cfg.App.Version, wsHub, callTelemetry)
	if cfg.Metrics.Enabled {
		metrics.DefaultAIAccounting().StartAIDatabaseSampler(ctx, db, time.Minute, cfg.AIReconciliation.StaleAfter)
	}
	wsHub.ParticipantResolver = func(ctx context.Context, conversationID uuid.UUID) ([]int64, error) {
		// 1. Try to get as group members
		// Note from context: conversation_id in messages often equals group_id for groups.
		members, err := groupRepo.GetGroupMembers(ctx, conversationID)
		if err == nil && len(members) > 0 {
			return members, nil
		}

		// 2. If no group members found, try partial/private chat logic
		// We might need a method from chatRepo.
		// For now, let's try getting it from chatRepo if available
		// or implementing a simple SQL lookup here? No, stick to repo.
		// assumption: chatRepo has GetParticipants or similar.
		// If not, we will add it.
		return chatRepo.GetConversationParticipants(ctx, conversationID)
	}

	pinCryptoKey := os.Getenv("PIN_CRYPTO_KEY")
	if pinCryptoKey == "" {
		pinCryptoKey = "messenger-pin-default-key-change-me"
	}

	crypto := service.NewCryptoService(pinCryptoKey)

	kcPublicURL := cfg.Keycloak.PublicURL
	if kcPublicURL == "" {
		kcPublicURL = cfg.Keycloak.ServerURL
	}

	kcClient := keycloak.NewWithPublicURL(
		cfg.Keycloak.ServerURL,
		kcPublicURL,
		cfg.Keycloak.Realm,
		cfg.Keycloak.ClientID,
		cfg.Keycloak.ClientSecret,
	)
	kcClients := map[string]*keycloak.Client{
		cfg.Keycloak.ClientID: kcClient,
	}
	if cfg.Keycloak.MobileClientID != "" {
		kcClients[cfg.Keycloak.MobileClientID] = keycloak.NewWithPublicURL(
			cfg.Keycloak.ServerURL,
			kcPublicURL,
			cfg.Keycloak.Realm,
			cfg.Keycloak.MobileClientID,
			cfg.Keycloak.MobileClientSecret,
		)
	}
	if cfg.ExternalAdmin.Enabled && cfg.ExternalAdmin.ClientID != "" && cfg.ExternalAdmin.ClientID != cfg.Keycloak.ClientID && cfg.ExternalAdmin.ClientID != cfg.Keycloak.MobileClientID {
		externalAdminSecret := cfg.ExternalAdmin.ClientSecret
		if externalAdminSecret == "" {
			externalAdminSecret = cfg.Keycloak.ClientSecret
		}
		kcClients[cfg.ExternalAdmin.ClientID] = keycloak.NewWithPublicURL(
			cfg.Keycloak.ServerURL,
			kcPublicURL,
			cfg.Keycloak.Realm,
			cfg.ExternalAdmin.ClientID,
			externalAdminSecret,
		)
	}

	var kcAdminClient *keycloak.AdminClient
	if strings.TrimSpace(cfg.Keycloak.AdminClientID) != "" && strings.TrimSpace(cfg.Keycloak.AdminUser) != "" {
		kcAdminClient = keycloak.NewAdminWithFallback(
			cfg.Keycloak.ServerURL,
			cfg.Keycloak.AdminClientRealm,
			cfg.Keycloak.AdminClientID,
			cfg.Keycloak.AdminClientSecret,
			cfg.Keycloak.AdminUser,
			cfg.Keycloak.AdminPass,
		)
	} else if strings.TrimSpace(cfg.Keycloak.AdminClientID) != "" {
		kcAdminClient = keycloak.NewAdminWithClientCredentials(
			cfg.Keycloak.ServerURL,
			cfg.Keycloak.AdminClientRealm,
			cfg.Keycloak.AdminClientID,
			cfg.Keycloak.AdminClientSecret,
		)
	} else {
		kcAdminClient = keycloak.NewAdmin(
			cfg.Keycloak.ServerURL,
			cfg.Keycloak.AdminRealm,
			cfg.Keycloak.AdminUser,
			cfg.Keycloak.AdminPass,
		)
	}

	emailService := service.NewEmailService(cfg.SMTP, logger)
	aiCache := service.NewAICache(redisClient, logger)

	gamificationService := service.NewGamificationService(gamificationRepo, logger)

	// Initialize services
	notificationService := service.NewNotificationService(deviceRepo, notifRepo, callRepo, userRepo, emailService, wsHub, cfg, logger)
	internalNotificationService := service.NewInternalNotificationService(userRepo, notifRepo, notificationService, logger)
	statusService := service.NewStatusService(statusRepo, wsHub, logger)
	chatService := service.NewChatService(chatRepo, messageRepo, groupRepo, userRepo, pinnedRepo, reactionRepo, wsHub, notificationService, gamificationService, eventPublisher, logger)
	pinnedService := service.NewPinnedService(pinnedRepo, chatRepo, groupRepo, channelRepo, wsHub, logger)
	unifiedService := service.NewUnifiedService(chatRepo, groupRepo, pinnedRepo, unifiedAIRepo, channelRepo, wsHub, logger)

	e2eeService := service.NewE2EEService(e2eeRepo, logger)
	telegramGuardClient := telegramguard.New(cfg.TelegramGuard.BaseURL, cfg.TelegramGuard.APIKey, cfg.TelegramGuard.Timeout, logger)
	var mailboxProvisioner auth.MailboxProvisioner
	if cfg.Stalwart.Enabled() {
		mailboxProvisioner = auth.NewStalwartMailboxProvisioner(auth.StalwartProvisionerConfig{
			BaseURL:           cfg.Stalwart.URL,
			PublicURL:         cfg.Stalwart.PublicURL,
			AdminUser:         cfg.Stalwart.AdminUser,
			AdminPassword:     cfg.Stalwart.AdminPassword,
			DefaultDomain:     cfg.Stalwart.DefaultDomain,
			DefaultQuotaMB:    cfg.Stalwart.DefaultQuotaMB,
			RequestTimeout:    cfg.Stalwart.RequestTimeout,
			MaxSuffixAttempts: cfg.Stalwart.MaxSuffixAttempts,
		}, logger)
		logger.Info("Stalwart mailbox provisioning enabled", zap.String("base_url", cfg.Stalwart.URL), zap.String("domain", cfg.Stalwart.DefaultDomain))
	} else {
		mailboxProvisioner = auth.NewMailcowMailboxProvisioner(auth.MailcowProvisionerConfig{
			BaseURL:             cfg.Mailcow.APIBaseURL,
			APIKey:              cfg.Mailcow.APIKey,
			DefaultDomain:       cfg.Mailcow.DefaultDomain,
			DefaultQuotaMB:      cfg.Mailcow.DefaultQuotaMB,
			TLSEnforceIn:        cfg.Mailcow.TLSEnforceIn,
			TLSEnforceOut:       cfg.Mailcow.TLSEnforceOut,
			ForcePasswordUpdate: cfg.Mailcow.ForcePWUpdate,
			InsecureSkipVerify:  cfg.Mailcow.InsecureSkipVerify,
			RequestTimeout:      cfg.Mailcow.RequestTimeout,
			MaxSuffixAttempts:   cfg.Mailcow.MaxSuffixAttempts,
		}, logger)
	}
	authService := auth.NewKeycloakService(
		userRepo,
		reviewRepo,
		loginLogRepo,
		chatService,
		kcClient,
		kcAdminClient,
		cfg.Keycloak.Realm,
		auth.WithRequireQRForWebLoginWhenMobileRegistered(cfg.Security.RequireQRForWebLoginWhenMobileRegistered),
		auth.WithTokenManager(tokenManager),
		auth.WithKeyBackupService(e2eeService),
		auth.WithKeycloakClients(kcClients),
		auth.WithMobileClientID(cfg.Keycloak.MobileClientID),
		auth.WithLoginMonitor(telegramGuardClient),
		auth.WithHRProfileRepository(userHRProfileRepo),
		auth.WithAICache(aiCache),
		auth.WithMailboxRepository(userRepo),
		auth.WithMailboxProvisioner(mailboxProvisioner),
	)

	// Ensure user attribute mappers exist in Keycloak so that position, department_id,
	// bin, and iin are included in JWT access tokens. This is idempotent.
	{
		mapperCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		for _, clientID := range []string{cfg.Keycloak.ClientID, cfg.Keycloak.MobileClientID} {
			if clientID == "" {
				continue
			}
			if err := kcAdminClient.EnsureUserAttributeMappers(mapperCtx, cfg.Keycloak.Realm, clientID); err != nil {
				logger.Warn("Failed to ensure Keycloak user attribute mappers",
					zap.String("client_id", clientID),
					zap.Error(err),
				)
			} else {
				logger.Info("Keycloak user attribute mappers ensured", zap.String("client_id", clientID))
			}
		}
		cancel()
	}

	meetingService := service.NewMeetingService(cfg.Jitsi, logger)
	pubService := service.NewPublicationService(pubRepo, userRepo, logger)
	pubService.SetNotificationSender(notificationService)
	pubService.SetEmailSender(emailService)
	pubService.SetAppPublicURL(cfg.App.PublicURL)

	// Initialize Google Calendar Service
	if cfg.GoogleCalendar.RedirectURL == "" {
		cfg.GoogleCalendar.RedirectURL = cfg.App.PublicURL + "/api/v1/calendar/google/oauth/callback"
	}

	var googleCalendarService *service.GoogleCalendarService
	if cfg.GoogleCalendar.ClientID != "" && cfg.GoogleCalendar.ClientSecret != "" {
		googleCalendarService = service.NewGoogleCalendarService(
			googleCalendarRepo,
			calendarRepo,
			logger,
			cfg.GoogleCalendar.ClientID,
			cfg.GoogleCalendar.ClientSecret,
			cfg.GoogleCalendar.RedirectURL,
		)
		logger.Info("Google Calendar integration enabled")
	} else {
		logger.Warn("Google Calendar integration disabled - missing credentials")
	}

	var idpOAuthService *service.IDPOAuthService
	if cfg.IDP.ClientID != "" && cfg.IDP.ClientSecret != "" {
		idpOAuthService = service.NewIDPOAuthService(cfg.IDP, logger)
		logger.Info("IDP OAuth integration enabled")
	} else {
		logger.Warn("IDP OAuth integration disabled - missing credentials")
	}

	// Initialize Apple Calendar Service
	appleCalendarService := service.NewAppleCalendarService(
		appleCalendarRepo,
		calendarRepo,
		logger,
	)

	liveKitService := service.NewLiveKitService(cfg.LiveKit, cfg.MinIO, userRepo, callTelemetry, logger)
	logger.Info("LiveKit configured",
		zap.String("client_livekit_url", liveKitService.GetClientURL()),
		zap.String("server_livekit_api_url", liveKitService.GetAPIURL()),
		zap.Bool("server_livekit_api_url_fallback", strings.TrimSpace(cfg.LiveKit.APIURL) == ""),
	)

	calendarAvailabilityService := service.NewCalendarAvailabilityService(calendarRepo, userRepo, cfg.CalendarMeetings)
	calendarMeetingProviderService := service.NewCalendarMeetingProviderService(cfg.CalendarMeetings, liveKitService, logger)
	calendarService := service.NewCalendarService(
		calendarRepo,
		taskRepo,
		notificationService,
		userRepo,
		googleCalendarService,
		appleCalendarService,
		calendarAvailabilityService,
		calendarMeetingProviderService,
		gamificationService,
		eventPublisher,
		logger,
	)

	quickPinDefaultClient := cfg.Keycloak.QuickPinDefaultClientID
	if quickPinDefaultClient == "" {
		quickPinDefaultClient = cfg.Keycloak.MobileClientID
	}
	quickPinService := auth.NewQuickPinService(
		userRepo,
		quickPinRepo,
		kcClient,
		crypto,
		cfg.Security.BcryptCost,
		logger,
	).WithLoginLogRepo(loginLogRepo).WithLoginMonitor(telegramGuardClient).WithKeycloakClients(kcClients).WithQuickPinDefaultClientID(quickPinDefaultClient).WithTokenManager(tokenManager)
	publicAppService := service.NewPublicAppService(publicAppRepo, userRepo, logger)
	notificationService.SetLiveKitTokenIssuer(liveKitService)

	// Initialize FileService (with MinIO)
	fileService, err := service.NewFileService(cfg.MinIO, cfg.App.PublicURL, driveRepo, logger)
	if err != nil {
		logger.Warn("Failed to initialize file service", zap.Error(err))
		fileService = nil
	} else if fileService != nil {
		pubService.SetFileService(fileService)
	}

	// Initialize groupService (requires fileService for avatar upload)
	go pubService.RunScheduledPublications(ctx)
	go pubService.RunPublicationObjectCleanup(ctx)
	groupService := service.NewGroupService(groupRepo, calendarRepo, messageRepo, pinnedRepo, reactionRepo, calendarService, userRepo, fileService, wsHub, notificationService, gamificationService, eventPublisher, logger)

	kanbanService := service.NewKanbanService(boardRepo, taskRepo, userRepo, driveRepo, notificationService, fileService, gamificationService, eventPublisher, logger)
	odoIssuer := cfg.Keycloak.Issuer
	if odoIssuer == "" {
		odoIssuer = cfg.Keycloak.RealmURL
	}
	odoCommandClient, err := service.NewODOCommandClient(cfg.Internal.ODOCommandsBaseURL, odoIssuer, cfg.Internal.ODOCommandsEnabled, cfg.Internal.ODOCommandsTimeout)
	if err != nil {
		logger.Fatal("Invalid ODO command configuration", zap.Error(err))
	}
	kanbanService.SetODOCommandClient(odoCommandClient)
	wsHub.KanbanTaskMovedResolver = kanbanService.ResolveTaskMovedRecipients
	kanbanService.SetRealtimeHub(wsHub)
	whiteboardService := service.NewWhiteboardService(boardRepo, whiteboardRepo, logger)
	whiteboardManager := boardws.NewManager(whiteboardService, whiteboardService, logger)
	var driveService *service.DriveService
	if fileService != nil {
		driveService = service.NewDriveService(driveRepo, recentRepo, fileService, cfg.App.PublicURL, notificationService, gamificationService, logger)
	} else {
		logger.Warn("Drive service disabled because file service is nil")
	}
	internalNotificationService.SetDriveService(driveService)
	internalDriveImportService := service.NewInternalDriveImportService(userRepo, driveService)
	chatCommandService := service.NewChatCommandService(kanbanService, calendarService, driveService, chatService, groupService, logger)

	deviceObservabilityService := service.NewDeviceObservabilityService(
		deviceRepo,
		userRepo,
		loginLogRepo,
		kcAdminClient,
		cfg.Keycloak.Realm,
		cfg.Keycloak.AllowedClientIDs,
		wsHub,
		logger,
	)

	logger.Info("Services initialized successfully")

	aiLimiter := service.NewAIGenerationLimiter(redisClient, logger, cfg.AILimiter)
	userConcurrencyLimit := 0
	if cfg.AILimiter.Enabled {
		userConcurrencyLimit = cfg.AILimiter.MaxConcurrentPerUser
	}
	paidCallerService := service.NewAIPaidCallerService(
		repository.NewAIReservationRepository(
			db, nil,
			repository.WithAIUserConcurrencyLimit(userConcurrencyLimit),
			repository.WithAIAccountingEnabled(cfg.AIAccounting.Enabled),
			repository.WithAIEnforcementMode(cfg.AIAccounting.EnforcementMode),
			repository.WithAIQuotaTestSubjects(cfg.AIAccounting.QuotaTestSubjects),
		),
		service.WithAILimiter(aiLimiter),
	)

	contentExtractionService := service.NewContentExtractionService(cfg.DocumentParse, logger)
	alemService := service.NewAlemService(cfg, aiRepo, fileService, contentExtractionService, logger)
	alemService.SetPaidAccounting(paidCallerService, userRepo)
	var semanticSearch *documentsearch.Search
	if strings.TrimSpace(cfg.DocumentSearch.URL) != "" {
		semanticSearch, err = documentsearch.New(ctx, db, cfg.DocumentSearch)
		if err != nil {
			logger.Fatal("Invalid document semantic search configuration", zap.Error(err))
		}
		logger.Info("Local document semantic search enabled", zap.String("model", semanticSearch.ModelKey()))
	}
	var semanticRanker documentsearch.SemanticRanker
	if semanticSearch != nil {
		semanticRanker = semanticSearch
	}
	retrievalPipeline, err := documentsearch.NewPipeline(cfg.DocumentSearch, semanticRanker, logger)
	if err != nil {
		logger.Fatal("Invalid retrieval pipeline configuration", zap.Error(err))
	}
	var fileRetrieval *service.FileRetrievalService
	switch cfg.DriveSearch.Backend {
	case "postgres":
		fileRetrieval = service.NewFileRetrievalService(repository.NewFileSearchRepository(db), driveRepo, fileService, contentExtractionService, semanticSearch, retrievalPipeline, logger)
	case "elasticsearch":
	default:
		logger.Fatal("DRIVE_SEARCH_BACKEND must be postgres or elasticsearch")
	}
	aiDocumentRepo := repository.NewAIDocumentRepository(db, logger)
	aiDocumentRepo.SetSemanticSearch(semanticSearch)
	aiDocumentRepo.SetRetrievalPipeline(retrievalPipeline)
	aiDocumentService := service.NewAIDocumentService(aiDocumentRepo, aiRepo, fileService, contentExtractionService, logger)
	alemService.SetDocumentRepository(aiDocumentRepo)
	if fileRetrieval != nil {
		aiDocumentService.SetFileIndexer(fileRetrieval)
	}
	if cfg.AIReconciliation.Enabled {
		// Usage lookup adapters are registered only after their provider contract
		// is confirmed. Unsupported outcomes enter durable manual reconciliation.
		worker, err := service.NewAIReconciliationWorker(repository.NewAIReservationRepository(db, nil), cfg.AIReconciliation, nil, logger)
		if err != nil {
			logger.Fatal("Invalid AI reconciliation configuration", zap.Error(err))
		}
		workerCtx, cancelWorker := context.WithCancel(ctx)
		workerDone := make(chan struct{})
		go func() { defer close(workerDone); worker.Run(workerCtx) }()
		defer func() { cancelWorker(); <-workerDone }()
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("AI_DOCUMENT_WORKER_ONLY")), "true") {
		logger.Info("Starting AI document worker-only process")
		aiDocumentService.Start(ctx)
		if err := server.RunWorkerHealth(ctx, cfg.HTTP.Port, db.Ping); err != nil {
			logger.Fatal("Worker health server failed", zap.Error(err))
		}
		return
	}
	unifiedAIService := service.NewUnifiedAIService(cfg, unifiedAIRepo, fileService, logger)
	unifiedAIService.SetPaidAccounting(paidCallerService, userRepo)
	folderService := service.NewFolderService(folderRepo, chatRepo, groupRepo, logger)
	reviewService := service.NewReviewService(reviewRepo, chatRepo, taskRepo, calendarRepo, gamificationService, logger)
	notebookService := service.NewNotebookService(notebookRepo, logger, driveRepo)
	twoSlidesService := service.NewTwoSlidesService(cfg.TwoSlides, logger)
	noteService := service.NewNoteService(noteRepo, fileService, logger, service.WithNoteCache(aiCache))
	go noteService.RunObjectCleanup(ctx)
	searchService := service.NewSearchService(userRepo, messageRepo, noteService, logger)
	driveSearchService := service.NewDriveSearchService(
		cfg.DriveSearch,
		logger,
		service.WithDriveIndexDependencies(driveRepo, fileService, contentExtractionService),
	)
	if fileRetrieval != nil {
		driveSearchService.SetLocalRetrieval(fileRetrieval)
	}
	if driveService != nil {
		driveService.SetDriveSearchService(driveSearchService)
	}
	emailLogService := service.NewEmailLogService(emailLogRepo, emailMappingRepo, userRepo, emailService, notificationService, logger)
	chatThemeService := service.NewChatThemeService(chatThemeRepo, logger)
	dashboardService := service.NewDashboardService(dashboardRepo, logger)
	channelService := service.NewChannelService(channelRepo, userRepo, pinnedRepo, wsHub, notificationService, logger)
	userQRService := service.NewUserQRService(userQRRepo, userRepo, chatRepo, logger)
	profileSectionService := service.NewProfileSectionService(profileSectionRepo, userRepo, fileService, logger)

	aiAssistantService := service.NewAIAssistantService(calendarService, kanbanService, userRepo, aiCache, logger)
	e2eeHandler := handler.NewE2EEHandler(e2eeService, logger, tokenManager)
	e2eeHandler.SetRealtimeSessionTerminator(wsHub)

	// Initialize handlers
	authHandler := handler.NewAuthHandler(authService, quickPinService, e2eeService, logger, tokenManager)
	if strings.TrimSpace(cfg.Internal.ODOHandoffToken) != "" {
		if redisClient == nil && strings.EqualFold(cfg.App.Env, "production") {
			logger.Error("ODO handoff disabled: Redis is required in production")
		} else {
			authHandler.SetODOHandoffStore(auth.NewODOHandoffStore(redisClient))
		}
	}
	authHandler.SetExternalAdminClientID(cfg.ExternalAdmin.ClientID)
	externalAdminHandler := handler.NewExternalAdminHandler(authService, logger)
	externalAdminViewHandler := handler.NewExternalAdminViewHandler(logger)
	authHandler.SetSessionTerminator(wsHub)
	authHandler.SetPushDeviceManager(notificationService)
	chatHandler := handler.NewChatHandler(chatService, groupService, unifiedAIService, kanbanService, calendarService, noteService, logger)
	chatCommandHandler := handler.NewChatCommandHandler(chatCommandService, logger)
	groupHandler := handler.NewGroupHandler(groupService, noteService, logger)

	pinnedHandler := handler.NewPinnedHandler(pinnedService, logger)
	unifiedHandler := handler.NewUnifiedHandler(unifiedService, logger)
	searchHandler := handler.NewSearchHandler(searchService, kanbanService, calendarService, driveService, logger)
	messageSyncHandler := handler.NewMessageSyncHandler(messageSyncService, logger)
	wsHandler := handler.NewWebSocketHandler(wsHub, statusService, logger)
	whiteboardHandler := handler.NewWhiteboardHandler(whiteboardService, whiteboardManager, logger)

	deviceHandler := handler.NewDeviceHandler(notificationService, logger)
	meetingHandler := handler.NewMeetingHandler(meetingService, userRepo, logger)
	kanbanHandler := handler.NewKanbanHandler(kanbanService, logger)
	calendarHandler := handler.NewCalendarHandler(calendarService, cfg.JWT.Secret, logger)
	pinAuthHandler := handler.NewPinAuthHandler(quickPinService, logger)
	publicAppHandler := handler.NewPublicAppHandler(publicAppService, logger)
	alemHandler := handler.NewAlemHandler(alemService, logger)
	workspaceAIProvider := service.NewWorkspaceAIProvider(paidCallerService, userRepo, alem.BaseURL, cfg.Alem.APIKey, cfg.Alem.RequestTimeout)
	workspaceAIJobKey := os.Getenv("WORKSPACE_AI_JOB_SIGNING_KEY")
	if workspaceAIJobKey == "" {
		workspaceAIJobKey = cfg.JWT.Secret
	}
	workspaceAIProvider.SetSpeechCredentials(os.Getenv("WORKSPACE_AI_OPENAI_API_KEY"), os.Getenv("WORKSPACE_AI_ELEVENLABS_API_KEY"), workspaceAIJobKey)
	workspaceAIProvider.SetStreamingUsageConfirmed(strings.EqualFold(os.Getenv("WORKSPACE_AI_STREAMING_USAGE_CONFIRMED"), "true"))
	alemHandler.SetWorkspaceAIProvider(workspaceAIProvider)
	alemHandler.SetDocumentService(aiDocumentService)
	unifiedAIHandler := handler.NewUnifiedAIHandler(unifiedAIService, logger)
	folderHandler := handler.NewFolderHandler(folderService, logger)
	reviewHandler := handler.NewReviewHandler(reviewService, logger)
	notifHandler := handler.NewNotificationHandler(notificationService, liveKitService, logger)
	internalNotificationHandler := handler.NewInternalNotificationHandler(internalNotificationService, logger)
	internalDriveImportHandler := handler.NewInternalDriveImportHandler(internalDriveImportService, logger)
	liveKitHandler := handler.NewLiveKitHandler(liveKitService, notificationService, groupService, userRepo, callRepo, recordingRepo, personalRecordingRepo, fileService, callTelemetry, logger)
	personalRecordingHandler := handler.NewPersonalRecordingHandler(liveKitService, personalRecordingRepo, callRepo, fileService, wsHub, logger)
	contentExtractionHandler := handler.NewContentExtractionHandler(contentExtractionService, logger)
	notebookResearch := service.NewNotebookResearchService(notebookService, driveSearchService, cfg.Alem, logger)
	notebookResearch.SetSemanticSearch(retrievalPipeline)
	notebookResearch.SetPaidAccounting(paidCallerService, userRepo)
	notebookHandler := handler.NewNotebookHandler(notebookService, twoSlidesService, logger, notebookResearch)
	notebookJobs := service.NewNotebookResearchJobService(notebookResearch, repository.NewNotebookResearchJobRepository(db, logger), wsHub, logger)
	notebookHandler.SetResearchJobs(notebookJobs)
	noteHandler := handler.NewNoteHandler(noteService, logger)
	emailLogHandler := handler.NewEmailLogHandler(emailLogService, logger)
	analyticsHandler := handler.NewAnalyticsHandler(loginLogRepo, logger)
	gamificationHandler := handler.NewGamificationHandler(gamificationService)
	chatThemeHandler := handler.NewChatThemeHandler(chatThemeService, logger)
	dashboardHandler := handler.NewDashboardHandler(dashboardService)
	channelHandler := handler.NewChannelHandler(channelService, logger)
	userQRHandler := handler.NewUserQRHandler(userQRService, logger)
	profileSectionHandler := handler.NewProfileSectionHandler(profileSectionService, logger)
	aiAssistantHandler := handler.NewAIAssistantHandler(aiAssistantService, logger)
	deviceObservabilityHandler := handler.NewDeviceObservabilityHandler(deviceObservabilityService, logger)
	pubHandler := handler.NewPublicationHandler(pubService, logger)
	minioCapacity, err := service.NewMinIOCapacity(cfg.MinIO)
	if err != nil {
		logger.Fatal("Invalid MinIO capacity configuration", zap.Error(err))
	}
	organizationRepo := repository.NewOrganizationRepository(db, logger)
	organizationRepo.SetStorageCapacityProvider(minioCapacity)
	organizationRepo.SetAIAccountingEnabled(cfg.AIAccounting.Enabled)
	organizationRepo.SetEnforcementMode(cfg.AIAccounting.EnforcementMode)
	organizationRepo.SetAIQuotaTestSubjects(cfg.AIAccounting.QuotaTestSubjects)
	organizationService := service.NewOrganizationService(organizationRepo, logger)
	organizationService.SetContactFileService(fileService)
	organizationService.SetOffboardingIdentitySync(authService.DisableOffboardedIdentity)
	if driveSearchService.Enabled() {
		organizationService.SetOffboardingFileReindex(driveSearchService.IndexFile)
	}
	go organizationService.RunOffboardingIdentitySync(ctx)
	invitationService := auth.NewInvitationService(organizationRepo, authService, emailService, cfg.App.PublicURL, logger)
	authHandler.SetInvitationService(invitationService)
	recoveryRepo := repository.NewRecoveryRepository(db)
	recoveryService := auth.NewRecoveryService(recoveryRepo, authService, emailService, cfg.App.PublicURL, logger)
	recoveryService.SetSessionCloser(func(id int64) { wsHub.CloseAllUserSessions(id) })
	authHandler.SetRecoveryService(recoveryService)
	recoveryService.StartWorker(ctx)
	invitationService.StartActivationWorker(ctx)
	organizationService.SetUserSessionCloser(func(id int64) { wsHub.CloseAllUserSessions(id) })
	authService.SetUserDeletionHandler(organizationService.DeleteUser)
	organizationService.StartIdentityDeletionWorker(ctx, authService.DeleteRetiredIdentity)
	alemHandler.SetLimitsService(organizationService)
	organizationHandler := handler.NewOrganizationHandler(organizationService, logger)
	tenantHostMiddleware := middleware.NewTenantHostMiddleware(cfg.TenantBaseDomain, organizationRepo, logger)
	authHandler.SetTenantHostMiddleware(tenantHostMiddleware)
	authHandler.SetTenantHandoffStore(auth.NewTenantHandoffStore(redisClient))
	organizationIdentityProviderRepo := repository.NewOrganizationIdentityProviderRepository(db, logger)
	authHandler.SetOrganizationIdentityProviderResolver(organizationIdentityProviderRepo)
	organizationIdentityProviderService := service.NewOrganizationIdentityProviderService(organizationIdentityProviderRepo, kcAdminClient, cfg.Keycloak.Realm, logger)
	organizationIdentityProviderHandler := handler.NewOrganizationIdentityProviderHandler(organizationIdentityProviderService, logger)

	var googleCalendarHandler *handler.GoogleCalendarHandler
	if googleCalendarService != nil {
		googleCalendarHandler = handler.NewGoogleCalendarHandler(googleCalendarService, logger)
	}

	var idpHandler *handler.IDPHandler
	if idpOAuthService != nil {
		idpHandler = handler.NewIDPHandler(idpOAuthService, logger)
	}

	appleCalendarHandler := handler.NewAppleCalendarHandler(appleCalendarService, logger)

	var fileHandler *handler.FileHandler
	if fileService != nil {
		fileHandler = handler.NewFileHandler(fileService, logger)
	}

	var driveHandler *handler.DriveHandler
	if driveService != nil {
		driveHandler = handler.NewDriveHandler(driveService, driveSearchService, cfg.App.PublicURL, cfg.JWT.Secret, logger)
	}

	smartAgentService := service.NewSmartAgentService(cfg, logger, userRepo, calendarService, kanbanService, chatService, meetingService, redisClient, boardRepo)
	smartAgentService.SetPaidAccounting(paidCallerService, userRepo)
	smartAgentService.SetFileSearch(driveSearchService)
	if cfg.Agent.Enabled {
		alemService.SetChatAgent(smartAgentService)
	}
	agentTools := agentcore.BuildDefaultTools(agentcore.ToolDependencies{
		Notifications: notifRepo,
		UserRepo:      userRepo,
		Calendar:      calendarService,
		Kanban:        kanbanService,
		BoardRepo:     boardRepo,
	})
	agentService := agentcore.NewService(
		cfg.Agent,
		logger,
		agentRepo,
		agentcore.NewKeywordIntentResolver(cfg.Agent.IntentConfidenceThreshold),
		agentcore.NewSimpleEntityExtractor(),
		agentcore.NewPermissionChecker(),
		agentcore.NewToolRegistry(agentTools...),
		agentcore.NewResponseBuilder(),
		agentcore.NewAuditLogger(agentRepo),
		smartAgentService,
	)
	smartAgentHandler := handler.NewSmartAgentHandler(agentService, logger)

	// --- Keycloak verifier init (one-time on startup) ---
	jwksURL := cfg.Keycloak.JWKSURL
	if jwksURL == "" {
		jwksURL = keycloak.BuildJWKSURL(cfg.Keycloak.ServerURL, cfg.Keycloak.Realm)
	}
	issuer := cfg.Keycloak.Issuer
	if issuer == "" {
		issuer = keycloak.BuildIssuer(kcPublicURL, cfg.Keycloak.Realm)
	}

	var kcAuthMiddleware *middleware.KeycloakAuthMiddleware
	kcVerifier, err := keycloak.NewVerifier(ctx, jwksURL, issuer, cfg.Keycloak.TrustedAudiences, cfg.Keycloak.AllowedAZP)
	if err != nil {
		logger.Warn("Failed to init Keycloak verifier (Keycloak realm may not be configured yet), standalone auth active", zap.Error(err))
	} else {
		kcAuthMiddleware = middleware.NewKeycloakAuthMiddleware(kcVerifier, userRepo, logger, tokenManager)
		logger.Info("Keycloak verifier initialized",
			zap.String("jwks_url", jwksURL),
			zap.String("issuer", issuer),
			zap.Strings("trusted_audiences", cfg.Keycloak.TrustedAudiences),
			zap.Strings("allowed_azp", cfg.Keycloak.AllowedAZP),
		)
	}

	authHandler.SetODOHandoffAccessCheck(func(ctx context.Context, token string) error {
		if claims, err := tokenManager.ValidateToken(ctx, token); err == nil {
			return organizationService.RequireUserModule(ctx, claims.UserID, "odo")
		}
		if kcVerifier == nil {
			return apperrors.TokenInvalid()
		}
		_, claims, err := kcVerifier.Parse(ctx, token)
		if err != nil {
			return apperrors.TokenInvalid()
		}
		sub, _ := claims["sub"].(string)
		if sub == "" {
			return apperrors.TokenInvalid()
		}
		user, err := userRepo.GetByKeycloakID(ctx, sub)
		if err != nil {
			return err
		}
		if user == nil {
			return apperrors.Forbidden("user not found")
		}
		return organizationService.RequireUserModule(ctx, user.ID, "odo")
	})
	externalAdminMiddleware := middleware.NewExternalAdminMiddleware(cfg.ExternalAdmin, logger)

	// Initialize middleware
	authMiddleware := middleware.NewAuthMiddleware(tokenManager, logger)
	tokenManager.SetUserActiveLookup(userRepo.IsUserActive)
	tokenManager.SetCredentialLookup(recoveryRepo.TokenAllowed)
	var rateLimiter *middleware.RateLimiter
	if cfg.Security.RateLimitEnabled {
		rateLimiter = middleware.NewRateLimiter(
			redisClient,
			logger,
			cfg.Security.RateLimitReqs,
			cfg.Security.RateLimitWindow,
			cfg.Security.RateLimitEnabled,
		)
	} else {
		// Create a disabled rate limiter
		rateLimiter = middleware.NewRateLimiter(nil, logger, 0, 0, false)
	}
	var agentRateLimiter *middleware.RateLimiter
	if cfg.Agent.RateLimitPerMinute > 0 {
		agentRateLimiter = middleware.NewRateLimiter(
			redisClient,
			logger,
			cfg.Agent.RateLimitPerMinute,
			time.Minute,
			true,
		)
	} else {
		agentRateLimiter = middleware.NewRateLimiter(nil, logger, 0, 0, false)
	}

	// Setup router
	router := server.NewRouter(
		cfg, logger,
		authHandler, externalAdminHandler, externalAdminViewHandler, kcAuthMiddleware, externalAdminMiddleware, chatHandler, chatCommandHandler, groupHandler,
		pinnedHandler, unifiedHandler, searchHandler, messageSyncHandler, fileHandler,
		authMiddleware, rateLimiter, agentRateLimiter, tokenManager,
		wsHandler, whiteboardHandler, deviceHandler, meetingHandler,
		kanbanHandler, calendarHandler, pinAuthHandler, driveHandler, publicAppHandler,
		alemHandler, unifiedAIHandler, folderHandler, reviewHandler, notifHandler, internalNotificationHandler, internalDriveImportHandler, smartAgentHandler, liveKitHandler, e2eeHandler, contentExtractionHandler, notebookHandler, noteHandler, googleCalendarHandler, appleCalendarHandler, emailLogHandler, analyticsHandler, gamificationHandler, chatThemeHandler, personalRecordingHandler, dashboardHandler, channelHandler, userQRHandler, profileSectionHandler, metricsCollector,
		idpHandler,
		aiAssistantHandler,
		deviceObservabilityHandler,
		pubHandler,
		organizationHandler,
		tenantHostMiddleware,
		organizationIdentityProviderHandler,
	)

	dataReader := service.NewHTTPAIUserDataReader(router, map[string]service.AIConnector{
		"odo":  {ForwardHost: true, BaseURL: strings.TrimSpace(os.Getenv("AI_CONTEXT_ODO_URL"))},
		"sd":   {BaseURL: strings.TrimSpace(os.Getenv("SD_ACCESS_API_URL")), Authorize: organizationService.AIReadSDAuthorization},
		"mail": {BaseURL: strings.TrimSpace(os.Getenv("AI_CONTEXT_MAIL_URL"))},
	})
	service.RegisterAIDocumentReaders(dataReader, driveService, fileService, contentExtractionService, aiDocumentService, aiDocumentRepo, aiRepo)
	service.RegisterFileSearchReader(dataReader, driveSearchService)
	alemService.SetUserDataReader(dataReader)

	// Create HTTP server
	httpServer := server.NewHTTPServer(cfg.HTTP, router, logger)
	logger.Info("Application initialized successfully")

	// Create error group for concurrent operations
	g, gCtx := errgroup.WithContext(ctx)
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("AI_DOCUMENT_WORKER_DISABLED")), "true") {
		aiDocumentService.Start(gCtx)
	}
	notebookJobs.Start(gCtx)

	// Start WebSocket hub
	g.Go(func() error {
		wsHub.Run(gCtx)
		return nil
	})

	// Start HTTP server
	g.Go(func() error {
		logger.Info("Starting HTTP server", zap.String("port", cfg.HTTP.Port))
		return httpServer.Start()
	})

	// Handle graceful shutdown
	g.Go(func() error {
		<-gCtx.Done()
		logger.Info("Shutdown signal received, starting graceful shutdown...")

		// Create shutdown context with timeout
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.HTTP.ShutdownTimeout)
		defer cancel()

		// Shutdown HTTP server
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			logger.Error("HTTP server shutdown error", zap.Error(err))
		}
		if err := whiteboardManager.Shutdown(shutdownCtx); err != nil {
			logger.Error("Whiteboard manager shutdown error", zap.Error(err))
		}
		// Notebook jobs were interrupted with gCtx; let them store that state.
		notebookJobsStopped := make(chan struct{})
		go func() {
			notebookJobs.Wait()
			close(notebookJobsStopped)
		}()
		select {
		case <-notebookJobsStopped:
		case <-shutdownCtx.Done():
			logger.Warn("Notebook research jobs did not stop before the shutdown timeout")
		}

		logger.Info("Graceful shutdown completed")
		return nil
	})

	g.Go(func() error {
		calendarService.StartReminderWorker(gCtx)
		return nil
	})
	g.Go(func() error {
		kanbanService.StartDeadlineWorker(gCtx)
		return nil
	})

	g.Go(func() error {
		chatService.StartEphemeralCleaner(gCtx)
		return nil
	})

	if redisClient != nil {
		g.Go(func() error {
			logger.Info("Starting background default password scan...")
			if err := authService.FlagUsersWithDefaultPassword(gCtx, redisClient); err != nil {
				if gCtx.Err() != nil {
					logger.Info("Default password scan canceled by shutdown context")
				} else {
					logger.Error("Default password scan failed", zap.Error(err))
				}
			} else {
				logger.Info("Default password scan completed successfully")
			}
			return nil
		})
	}

	// Wait for all goroutines
	if err := g.Wait(); err != nil {
		logger.Error("Application error", zap.Error(err))
		os.Exit(1)
	}

	logger.Info("Application stopped successfully")
}

// initLogger initializes the logger based on configuration
func initLogger(cfg config.LoggerConfig) (*zap.Logger, error) {
	return config.NewLogger(cfg)
}
