package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/west6ide/workspace-backend/internal/documentsearch"
)

type Config struct {
	App              AppConfig
	TenantBaseDomain string
	HTTP             HTTPConfig
	Database         DatabaseConfig
	Redis            RedisConfig
	JWT              JWTConfig
	Security         SecurityConfig
	MinIO            MinIOConfig
	RabbitMQ         RabbitMQConfig
	Logger           LoggerConfig
	CORS             CORSConfig
	Metrics          MetricsConfig
	WebSocket        WebSocketConfig
	FileCleanup      FileCleanupConfig
	Firebase         FirebaseConfig
	APNS             APNSConfig
	Jitsi            JitsiConfig
	Keycloak         KeycloakConfig
	ExternalAdmin    ExternalAdminConfig
	Alem             AlemConfig
	SMTP             SMTPConfig
	Mailcow          MailcowConfig
	Stalwart         StalwartConfig
	LiveKit          LiveKitConfig
	CalendarMeetings CalendarMeetingsConfig
	QMeet            QMeetConfig
	GoogleCalendar   GoogleCalendarConfig
	IDP              IDPConfig
	TwoSlides        TwoSlidesConfig
	Internal         InternalConfig
	Agent            AgentConfig
	DocumentParse    DocumentParseConfig
	DocumentSearch   documentsearch.Config
	DriveSearch      DriveSearchConfig
	AIReconciliation AIReconciliationConfig
	AILimiter        AILimiterConfig
	AIAccounting     AIAccountingConfig
}

type GoogleCalendarConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
}

// QMeetConfig is a backend-only identity bridge to the separate QMeet service.
type QMeetConfig struct {
	BaseURL string
	Secret  string
	Issuer  string
	Timeout time.Duration
}

type CalendarMeetingsConfig struct {
	CiscoSchedulerURL      string
	MeetingBaseURL         string
	ProviderTimeout        time.Duration
	LiveKitMaxParticipants int
	WorkdayStartHour       int
	WorkdayEndHour         int
}

type IDPConfig struct {
	BaseURL        string
	ClientID       string
	ClientSecret   string
	RedirectURL    string
	DefaultScopes  []string
	DefaultLang    string
	StateTTL       time.Duration
	Environment    string
	EnableDebugAPI bool
	RequestTimeout time.Duration
}

type TwoSlidesConfig struct {
	BaseURL string
	APIKey  string
	Timeout time.Duration
}

type InternalConfig struct {
	ODOCommandsEnabled            bool
	ODOCommandsBaseURL            string
	ODOCommandsTimeout            time.Duration
	NotificationToken             string
	MailToken                     string
	MeetToken                     string
	ODOHandoffToken               string
	ODOAssignmentsEnabled         bool
	ODOAssignmentsClientID        string
	ODOAssignmentsSubject         string
	ODOAssignmentsAudience        string
	ODOAssignmentsCompanies       string
	ODOAssignmentsSourceOrigin    string
	ODOAssignmentsBackfillEnabled bool
}

type MailcowConfig struct {
	APIBaseURL         string
	APIKey             string
	DefaultDomain      string
	DefaultQuotaMB     int
	TLSEnforceIn       bool
	TLSEnforceOut      bool
	ForcePWUpdate      bool
	InsecureSkipVerify bool
	RequestTimeout     time.Duration
	MaxSuffixAttempts  int
}

// StalwartConfig contains backend-only credentials for mailbox provisioning.
type StalwartConfig struct {
	URL               string
	PublicURL         string
	AdminUser         string
	AdminPassword     string
	DefaultDomain     string
	DefaultQuotaMB    int
	RequestTimeout    time.Duration
	MaxSuffixAttempts int
}

func (c StalwartConfig) Enabled() bool {
	return strings.TrimSpace(c.URL) != "" &&
		strings.TrimSpace(c.AdminUser) != "" &&
		strings.TrimSpace(c.AdminPassword) != "" &&
		strings.TrimSpace(c.DefaultDomain) != ""
}

type AgentConfig struct {
	Enabled                   bool
	V2Enabled                 bool
	IntentConfidenceThreshold float64
	SessionTTL                time.Duration
	ConfirmationTTL           time.Duration
	RateLimitPerMinute        int
	GraphifyEnabled           bool
	ExternalTimeout           time.Duration
	AdaptiveWebSearchURL      string
	WebSearchURL              string
	RouterModel               string
	ExecutorModel             string
	FallbackModel             string
}

type DocumentParseConfig struct {
	URL     string
	Timeout time.Duration
}

type DriveSearchConfig struct {
	Backend               string
	ElasticsearchURL      string
	Index                 string
	RequestTimeout        time.Duration
	ResultLimit           int
	ChunksPerFile         int
	ChunkSize             int
	ChunkOverlap          int
	ParentContextWindow   int
	MaxFileSizeBytes      int64
	EmbeddingModel        string
	EmbeddingDims         int
	VectorField           string
	VectorSimilarity      string
	HybridK               int
	NumCandidates         int
	RerankModel           string
	RerankTopN            int
	RRFConstant           int
	QueryRewriteModel     string
	HydeModel             string
	QueryRewriteMaxTokens int
	HydeMaxTokens         int
	AlemAPIKey            string
	AlemTimeout           time.Duration
	AlemMaxRetries        int
}

type AlemConfig struct {
	APIKey       string
	DefaultModel string
	// KazModel answers Kazakh notebook questions; empty disables it.
	KazModel       string
	RequestTimeout time.Duration
	MaxRetries     int
}

type AppConfig struct {
	Name      string
	Version   string
	Env       string
	PublicURL string
}

