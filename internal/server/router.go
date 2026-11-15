package server

import (
	"context"
	"net/netip"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/auth"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/handler"
	"github.com/west6ide/workspace-backend/internal/metrics"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"github.com/west6ide/workspace-backend/internal/telemetry"
	"go.uber.org/zap"

	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	_ "github.com/west6ide/workspace-backend/docs"
)

// NewRouter creates and configures the HTTP router
func NewRouter(
	cfg *config.Config,
	logger *zap.Logger,
	authHandler *handler.AuthHandler,
	externalAdminHandler *handler.ExternalAdminHandler,
	externalAdminViewHandler *handler.ExternalAdminViewHandler,
	kcAuthMiddleware *middleware.KeycloakAuthMiddleware,
	externalAdminMiddleware *middleware.ExternalAdminMiddleware,
	searchHandler *handler.SearchHandler,
	fileHandler *handler.FileHandler,
	authMiddleware *middleware.AuthMiddleware,
	rateLimiter *middleware.RateLimiter,
	agentRateLimiter *middleware.RateLimiter,
	tokenManager *auth.TokenManager,
	wsHandler *handler.WebSocketHandler,
	whiteboardHandler *handler.WhiteboardHandler,
	deviceHandler *handler.DeviceHandler,
	meetingHandler *handler.MeetingHandler,
	kanbanHandler *handler.KanbanHandler,
	calendarHandler *handler.CalendarHandler,
	pinAuthHandler *handler.PinAuthHandler,
	driveHandler *handler.DriveHandler,
	publicAppHandler *handler.PublicAppHandler,
	alemHandler *handler.AlemHandler,
	unifiedAIHandler *handler.UnifiedAIHandler,
	reviewHandler *handler.ReviewHandler,
	notifHandler *handler.NotificationHandler,
	internalNotificationHandler *handler.InternalNotificationHandler,
	internalDriveImportHandler *handler.InternalDriveImportHandler,
	smartAgentHandler *handler.SmartAgentHandler,
	liveKitHandler *handler.LiveKitHandler,
	e2eeHandler *handler.E2EEHandler,
	contentExtractionHandler *handler.ContentExtractionHandler,
	notebookHandler *handler.NotebookHandler,
	noteHandler *handler.NoteHandler,
	googleCalendarHandler *handler.GoogleCalendarHandler,
	appleCalendarHandler *handler.AppleCalendarHandler,
	emailLogHandler *handler.EmailLogHandler,
	analyticsHandler *handler.AnalyticsHandler,
	gamificationHandler *handler.GamificationHandler,
	personalRecordingHandler *handler.PersonalRecordingHandler,
	dashboardHandler *handler.DashboardHandler,
	userQRHandler *handler.UserQRHandler,
	profileSectionHandler *handler.ProfileSectionHandler,
	metricsCollector *metrics.Collector,
	idpHandler *handler.IDPHandler,
	aiAssistantHandler *handler.AIAssistantHandler,
	deviceObservabilityHandler *handler.DeviceObservabilityHandler,
	pubHandler *handler.PublicationHandler,
	organizationHandler *handler.OrganizationHandler,
	tenantHostMiddleware *middleware.TenantHostMiddleware,
	pingDatabase func(context.Context) error,
	organizationIdentityProviderHandlers ...*handler.OrganizationIdentityProviderHandler,
) *gin.Engine {
	// Set Gin mode
	if cfg.App.Env == "production" {
		gin.SetMode(gin.ReleaseMode)
	} else {
		gin.SetMode(gin.DebugMode)
	}

	router := gin.New()

	// Initialize non-blocking API telemetry tracker
	apiTracker := telemetry.NewAPITracker(cfg.CORS.AllowedOrigins...)
	apiMonitoringHandler := handler.NewAPIMonitoringHandler(apiTracker)

	// Set maximum multipart memory to 1GB to support large video uploads
	router.MaxMultipartMemory = 1024 * 1024 * 1024 // 1GB

	// Global middleware
	router.Use(middleware.RequestID())
	if organizationHandler != nil {
		trusted := []netip.Prefix{}
		for _, raw := range strings.Split(os.Getenv("SECURITY_AUDIT_TRUSTED_PROXIES"), ",") {
			raw = strings.TrimSpace(raw)
			if raw == "" {
				continue
			}
			prefix, err := netip.ParsePrefix(raw)
			if err != nil {
				panic("invalid SECURITY_AUDIT_TRUSTED_PROXIES CIDR")
			}
			trusted = append(trusted, prefix)
		}
		router.Use(middleware.SecurityAudit(organizationHandler, logger, trusted))
	}

	router.Use(middleware.TraceID()) // X-Trace-ID для cross-service request correlation
	if cfg.Metrics.Enabled && metricsCollector != nil {
		router.Use(metricsCollector.Middleware())
	}
	router.Use(middleware.Logger(logger))
	router.Use(middleware.APITracking(apiTracker))
	router.Use(middleware.Recovery(logger))
	router.Use(middleware.CORS(cfg.CORS))
	router.Use(middleware.SecureHeaders())
	router.Use(middleware.Localization(nil))

	// Health check & Monitoring endpoints
	router.Any("/health", healthCheck)
	router.Any("/ready", readyCheck(pingDatabase))
	monitoring := router.Group("/api/admin/monitoring")
	if authMiddleware != nil {
		monitoring.Use(authMiddleware.Authenticate())
	}
	monitoring.GET("", apiMonitoringHandler.ServeDashboard)
	monitoring.GET("/stats", apiMonitoringHandler.GetStats)

	if cfg.Metrics.Enabled && metricsCollector != nil && authMiddleware != nil {
		router.GET("/metrics", authMiddleware.Authenticate(), metricsCollector.Handler())
	}
	router.GET("/auth/login", authHandler.CompleteKeycloakLogin)

	if internalNotificationHandler != nil || internalDriveImportHandler != nil || emailLogHandler != nil {
		internal := router.Group("/api/internal")
		internal.Use(middleware.InternalTokenAuth(cfg.Internal.NotificationToken))
		{
			if internalNotificationHandler != nil {
				internal.POST("/notifications", internalNotificationHandler.AcceptNotification)
			}
			if internalDriveImportHandler != nil {
				internal.POST("/drive/import", internalDriveImportHandler.Import)
			}
			if emailLogHandler != nil {
				internal.POST("/email/test", emailLogHandler.SendTestEmail)
			}
		}
	}

	if authHandler != nil && cfg.Internal.MailToken != "" {
		internalMail := router.Group("/api/internal/mail")
		internalMail.Use(middleware.InternalTokenAuth(cfg.Internal.MailToken))
		if kcAuthMiddleware != nil {
			internalMail.Use(kcAuthMiddleware.Authenticate())
		} else {
			internalMail.Use(authMiddleware.Authenticate())
		}
		internalMail.Use(middleware.Localization(authHandler.LookupUserLanguage))
		{
			internalMail.GET("/oauth/profile", authHandler.MailOAuthProfile)
		}
	}

	if authHandler != nil && cfg.Internal.MeetToken != "" {
		internalMeet := router.Group("/api/internal/meet")
		internalMeet.Use(middleware.InternalTokenAuth(cfg.Internal.MeetToken))
		if kcAuthMiddleware != nil {
			internalMeet.Use(kcAuthMiddleware.Authenticate())
		} else {
			internalMeet.Use(authMiddleware.Authenticate())
		}
		internalMeet.Use(middleware.Localization(authHandler.LookupUserLanguage))
		internalMeet.POST("/users/resolve", authHandler.ResolveMeetUsers)
	}

	if authHandler != nil && cfg.Internal.ODOHandoffToken != "" {
		internalODO := router.Group("/api/internal/odo-handoff")
		internalODO.Use(middleware.InternalTokenAuth(cfg.Internal.ODOHandoffToken))
		internalODO.POST("/redeem", authHandler.RedeemODOHandoff)
	}

	if organizationHandler != nil {
		if token := os.Getenv("IFORM_DIRECTORY_TOKEN"); token != "" {
			internalIForm := router.Group("/api/internal/iform")
			internalIForm.Use(middleware.InternalTokenAuth(token))
			internalIForm.POST("/context", organizationHandler.IFormContext)
			internalIForm.POST("/email-suggestion", organizationHandler.IFormEmailSuggestion)
			internalIForm.POST("/employment-access", organizationHandler.IFormEmploymentAccess)
			if authHandler != nil {
				internalIForm.POST("/operations", func(c *gin.Context) { organizationHandler.IFormOperation(c, authHandler) })
				internalIForm.POST("/onboarding-credentials", func(c *gin.Context) { organizationHandler.IFormOnboardingCredential(c, authHandler) })
			}
		}
		if token := os.Getenv("SD_ACCESS_INTERNAL_TOKEN"); token != "" {
			internalAccess := router.Group("/api/internal/access")
			internalAccess.Use(middleware.InternalTokenAuth(token))
			internalAccess.POST("/resolve-sd", organizationHandler.ResolveSDApplicationAccess)
			internalAccess.POST("/identity-sd", organizationHandler.ResolveSDWorkspaceIdentity)
		}
		if accessToken := os.Getenv("GROWY_ACCESS_CHECK_TOKEN"); accessToken != "" {
			internalGrowy := router.Group("/api/internal/growy")
			internalGrowy.Use(middleware.InternalTokenAuth(accessToken))
			internalGrowy.POST("/access-check", organizationHandler.CheckGrowyAccess)
		}
	}

	// Swagger UI
	router.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	if cfg.ExternalAdmin.Enabled && externalAdminViewHandler != nil {
		router.GET("/external-admin", externalAdminViewHandler.ServeIfEnabled(true))
		router.GET("/external-admin/", externalAdminViewHandler.ServeIfEnabled(true))
		router.GET("/external-admin/login", externalAdminViewHandler.ServeIfEnabled(true))
		router.GET("/external-admin/auth/callback", externalAdminViewHandler.ServeIfEnabled(true))
	}

	// API v1 routes
	if alemHandler != nil {
		jobs := router.Group("/api/internal/ai/jobs/:job_id", alemHandler.AuthenticateWorkspaceAIJob())
		jobs.POST("/provider/chat/completions", alemHandler.WorkspaceAICompletion)
		jobs.POST("/provider/audio/*operation", alemHandler.WorkspaceAIAudio)
	}
	v1 := router.Group("/api/v1")
	{
		// Public, meeting-scoped guests. Workspace APIs below still require authentication.
		v1.Any("/meet-guest/:meetingId/*action", handler.NewMeetGuestHandler(cfg.QMeet))

		// Public file downloads
		v1.GET("/files/download", fileHandler.DownloadFile)
		// Public upload does not grant permission to delete storage objects.
		v1.POST("/files/public/upload", fileHandler.UploadPublicFile)

		// Tools routes
		if contentExtractionHandler != nil {
			v1.POST("/tools/convert-to-text", contentExtractionHandler.ConvertFile)
		}

		// Public Apps routes (Full CRUD, Open API)

		// Drive WOPI callbacks are authenticated by short-lived, file-scoped access tokens.
		if driveHandler != nil {
			v1.GET("/drive/onlyoffice/editor", driveHandler.OnlyOfficeEditorPage)
			v1.GET("/drive/onlyoffice/collaboration", driveHandler.OfficeCollaborationUsers)
			v1.POST("/drive/onlyoffice/collaboration", driveHandler.OfficePresence)
			v1.GET("/drive/wopi/files/:id", driveHandler.WOPICheckFileInfo)
			v1.POST("/drive/wopi/files/:id", driveHandler.WOPILock)
			v1.GET("/drive/wopi/files/:id/contents", driveHandler.WOPIGetFile)
			v1.POST("/drive/wopi/files/:id/contents", driveHandler.WOPIPutFile)

			// Legacy OnlyOffice callback routes are retained during cutover.
			v1.GET("/drive/onlyoffice/download", driveHandler.OnlyOfficeSignedDownload)
			v1.POST("/drive/onlyoffice/callback", driveHandler.OnlyOfficeCallback)

			// Public shared links
			publicDrive := v1.Group("/public/drive")
			{
				publicDrive.GET("/items/:token", driveHandler.GetPublicItem)
				publicDrive.GET("/files/:token/download", driveHandler.DownloadPublicFile)
				publicDrive.GET("/files/:token/preview", driveHandler.PreviewPublicFile)
				publicDrive.GET("/files/:token/preview/info", driveHandler.GetPublicFilePreview)
				publicDrive.GET("/files/:token/preview/office", driveHandler.PreviewPublicOfficeFile)
				publicDrive.HEAD("/files/:token/preview", driveHandler.PreviewPublicFile)
			}
		}

		// Signed invitation actions and timezone metadata do not create events.
		if calendarHandler != nil {
			v1.GET("/calendar/invitations/action", calendarHandler.HandleCalendarInviteAction)
			v1.GET("/calendar/timezones", calendarHandler.GetTimezones)
		}

		// Public Calls routes (Webhooks)
		callsPublic := v1.Group("/calls")
		{
			callsPublic.POST("/webhook", liveKitHandler.HandleWebhook)
		}

		// Email Notifications (Incoming)
		if emailLogHandler != nil {
			v1.POST("/users/notification", emailLogHandler.HandleIncomingEmail)
			v1.GET("/users/notifications", emailLogHandler.GetLogs)

			emailGroup := v1.Group("/email")
			{
				emailGroup.POST("/mappings", emailLogHandler.CreateMapping)
				emailGroup.GET("/mappings", emailLogHandler.GetMappings)
				emailGroup.DELETE("/mappings/:id", emailLogHandler.DeleteMapping)
			}
		}

		if e2eeHandler != nil {
			webPairing := v1.Group("/web/pairing")
			{
				webPairing.POST("/sessions", e2eeHandler.CreateWebPairingSession)
				webPairing.GET("/sessions/:pairing_id", e2eeHandler.GetWebPairingSessionStatus)
				webPairing.POST("/sessions/:pairing_id/redeem", e2eeHandler.RedeemWebPairingSession)
			}
		}

		// Auth routes (no auth required)
		auth := v1.Group("/auth")
		{
			auth.POST("/tenant-handoff/redeem", authHandler.RedeemTenantHandoff)
			auth.POST("/register", authHandler.Register)
			invitations := auth.Group("/invitations")
			if rateLimiter != nil {
				invitations.Use(rateLimiter.Limit())
			}
			invitations.POST("/preview", authHandler.PreviewInvitation)
			invitations.POST("/code", authHandler.SendInvitationCode)
			invitations.POST("/register", authHandler.RegisterByInvitation)
			recovery := auth.Group("/recovery")
			recovery.POST("/request", authHandler.RequestPasswordRecovery)
			recovery.POST("/preview", authHandler.PreviewPasswordRecovery)
			recovery.POST("/complete", authHandler.CompletePasswordRecovery)
			auth.POST("/login", authHandler.Login)
			if cfg.Security.RateLimitEnabled && rateLimiter != nil {
				auth.GET("/login-options", rateLimiter.Limit(), authHandler.LoginOptions)
			} else {
				auth.GET("/login-options", authHandler.LoginOptions)
			}
			auth.GET("/keycloak/login", authHandler.StartKeycloakLogin)
			auth.GET("/keycloak/callback", authHandler.CompleteKeycloakLogin)
			auth.GET("/keycloak/mobile/login", authHandler.StartMobileKeycloakLogin)
			auth.POST("/keycloak/mobile/callback", authHandler.CompleteMobileKeycloakLogin)
			auth.POST("/keycloak/exchange", authHandler.ExchangeKeycloakCode)
			auth.POST("/keycloak/bootstrap", authHandler.BootstrapKeycloakSession)
			// Silent SSO check: prompt=none flow backed by Keycloak browser SSO cookies.
			auth.GET("/keycloak/sso-check", authHandler.StartKeycloakSilentCheck)
			auth.GET("/keycloak/sso-check/callback", authHandler.CompleteKeycloakSilentCheck)
			// Backward-compatible aliases.
			auth.GET("/keycloak/silent", authHandler.StartKeycloakSilentCheck)
			auth.GET("/keycloak/silent/callback", authHandler.CompleteKeycloakSilentCheck)
			// Browser-redirect logout: clears app cookies AND Keycloak SSO session cookies
			// Use this endpoint instead of POST /logout for web browser clients
			auth.GET("/keycloak/logout", authHandler.KeycloakLogoutRedirect)
			auth.PUT("/users/avatar", authHandler.UpdateUserAvatar)
			auth.POST("/pin/login-by-pin", pinAuthHandler.LoginByPin)

			// Refresh token (requires refresh token)
			auth.POST("/refresh", authHandler.RefreshToken)
			auth.POST("/logout", authHandler.Logout)
			auth.POST("/logout-all", authHandler.LogoutAll)

			// Sync users
			auth.POST("/sync-keycloak", authHandler.SyncUsersWithKeycloak)
		}

		if cfg.ExternalAdmin.Enabled && authHandler != nil {
			externalAdminAuth := v1.Group("/external-admin/auth")
			{
				externalAdminAuth.GET("/keycloak/login", authHandler.StartExternalAdminKeycloakLogin)
				externalAdminAuth.GET("/keycloak/callback", authHandler.CompleteExternalAdminKeycloakLogin)
				externalAdminAuth.GET("/keycloak/logout", authHandler.ExternalAdminKeycloakLogoutRedirect)
			}
		}

		if idpHandler != nil && cfg.IDP.EnableDebugAPI {
			idp := v1.Group("/idp")
			{
				idp.GET("/auth-url", idpHandler.GetAuthURL)
				idp.POST("/token", idpHandler.ExchangeToken)
				idp.POST("/token/refresh", idpHandler.RefreshToken)
				idp.GET("/user/basic", idpHandler.GetUserBasic)
				idp.GET("/user/phone", idpHandler.GetUserPhone)
				idp.GET("/user/email", idpHandler.GetUserEmail)
			}
		}

		// Protected routes (require authentication)
		if kanbanHandler != nil {
			// Service authentication is separate from browser/local JWT auth.
			// Missing config fails closed; the legacy user-token endpoint is not exposed.
			odoSync := v1.Group("/internal/documentolog")
			if cfg.Security.RateLimitEnabled {
				odoSync.Use(rateLimiter.Limit())
			}
			odoSync.POST("/assignments/upsert", kcAuthMiddleware.AuthenticateODOAssignments(cfg.Internal), kanbanHandler.UpsertODOServiceAssignment)
			odoSync.POST("/assignments/backfill", kcAuthMiddleware.AuthenticateODOAssignments(cfg.Internal), kanbanHandler.BackfillODOAssignment)
		}
		protected := v1.Group("")
		if kcAuthMiddleware != nil {
			protected.Use(kcAuthMiddleware.Authenticate())
		} else {
			protected.Use(authMiddleware.Authenticate())
		}

		if authHandler != nil {
			protected.Use(middleware.Localization(authHandler.LookupUserLanguage))
		}

		if cfg.Security.RateLimitEnabled {
			protected.Use(rateLimiter.Limit())
		}
		if tenantHostMiddleware != nil {
			protected.Use(tenantHostMiddleware.Validate())
		}
		protected.POST("/auth/tenant-handoff", authHandler.CreateTenantHandoff)
		protected.POST("/auth/invitations/accept", authHandler.AcceptInvitation)
		for _, path := range []string{"/organizations/current/invitations", "/platform/organizations/:organization_id/invitations"} {
			group := protected.Group(path)
			group.GET("", authHandler.ListInvitations)
			group.POST("", authHandler.CreateInvitation)
			group.POST("/:invitation_id/resend", authHandler.ResendInvitation)
			group.DELETE("/:invitation_id", authHandler.RevokeInvitation)
		}
		if authHandler != nil {
			protected.Any("/meet/*path", authHandler.MeetProxy(cfg.QMeet))
		}

		{
			if cfg.ExternalAdmin.Enabled && externalAdminHandler != nil && externalAdminMiddleware != nil {
				externalAdmin := protected.Group("/external-admin")
				externalAdmin.Use(externalAdminMiddleware.RequireAccess())
				{
					externalAdmin.GET("/users", externalAdminHandler.ListUsers)
					externalAdmin.GET("/users/:user_id", externalAdminHandler.GetUser)
					externalAdmin.POST("/users", externalAdminHandler.CreateUser)
					externalAdmin.PATCH("/users/:user_id", externalAdminHandler.UpdateUser)
					externalAdmin.POST("/users/:user_id/reset-password", externalAdminHandler.ResetPassword)
					externalAdmin.POST("/users/:user_id/logout-all", externalAdminHandler.LogoutAllSessions)
				}
			}

			if organizationHandler != nil {
				protected.Any("/iform/api/*path", organizationHandler.ProxyIFormAPI)
				protected.Any("/growy/api/*path", organizationHandler.ProxyGrowyAPI)
			}

			// Auth protected
			authProtected := protected.Group("/auth")
			{
				authProtected.GET("/me", authHandler.Me)
				authProtected.PUT("/me/language", authHandler.UpdateLanguage)
				if organizationHandler != nil {
					authProtected.POST("/growy/handoff", organizationHandler.CreateGrowyHandoff)
					authProtected.POST("/sd-handoff", organizationHandler.CreateSDHandoff)
					authProtected.POST("/odo-handoff", organizationHandler.RequireModule("odo"), authHandler.CreateODOHandoff)
				}
				authProtected.PUT("/me/avatar", authHandler.UpdateAvatar)
				authProtected.PUT("/me/email", authHandler.UpdateEmail)
				authProtected.PUT("/password", authHandler.ChangePassword)
				authProtected.GET("/recovery-email", authHandler.RecoveryEmailState)
				authProtected.POST("/recovery-email/code", authHandler.BeginRecoveryEmail)
				authProtected.POST("/recovery-email/confirm", authHandler.ConfirmRecoveryEmail)
				authProtected.POST("/pin/setup", pinAuthHandler.Setup)
				authProtected.POST("/pin/sync-refresh", pinAuthHandler.SyncRefreshToken)
				authProtected.POST("/onboarding/complete", authHandler.CompleteOnboarding)
				authProtected.GET("/users/never-logged-in", authHandler.GetNeverLoggedInUsers)
			}

			// Customer organization builder. All resources are resolved from the
			// authenticated user's organization; cross-tenant IDs are rejected in
			// the repository before any mutation is made.
			if organizationHandler != nil {
				protected.POST("/contacts/import", organizationHandler.ImportContacts)
				protected.GET("/contacts/directory", organizationHandler.ContactDirectoryConfig)
				protected.PUT("/contacts/directory", organizationHandler.SaveContactDirectoryConfig)
				protected.POST("/contacts/directory/sync", organizationHandler.SyncContactDirectory)
				protected.GET("/contacts", organizationHandler.ListContacts)
				protected.GET("/contacts/filters", organizationHandler.ContactFilters)
				protected.GET("/contacts/:user_id", organizationHandler.GetContactProfile)
				protected.GET("/contacts/:user_id/actions", organizationHandler.ContactActions)
				protected.PATCH("/contacts/:user_id", organizationHandler.UpdateContact)
				protected.POST("/contacts/:user_id/avatar", organizationHandler.UploadContactAvatar)
				protected.GET("/contacts/:user_id/reports", organizationHandler.DirectReports)
				protected.POST("/contacts/:user_id/offboarding", organizationHandler.OffboardContact)
				protected.GET("/contacts/:user_id/offboarding", organizationHandler.ContactOffboarding)
				organizations := protected.Group("/organizations")
				{
					organizations.POST("", organizationHandler.CreateOrganization)
					organizations.GET("/current", organizationHandler.GetCurrentOrganization)
					organizations.GET("/current/sd-ticket-count", organizationHandler.SDOpenTicketCount)
					if len(organizationIdentityProviderHandlers) > 0 && organizationIdentityProviderHandlers[0] != nil {
						providers := organizations.Group("/current/identity-providers")
						providers.GET("", organizationIdentityProviderHandlers[0].List)
						providers.POST("", organizationIdentityProviderHandlers[0].Create)
						providers.PATCH("/:provider_id", organizationIdentityProviderHandlers[0].Update)
						providers.DELETE("/:provider_id", organizationIdentityProviderHandlers[0].Delete)
					}

					organizations.GET("/current/security-audit", organizationHandler.SecurityAudit)
					organizations.GET("/current/security-audit/export", organizationHandler.ExportSecurityAudit)
					organizations.GET("/current/administration/overview", organizationHandler.AdministrationOverview)
					organizations.GET("/current/administration/audit", organizationHandler.ListAdministrationAudit)
					organizationHandler.RegisterAIBudgetRoutes(organizations.Group("/current/administration"), false)
					organizations.GET("/current/administration/storage", organizationHandler.AdministrationStorage)
					organizations.GET("/current/administration/storage/users", organizationHandler.AdministrationStorageUsers)
					organizations.PUT("/current/administration/storage/users/:user_id", organizationHandler.SetAdministrationUserStorage)
					organizations.GET("/current/administration/modules", organizationHandler.ListAdministrationModules)
					organizations.PUT("/current/administration/modules/:application", organizationHandler.SetAdministrationModule)
					organizations.GET("/current/administration/users", organizationHandler.ListAdministrationUsers)
					organizations.PUT("/current/administration/users/:user_id/block", organizationHandler.SetEmployeeBlocked)
					organizations.DELETE("/current/administration/users/:user_id", organizationHandler.DeleteAdministrationUser)
					organizations.GET("/current/access", organizationHandler.GetOwnApplicationAccess)
					organizations.GET("/current/access-management", organizationHandler.GetOrganizationAccess)
					organizations.GET("/current/employment-timezone", organizationHandler.EmploymentTimezone)
					organizations.PUT("/current/employment-timezone", organizationHandler.EmploymentTimezone)
					organizations.PUT("/current/access-management/:action", organizationHandler.ChangeOrganizationAccess)
					organizations.GET("/current/units", organizationHandler.ListUnits)
					organizations.GET("/current/tree", organizationHandler.OrganizationTree)
					organizations.GET("/current/users", organizationHandler.ListMembers)
					organizations.GET("/current/growy-role-mappings", organizationHandler.ListGrowyPositionRoles)
					organizations.PUT("/current/growy-role-mappings", organizationHandler.SaveGrowyPositionRoles)
					organizations.POST("/current/units", organizationHandler.CreateUnit)
					organizations.PATCH("/current/units/:unit_id", organizationHandler.UpdateUnit)
					organizations.DELETE("/current/units/:unit_id", organizationHandler.ArchiveUnit)
					organizations.PUT("/current/users/:user_id/assignment", organizationHandler.AssignUser)
				}

				// Platform tenant administration. These endpoints are backed by an
				// explicit server-side platform-admin check and work even when the
				// platform administrator has no current organization.
				platform := protected.Group("/platform/organizations")
				protected.GET("/platform/security-audit", organizationHandler.SecurityAudit)
				protected.GET("/platform/security-audit/export", organizationHandler.ExportSecurityAudit)
				protected.GET("/platform/administration/overview", organizationHandler.PlatformAdministrationOverview)
				protected.GET("/platform/administration/audit", organizationHandler.ListAdministrationAudit)
				protected.GET("/platform/ai/users/:user_id/quota", organizationHandler.PlatformPersonalAIQuota)
				protected.PUT("/platform/ai/users/:user_id/quota", organizationHandler.PlatformPersonalAIQuota)
				{
					platform.GET("", organizationHandler.ListPlatformOrganizations)
					platform.DELETE("/:organization_id", organizationHandler.DeletePlatformOrganization)
					platform.GET("/:organization_id/security-audit", organizationHandler.SecurityAudit)
					platform.GET("/:organization_id/security-audit/export", organizationHandler.ExportSecurityAudit)
					platform.GET("/:organization_id/administration/overview", organizationHandler.AdministrationOverview)
					platform.GET("/:organization_id/administration/audit", organizationHandler.ListAdministrationAudit)
					organizationHandler.RegisterAIBudgetRoutes(platform.Group("/:organization_id/administration"), true)
					platform.GET("/:organization_id/administration/storage/capacity", organizationHandler.AdministrationStorageCapacity)
					platform.GET("/:organization_id/administration/storage", organizationHandler.AdministrationStorage)
					platform.PUT("/:organization_id/administration/storage", organizationHandler.SetAdministrationStorage)
					platform.GET("/:organization_id/administration/storage/users", organizationHandler.AdministrationStorageUsers)
					platform.PUT("/:organization_id/administration/storage/users/:user_id", organizationHandler.SetAdministrationUserStorage)
					platform.GET("/:organization_id/administration/modules", organizationHandler.ListAdministrationModules)
					platform.PUT("/:organization_id/administration/modules/:application", organizationHandler.SetAdministrationModule)
					platform.GET("/:organization_id/administration/users", organizationHandler.ListAdministrationUsers)
					platform.PUT("/:organization_id/administration/users/:user_id/block", organizationHandler.SetEmployeeBlocked)
					platform.DELETE("/:organization_id/administration/users/:user_id", organizationHandler.DeleteAdministrationUser)
					platform.GET("/:organization_id/access-management", organizationHandler.GetOrganizationAccess)
					platform.GET("/:organization_id/employment-timezone", organizationHandler.EmploymentTimezone)
					platform.PUT("/:organization_id/employment-timezone", organizationHandler.EmploymentTimezone)
					platform.GET("/:organization_id/access-management/sd-companies", organizationHandler.GetAccessSDCompanies)
					platform.POST("/:organization_id/access-management/sd-company", organizationHandler.ProvisionSDCompany)
					platform.PUT("/:organization_id/access-management/:action", organizationHandler.ChangeOrganizationAccess)
					platform.POST("", organizationHandler.CreatePlatformOrganization)
					platform.GET("/owner-candidates", organizationHandler.ListPlatformOrganizationOwnerCandidates)
					if len(organizationIdentityProviderHandlers) > 0 && organizationIdentityProviderHandlers[0] != nil {
						identityProviders := platform.Group("/:organization_id/identity-providers")
						identityProviders.GET("", organizationIdentityProviderHandlers[0].List)
						identityProviders.POST("", organizationIdentityProviderHandlers[0].Create)
						identityProviders.PATCH("/:provider_id", organizationIdentityProviderHandlers[0].Update)
						identityProviders.DELETE("/:provider_id", organizationIdentityProviderHandlers[0].Delete)
					}
					platform.GET("/:organization_id/units", organizationHandler.ListPlatformOrganizationUnits)
					platform.GET("/:organization_id/users", organizationHandler.ListPlatformOrganizationMembers)
					platform.GET("/:organization_id/growy-role-mappings", organizationHandler.ListPlatformGrowyPositionRoles)
					platform.PUT("/:organization_id/growy-role-mappings", organizationHandler.SavePlatformGrowyPositionRoles)
					platform.POST("/:organization_id/units", organizationHandler.CreatePlatformOrganizationUnit)
					platform.PATCH("/:organization_id/units/:unit_id", organizationHandler.UpdatePlatformOrganizationUnit)
					platform.DELETE("/:organization_id/units/:unit_id", organizationHandler.ArchivePlatformOrganizationUnit)
					platform.PUT("/:organization_id/users/:user_id/assignment", organizationHandler.AssignPlatformOrganizationUser)
					platform.GET("/:organization_id", organizationHandler.GetPlatformOrganization)
					platform.PATCH("/:organization_id", organizationHandler.UpdatePlatformOrganization)
					platform.PUT("/:organization_id/owner", organizationHandler.AssignPlatformOrganizationOwner)
				}
			}

			// User routes
			protected.GET("/users", authHandler.GetAllUsers)

			protected.GET("/users/online", authHandler.GetOnlineUsers)
			if userQRHandler != nil {
				protected.POST("/users/me/qr", userQRHandler.CreateOrRefreshMyQR)
				protected.POST("/users/qr/scan", userQRHandler.ScanQR)
			}
			protected.POST("/users/profile/education", profileSectionHandler.CreateEducation)
			protected.GET("/users/profile/education", profileSectionHandler.ListMyEducations)
			protected.GET("/users/profile/education/:education_id", profileSectionHandler.GetMyEducation)
			protected.PUT("/users/profile/education/:education_id", profileSectionHandler.UpdateEducation)
			protected.DELETE("/users/profile/education/:education_id", profileSectionHandler.DeleteEducation)
			protected.GET("/users/:user_id/profile/education", profileSectionHandler.ListUserEducations)
			protected.GET("/users/:user_id/profile/education/:education_id", profileSectionHandler.GetUserEducation)
			protected.POST("/users/profile/licenses-certifications", profileSectionHandler.CreateLicenseCertification)
			protected.GET("/users/profile/licenses-certifications", profileSectionHandler.ListMyLicenseCertifications)
			protected.GET("/users/profile/licenses-certifications/:license_id", profileSectionHandler.GetMyLicenseCertification)
			protected.PUT("/users/profile/licenses-certifications/:license_id", profileSectionHandler.UpdateLicenseCertification)
			protected.DELETE("/users/profile/licenses-certifications/:license_id", profileSectionHandler.DeleteLicenseCertification)
			protected.GET("/users/:user_id/profile/licenses-certifications", profileSectionHandler.ListUserLicenseCertifications)
			protected.POST("/users", authHandler.CreateUser)
			protected.PUT("/users/status", authHandler.ChangeStatus) // Change own status
			protected.PUT("/users/:user_id", authHandler.UpdateUserAdmin)
			protected.DELETE("/users/:user_id", authHandler.DeleteUser)
			protected.GET("/users/:user_id/details", authHandler.GetUserFullDetails)

			if emailLogHandler != nil {
				protected.POST("/admin/email/test", emailLogHandler.SendTestEmail)
			}

			calls := protected.Group("/calls")
			{
				calls.GET("/telemetry", liveKitHandler.GetTelemetry)
				calls.GET("/debug/livekit", liveKitHandler.GetLiveKitDebug)
				calls.POST("/start", liveKitHandler.StartCall)
				calls.POST("/assistant/start", liveKitHandler.StartAssistantCall)
				calls.POST("/accept", liveKitHandler.AcceptCall)
				calls.POST("/join", liveKitHandler.JoinRoom)
				calls.POST("/kick", liveKitHandler.KickParticipant)
				calls.POST("/mute-all", liveKitHandler.MuteAll)
				calls.POST("/decline", liveKitHandler.DeclineCall)
				calls.POST("/end", liveKitHandler.EndCall)
				calls.POST("/invite", liveKitHandler.InviteToCall)
				calls.POST("/rejoin", liveKitHandler.RejoinCall)
				if calendarHandler != nil {
					calls.POST("/calendar/rejoin", calendarHandler.RejoinCalendarMeeting)
				}

				// Video recording (RoomComposite, MP4)
				calls.POST("/record", liveKitHandler.StartRecording)
				calls.DELETE("/record/:id", liveKitHandler.StopRecording)

				// Audio recording (RoomComposite AudioOnly, OGG/Opus) ─ best practice
				calls.GET("/recordings/audio", liveKitHandler.ListUserRecordings)
				calls.POST("/record/audio", liveKitHandler.StartAudioRecording)
				calls.DELETE("/record/audio/:id", liveKitHandler.StopAudioRecording)
				calls.GET("/record/audio/:id/status", liveKitHandler.GetAudioRecordingStatus)
				calls.GET("/record/audio/:id/download", liveKitHandler.DownloadAudioRecording)

				// Call info
				calls.GET("/:id/status", liveKitHandler.GetCallStatus)
				calls.GET("/:id/participants", liveKitHandler.GetCallParticipants)
				calls.GET("/:id/recordings", liveKitHandler.ListCallRecordings)

				// Personal recordings (per-participant)
				if personalRecordingHandler != nil {
					calls.POST("/recordings/personal/start", personalRecordingHandler.StartPersonalRecording)
					calls.POST("/recordings/personal/:recording_id/stop", personalRecordingHandler.StopPersonalRecording)
					calls.GET("/recordings/personal", personalRecordingHandler.ListMyPersonalRecordings)
					calls.GET("/recordings/personal/:recording_id/download", personalRecordingHandler.DownloadPersonalRecording)
					calls.GET("/recordings/personal/active", personalRecordingHandler.GetActiveRecordingsForCall)
				}
			}

			if driveHandler != nil {
				registerDriveRoutes(protected, driveHandler)
			}

			if notifHandler != nil {
				protected.GET("/notifications", notifHandler.GetMyNotifications)
				protected.POST("/notifications/:id/read", notifHandler.MarkAsRead)
				protected.POST("/notifications/read-all", notifHandler.MarkAllAsRead)
				protected.POST("/notifications/incoming-call", notifHandler.SendIncomingCallPush)
				protected.GET("/calls", notifHandler.GetMyCalls)
				protected.GET("/calls/active", notifHandler.GetMyActiveCalls)
				protected.POST("/notifications/call-cancel", notifHandler.SendCallCancelPush)
			}

			reviews := protected.Group("/reviews")
			{
				reviews.POST("", reviewHandler.CreateReview)
				reviews.GET("/my-stats", reviewHandler.GetMyStats)
			}

			e2ee := protected.Group("/e2ee")
			{
				e2ee.POST("/keys", e2eeHandler.UploadKey)
				e2ee.GET("/keys/:user_id", e2eeHandler.GetKeys)
				// Key Backup — для восстановления ключей при смене устройства
				e2ee.POST("/backup", e2eeHandler.SaveKeyBackup)
				e2ee.GET("/backup", e2eeHandler.GetKeyBackup)
				e2ee.DELETE("/backup", e2eeHandler.DeleteKeyBackup)
				// Password-login web device registration (без QR)
				// Используется когда пользователь входит через логин/пароль на web UI
				// и хочет расшифровывать E2EE сообщения через Key Backup + мастер-пароль.
				e2ee.POST("/device/register", e2eeHandler.RegisterWebDevice)
				e2ee.POST("/web/pairing/sessions/:pairing_id/preview", e2eeHandler.PreviewWebPairingSession)
				e2ee.POST("/web/pairing/sessions/:pairing_id/approve", e2eeHandler.ApproveWebPairingSession)
				e2ee.POST("/web/pairing/sessions/:pairing_id/reject", e2eeHandler.RejectWebPairingSession)
				e2ee.GET("/devices", e2eeHandler.ListLinkedDevices)
				e2ee.DELETE("/devices/:device_id", e2eeHandler.RevokeLinkedDevice)
			}

			// Search routes
			if searchHandler != nil {
				search := protected.Group("/search")
				{
					search.GET("/users", searchHandler.SearchUsers)
					search.GET("/all", searchHandler.SearchAll)
					search.GET("", searchHandler.SearchGlobal)
				}
			}

			if analyticsHandler != nil {
				analytics := protected.Group("/analytics")
				{
					analytics.GET("/logins", analyticsHandler.GetLoginAnalytics)
					analytics.POST("/section-visits", analyticsHandler.TrackSectionVisit)
				}
			}

			// File routes
			if fileHandler != nil {
				files := protected.Group("/files")
				{
					files.POST("/upload", fileHandler.UploadFile)
					files.GET("/url", fileHandler.GetFileURL)
					files.DELETE("", fileHandler.DeleteFile)
					files.DELETE("/public", fileHandler.DeletePublicFile)
				}
			}

			// Public Apps (Protected)
			if publicAppHandler != nil {
				publicApps := protected.Group("/public-apps")
				{
					// Favorites
					fav := publicApps.Group("/favorites")
					{
						fav.POST("", publicAppHandler.AddFavorite)
						fav.GET("", publicAppHandler.GetFavorites)
						fav.DELETE("/:app_id", publicAppHandler.RemoveFavorite)
					}

					// Main CRUD
					publicApps.GET("", publicAppHandler.GetPublicApps)
					publicApps.POST("", publicAppHandler.CreatePublicApp)
					publicApps.PUT("/reorder", publicAppHandler.Reorder)

					// Parameter based routes (must be after specific static routes if any overlap, though favorities is a sub-path)
					publicApps.GET("/:id", publicAppHandler.GetPublicApp)
					publicApps.POST("/:id/rating", publicAppHandler.RatePublicApp)
					publicApps.DELETE("/:id/rating", publicAppHandler.RemovePublicAppRating)
					publicApps.PUT("/:id", publicAppHandler.UpdatePublicApp)
					publicApps.DELETE("/:id", publicAppHandler.DeletePublicApp)

					// Access routes
					publicApps.POST("/:id/access", publicAppHandler.GrantAccess)
					publicApps.DELETE("/:id/access/:user_id", publicAppHandler.RevokeAccess)
				}
			}

			// Gamification endpoints
			if gamificationHandler != nil {
				protected.GET("/gamification/dashboard", gamificationHandler.GetDashboard)
				protected.GET("/gamification/my-stats", gamificationHandler.GetMyStats)
			}

			// Dashboard endpoints
			if dashboardHandler != nil {
				protected.GET("/dashboard/summary", dashboardHandler.GetSummary)
			}

			// WebSocket route
			if wsHandler != nil {
				protected.GET("/ws", wsHandler.HandleConnection)
			}
			if whiteboardHandler != nil {
				protected.GET("/whiteboards/ws/:roomId", whiteboardHandler.HandleWS)
			}

			// Device routes
			protected.POST("/devices", deviceHandler.RegisterDevice)

			// Client Device Observability routes
			if deviceObservabilityHandler != nil {
				clientDevices := protected.Group("/client/devices")
				{
					clientDevices.POST("/metadata", deviceObservabilityHandler.SaveMetadata)
				}
			}

			// Admin Device Observability routes
			if deviceObservabilityHandler != nil {
				adminObservability := protected.Group("/admin/users")
				{
					adminObservability.GET("", deviceObservabilityHandler.ListUsers)
				}
			}

			// Meeting routes
			if meetingHandler != nil {
				protected.POST("/meetings/link", meetingHandler.CreateMeetingLink)
			}

			// Aggregated task metrics for frontend dashboards
			if kanbanHandler != nil {
				metrics := protected.Group("/metrics")
				{
					metrics.GET("/sla-compliance", kanbanHandler.GetSLACompliance)
					metrics.GET("/cycle-time", kanbanHandler.GetCycleTime)
					metrics.GET("/reopen-rate", kanbanHandler.GetReopenRate)
					metrics.GET("/time-in-status", kanbanHandler.GetTimeInStatus)
					metrics.GET("/workload-distribution", kanbanHandler.GetWorkloadDistribution)
					metrics.GET("/capacity", kanbanHandler.GetCapacity)
					metrics.GET("/burn-down", kanbanHandler.GetBurnDown)
					metrics.GET("/throughput", kanbanHandler.GetThroughput)
					metrics.GET("/efficiency-trend", kanbanHandler.GetEfficiencyTrend)
				}
				protected.GET("/tasks/overdue", kanbanHandler.GetOverdueTasks)
				protected.GET("/tasks/stale", kanbanHandler.GetStaleTasks)
			}

			// Kanban routes
			if kanbanHandler != nil {
				kanban := protected.Group("/kanban")
				{
					kanban.POST("/boards", kanbanHandler.CreateBoard)
					kanban.GET("/boards", kanbanHandler.ListBoards)
					kanban.PUT("/boards/:id", kanbanHandler.UpdateBoard)
					kanban.DELETE("/boards/:id", kanbanHandler.DeleteBoard)
					kanban.GET("/sprints", kanbanHandler.ListUserSprints)
					kanban.GET("/tasks/search", kanbanHandler.SearchTasks)
					kanban.GET("/tasks/my-todo", kanbanHandler.GetMyTodoTasks)
					kanban.GET("/tasks/my", kanbanHandler.ListMyTasks)
					kanban.GET("/tasks/my-by-status", kanbanHandler.GetMyTasksByStatus)
					kanban.GET("/documentolog/assignments", kanbanHandler.ListMyDocumentologAssignments)
					kanban.POST("/documentolog/assignments/:id/complete", kanbanHandler.CompleteODOAssignment)
					kanban.GET("/tasks/my-summary-by-board", kanbanHandler.GetMyTaskBoardsSummary)

					kanban.GET("/boards/:id/stats", kanbanHandler.GetBoardStats)
					kanban.GET("/stats", kanbanHandler.GetMultiBoardStats)
					kanban.GET("/boards/:id", kanbanHandler.GetBoard)
					kanban.GET("/boards/:id/tasks-by-user", kanbanHandler.GetBoardTasksByUser)
					kanban.GET("/assistant/boards/:id/analysis", kanbanHandler.GetAssistantBoardAnalysis)
					kanban.GET("/assistant/boards/:id/users/:userId/tasks", kanbanHandler.GetAssistantBoardUserTasks)
					kanban.GET("/assistant/boards/:id/my-tasks", kanbanHandler.GetAssistantBoardMyTasks)
					kanban.POST("/assistant/boards/:id/members", kanbanHandler.AddAssistantBoardMember)
					kanban.DELETE("/assistant/boards/:id/members/:userId", kanbanHandler.RemoveAssistantBoardMember)
					kanban.POST("/assistant/tasks", kanbanHandler.CreateAssistantTask)
					kanban.PUT("/assistant/tasks/:id/assignee", kanbanHandler.AssignAssistantTask)
					kanban.PUT("/assistant/tasks/:id/status", kanbanHandler.UpdateAssistantTaskStatus)
					kanban.PUT("/boards/:id/background/preset", kanbanHandler.SetBoardBackgroundPreset)
					kanban.POST("/boards/:id/background/image", kanbanHandler.UploadBoardBackgroundImage)
					kanban.DELETE("/boards/:id/background/image", kanbanHandler.RemoveBoardBackgroundImage)
					kanban.GET("/boards/:id/background/image/download", kanbanHandler.GetBoardBackgroundImage)
					kanban.POST("/boards/:id/members", kanbanHandler.AddMembers)
					kanban.DELETE("/boards/:id/members/:userId", kanbanHandler.RemoveMember)
					kanban.PUT("/boards/:id/members/:userId/role", kanbanHandler.UpdateMemberRole)
					kanban.PUT("/boards/:id/members/:userId/team-role", kanbanHandler.UpdateMemberTeamRole)
					kanban.POST("/boards/:id/goals", kanbanHandler.CreateGoal)
					kanban.PUT("/boards/:id/goals/:goalId", kanbanHandler.UpdateGoal)
					kanban.DELETE("/boards/:id/goals/:goalId", kanbanHandler.DeleteGoal)
					kanban.GET("/goals/my", kanbanHandler.ListMyGoals)
					kanban.POST("/boards/:id/projects", kanbanHandler.CreateProject)
					kanban.PUT("/boards/:id/projects/:projectId", kanbanHandler.UpdateProject)
					kanban.DELETE("/boards/:id/projects/:projectId", kanbanHandler.DeleteProject)
					kanban.POST("/boards/:id/sprints", kanbanHandler.CreateSprint)
					kanban.PUT("/boards/:id/sprints/:sprintId", kanbanHandler.UpdateSprint)
					kanban.DELETE("/boards/:id/sprints/:sprintId", kanbanHandler.DeleteSprint)

					kanban.POST("/columns", kanbanHandler.CreateColumn)
					kanban.PUT("/columns/:id", kanbanHandler.UpdateColumn)
					kanban.PUT("/columns/:id/move", kanbanHandler.MoveColumn)
					kanban.DELETE("/columns/:id", kanbanHandler.DeleteColumn)

					kanban.POST("/tasks", kanbanHandler.CreateTask)
					kanban.PUT("/tasks/:id", kanbanHandler.UpdateTask)
					kanban.PUT("/tasks/:id/status", kanbanHandler.UpdateTaskStatus)
					kanban.DELETE("/tasks/:id", kanbanHandler.DeleteTask)
					kanban.PUT("/tasks/:id/move", kanbanHandler.MoveTask)
					kanban.POST("/tasks/:id/attachments", kanbanHandler.AttachFile)
					kanban.GET("/tasks/:id", kanbanHandler.GetTask)
					kanban.GET("/board/filtered", kanbanHandler.GetBoardFiltered)
					kanban.POST("/tasks/:id/files", kanbanHandler.UploadTaskFile)
					kanban.GET("/tasks/:id/files", kanbanHandler.ListTaskFiles)
					kanban.GET("/tasks/:id/files/:fileId/download", kanbanHandler.DownloadTaskFile)
					kanban.DELETE("/tasks/:id/files/:fileId", kanbanHandler.DeleteTaskFile)
					kanban.POST("/boards/:id/labels", kanbanHandler.CreateLabel)
					kanban.GET("/boards/:id/labels", kanbanHandler.ListLabels)
					kanban.DELETE("/labels/:id", kanbanHandler.DeleteLabel)
					kanban.POST("/tasks/:id/labels", kanbanHandler.AddLabelToTask)
					kanban.DELETE("/tasks/:id/labels/:labelId", kanbanHandler.RemoveLabelFromTask)

					kanban.POST("/tasks/:id/worklogs", kanbanHandler.AddWorklog)
					kanban.GET("/tasks/:id/worklogs", kanbanHandler.ListWorklogs)
					kanban.GET("/tasks/:id/history", kanbanHandler.GetTaskHistory)

					// Comments
					kanban.POST("/tasks/:id/comments", kanbanHandler.CreateComment)
					kanbanHandler.RegisterChecklistRoutes(kanban)
					kanban.GET("/tasks/:id/comments", kanbanHandler.ListComments)
					kanban.POST("/tasks/:id/comments/photo", kanbanHandler.UploadCommentPhoto)
					kanban.PUT("/comments/:id", kanbanHandler.UpdateComment)
					kanban.DELETE("/comments/:id", kanbanHandler.DeleteComment)

					// Общая загрузка файлов для Kanban (без привязки к задаче)
					kanban.POST("/tasks/upload", kanbanHandler.UploadFile)
				}
			}

			// Calendar routes
			if calendarHandler != nil {
				cal := protected.Group("/calendar")
				{
					cal.POST("/events", calendarHandler.CreateEvent)
					cal.POST("/events/batch", calendarHandler.BatchCreateEvents)
					// Compatibility URLs share authenticated event creation.
					cal.POST("/public/events", calendarHandler.CreatePublicEvent)
					cal.POST("/events/public", calendarHandler.CreatePublicEvent)
					cal.GET("/calendars", calendarHandler.ListCalendars)
					cal.POST("/calendars", calendarHandler.CreateCalendar)

					// Meeting rooms & resource booking (CAL-006)
					cal.GET("/sites", calendarHandler.ListSites)
					cal.POST("/sites", calendarHandler.CreateSite)
					cal.GET("/sites/:id", calendarHandler.GetSite)
					cal.PATCH("/sites/:id", calendarHandler.UpdateSite)
					cal.DELETE("/sites/:id", calendarHandler.DeleteSite)
					cal.GET("/rooms/unassigned", calendarHandler.ListUnassignedRooms)
					cal.GET("/rooms", calendarHandler.ListRooms)
					cal.POST("/rooms", calendarHandler.CreateRoom)
					cal.GET("/rooms/availability", calendarHandler.GetRoomsAvailability)
					cal.GET("/rooms/:id", calendarHandler.GetRoom)
					cal.PUT("/rooms/:id", calendarHandler.UpdateRoom)
					cal.PATCH("/rooms/:id", calendarHandler.UpdateRoom)
					cal.DELETE("/rooms/:id", calendarHandler.DeleteRoom)
					cal.GET("/rooms/:id/availability", calendarHandler.GetRoomAvailability)
					cal.POST("/rooms/:id/book", calendarHandler.BookRoom)
					cal.DELETE("/rooms/bookings/:booking_id", calendarHandler.CancelRoomBooking)

					cal.POST("/events/check-availability", calendarHandler.CheckAvailability)
					cal.POST("/events/suggest-times", calendarHandler.SuggestTimes)
					cal.POST("/free-busy", calendarHandler.GetFreeBusy)
					cal.GET("/free-busy", calendarHandler.GetFreeBusy)
					cal.POST("/events/free-busy", calendarHandler.GetFreeBusy)
					cal.GET("/events/free-busy", calendarHandler.GetFreeBusy)
					cal.GET("/events", calendarHandler.ListEvents)
					cal.GET("/events/upcoming", calendarHandler.GetUpcomingEvents)
					cal.GET("/events/this-week", calendarHandler.GetThisWeekEvents)
					cal.GET("/events/:id", calendarHandler.GetEvent)
					cal.POST("/events/:id/respond", calendarHandler.RespondToInvite)
					cal.PATCH("/events/:id/response", calendarHandler.RespondToInvite)
					cal.GET("/events/:id/attendees", calendarHandler.GetEventAttendees)
					cal.GET("/today", calendarHandler.GetTodaySummary)
					cal.PUT("/events/:id", calendarHandler.UpdateEvent)
					cal.PATCH("/events/:id", calendarHandler.UpdateEvent)
					cal.DELETE("/events/:id", calendarHandler.DeleteEvent)
					cal.PUT("/events/:id/occurrences/:occurrence_date", calendarHandler.UpdateOccurrence)
					cal.DELETE("/events/:id/occurrences/:occurrence_date", calendarHandler.DeleteOccurrence)
					cal.GET("/events/:id/exceptions", calendarHandler.ListOccurrenceExceptions)
					cal.DELETE("/events/:id/exceptions/:exception_id", calendarHandler.RestoreOccurrence)
					cal.GET("/users/search", calendarHandler.SearchUsers)
					cal.GET("/users/:id/events", calendarHandler.GetUserEvents)
					cal.GET("/users/:id/events/:event_id", calendarHandler.GetUserEvent)
					cal.PUT("/events/:id/reminder", calendarHandler.SetReminder)
					cal.POST("/events/:id/reminder", calendarHandler.SetReminder)
					cal.GET("/events/:id/reminder", calendarHandler.GetReminder)
					cal.DELETE("/events/:id/reminder", calendarHandler.DeleteReminder)
					cal.GET("/reminders/presets", calendarHandler.GetReminderPresets)

					// CAL-007: QMeet integration
					cal.POST("/events/:id/qmeet", calendarHandler.GenerateQMeetLink)

					// CAL-007: Timezone integration
					cal.POST("/timezone/convert", calendarHandler.ConvertTimezones)
					cal.POST("/timezones/convert", calendarHandler.ConvertTimezones)
					cal.GET("/events/search", calendarHandler.SearchEvents)

					// Calendar grid views (day/week/month)
					cal.POST("/grid", calendarHandler.GetCalendarGrid)
					cal.GET("/calendars/settings", calendarHandler.GetCalendarsWithSettings)
					cal.PUT("/calendars/:id/visibility", calendarHandler.UpdateCalendarVisibility)
					cal.GET("/group-calendars/settings", calendarHandler.GetGroupCalendarsWithSettings)
					cal.PUT("/group-calendars/:id/visibility", calendarHandler.UpdateGroupCalendarVisibility)

					// Calendar calls — mobile/web deep-link join
					// POST /api/v1/calendar/calls/join
					// Accepts {"room_name": "..."} and returns livekit_url + livekit_token.
					// This is the canonical endpoint opened after clicking the meeting link:
					//   https://alem-workspace.gov.kz/web/calls/calendar/:roomName
					cal.POST("/calls/join", calendarHandler.JoinCalendarCall)

					// Google Calendar integration routes
					if googleCalendarHandler != nil {
						googleCal := cal.Group("/google")
						{
							googleCal.GET("/auth-url", googleCalendarHandler.GetAuthURL)
							googleCal.POST("/callback", googleCalendarHandler.HandleOAuthCallback)
							googleCal.GET("/status", googleCalendarHandler.GetSyncStatus)
							googleCal.POST("/disconnect", googleCalendarHandler.DisconnectGoogleCalendar)
						}
					}

					// Apple Calendar integration routes
					if appleCalendarHandler != nil {
						appleCal := cal.Group("/apple")
						{
							appleCal.POST("/connect", appleCalendarHandler.ConnectAppleCalendar)
							appleCal.GET("/status", appleCalendarHandler.GetSyncStatus)
							appleCal.POST("/disconnect", appleCalendarHandler.DisconnectAppleCalendar)
						}
					}
				}
			}

		}

		// AI routes (New Implementation)
		if alemHandler != nil {
			ai := protected.Group("/ai")
			{
				ai.GET("/limits", alemHandler.GetAILimits)
				ai.GET("/models", alemHandler.ListModels)
				ai.POST("/provider/chat/completions", alemHandler.WorkspaceAICompletion)
				ai.POST("/provider/audio/*operation", alemHandler.WorkspaceAIAudio)
				ai.POST("/provider/jobs", alemHandler.WorkspaceAIJobGrant)
				ai.GET("/chats", alemHandler.GetUserChats)
				ai.POST("/chats", alemHandler.CreateChat)
				ai.GET("/chats/:id/messages", alemHandler.GetChatHistory)
				ai.POST("/chats/:id/messages", alemHandler.SendMessage)
				ai.DELETE("/chats/:id", alemHandler.DeleteChat)
				ai.PATCH("/chats/:id", alemHandler.PatchChat)
				ai.GET("/sections", alemHandler.ListChatSections)
				ai.POST("/sections", alemHandler.CreateChatSection)
				ai.PATCH("/sections/:sectionId", alemHandler.UpdateChatSection)
				ai.DELETE("/sections/:sectionId", alemHandler.DeleteChatSection)
				ai.POST("/chats/:id/documents", alemHandler.UploadDocument)
				ai.GET("/chats/:id/documents", alemHandler.ListDocuments)
				ai.GET("/chats/:id/documents/:documentId", alemHandler.GetDocument)
				ai.GET("/chats/:id/documents/:documentId/download", alemHandler.DownloadDocument)
				ai.POST("/chats/:id/documents/:documentId/retry", alemHandler.RetryDocument)
				ai.DELETE("/chats/:id/documents/:documentId", alemHandler.DeleteDocument)

				ai.POST("/embeddings", alemHandler.CreateEmbeddings)
				ai.POST("/rerank", alemHandler.Rerank)
			}
		}

		// Unified AI routes (WhatsApp-style singleton AI chat)
		if unifiedAIHandler != nil {
			uai := protected.Group("/unified-ai")
			{
				// Chat management (singleton pattern)
				uai.POST("/chat", unifiedAIHandler.CreateOrGetChat)       // Create or get user's AI chat
				uai.GET("/chats/:id", unifiedAIHandler.GetChat)           // Get chat details
				uai.PUT("/chats/:id/title", unifiedAIHandler.UpdateTitle) // Update chat title
				uai.DELETE("/chats/:id", unifiedAIHandler.DeleteChat)     // Delete entire chat

				// Message management
				uai.GET("/chats/:id/messages", unifiedAIHandler.GetMessages)                  // Get all messages
				uai.POST("/chats/:id/messages", unifiedAIHandler.SendMessage)                 // Send message to AI
				uai.DELETE("/chats/:id/messages", unifiedAIHandler.ClearHistory)              // Clear chat history
				uai.DELETE("/chats/:id/messages/:message_id", unifiedAIHandler.DeleteMessage) // Delete single message

				// AI utilities
				uai.GET("/models", unifiedAIHandler.ListModels)
				uai.POST("/embeddings", unifiedAIHandler.CreateEmbeddings)
				uai.POST("/rerank", unifiedAIHandler.Rerank)
			}
		}

		// AI Assistant routes — компактные AI-ready endpoints для голосового ассистента.
		// Возвращают speech_text + минимальный payload для TTS.
		// Значительно быстрее общих endpoints (меньше данных, меньше JSON parsing).
		if aiAssistantHandler != nil {
			aiAssistant := protected.Group("/ai")
			{
				aiAssistant.POST("/execute", aiAssistantHandler.Execute)
				aiAssistant.GET("/calendar/today", aiAssistantHandler.GetCalendarToday)
				aiAssistant.GET("/tasks/summary", aiAssistantHandler.GetTasksSummary)
				aiAssistant.GET("/contacts/search", aiAssistantHandler.SearchContacts)
			}
		}

		// Smart Agent routes
		if smartAgentHandler != nil {
			agent := protected.Group("/agent")
			if agentRateLimiter != nil {
				agent.Use(agentRateLimiter.Limit())
			}
			{
				agent.POST("/chat", smartAgentHandler.Chat)
				agent.POST("/chat/v2", smartAgentHandler.ChatV2)
				agent.POST("/sessions", smartAgentHandler.CreateSession)
				agent.GET("/sessions", smartAgentHandler.ListSessions)
				agent.GET("/sessions/:sessionId", smartAgentHandler.GetSession)
				agent.GET("/sessions/:sessionId/messages", smartAgentHandler.GetMessages)
				agent.PATCH("/sessions/:sessionId", smartAgentHandler.UpdateSessionTitle)
				agent.DELETE("/sessions/:sessionId", smartAgentHandler.DeleteSession)
				agent.POST("/intent/resolve", smartAgentHandler.ResolveIntent)
				agent.POST("/actions/execute", smartAgentHandler.ExecuteAction)
				agent.POST("/feedback", smartAgentHandler.SaveFeedback)
				agent.GET("/capabilities", smartAgentHandler.Capabilities)
			}
		}

		// Notebook AI routes
		if notebookHandler != nil {
			nb := protected.Group("/notebook")
			{
				nb.POST("/chats", notebookHandler.CreateChat)
				nb.GET("/chats", notebookHandler.GetChats)
				nb.GET("/chats/:id/messages", notebookHandler.GetChatHistory)
				nb.POST("/chats/:id/sync", notebookHandler.SyncMessages)
				nb.POST("/chats/:id/ask", notebookHandler.Ask)
				nb.POST("/chats/:id/ask/jobs", notebookHandler.CreateAskJob)
				nb.GET("/chats/:id/ask/jobs/:job_id", notebookHandler.GetAskJob)
				nb.DELETE("/chats/:id/ask/jobs/:job_id", notebookHandler.CancelAskJob)
				nb.GET("/studio/presets", notebookHandler.StudioPresets)
				nb.POST("/chats/:id/context", notebookHandler.ResearchContext)
				nb.GET("/chats/:id/sources/:doc_id", notebookHandler.SourceExcerpt)
				nb.GET("/chats/:id", notebookHandler.GetChat)
				nb.PUT("/chats/:id", notebookHandler.UpdateChat)
				nb.DELETE("/chats/:id", notebookHandler.DeleteChat)
				nb.POST("/chats/:id/documents", notebookHandler.SaveChatDocuments)
				nb.GET("/chats/:id/documents", notebookHandler.GetChatDocuments)
				nb.POST("/chats/:id/documents/:doc_id", notebookHandler.AddDocument)
				nb.DELETE("/chats/:id/documents/:doc_id", notebookHandler.DeleteDocument)
				nb.POST("/slides/2slides/generate", notebookHandler.GenerateSlidesWithTwoSlides)
				nb.GET("/slides/2slides/jobs/:jobId", notebookHandler.GetTwoSlidesJob)
				nb.GET("/slides/2slides/themes/search", notebookHandler.SearchTwoSlidesThemes)
			}
		}

		if noteHandler != nil {
			notes := protected.Group("/notes")
			{
				notes.GET("/sync", noteHandler.SyncNotes)
				notes.POST("/sync/batch", noteHandler.SyncBatch)
				notes.POST("/folders", noteHandler.CreateFolder)
				notes.GET("/folders", noteHandler.ListFolders)
				notes.PATCH("/folders/:id", noteHandler.UpdateFolder)
				notes.DELETE("/folders/:id", noteHandler.DeleteFolder)

				// Navigation: tags and search
				notes.GET("/tags", noteHandler.GetTags)
				notes.GET("/search", noteHandler.SearchNotes)

				// NTS-003: Reliability - Autosave and Trash
				notes.POST("/autosave", noteHandler.AutosaveNote)
				notes.GET("/trash", noteHandler.ListTrash)
				notes.GET("/trash/count", noteHandler.GetTrashCount)
				notes.DELETE("/trash", noteHandler.EmptyTrash)
				notes.POST("/trash/empty", noteHandler.EmptyTrash)
				notes.POST("/trash/:id/restore", noteHandler.RestoreNote)
				notes.DELETE("/trash/:id", noteHandler.HardDeleteNote)

				notes.POST("", noteHandler.CreateNote)
				notes.POST("/meeting-result", noteHandler.SaveMeetingNote)
				notes.GET("", noteHandler.ListNotes)
				notes.GET("/:id", noteHandler.GetNote)
				notes.PATCH("/:id", noteHandler.UpdateNote)
				notes.PUT("/:id/autosave", noteHandler.AutosaveNote)
				notes.POST("/:id/pin", noteHandler.PinNote)
				notes.POST("/:id/unpin", noteHandler.UnpinNote)
				notes.POST("/:id/favorite", noteHandler.FavoriteNote)
				notes.POST("/:id/unfavorite", noteHandler.UnfavoriteNote)
				notes.PATCH("/:id/favorite", noteHandler.SetFavoriteNote)
				notes.POST("/:id/move", noteHandler.MoveNote)
				notes.DELETE("/:id", noteHandler.DeleteNote)
				notes.DELETE("/:id/tables/:blockId/columns/:colIndex", noteHandler.DeleteTableColumn)
				notes.DELETE("/:id/tables/:blockId/cells/:rowIndex/:colIndex", noteHandler.ClearTableCell)
				notes.POST("/:id/restore", noteHandler.RestoreNote)
				notes.DELETE("/:id/hard", noteHandler.HardDeleteNote)

				// Presigned-URL photo attachments (TZ §5.2)
				notes.POST("/:id/attachments/presign", noteHandler.PresignPhotoUpload)
				notes.POST("/:id/attachments/upload", noteHandler.UploadPhoto)
				notes.POST("/:id/attachments/:attachmentId/complete", noteHandler.CompletePhotoUpload)
				notes.DELETE("/:id/attachments/:attachmentId", noteHandler.DeletePhotoAttachment)

				// Legacy direct-upload attachments
				notes.POST("/:id/attachments", noteHandler.UploadAttachment)
				notes.GET("/:id/attachments", noteHandler.ListAttachments)

				// NTS-004: Audio Notes (dictaphone recording & file uploads)
				notes.POST("/audio", noteHandler.CreateAudioNote)
				notes.POST("/:id/audio", noteHandler.UploadAudio)
				notes.POST("/:id/audio/presign", noteHandler.PresignAudioUpload)
				notes.POST("/:id/audio/:attachmentId/complete", noteHandler.CompleteAudioUpload)
				notes.GET("/:id/audio", noteHandler.ListAudioAttachments)
				notes.GET("/:id/audio/:attachmentId", noteHandler.GetAudioAttachment)
				notes.GET("/:id/audio/:attachmentId/stream", noteHandler.StreamAudioAttachment)
				notes.DELETE("/:id/audio/:attachmentId", noteHandler.DeleteAudioAttachment)
			}
		}

		if pubHandler != nil {
			pubs := protected.Group("/publications")
			{
				pubs.POST("", pubHandler.CreatePublication)
				pubs.POST("/archive/obsolete", pubHandler.ArchiveObsoletePublications)
				pubs.GET("", pubHandler.ListPublications)
				pubs.GET("/banners", pubHandler.ListBanners)
				pubs.GET("/:id", pubHandler.GetPublication)
				pubs.PUT("/:id", pubHandler.UpdatePublication)
				pubs.DELETE("/:id", pubHandler.DeletePublication)
				pubs.GET("/:id/likes", pubHandler.ListPublicationLikes)
				pubs.GET("/:id/activity", pubHandler.ListPublicationActivity)
				pubs.POST("/:id/like", pubHandler.LikePublication)
				pubs.DELETE("/:id/like", pubHandler.UnlikePublication)
				pubs.POST("/:id/reactions", pubHandler.AddReaction)
				pubs.DELETE("/:id/reactions/:reaction", pubHandler.RemoveReaction)
				pubs.GET("/:id/reactions", pubHandler.ListReactions)
				pubs.POST("/:id/view", pubHandler.RecordView)
				pubs.POST("/:id/comments", pubHandler.AddComment)
				pubs.GET("/:id/comments", pubHandler.ListComments)
				pubs.GET("/:id/comments/:comment_id/replies", pubHandler.ListReplies)
				pubs.PUT("/:id/comments/:comment_id/pin", pubHandler.PinComment)
				pubs.DELETE("/:id/comments/:comment_id/pin", pubHandler.UnpinComment)
				pubs.PUT("/:id/comments/:comment_id/moderate", pubHandler.ModerateComment)
				pubs.PUT("/:id/comments/lock", pubHandler.LockComments)
				pubs.POST("/:id/comments/:comment_id/reactions", pubHandler.AddCommentReaction)
				pubs.DELETE("/:id/comments/:comment_id/reactions/:reaction", pubHandler.RemoveCommentReaction)
				pubs.DELETE("/:id/comments/:comment_id", pubHandler.DeleteComment)
				pubs.POST("/:id/acknowledge", pubHandler.AcknowledgePublication)
				pubs.POST("/:id/share", pubHandler.SharePublication)
				pubs.POST("/:id/publish", pubHandler.PublishPublication)
				pubs.POST("/:id/unpublish", pubHandler.UnpublishPublication)
				pubs.POST("/:id/archive", pubHandler.ArchivePublication)
				pubs.POST("/:id/unarchive", pubHandler.UnarchivePublication)
				pubs.POST("/:id/cover", pubHandler.SetCoverImage)
				pubs.DELETE("/:id/cover", pubHandler.RemoveCoverImage)
				pubs.POST("/:id/attachments", pubHandler.UploadAttachments)
				pubs.POST("/:id/gallery", pubHandler.UploadGallery)
				pubs.POST("/:id/videos", pubHandler.AddVideoEmbed)
				pubs.DELETE("/:id/attachments/:attachment_id", pubHandler.DeleteAttachment)
				pubs.PUT("/:id/attachments/reorder", pubHandler.ReorderAttachments)
				pubs.POST("/:id/banner/dismiss", pubHandler.DismissBanner)
				pubs.PUT("/:id/banner", pubHandler.SetBanner)
				pubs.DELETE("/:id/banner", pubHandler.RemoveBanner)
				pubs.PUT("/:id/pin", pubHandler.PinPublication)
				pubs.DELETE("/:id/pin", pubHandler.UnpinPublication)
				pubs.POST("/:id/broadcast", pubHandler.BroadcastPublication)
				pubs.GET("/:id/broadcast/stats", pubHandler.GetBroadcastStats)
				pubs.DELETE("/:id/acknowledge", pubHandler.UnacknowledgePublication)
				pubs.POST("/:id/unacknowledge", pubHandler.UnacknowledgePublication)
				pubs.GET("/:id/analytics", pubHandler.GetPublicationAnalytics)
				pubs.GET("/:id/read-stats", pubHandler.GetPublicationAnalytics)
				pubs.GET("/:id/readers", pubHandler.ListPublicationReaders)
				pubs.GET("/:id/readers/export", pubHandler.ExportPublicationReaders)
				pubs.GET("/:id/export/readers", pubHandler.ExportPublicationReaders)
				pubs.GET("/:id/acknowledgements", pubHandler.ListAcknowledgements)
			}
			protected.DELETE("/publications/comments/:comment_id", pubHandler.DeleteComment)
		}
	}

	return router
}

// healthCheck returns 200 if service is healthy
func healthCheck(c *gin.Context) {
	c.JSON(200, gin.H{
		"status":  "ok",
		"service": "messenger-api",
	})
}