type KeycloakConfig struct {
	ServerURL               string
	PublicURL               string
	RealmURL                string
	Realm                   string
	Issuer                  string
	JWKSURL                 string
	ClientID                string
	AllowedClientIDs        []string
	TrustedAudiences        []string
	AllowedAZP              []string
	MobileClientID          string
	MobileRedirectURIs      []string
	MobileClientSecret      string
	ClientSecret            string
	AdminRealm              string
	AdminClientRealm        string
	AdminClientID           string
	AdminClientSecret       string
	AdminUser               string
	AdminPass               string
	QuickPinDefaultClientID string
}

type ExternalAdminConfig struct {
	Enabled      bool
	ClientID     string
	ClientSecret string
	Roles        []string
}

type HTTPConfig struct {
	Host            string
	Port            string
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	ShutdownTimeout time.Duration
}

type DatabaseConfig struct {
	Host            string
	Port            string
	Database        string
	User            string
	Password        string
	SSLMode         string
	MaxConns        int32
	MinConns        int32
	MaxConnLifetime time.Duration
	MaxConnIdleTime time.Duration
}

type RedisConfig struct {
	Host     string
	Port     string
	Password string
	DB       int
	PoolSize int
}

type JWTConfig struct {
	Secret             string
	AccessTokenExpiry  time.Duration
	RefreshTokenExpiry time.Duration
	Issuer             string
	Audience           string
}

type SecurityConfig struct {
	BcryptCost                               int
	RateLimitReqs                            int
	RateLimitWindow                          time.Duration
	RateLimitEnabled                         bool
	PinCryptoKey                             string
	PinSessionTTLSeconds                     int
	RequireQRForWebLoginWhenMobileRegistered bool
}

type MinIOConfig struct {
	MetricsToken           string
	CapacityReservePercent int64
	Endpoint               string
	EgressEndpoint         string
	AccessKey              string
	SecretKey              string
	UseSSL                 bool
	Bucket                 string
	PublicBaseURL          string
	Region                 string
	MaxFileSize            int64
}

type RabbitMQConfig struct {
	URL      string
	Exchange string
	Producer string
	Enabled  bool
}

type LoggerConfig struct {
	Level  string
	Format string // console (colored), text (alias), or json
	Output string // stdout or filepath
}

type CORSConfig struct {
	AllowedOrigins []string
	AllowedMethods []string
	AllowedHeaders []string
	MaxAge         int
}

type MetricsConfig struct {
	Enabled bool
}

type WebSocketConfig struct {
	ReadBufferSize  int
	WriteBufferSize int
	MaxMessageSize  int64
	PingInterval    time.Duration
	PongWait        time.Duration
	WriteWait       time.Duration
}

type FileCleanupConfig struct {
	Enabled   bool
	Interval  time.Duration
	BatchSize int
}

type FirebaseConfig struct {
	CredentialsFile string
}

type APNSConfig struct {
	KeyPath    string // путь к .p8 файлу Apple Auth Key
	KeyContent string // содержимое .p8 ключа; поддерживает \n в env
	KeyID      string // 10-символьный Key ID из Apple Developer
	TeamID     string // 10-символьный Team ID
	BundleID   string // bundle ID приложения, например com.company.app
	Sandbox    bool   // true = sandbox (development), false = production
}

type JitsiConfig struct {
	AppID     string
	AppSecret string
	Domain    string
}

func Load() (*Config, error) {
	_ = godotenv.Load()

	appPublicURL := getEnv("APP_PUBLIC_URL", "http://localhost:8080")
	calendarMeetingBaseURL := getEnv(
		"CALENDAR_MEETING_BASE_URL",
		strings.TrimRight(strings.TrimSpace(appPublicURL), "/")+"/web/calls/calendar",
	)

	cfg := &Config{
		AIAccounting:     loadAIAccountingConfig(),
		AIReconciliation: loadAIReconciliationConfig(),
		AILimiter:        loadAILimiterConfig(),
		App: AppConfig{
			Name:      getEnv("APP_NAME", "messenger"),
			Version:   getEnv("APP_VERSION", "1.0.0"),
			Env:       getEnv("ENV", "development"),
			PublicURL: appPublicURL,
		},
		TenantBaseDomain: strings.ToLower(strings.TrimSpace(getEnv("TENANT_BASE_DOMAIN", "teamspace.kz"))),
		HTTP: HTTPConfig{
			Host:            getEnv("HTTP_HOST", ""),
			Port:            getEnv("HTTP_PORT", "8080"),
			ReadTimeout:     getDuration("HTTP_READ_TIMEOUT", 120*time.Second),
			WriteTimeout:    getDuration("HTTP_WRITE_TIMEOUT", 120*time.Second),
			IdleTimeout:     getDuration("HTTP_IDLE_TIMEOUT", 60*time.Second),
			ShutdownTimeout: getDuration("HTTP_SHUTDOWN_TIMEOUT", 5*time.Second),
		},
		Database: DatabaseConfig{
			Host:            getEnv("POSTGRES_HOST", "localhost"),
			Port:            getEnv("POSTGRES_PORT", "5432"),
			Database:        getEnv("POSTGRES_DB", "messenger"),
			User:            getEnv("POSTGRES_USER", "postgres"),
			Password:        getEnv("POSTGRES_PASSWORD", "postgres"),
			SSLMode:         getEnv("POSTGRES_SSL_MODE", "disable"),
			MaxConns:        int32(getEnvAsInt("POSTGRES_MAX_CONNS", 25)),
			MinConns:        int32(getEnvAsInt("POSTGRES_MIN_CONNS", 5)),
			MaxConnLifetime: getDuration("POSTGRES_MAX_CONN_LIFETIME", 5*time.Minute),
			MaxConnIdleTime: getDuration("POSTGRES_MAX_CONN_IDLE_TIME", 1*time.Minute),
		},
		Redis: RedisConfig{
			Host:     getEnv("REDIS_HOST", "localhost"),
			Port:     getEnv("REDIS_PORT", "6379"),
			Password: getEnv("REDIS_PASSWORD", ""),
			DB:       getEnvAsInt("REDIS_DB", 0),
			PoolSize: getEnvAsInt("REDIS_POOL_SIZE", 10),
		},
		JWT: JWTConfig{
			Secret:             getEnv("JWT_SECRET", "change-this-secret"),
			AccessTokenExpiry:  getDuration("JWT_ACCESS_TOKEN_EXPIRY", 24*time.Hour),
			RefreshTokenExpiry: getDuration("JWT_REFRESH_TOKEN_EXPIRY", 7*24*time.Hour),
			Issuer:             getEnv("JWT_ISSUER", "messenger-api"),
			Audience:           getEnv("JWT_AUDIENCE", "messenger-client"),
		},
		Security: SecurityConfig{
			BcryptCost:                               getEnvAsInt("BCRYPT_COST", 12),
			RateLimitReqs:                            getEnvAsInt("RATE_LIMIT_REQUESTS", 100),
			RateLimitWindow:                          getDuration("RATE_LIMIT_WINDOW", 1*time.Minute),
			RateLimitEnabled:                         getEnvAsBool("RATE_LIMIT_ENABLED", false),
			PinCryptoKey:                             getEnv("PIN_CRYPTO_KEY", "default-key-should-be-changed"),
			PinSessionTTLSeconds:                     getEnvAsInt("PIN_SESSION_TTL_SECONDS", 300),
			RequireQRForWebLoginWhenMobileRegistered: getEnvAsBool("REQUIRE_QR_FOR_WEB_LOGIN_WHEN_MOBILE_REGISTERED", false),
		},
		MinIO: MinIOConfig{
			MetricsToken:           getEnv("MINIO_METRICS_TOKEN", ""),
			CapacityReservePercent: int64(getEnvAsInt("MINIO_CAPACITY_RESERVE_PERCENT", 10)),
			Endpoint:               getEnv("MINIO_ENDPOINT", "localhost:9000"),
			EgressEndpoint:         getEnv("MINIO_EGRESS_ENDPOINT", ""),
			AccessKey:              getEnv("MINIO_ACCESS_KEY", "minioadmin"),
			SecretKey:              getEnv("MINIO_SECRET_KEY", "minioadmin"),
			UseSSL:                 getEnvAsBool("MINIO_USE_SSL", false),
			Bucket:                 getEnv("MINIO_BUCKET", "messenger"),
			PublicBaseURL:          getEnv("MINIO_PUBLIC_BASE_URL", "http://localhost:9000/messenger"),
			Region:                 getEnv("MINIO_REGION", "us-east-1"),
			MaxFileSize:            int64(getEnvAsInt("MINIO_MAX_FILE_SIZE", 1024*1024*1024)),
		},
		RabbitMQ: RabbitMQConfig{
			URL:      getEnv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/"),
			Exchange: getEnv("RABBITMQ_EXCHANGE", "alem.events.exchange"),
			Producer: getEnv("RABBITMQ_PRODUCER", "messenger-api"),
			Enabled:  getEnvAsBool("RABBITMQ_ENABLED", true),
		},
		Logger: LoggerConfig{
			Level:  getEnv("LOG_LEVEL", "debug"),
			Format: getEnv("LOG_FORMAT", "console"),
			Output: getEnv("LOG_OUTPUT", "stdout"),
		},
		CORS: CORSConfig{
			AllowedOrigins: getEnvAsSlice("CORS_ALLOWED_ORIGINS", []string{"*"}),
			AllowedMethods: getEnvAsSlice("CORS_ALLOWED_METHODS", []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"}),
			AllowedHeaders: getEnvAsSlice("CORS_ALLOWED_HEADERS", []string{"Origin", "Content-Type", "Accept", "Authorization", "X-Request-ID", "X-Web-Device-ID", "X-Client-Platform", "X-Requested-With", "X-Internal-Token"}),
			MaxAge:         getEnvAsInt("CORS_MAX_AGE", 3600),
		},
		Metrics: MetricsConfig{
			Enabled: getEnvAsBool("METRICS_ENABLED", true),
		},
		WebSocket: WebSocketConfig{
			ReadBufferSize:  getEnvAsInt("WS_READ_BUFFER_SIZE", 1024),
			WriteBufferSize: getEnvAsInt("WS_WRITE_BUFFER_SIZE", 1024),
			MaxMessageSize:  int64(getEnvAsInt("WS_MAX_MESSAGE_SIZE", 512)),
			PingInterval:    getDuration("WS_PING_INTERVAL", 54*time.Second),
			PongWait:        getDuration("WS_PONG_WAIT", 60*time.Second),
			WriteWait:       getDuration("WS_WRITE_WAIT", 10*time.Second),
		},
		FileCleanup: FileCleanupConfig{
			Enabled:   getEnvAsBool("FILE_CLEANUP_ENABLED", true),
			Interval:  getDuration("FILE_CLEANUP_INTERVAL", 24*time.Hour),
			BatchSize: getEnvAsInt("FILE_CLEANUP_BATCH_SIZE", 100),
		},
		Firebase: FirebaseConfig{
			CredentialsFile: getEnv("FIREBASE_CREDENTIALS_FILE", ""),
		},
		APNS: APNSConfig{
			KeyPath:    getEnv("APNS_KEY_PATH", ""),
			KeyContent: normalizeEnvMultiline(getEnv("APNS_KEY_CONTENT", "")),
			KeyID:      getEnv("APNS_KEY_ID", "JZTZA38DW8"),
			TeamID:     getEnv("APNS_TEAM_ID", "7MXGHGHD2W"),
			BundleID:   getEnv("APNS_BUNDLE_ID", "kz.alemSuperapp.app"),
			Sandbox:    getEnvAsBool("APNS_SANDBOX", false),
		},
		Jitsi: JitsiConfig{
			AppID:     getEnv("JITSI_APP_ID", "my_alemet_app"),
			AppSecret: getEnv("JITSI_APP_SECRET", ""),
			Domain:    getEnv("JITSI_DOMAIN", "meet.teamspace.kz"),
		},
		Keycloak: KeycloakConfig{
			ServerURL:          getEnv("KEYCLOAK_URL", "http://92.38.48.9:8080"),
			PublicURL:          getEnv("KEYCLOAK_PUBLIC_URL", "http://92.38.48.9:8080"),
			RealmURL:           getEnv("KEYCLOAK_REALM_URL", ""),
			Realm:              getEnv("KEYCLOAK_REALM", "GovApps"),
			Issuer:             getEnv("KEYCLOAK_ISSUER", ""),
			JWKSURL:            getEnv("KEYCLOAK_JWKS_URL", ""),
			ClientID:           getEnv("KEYCLOAK_CLIENT_ID", "alem-workspace"),
			AllowedClientIDs:   getEnvAsSlice("KEYCLOAK_ALLOWED_CLIENT_IDS", nil),
			TrustedAudiences:   getEnvAsSlice("KEYCLOAK_TRUSTED_AUDIENCES", nil),
			AllowedAZP:         getEnvAsSlice("KEYCLOAK_ALLOWED_AZP", nil),
			MobileClientID:     getEnv("KEYCLOAK_CLIENT_ID_MOBILE", ""),
			MobileRedirectURIs: keycloakMobileRedirectURIs(),
			MobileClientSecret: getEnv("KEYCLOAK_CLIENT_SECRET_MOBILE", ""),
			ClientSecret:       getEnv("KEYCLOAK_CLIENT_SECRET", ""),
			AdminRealm:         getEnv("KEYCLOAK_ADMIN_REALM", "master"),
			AdminClientRealm:   firstNonEmptyEnv("KEYCLOAK_ADMIN_CLIENT_REALM", "KEYCLOAK_REALM"),
			AdminClientID:      getEnv("KEYCLOAK_ADMIN_CLIENT_ID", ""),
			AdminClientSecret:  getEnv("KEYCLOAK_ADMIN_CLIENT_SECRET", ""),
			AdminUser:          getEnv("KEYCLOAK_ADMIN_USER", "admin"),
			// The production Compose file uses Keycloak's official
			// KEYCLOAK_ADMIN_PASSWORD name, while older backend .env files use
			// KEYCLOAK_ADMIN_PASS. Accept both so the API cannot silently fall
			// back to the default password and fail with a Keycloak 401.
			AdminPass:               firstNonEmptyEnvOrDefault("admin", "KEYCLOAK_ADMIN_PASS", "KEYCLOAK_ADMIN_PASSWORD"),
			QuickPinDefaultClientID: getEnv("KEYCLOAK_QUICK_PIN_DEFAULT_CLIENT_ID", ""),
		},
		ExternalAdmin: ExternalAdminConfig{
			Enabled:      getEnvAsBool("EXTERNAL_ADMIN_ENABLED", false),
			ClientID:     getEnv("EXTERNAL_ADMIN_CLIENT_ID", ""),
			ClientSecret: getEnv("EXTERNAL_ADMIN_CLIENT_SECRET", ""),
			Roles:        getEnvAsSlice("EXTERNAL_ADMIN_ROLES", []string{"external_user_admin"}),
		},
		Alem: AlemConfig{
			APIKey:         getEnv("ALEM_API_KEY", ""),
			DefaultModel:   firstNonEmptyEnvOrDefault("openai/gpt-oss-120b", "ALEM_DEFAULT_MODEL", "ALEM_MODEL"),
			KazModel:       getEnvAllowEmpty("ALEM_KAZ_MODEL", "astanahub/alemllm"),
			RequestTimeout: getDuration("ALEM_REQUEST_TIMEOUT", 15*time.Second),
			MaxRetries:     getEnvAsInt("ALEM_MAX_RETRIES", 1),
		},
		SMTP: SMTPConfig{
			Enabled:           getEnvAsBool("MAIL_ENABLED", false),
			Provider:          getEnv("MAIL_PROVIDER", "smtp"),
			Host:              getEnv("SMTP_HOST", ""),
			Hosts:             getEnvAsSlice("SMTP_HOSTS", []string{"10.61.140.228", "10.245.13.61"}),
			Port:              getEnvAsInt("SMTP_PORT", 465),
			Username:          getEnv("SMTP_USERNAME", ""),
			Password:          getEnv("SMTP_PASSWORD", ""),
			From:              getEnv("SMTP_FROM", "alemworkspace@eps.gov.kz"),
			FromEmail:         getEnv("SMTP_FROM_EMAIL", ""),
			FromName:          getEnv("SMTP_FROM_NAME", "Alem Workspace"),
			UseTLS:            getEnvAsBool("SMTP_USE_TLS", true),
			ImplicitTLS:       getEnvAsBool("SMTP_IMPLICIT_TLS", false),
			TLSServerName:     getEnv("SMTP_TLS_SERVER_NAME", ""),
			SkipTLSVerify:     getEnvAsBool("SMTP_SKIP_TLS_VERIFY", false),
			CDNKindyDomain:    getEnv("MAIL_CDN_KINDY_DOMAIN", ""),
			CDNKindyUsers:     getEnvAsSlice("MAIL_CDN_KINDY_USERS", nil),
			NotificationTypes: getEnvAsSlice("MAIL_NOTIFICATION_TYPES", []string{"new_message", "incoming_call", "system", "security", "calendar_invite", "calendar_event", "web_login", "kanban_task_assigned", "kanban_task_due_soon", "kanban_task_overdue"}),
		},
		Mailcow: MailcowConfig{
			APIBaseURL:         getEnv("MAILCOW_API_BASE_URL", ""),
			APIKey:             getEnv("MAILCOW_API_KEY", ""),
			DefaultDomain:      getEnv("MAILCOW_DEFAULT_DOMAIN", ""),
			DefaultQuotaMB:     getEnvAsInt("MAILCOW_DEFAULT_QUOTA_MB", 3072),
			TLSEnforceIn:       getEnvAsBool("MAILCOW_TLS_ENFORCE_IN", true),
			TLSEnforceOut:      getEnvAsBool("MAILCOW_TLS_ENFORCE_OUT", true),
			ForcePWUpdate:      getEnvAsBool("MAILCOW_FORCE_PW_UPDATE", false),
			InsecureSkipVerify: getEnvAsBool("MAILCOW_INSECURE_SKIP_VERIFY", false),
			RequestTimeout:     getDuration("MAILCOW_REQUEST_TIMEOUT", 15*time.Second),
			MaxSuffixAttempts:  getEnvAsInt("MAILCOW_MAX_SUFFIX_ATTEMPTS", 100),
		},
		Stalwart: StalwartConfig{
			URL:               getEnv("STALWART_URL", ""),
			PublicURL:         getEnv("STALWART_PUBLIC_URL", ""),
			AdminUser:         getEnv("STALWART_ADMIN_USER", ""),
			AdminPassword:     getEnv("STALWART_ADMIN_PASSWORD", ""),
			DefaultDomain:     getEnv("STALWART_DEFAULT_DOMAIN", ""),
			DefaultQuotaMB:    getEnvAsInt("STALWART_DEFAULT_QUOTA_MB", 3072),
			RequestTimeout:    getDuration("STALWART_REQUEST_TIMEOUT", 15*time.Second),
			MaxSuffixAttempts: getEnvAsInt("STALWART_MAX_SUFFIX_ATTEMPTS", 100),
		},
		LiveKit: LiveKitConfig{
			APIKey:      getEnv("LIVEKIT_API_KEY", ""),
			APISecret:   getEnv("LIVEKIT_API_SECRET", ""),
			URL:         getEnv("LIVEKIT_URL", ""),
			APIURL:      getEnv("LIVEKIT_API_URL", ""),
			PublicURL:   getEnv("LIVEKIT_PUBLIC_URL", ""),
			PublicUsers: getEnvAsSlice("LIVEKIT_PUBLIC_USERS", nil),
		},
		CalendarMeetings: CalendarMeetingsConfig{
			CiscoSchedulerURL:      getEnv("CALENDAR_CISCO_SCHEDULER_URL", "https://100.72.193.178/scheduler/api/create_meeting"),
			MeetingBaseURL:         calendarMeetingBaseURL,
			ProviderTimeout:        getDuration("CALENDAR_MEETING_PROVIDER_TIMEOUT", 5*time.Second),
			LiveKitMaxParticipants: getEnvAsInt("CALENDAR_LIVEKIT_MAX_PARTICIPANTS", 5),
			WorkdayStartHour:       getEnvAsInt("CALENDAR_WORKDAY_START_HOUR", 9),
			WorkdayEndHour:         getEnvAsInt("CALENDAR_WORKDAY_END_HOUR", 18),
		},
		QMeet: QMeetConfig{
			BaseURL: getEnv("QMEET_API_URL", ""),
			Secret:  getEnv("QMEET_WORKSPACE_SECRET", ""),
			Issuer:  getEnv("QMEET_WORKSPACE_ISSUER", "workspace"),
			Timeout: getDuration("QMEET_REQUEST_TIMEOUT", 45*time.Second),
		},
		GoogleCalendar: GoogleCalendarConfig{
			ClientID:     getEnv("GOOGLE_CLIENT_ID", "1037144957688-sjo4pi3tjudi5ia2rg784gq614rpkngp.apps.googleusercontent.com"),
			ClientSecret: getEnv("GOOGLE_CLIENT_SECRET", ""),
			RedirectURL:  getEnv("GOOGLE_REDIRECT_URL", ""),
		},
		IDP: IDPConfig{
			BaseURL:        getEnv("IDP_BASE_URL", "https://idp.egov.kz"),
			ClientID:       getEnv("IDP_CLIENT_ID", ""),
			ClientSecret:   getEnv("IDP_CLIENT_SECRET", ""),
			RedirectURL:    firstNonEmptyEnv("IDP_REDIRECT_URI", "IDP_REDIRECT_URL"),
			DefaultScopes:  getEnvAsSlice("IDP_SCOPES", getEnvAsSlice("IDP_DEFAULT_SCOPES", []string{"user:basic:read", "user:phone:read", "user:email:read"})),
			DefaultLang:    getEnv("IDP_DEFAULT_LANG", "ru"),
			StateTTL:       getFlexibleDuration([]string{"IDP_STATE_TTL", "IDP_STATE_TTL_SECONDS"}, 10*time.Minute),
			Environment:    getEnv("IDP_ENV", "prod"),
			EnableDebugAPI: getEnvAsBool("IDP_ENABLE_DEBUG_API", false),
			RequestTimeout: getDuration("IDP_REQUEST_TIMEOUT", 15*time.Second),
		},
		TwoSlides: TwoSlidesConfig{
			BaseURL: getEnv("TWOSLIDES_BASE_URL", "https://2slides.com"),
			APIKey:  getEnv("TWOSLIDES_API_KEY", ""),
			Timeout: getDuration("TWOSLIDES_TIMEOUT", 90*time.Second),
		},
		Internal: InternalConfig{
			ODOCommandsEnabled:            getEnvAsBool("ODO_COMMANDS_ENABLED", false),
			ODOCommandsBaseURL:            getEnv("ODO_COMMANDS_BASE_URL", ""),
			ODOCommandsTimeout:            getDuration("ODO_COMMANDS_TIMEOUT", 10*time.Second),
			NotificationToken:             getEnv("INTERNAL_NOTIFICATION_TOKEN", ""),
			MailToken:                     getEnv("INTERNAL_MAIL_TOKEN", ""),
			MeetToken:                     getEnv("ALEM_WORKSPACE_INTERNAL_TOKEN", ""),
			ODOHandoffToken:               getEnv("ODO_HANDOFF_INTERNAL_TOKEN", ""),
			ODOAssignmentsEnabled:         getEnvAsBool("ODO_ASSIGNMENTS_ENABLED", false),
			ODOAssignmentsClientID:        getEnv("ODO_ASSIGNMENTS_CLIENT_ID", ""),
			ODOAssignmentsSubject:         getEnv("ODO_ASSIGNMENTS_SERVICE_SUB", ""),
			ODOAssignmentsAudience:        getEnv("ODO_ASSIGNMENTS_AUDIENCE", ""),
			ODOAssignmentsCompanies:       getEnv("ODO_ASSIGNMENTS_COMPANIES", ""),
			ODOAssignmentsSourceOrigin:    getEnv("ODO_ASSIGNMENTS_SOURCE_ORIGIN", ""),
			ODOAssignmentsBackfillEnabled: getEnvAsBool("ODO_ASSIGNMENTS_BACKFILL_ENABLED", false),
		},
		Agent: AgentConfig{
			Enabled:                   getEnvAsBool("AGENT_ENABLED", true),
			V2Enabled:                 getEnvAsBool("AGENT_V2_ENABLED", true),
			IntentConfidenceThreshold: getEnvAsFloat("AGENT_INTENT_CONFIDENCE_THRESHOLD", 0.75),
			SessionTTL:                getDuration("AGENT_SESSION_TTL", 24*time.Hour),
			ConfirmationTTL:           getDuration("AGENT_CONFIRMATION_TTL", 5*time.Minute),
			RateLimitPerMinute:        getEnvAsInt("AGENT_RATE_LIMIT_PER_MINUTE", 30),
			GraphifyEnabled:           getEnvAsBool("AGENT_GRAPHIFY_ENABLED", true),
			ExternalTimeout:           getDuration("AGENT_EXTERNAL_TIMEOUT", 15*time.Second),
			AdaptiveWebSearchURL:      getEnv("AGENT_ADAPTIVE_WEB_SEARCH_URL", "https://alem-workspace.gov.kz/v1/chat/ask_adaptive"),
			WebSearchURL:              getEnv("AI_WEB_SEARCH_URL", "http://alemcloud-api:8000/tools/web-search"),
			RouterModel:               getEnv("AGENT_ROUTER_MODEL", "Qwen/Qwen3-8B"),
			ExecutorModel:             getEnv("AGENT_EXECUTOR_MODEL", "deepseek-ai/DeepSeek-V4-Pro"),
			FallbackModel:             getEnv("AGENT_FALLBACK_MODEL", "moonshotai/Kimi-K2.6"),
		},
		DocumentParse: DocumentParseConfig{
			URL:     getEnv("DOCUMENT_PARSE_URL", ""),
			Timeout: getDuration("DOCUMENT_PARSE_TIMEOUT", 30*time.Second),
		},
		DocumentSearch: documentsearch.Config{
			URL:           getEnv("DOCUMENT_EMBEDDING_URL", ""),
			Model:         getEnv("DOCUMENT_EMBEDDING_MODEL", "bge-m3"),
			Dimensions:    getEnvAsInt("DOCUMENT_EMBEDDING_DIMS", 1024),
			Timeout:       getDuration("DOCUMENT_EMBEDDING_TIMEOUT", 30*time.Second),
			QueryURL:      getEnv("DOCUMENT_QUERY_URL", ""),
			QueryModel:    getEnv("DOCUMENT_QUERY_MODEL", ""),
			QueryTimeout:  getDuration("DOCUMENT_QUERY_TIMEOUT", 3*time.Second),
			RerankURL:     getEnv("DOCUMENT_RERANK_URL", ""),
			RerankTimeout: getDuration("DOCUMENT_RERANK_TIMEOUT", 5*time.Second),
		},
		DriveSearch: DriveSearchConfig{
			Backend:               getEnv("DRIVE_SEARCH_BACKEND", "postgres"),
			ElasticsearchURL:      firstNonEmptyEnv("DRIVE_SEARCH_ELASTICSEARCH_URL", "DRIVE_BACKFILL_ELASTICSEARCH_URL"),
			Index:                 getEnv("DRIVE_SEARCH_ELASTICSEARCH_INDEX", getEnv("DRIVE_BACKFILL_ELASTICSEARCH_INDEX", "alem_drive_chunks")),
			RequestTimeout:        getDuration("DRIVE_SEARCH_REQUEST_TIMEOUT", 10*time.Second),
			ResultLimit:           getEnvAsInt("DRIVE_SEARCH_RESULT_LIMIT", 10),
			ChunksPerFile:         getEnvAsInt("DRIVE_SEARCH_CHUNKS_PER_FILE", 5),
			ChunkSize:             getEnvAsInt("DRIVE_SEARCH_CHUNK_SIZE", getEnvAsInt("DRIVE_BACKFILL_CHUNK_SIZE", 1200)),
			ChunkOverlap:          getEnvAsInt("DRIVE_SEARCH_CHUNK_OVERLAP", getEnvAsInt("DRIVE_BACKFILL_CHUNK_OVERLAP", 200)),
			ParentContextWindow:   getEnvAsInt("DRIVE_SEARCH_PARENT_CONTEXT_WINDOW", 1),
			MaxFileSizeBytes:      int64(getEnvAsInt("DRIVE_SEARCH_MAX_FILE_SIZE_BYTES", 50*1024*1024)),
			EmbeddingModel:        getEnv("DRIVE_SEARCH_EMBEDDING_MODEL", "BAAI/bge-m3"),
			EmbeddingDims:         getEnvAsInt("DRIVE_SEARCH_EMBEDDING_DIMS", 1024),
			VectorField:           getEnv("DRIVE_SEARCH_VECTOR_FIELD", "content_vector"),
			VectorSimilarity:      getEnv("DRIVE_SEARCH_VECTOR_SIMILARITY", "cosine"),
			HybridK:               getEnvAsInt("DRIVE_SEARCH_HYBRID_K", 50),
			NumCandidates:         getEnvAsInt("DRIVE_SEARCH_NUM_CANDIDATES", 100),
			RerankModel:           getEnv("DRIVE_SEARCH_RERANK_MODEL", "BAAI/bge-reranker-v2-m3"),
			RerankTopN:            getEnvAsInt("DRIVE_SEARCH_RERANK_TOP_N", 50),
			RRFConstant:           getEnvAsInt("DRIVE_SEARCH_RRF_CONSTANT", 60),
			QueryRewriteModel:     getEnv("DRIVE_SEARCH_QUERY_REWRITE_MODEL", "Qwen/Qwen3-8B"),
			HydeModel:             getEnv("DRIVE_SEARCH_HYDE_MODEL", "Qwen/Qwen3-8B"),
			QueryRewriteMaxTokens: getEnvAsInt("DRIVE_SEARCH_QUERY_REWRITE_MAX_TOKENS", 96),
			HydeMaxTokens:         getEnvAsInt("DRIVE_SEARCH_HYDE_MAX_TOKENS", 160),
			AlemAPIKey:            getEnv("DRIVE_SEARCH_ALEM_API_KEY", getEnv("ALEM_API_KEY", "")),
			AlemTimeout:           getDuration("DRIVE_SEARCH_ALEM_TIMEOUT", getDuration("ALEM_REQUEST_TIMEOUT", 15*time.Second)),
			AlemMaxRetries:        getEnvAsInt("DRIVE_SEARCH_ALEM_MAX_RETRIES", getEnvAsInt("ALEM_MAX_RETRIES", 2)),
		},
	}

	if cfg.SMTP.Host == "" && len(cfg.SMTP.Hosts) > 0 {
		cfg.SMTP.Host = strings.TrimSpace(cfg.SMTP.Hosts[0])
	}
	if cfg.SMTP.FromEmail == "" {
		cfg.SMTP.FromEmail = strings.TrimSpace(cfg.SMTP.From)
	}
	if len(cfg.SMTP.Hosts) == 0 && cfg.SMTP.Host != "" {
		cfg.SMTP.Hosts = []string{cfg.SMTP.Host}
	}

	return cfg, cfg.Validate()
}

// Validate validates the configuration
func (c *Config) Validate() error {
	if err := c.AIAccounting.Validate(); err != nil {
		return err
	}
	if err := c.AILimiter.Validate(); err != nil {
		return err
	}
	if c.AIReconciliation.Enabled {
		if err := c.AIReconciliation.Validate(); err != nil {
			return err
		}
	}
	if c.JWT.Secret == "change-this-secret" && c.App.Env == "production" {
		return fmt.Errorf("JWT secret must be changed in production")
	}
	if c.Keycloak.ServerURL == "" || c.Keycloak.Realm == "" || c.Keycloak.ClientID == "" {
		return fmt.Errorf("keycloak config is incomplete: KEYCLOAK_URL/REALM/CLIENT_ID required")
	}
	if len(c.Keycloak.AllowedClientIDs) == 0 {
		c.Keycloak.AllowedClientIDs = []string{c.Keycloak.ClientID}
	}
	if c.Keycloak.MobileClientID != "" && !containsString(c.Keycloak.AllowedClientIDs, c.Keycloak.MobileClientID) {
		c.Keycloak.AllowedClientIDs = append(c.Keycloak.AllowedClientIDs, c.Keycloak.MobileClientID)
	}
	if c.Keycloak.RealmURL == "" {
		c.Keycloak.RealmURL = strings.TrimRight(c.Keycloak.PublicURL, "/") + "/realms/" + c.Keycloak.Realm
	}
	if c.Keycloak.Issuer == "" {
		c.Keycloak.Issuer = c.Keycloak.RealmURL
	}
	if c.Keycloak.JWKSURL == "" {
		c.Keycloak.JWKSURL = c.Keycloak.RealmURL + "/protocol/openid-connect/certs"
	}
	if c.Keycloak.AdminClientRealm == "" {
		c.Keycloak.AdminClientRealm = c.Keycloak.Realm
	}
	if len(c.Keycloak.TrustedAudiences) == 0 {
		c.Keycloak.TrustedAudiences = append([]string(nil), c.Keycloak.AllowedClientIDs...)
	}
	if c.ExternalAdmin.Enabled && strings.TrimSpace(c.ExternalAdmin.ClientID) == "" {
		return fmt.Errorf("external admin config is incomplete: EXTERNAL_ADMIN_CLIENT_ID required when EXTERNAL_ADMIN_ENABLED=true")
	}
	if c.ExternalAdmin.Enabled && len(c.ExternalAdmin.Roles) == 0 {
		return fmt.Errorf("external admin config is incomplete: EXTERNAL_ADMIN_ROLES required when EXTERNAL_ADMIN_ENABLED=true")
	}
	if c.Database.Password == "" {
		return fmt.Errorf("database password is required")
	}
	if c.LiveKit.URL != "" && (c.LiveKit.APIKey == "" || c.LiveKit.APISecret == "") {
		return fmt.Errorf("LiveKit API Key and Secret are required when LIVEKIT_URL is set")
	}
	return nil
}

// Helper functions
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getEnvAllowEmpty returns defaultValue only when key is unset, so an explicit
// empty value can disable an optional feature.
func getEnvAllowEmpty(key, defaultValue string) string {
	if value, ok := os.LookupEnv(key); ok {
		return strings.TrimSpace(value)
	}
	return defaultValue
}

func firstNonEmptyEnv(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func firstNonEmptyEnvOrDefault(defaultValue string, keys ...string) string {
	if value := firstNonEmptyEnv(keys...); value != "" {
		return value
	}
	return defaultValue
}

func getEnvAsInt(key string, defaultValue int) int {
	valueStr := os.Getenv(key)
	if valueStr == "" {
		return defaultValue
	}
	var value int
	_, err := fmt.Sscanf(valueStr, "%d", &value)
	if err != nil {
		return defaultValue
	}
	return value
}

func getEnvAsFloat(key string, defaultValue float64) float64 {
	valueStr := os.Getenv(key)
	if valueStr == "" {
		return defaultValue
	}
	var value float64
	_, err := fmt.Sscanf(valueStr, "%f", &value)
	if err != nil {
		return defaultValue
	}
	return value
}

func getEnvAsBool(key string, defaultValue bool) bool {
	valueStr := os.Getenv(key)
	if valueStr == "" {
		return defaultValue
	}
	return valueStr == "true" || valueStr == "1" || valueStr == "yes"
}

func normalizeEnvMultiline(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return strings.ReplaceAll(value, `\n`, "\n")
}

func getDuration(key string, defaultValue time.Duration) time.Duration {
	valueStr := os.Getenv(key)
	if valueStr == "" {
		return defaultValue
	}
	value, err := time.ParseDuration(valueStr)
	if err != nil {
		return defaultValue
	}
	return value
}

func getFlexibleDuration(keys []string, defaultValue time.Duration) time.Duration {
	for _, key := range keys {
		valueStr := strings.TrimSpace(os.Getenv(key))
		if valueStr == "" {
			continue
		}
		if duration, err := time.ParseDuration(valueStr); err == nil {
			return duration
		}
		if seconds, err := strconv.Atoi(valueStr); err == nil && seconds > 0 {
			return time.Duration(seconds) * time.Second
		}
	}
	return defaultValue
}

func getEnvAsSlice(key string, defaultValue []string) []string {
	valueStr := os.Getenv(key)
	if valueStr == "" {
		return defaultValue
	}

	var result []string
	for _, value := range strings.Split(valueStr, ",") {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		result = append(result, value)
	}
	if len(result) == 0 {
		return defaultValue
	}
	return result
}

func containsString(items []string, value string) bool {
	for _, item := range items {
		if strings.TrimSpace(item) == strings.TrimSpace(value) {
			return true
		}
	}
	return false
}

func keycloakMobileRedirectURIs() []string {
	redirects := getEnvAsSlice("KEYCLOAK_MOBILE_REDIRECT_URIS", nil)
	if len(redirects) > 0 {
		return redirects
	}

	return []string{
		"kz.alemSuperapp.app://auth/callback",
		"kz.superapp.app://auth/callback",
	}
}

type SMTPConfig struct {
	Enabled           bool
	Provider          string
	Host              string
	Hosts             []string `env:"SMTP_HOSTS" envDefault:"10.61.140.228,10.245.13.61" envSeparator:","`
	Port              int      `env:"SMTP_PORT" envDefault:"465"`
	Username          string   `env:"SMTP_USERNAME"`
	Password          string   `env:"SMTP_PASSWORD"`
	From              string   `env:"SMTP_FROM" envDefault:"alemworkspace@eps.gov.kz"`
	FromEmail         string
	FromName          string
	UseTLS            bool
	ImplicitTLS       bool
	TLSServerName     string
	SkipTLSVerify     bool
	CDNKindyDomain    string
	CDNKindyUsers     []string
	NotificationTypes []string
}

type LiveKitConfig struct {
	APIKey    string
	APISecret string
	URL       string
	// APIURL — URL для серверных API-вызовов (Twirp). Если не задан, используется URL.
	// Используй http://IP:7880 чтобы обойти TLS при прямом подключении к LiveKit.
	APIURL string
	// PublicURL — публичный WSS endpoint для внешних пользователей.
	PublicURL string
	// PublicUsers — список user ids, username или email, для которых нужно отдавать PublicURL.
	PublicUsers []string
}
