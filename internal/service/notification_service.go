package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/messaging"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/i18n"
	"github.com/west6ide/workspace-backend/internal/repository"
	"github.com/west6ide/workspace-backend/internal/websocket"
	"go.uber.org/zap"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

type deviceStore interface {
	SaveDevice(ctx context.Context, device *domain.UserDevice) error
	GetUserDevices(ctx context.Context, userID int64) ([]domain.UserDevice, error)
	GetUserTokens(ctx context.Context, userID int64) ([]string, error)
	DeactivateToken(ctx context.Context, userID int64, token string) error
	DeactivateTokenByValue(ctx context.Context, token string) error
	DeactivateDevice(ctx context.Context, userID int64, deviceID string) error
	DeactivateAllUserDevices(ctx context.Context, userID int64) error
	ClearVoIPToken(ctx context.Context, userID int64, deviceID string, voipToken string) error
}

type multicastSender interface {
	SendEachForMulticast(ctx context.Context, message *messaging.MulticastMessage) (*messaging.BatchResponse, error)
}

type voipSender interface {
	sendVoIP(ctx context.Context, deviceToken string, p apnsVoIPPayload, expiration time.Time, logger *zap.Logger) error
}

type notificationUserLookup interface {
	GetByID(ctx context.Context, id int64) (*domain.User, error)
}

type livekitTokenIssuer interface {
	GenerateToken(roomName string, userID int64, username string, tokenCtx LiveKitTokenContext) (string, error)
}

type NotificationService struct {
	deviceRepo     deviceStore
	notifRepo      *repository.NotificationRepository
	callRepo       *repository.CallHistoryRepository
	userRepo       notificationUserLookup
	tokenIssuer    livekitTokenIssuer
	logger         *zap.Logger
	fcmClient      multicastSender
	apnsVoip       voipSender
	queue          chan PushNotificationJob
	emailService   *EmailService
	emailResolver  *EmailRecipientResolver
	mailTypeFilter map[string]struct{}
	livekitURL     string
	urlResolver    liveKitClientURLResolver

	appPublicURL string
	jwtSecret    string
	wsHub        *websocket.Hub
}

type PushNotificationJob struct {
	UserID int64
	Title  string
	Body   string
	Data   map[string]string
}

const (
	workerCount           = 5
	queueSize             = 1000
	pushSendTimeout       = 6 * time.Second
	maxPushBodyRunes      = 180
	fcmCallTTL            = 35 * time.Second
	devicePlatformIOS     = "ios"
	devicePlatformAndroid = "android"
	devicePlatformWeb     = "web"
	devicePlatformUnknown = "unknown"
)

type voipPushTarget struct {
	UserID    int64
	DeviceID  string
	Platform  string
	VoIPToken string
}

type pushTargets struct {
	AndroidTokens  []string
	IOSTokens      []string
	FallbackTokens []string
	VoIPTargets    []voipPushTarget
}

type pushTargetLogMeta struct {
	DeviceID string
	Platform string
}

func buildPushTargetLogIndex(devices []domain.UserDevice) map[string]pushTargetLogMeta {
	index := make(map[string]pushTargetLogMeta, len(devices))
	for _, device := range devices {
		token := strings.TrimSpace(device.FCMToken)
		if token == "" {
			continue
		}
		index[token] = pushTargetLogMeta{
			DeviceID: strings.TrimSpace(device.DeviceID),
			Platform: tokenPlatform(device),
		}
	}
	return index
}

func NewNotificationService(
	deviceRepo *repository.DeviceRepository,
	notifRepo *repository.NotificationRepository,
	callRepo *repository.CallHistoryRepository,
	userRepo *repository.UserRepository,
	emailService *EmailService,
	wsHub *websocket.Hub,
	cfg *config.Config,
	logger *zap.Logger,
) *NotificationService {
	s := &NotificationService{
		deviceRepo:     deviceRepo,
		notifRepo:      notifRepo,
		callRepo:       callRepo,
		userRepo:       userRepo,
		emailService:   emailService,
		emailResolver:  NewEmailRecipientResolver(cfg.SMTP),
		mailTypeFilter: make(map[string]struct{}),
		wsHub:          wsHub,
		logger:         logger.With(zap.String("service", "notification")),
		queue:          make(chan PushNotificationJob, queueSize),
		livekitURL:     strings.TrimSpace(cfg.LiveKit.URL),
		urlResolver:    newLiveKitClientURLResolver(cfg.LiveKit),
	}
	for _, notificationType := range cfg.SMTP.NotificationTypes {
		normalized := strings.ToLower(strings.TrimSpace(notificationType))
		if normalized == "" {
			continue
		}
		s.mailTypeFilter[normalized] = struct{}{}
	}

	credPath := strings.TrimSpace(cfg.Firebase.CredentialsFile)
	if credPath == "" {
		s.logger.Warn("Firebase credentials not provided, push notifications disabled")
	} else {
		credBytes, err := os.ReadFile(credPath)
		if err != nil {
			s.logger.Error("Firebase credentials file not found/readable",
				zap.String("path", credPath),
				zap.Error(err),
			)
		} else {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			creds, err := google.CredentialsFromJSON(
				ctx,
				credBytes,
				"https://www.googleapis.com/auth/firebase.messaging",
				"https://www.googleapis.com/auth/cloud-platform",
			)
			if err != nil {
				s.logger.Error("Failed to parse Firebase credentials JSON", zap.Error(err))
			} else {
				app, err := firebase.NewApp(ctx, nil, option.WithCredentials(creds))
				if err != nil {
					s.logger.Error("Failed to initialize Firebase app", zap.Error(err))
				} else {
					client, err := app.Messaging(ctx)
					if err != nil {
						s.logger.Error("Failed to initialize Firebase messaging client", zap.Error(err))
					} else {
						s.fcmClient = client
						s.logger.Info("Firebase messaging initialized successfully",
							zap.String("credentials_file", credPath),
						)
					}
				}
			}
		}
	}

	s.apnsVoip = newAPNSVoIPClientFromConfig(cfg.APNS, s.logger)

	for i := 0; i < workerCount; i++ {
		go s.worker(i)
	}

	return s
}

func (s *NotificationService) worker(id int) {
	s.logger.Debug("Notification worker started", zap.Int("worker_id", id))
	for job := range s.queue {
		ctx, cancel := context.WithTimeout(context.Background(), pushSendTimeout)
		err := s.sendActualPush(ctx, job.UserID, job.Title, job.Body, job.Data)
		cancel()

		if err != nil {
			s.logger.Error("Worker failed to send push",
				zap.Error(err),
				zap.Int64("user_id", job.UserID),
			)
		}
	}
}

func (s *NotificationService) resolveLiveKitURLForUser(ctx context.Context, userID int64) string {
	if s == nil {
		return ""
	}
	if userID <= 0 {
		return s.livekitURL
	}
	if s.userRepo != nil {
		user, err := s.userRepo.GetByID(ctx, userID)
		if err == nil && user != nil {
			return s.urlResolver.ResolveUser(user)
		}
	}
	return s.urlResolver.Resolve(userID, "", "")
}

func (s *NotificationService) RegisterDevice(ctx context.Context, userID int64, fcmToken, deviceType, deviceID, voipToken string) error {
	fcmToken = strings.TrimSpace(fcmToken)
	rawDeviceType := strings.TrimSpace(deviceType)
	normalizedPlatform, knownPlatform := classifyDevicePlatform(rawDeviceType)
	deviceType = normalizeDeviceType(rawDeviceType)
	deviceID = strings.TrimSpace(deviceID)
	voipToken = strings.TrimSpace(voipToken)

	if fcmToken == "" {
		if !(normalizedPlatform == devicePlatformIOS && voipToken != "") {
			return apperrors.BadRequest("invalid fcm_token: empty")
		}
		if deviceID == "" {
			return apperrors.BadRequest("device_id is required when registering ios voip-only device")
		}
	} else {
		if err := validateFCMToken(fcmToken); err != nil {
			return err
		}
	}
	if err := validateVoIPToken(voipToken); err != nil {
		return err
	}
	if !knownPlatform {
		s.logger.Warn("Registering device with unknown device_type; push routing will use fallback",
			zap.Int64("user_id", userID),
			zap.String("device_type", rawDeviceType),
			zap.String("device_id", deviceID),
		)
	}

	device := &domain.UserDevice{
		UserID:     userID,
		FCMToken:   fcmToken,
		DeviceType: deviceType,
		Platform:   normalizedPlatform,
		DeviceID:   deviceID,
		VoIPToken:  optionalString(voipToken),
	}

	if err := s.deviceRepo.SaveDevice(ctx, device); err != nil {
		s.logger.Error("Failed to register device", zap.Error(err))
		return err
	}

	s.logger.Info("Device registered for push notifications",
		zap.Int64("user_id", userID),
		zap.String("device_type", deviceType),
		zap.String("platform", normalizedPlatform),
		zap.String("device_id", deviceID),
		zap.Bool("has_voip_token", device.VoIPToken != nil),
	)
	return nil
}

func (s *NotificationService) DeactivateDevice(ctx context.Context, userID int64, deviceID string) error {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return nil
	}
	return s.deviceRepo.DeactivateDevice(ctx, userID, deviceID)
}

func (s *NotificationService) DeactivateToken(ctx context.Context, userID int64, token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil
	}
	return s.deviceRepo.DeactivateToken(ctx, userID, token)
}

func (s *NotificationService) DeactivateAllDevices(ctx context.Context, userID int64) error {
	return s.deviceRepo.DeactivateAllUserDevices(ctx, userID)
}

func (s *NotificationService) SendPushNotification(userID int64, title, body string, data map[string]string) error {
	if s.fcmClient == nil {
		s.logger.Debug("FCM client not initialized, skipping push notification")
		return nil
	}

	title = strings.TrimSpace(title)
	body = trimBody(body, maxPushBodyRunes)

	select {
	case s.queue <- PushNotificationJob{UserID: userID, Title: title, Body: body, Data: data}:
		return nil
	default:
		s.logger.Warn("Notification queue full, dropping message", zap.Int64("user_id", userID))
		return nil
	}
}

func (s *NotificationService) SendPushNotificationNow(ctx context.Context, userID int64, title, body string, data map[string]string) (string, error) {
	if s.fcmClient == nil {
		s.logger.Info("FCM client not initialized, skipping synchronous push", zap.Int64("user_id", userID))
		return domain.PushStatusSkipped, nil
	}

	tokens, err := s.deviceRepo.GetUserTokens(ctx, userID)
	if err != nil {
		return domain.PushStatusFailed, err
	}
	if len(tokens) == 0 {
		s.logger.Info("No FCM tokens for user, skipping synchronous push", zap.Int64("user_id", userID))
		return domain.PushStatusSkipped, nil
	}

	pushCtx, cancel := context.WithTimeout(ctx, pushSendTimeout)
	defer cancel()

	if err := s.sendActualPush(pushCtx, userID, strings.TrimSpace(title), trimBody(body, maxPushBodyRunes), data); err != nil {
		return domain.PushStatusFailed, err
	}
	return domain.PushStatusSent, nil
}

func (s *NotificationService) sendActualPush(ctx context.Context, userID int64, title, body string, data map[string]string) error {
	tokens, err := s.deviceRepo.GetUserTokens(ctx, userID)
	if err != nil {
		return err
	}
	if len(tokens) == 0 || s.fcmClient == nil {
		return nil
	}

	seen := make(map[string]struct{}, len(tokens))
	sanitized := make([]string, 0, len(tokens))

	for _, t := range tokens {
		t = strings.TrimSpace(t)
		if t == "" || t == "string" || strings.ContainsAny(t, " \n\t") || len([]rune(t)) < 50 {
			_ = s.deviceRepo.DeactivateToken(ctx, userID, t)
			continue
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		sanitized = append(sanitized, t)
	}

	if len(sanitized) == 0 {
		s.logger.Warn("All tokens are invalid, nothing to send", zap.Int64("user_id", userID))
		return nil
	}

	s.logger.Info("Sending push notification",
		zap.Int64("user_id", userID),
		zap.String("title", title),
		zap.Int("tokens_count", len(sanitized)),
	)

	s.logger.Info("Tokens sanitized",
		zap.Int64("user_id", userID),
		zap.Int("tokens_before", len(tokens)),
		zap.Int("tokens_after", len(sanitized)),
	)

	msg := &messaging.MulticastMessage{
		Tokens: sanitized,
		Notification: &messaging.Notification{
			Title: title,
			Body:  body,
		},
		Data: data,
	}

	br, err := s.fcmClient.SendEachForMulticast(ctx, msg)
	if err != nil {
		return err
	}

	if br.FailureCount > 0 {
		deleted := make(map[string]struct{}, br.FailureCount)
		for i, resp := range br.Responses {
			if resp.Success {
				continue
			}
			tok := sanitized[i]

			s.logger.Warn("FCM send failed",
				zap.Int64("user_id", userID),
				zap.String("masked_token", maskedToken(tok)),
				zap.Error(resp.Error),
			)

			if shouldDeleteToken(resp.Error) {
				if _, ok := deleted[tok]; ok {
					continue
				}
				deleted[tok] = struct{}{}
				if err := s.deviceRepo.DeactivateTokenByValue(ctx, tok); err != nil {
					s.logger.Warn("Failed to deactivate bad token", zap.Error(err))
				} else {
					s.logger.Info("Deactivated bad FCM token", zap.Int64("user_id", userID))
				}
			}
		}
	}

	return nil
}

func (s *NotificationService) CreateAndSendNotification(ctx context.Context, req domain.CreateNotificationRequest) error {
	title, content, locale := s.localizeNotification(ctx, req)

	notification := &domain.Notification{
		ID:          uuid.New(),
		UserID:      req.UserID,
		OrganizerID: req.OrganizerID,
		Type:        req.Type,
		Title:       title,
		Content:     trimBody(content, maxPushBodyRunes),
		IsRead:      false,
		Metadata:    req.Metadata,
		CreatedAt:   time.Now(),
	}
	notification.Metadata = cloneNotificationMetadata(req.Metadata)
	notification.Metadata["language"] = string(locale)
	if req.Type == "calendar_reminder" {
		if minutes, err := strconv.Atoi(notification.Metadata["minutes_before"]); err == nil {
			notification.Metadata["countdown_text"] = localizedCountdown(locale, minutes)
		}
	}
	req.Metadata = notification.Metadata

	if err := s.notifRepo.Create(ctx, notification); err != nil {
		s.logger.Error("Failed to save notification to DB", zap.Error(err))
		return err
	}

	pushData := make(map[string]string, len(req.Metadata)+2)
	for k, v := range req.Metadata {
		pushData[k] = v
	}
	pushData["notification_id"] = notification.ID.String()
	pushData["type"] = req.Type

	if s.wsHub != nil {
		notificationID := notification.ID
		wsMsg := &websocket.Message{
			ID:        &notificationID,
			Type:      req.Type,
			To:        req.UserID,
			Title:     title,
			Body:      notification.Content,
			Content:   pushData,
			Metadata:  pushData,
			Timestamp: time.Now().Unix(),
		}
		s.wsHub.SendToUser(req.UserID, wsMsg)
	}

	pushErr := s.SendPushNotification(req.UserID, title, notification.Content, pushData)
	s.enqueueNotificationEmail(notification, req)
	return pushErr
}

func (s *NotificationService) enqueueNotificationEmail(notification *domain.Notification, req domain.CreateNotificationRequest) {
	if notification == nil || s.emailService == nil || !s.emailService.Enabled() {
		return
	}
	if !s.shouldSendNotificationEmail(req.Type) {
		return
	}

	go func(notificationCopy domain.Notification, requestCopy domain.CreateNotificationRequest) {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		if err := s.sendNotificationEmail(ctx, &notificationCopy, requestCopy); err != nil {
			s.logger.Warn("notification email send failed",
				zap.String("notification_id", notificationCopy.ID.String()),
				zap.String("event_type", requestCopy.Type),
				zap.Int64("user_id", requestCopy.UserID),
				zap.Error(err),
			)
		}
	}(*notification, req)
}

func (s *NotificationService) shouldSendNotificationEmail(notificationType string) bool {
	if len(s.mailTypeFilter) == 0 {
		return false
	}
	normalized := strings.ToLower(strings.TrimSpace(notificationType))
	if normalized == "" {
		return false
	}
	_, ok := s.mailTypeFilter[normalized]
	return ok
}

func (s *NotificationService) sendNotificationEmail(ctx context.Context, notification *domain.Notification, req domain.CreateNotificationRequest) error {
	if s.userRepo == nil || s.emailResolver == nil {
		return nil
	}

	user, err := s.userRepo.GetByID(ctx, req.UserID)
	if err != nil {
		return fmt.Errorf("load notification recipient user: %w", err)
	}

	recipient, err := s.emailResolver.ResolveEmailRecipient(user)
	if err != nil {
		s.logger.Warn("notification email recipient skipped",
			zap.String("notification_id", notification.ID.String()),
			zap.String("event_type", req.Type),
			zap.Int64("user_id", req.UserID),
			zap.String("username", user.Username),
			zap.String("email", user.Email),
			zap.Error(err),
		)
		return nil
	}

	fullName := strings.TrimSpace(strings.TrimSpace(user.FirstName) + " " + strings.TrimSpace(user.LastName))
	locale := i18n.ResolveRecipient(user.Language)
	emailTitle, emailContent := localizeNotificationForLocale(req, locale)
	template := BuildNotificationEmailTemplate(NotificationEmailTemplateInput{
		Language:     locale,
		Title:        deriveEmailSubject(req.Type, emailTitle, locale),
		GreetingName: fullName,
		Message:      emailContent,
		ActionURL:    firstNonEmpty(req.Metadata["action_url"], req.Metadata["deep_link"], req.Metadata["url"]),
		CreatedAt:    notification.CreatedAt,
		AppName:      "Alem Workspace",
	})

	if err := s.emailService.SendEmail(ctx, recipient, template.Subject, template.HTMLBody, template.TextBody); err != nil {
		return fmt.Errorf("send email to %s: %w", recipient, err)
	}

	s.logger.Info("notification email sent",
		zap.String("recipient", recipient),
		zap.String("subject", template.Subject),
		zap.String("notification_id", notification.ID.String()),
		zap.String("event_type", req.Type),
	)
	return nil
}

func deriveEmailSubject(notificationType, fallbackTitle string, languages ...i18n.Locale) string {
	locale := i18n.Russian
	if len(languages) > 0 {
		locale = i18n.ResolveRecipient(string(languages[0]))
	}
	switch strings.ToLower(strings.TrimSpace(notificationType)) {
	case "new_message":
		return i18n.Text(locale, "email.subject.message", nil)
	case "incoming_call":
		return i18n.Text(locale, "email.subject.notification", nil)
	case "web_login", "security", "security_web_login":
		return i18n.Text(locale, "email.subject.security", nil)
	default:
		if strings.TrimSpace(fallbackTitle) != "" {
			return strings.TrimSpace(fallbackTitle)
		}
		return i18n.Text(locale, "email.subject.notification", nil)
	}
}

func (s *NotificationService) GetUserNotifications(ctx context.Context, userID int64, limit, offset int) ([]domain.Notification, error) {
	notifications, err := s.notifRepo.GetUserNotifications(ctx, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	for i := range notifications {
		notifications[i].Title, notifications[i].Content = normalizeNotificationText(
			notifications[i].Type,
			notifications[i].Title,
			notifications[i].Content,
			notifications[i].Metadata,
		)
	}
	return notifications, nil
}

func (s *NotificationService) MarkAsRead(ctx context.Context, notifID uuid.UUID, userID int64) error {
	return s.notifRepo.MarkAsRead(ctx, notifID, userID)
}

func (s *NotificationService) MarkAllAsRead(ctx context.Context, userID int64) error {
	return s.notifRepo.MarkAllAsRead(ctx, userID)
}

func (s *NotificationService) SendChatPushOnly(ctx context.Context, userID int64, title, body string, meta map[string]string) {
	if s.fcmClient == nil {
		s.logger.Warn("FCM client not initialized, cannot send push",
			zap.Int64("user_id", userID),
		)
		return
	}

	tokens, err := s.deviceRepo.GetUserTokens(ctx, userID)
	if err != nil {
		s.logger.Error("Failed to get user tokens", zap.Error(err), zap.Int64("user_id", userID))
		return
	}
	if len(tokens) == 0 {
		s.logger.Warn("No FCM tokens for user, skipping push", zap.Int64("user_id", userID))
		return
	}

	if err := s.SendPushNotification(userID, title, body, meta); err != nil {
		s.logger.Error("SendPushNotification failed", zap.Error(err), zap.Int64("user_id", userID))
	}
}

func normalizeNotificationText(notificationType, title, content string, meta map[string]string) (string, string) {
	title = strings.TrimSpace(title)
	content = strings.TrimSpace(content)

	shouldReplaceTitle := title == "" || isGenericNotificationTitle(title)
	if meta == nil {
		meta = map[string]string{}
	}

	switch notificationType {
	case "kanban_task_created":
		taskLabel := firstNonEmpty(meta["task_key"], meta["task_id"])
		taskTitle := firstNonEmpty(meta["task_title"], "задача")
		boardName := firstNonEmpty(meta["board_name"], "AlemBoard")
		if shouldReplaceTitle {
			title = joinTitle("Новая задача", taskLabel)
		}
		if content == "" {
			content = fmt.Sprintf("Создана задача %s на доске «%s»: %s", taskLabel, boardName, taskTitle)
		}
	case "kanban_task_assigned":
		taskLabel := firstNonEmpty(meta["task_key"], meta["task_id"])
		taskTitle := firstNonEmpty(meta["task_title"], "задача")
		boardName := firstNonEmpty(meta["board_name"], "AlemBoard")
		if shouldReplaceTitle {
			title = joinTitle("Вам назначили задачу", taskLabel)
		}
		if content == "" {
			content = fmt.Sprintf("Проверьте задачу %s на доске «%s»: %s", taskLabel, boardName, taskTitle)
		}
	case "calendar_invite":
		if shouldReplaceTitle {
			title = "Приглашение на встречу"
		}
		if content == "" {
			content = "Вас добавили в событие календаря"
		}
	case "calendar_event_updated":
		if shouldReplaceTitle {
			title = "Встреча обновлена"
		}
		if content == "" {
			content = "Изменились детали встречи в календаре"
		}
	case "calendar_event_deleted":
		if shouldReplaceTitle {
			title = "Встреча отменена"
		}
		if content == "" {
			content = "Событие календаря было отменено"
		}
	case "calendar_reminder":
		if shouldReplaceTitle {
			eventTitle := firstNonEmpty(meta["event_title"], meta["title"])
			if eventTitle != "" {
				title = fmt.Sprintf("Напоминание: «%s»", eventTitle)
			} else {
				title = "Напоминание о встрече"
			}
		}
		if content == "" {
			eventTitle := firstNonEmpty(meta["event_title"], meta["title"], "Встреча")
			countdown := firstNonEmpty(meta["countdown_text"], "скоро")
			content = fmt.Sprintf("Встреча «%s» начнется %s", eventTitle, countdown)
		}
	case "drive_access_granted":
		if shouldReplaceTitle {
			title = "Доступ к файлам"
		}
		if content == "" {
			content = "Вам открыли доступ к документу или папке"
		}
	case "web_login":
		if shouldReplaceTitle {
			title = "Новый вход в веб-клиент"
		}
		if content == "" {
			content = "В ваш аккаунт вошли через веб-клиент"
		}
	}

	if title == "" {
		title = "Уведомление"
	}
	if content == "" {
		content = "Откройте уведомление, чтобы посмотреть детали"
	}
	return title, content
}

func isGenericNotificationTitle(title string) bool {
	switch strings.TrimSpace(strings.ToLower(title)) {
	case "новое сообщение", "статус пользователя", "набор текста", "система":
		return true
	default:
		return false
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func joinTitle(prefix, suffix string) string {
	suffix = strings.TrimSpace(suffix)
	if suffix == "" {
		return prefix
	}
	return prefix + " " + suffix
}

func (s *NotificationService) SendCalendarEventEmail(toEmail string, userName string, title string, description string, start, end time.Time) {
	if s.emailService == nil {
		s.logger.Warn("Email service is nil, skipping email notification")
		return
	}

	locale := s.recipientLocaleByEmail(context.Background(), toEmail)
	timeLayout := "02.01.2006 15:04"
	startStr := start.Format(timeLayout)
	endStr := end.Format(timeLayout)

	if userName == "" {
		userName = i18n.Text(locale, "email.colleague", nil)
	}
	if description == "" {
		description = i18n.Text(locale, "email.calendar.no_description", nil)
	}

	template := BuildNotificationEmailTemplate(NotificationEmailTemplateInput{
		Language:     locale,
		Title:        i18n.Text(locale, "email.calendar.event.subject", map[string]any{"Title": title}),
		GreetingName: userName,
		Message:      i18n.Text(locale, "email.calendar.event.body", map[string]any{"Title": title, "Start": startStr, "End": endStr, "Description": description}),
		CreatedAt:    time.Now().UTC(),
		AppName:      "Alem Workspace",
	})

	if err := s.emailService.SendEmail(context.Background(), toEmail, template.Subject, template.HTMLBody, template.TextBody); err != nil {
		s.logger.Error("Failed to send calendar email",
			zap.String("email", toEmail),
			zap.Error(err),
		)
	}
}

func (s *NotificationService) SendCalendarInviteEmail(
	ctx context.Context,
	eventID uuid.UUID,
	attendeeID int64,
	organizerID *int64,
	title, description, location string,
	start, end time.Time,
	isUpdate, isCancel bool,
	sequence int,
) {
	s.sendCalendarInviteEmail(ctx, eventID, attendeeID, organizerID, title, description, location, start, end, isUpdate, isCancel, sequence, "")
}

func (s *NotificationService) sendCalendarInviteEmail(
	ctx context.Context,
	eventID uuid.UUID,
	attendeeID int64,
	organizerID *int64,
	title, description, location string,
	start, end time.Time,
	isUpdate, isCancel bool,
	sequence int,
	meetingURL string,
) {
	if s.emailService == nil || !s.emailService.Enabled() {
		s.logger.Warn("Email service is nil or disabled, skipping calendar invite email")
		return
	}

	attendee, err := s.userRepo.GetByID(ctx, attendeeID)
	if err != nil || attendee == nil {
		s.logger.Error("Failed to fetch attendee for calendar invite", zap.Error(err))
		return
	}

	attendeeEmail := attendee.Email
	if attendeeEmail == "" {
		return // Cannot send email if no email
	}

	attendeeName := attendee.FirstName
	if attendeeName == "" {
		attendeeName = attendee.Username
	}

	organizerName := i18n.Text(i18n.ResolveRecipient(attendee.Language), "email.organizer", nil)
	organizerEmail := "no-reply@workspace.local"
	if organizerID != nil {
		organizer, err := s.userRepo.GetByID(ctx, *organizerID)
		if err == nil && organizer != nil {
			if organizer.FirstName != "" {
				organizerName = organizer.FirstName
			} else if organizer.Username != "" {
				organizerName = organizer.Username
			}
			if organizer.Email != "" {
				organizerEmail = organizer.Email
			}
		}
	}

	actionTokenAccept, err := GenerateCalendarActionToken(s.jwtSecret, attendeeID, eventID, "accepted")
	if err != nil {
		s.logger.Error("Failed to generate accept token", zap.Error(err))
		return
	}
	actionTokenDecline, err := GenerateCalendarActionToken(s.jwtSecret, attendeeID, eventID, "declined")
	if err != nil {
		s.logger.Error("Failed to generate decline token", zap.Error(err))
		return
	}
	actionTokenTentative, err := GenerateCalendarActionToken(s.jwtSecret, attendeeID, eventID, "tentative")
	if err != nil {
		s.logger.Error("Failed to generate tentative token", zap.Error(err))
		return
	}

	emailTemplate := BuildCalendarInviteEmail(CalendarInviteEmailInput{
		Language:      i18n.ResolveRecipient(attendee.Language),
		EventID:       eventID,
		EventTitle:    title,
		OrganizerName: organizerName,
		AttendeeName:  attendeeName,
		StartTime:     start,
		Location:      location,
		Description:   description,
		BaseURL:       s.appPublicURL,
		JWTSecret:     s.jwtSecret,
		IsUpdate:      isUpdate,
		IsCancel:      isCancel,
	}, actionTokenAccept, actionTokenDecline, actionTokenTentative)

	icsMethod := ICSMethodRequest
	icsStatus := "CONFIRMED"
	if isCancel {
		icsMethod = ICSMethodCancel
		icsStatus = "CANCELLED"
	}

	icsEvent := ICSEventData{
		UID:    fmt.Sprintf("%s@workspace.local", eventID.String()),
		Method: icsMethod,
		Organizer: ICSAttendee{
			Name:  organizerName,
			Email: organizerEmail,
		},
		Attendees: []ICSAttendee{
			{Name: attendeeName, Email: attendeeEmail, Status: "NEEDS-ACTION"},
		},
		Summary:     title,
		Description: description,
		Location:    location,
		URL:         meetingURL,
		Start:       start,
		End:         end,
		Created:     time.Now(),
		Modified:    time.Now(),
		Sequence:    sequence,
		Status:      icsStatus,
	}

	icsData := BuildICS(icsEvent)

	if err := s.emailService.SendCalendarInvite(
		ctx,
		attendeeEmail,
		emailTemplate.Subject,
		emailTemplate.HTMLBody,
		emailTemplate.TextBody,
		icsData,
	); err != nil {
		s.logger.Error("Failed to send calendar invite email",
			zap.String("email", attendeeEmail),
			zap.Error(err),
		)
	}
}

func (s *NotificationService) SendCalendarEventNotification(
	ctx context.Context,
	userID int64,
	organizerID *int64,
	toEmail string,
	_ string,
	title string,
	description string,
	start, end time.Time,
	eventID string,
) {
	timeLayout := "02.01.2006 15:04"
	startStr := start.Format(timeLayout)
	endStr := end.Format(timeLayout)

	if description == "" {
		description = i18n.SystemNotification(s.recipientLocale(ctx, userID), "Без описания")
	}

	content := fmt.Sprintf("📅 %s\n%s — %s\n%s", title, startStr, endStr, description)

	metadata := map[string]string{
		"kind":     "calendar_event",
		"start":    start.Format(time.RFC3339),
		"end":      end.Format(time.RFC3339),
		"title":    title,
		"to_email": toEmail,
	}
	if eventID != "" {
		metadata["event_id"] = eventID
	}

	req := domain.CreateNotificationRequest{
		UserID:        userID,
		OrganizerID:   organizerID,
		Type:          "calendar_event",
		TitleTemplate: &domain.NotificationTemplate{Source: "Новое событие в календаре", Args: []any{}},
		Title:         "Новое событие в календаре",
		Content:       content,
		Metadata:      metadata,
	}

	if err := s.CreateAndSendNotification(ctx, req); err != nil {
		s.logger.Error("Failed to create/send calendar notification",
			zap.Int64("user_id", userID),
			zap.Error(err),
		)
	}
}

func validateFCMToken(token string) error {
	if token == "" {
		return apperrors.BadRequest("invalid fcm_token: empty")
	}
	if token == "string" {
		return apperrors.BadRequest("invalid fcm_token: placeholder")
	}
	if strings.ContainsAny(token, " \n\t") {
		return apperrors.BadRequest("invalid fcm_token: contains spaces/newlines")
	}
	if len([]rune(token)) < 50 {
		return apperrors.BadRequest("invalid fcm_token: too short")
	}
	return nil
}

func validateVoIPToken(token string) error {
	if token == "" {
		return nil
	}
	if token == "string" {
		return apperrors.BadRequest("invalid voip_token: placeholder")
	}
	if strings.ContainsAny(token, " \n\t") {
		return apperrors.BadRequest("invalid voip_token: contains spaces/newlines")
	}
	return nil
}

func optionalString(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func normalizeDeviceType(deviceType string) string {
	platform, _ := classifyDevicePlatform(deviceType)
	return platform
}

func buildBackgroundAPNSConfigWithData(expiration time.Time, customData map[string]interface{}) *messaging.APNSConfig {
	payload := &messaging.APNSPayload{
		Aps: &messaging.Aps{
			ContentAvailable: true,
		},
	}
	if len(customData) > 0 {
		payload.CustomData = customData
	}

	return &messaging.APNSConfig{
		Headers: map[string]string{
			"apns-push-type":  "background",
			"apns-priority":   "5",
			"apns-expiration": fmt.Sprintf("%d", expiration.Unix()),
		},
		Payload: payload,
	}
}

func buildIncomingCallAPNSConfig(expiration time.Time, title, body, category string, customData map[string]interface{}) *messaging.APNSConfig {
	title = strings.TrimSpace(title)
	body = strings.TrimSpace(body)
	category = strings.TrimSpace(category)

	payload := &messaging.APNSPayload{
		Aps: &messaging.Aps{
			Alert: &messaging.ApsAlert{
				Title: title,
				Body:  body,
			},
			Sound:            "default",
			ContentAvailable: true,
			MutableContent:   true,
			Category:         category,
			ThreadID:         "incoming_call",
		},
	}
	if len(customData) > 0 {
		payload.CustomData = customData
	}

	return &messaging.APNSConfig{
		Headers: map[string]string{
			"apns-push-type":  "alert",
			"apns-priority":   "10",
			"apns-expiration": fmt.Sprintf("%d", expiration.Unix()),
		},
		Payload: payload,
	}
}

func sendCallMulticast(
	ctx context.Context,
	client multicastSender,
	msg *messaging.MulticastMessage,
) (*messaging.BatchResponse, error) {
	if client == nil || msg == nil || len(msg.Tokens) == 0 {
		return nil, nil
	}
	return client.SendEachForMulticast(ctx, msg)
}

func classifyDevicePlatform(deviceType string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(deviceType)) {
	case "ios", "iphone", "ipad", "apple", "apns":
		return devicePlatformIOS, true
	case "android":
		return devicePlatformAndroid, true
	case "web", "browser":
		return devicePlatformWeb, true
	case "", "unknown":
		return devicePlatformUnknown, false
	default:
		return devicePlatformUnknown, false
	}
}

type callNotificationPayload struct {
	Type                string
	Event               string
	Action              string
	NotificationType    string
	CallEvent           string
	PayloadType         string
	EventID             string
	NotificationID      string
	CallID              string
	RoomID              string
	RoomName            string
	ChatID              string
	FromUserID          int64
	FromName            string
	FromFullName        string
	CallerID            int64
	CallerName          string
	CallerAvatar        string
	LivekitURL          string
	LivekitToken        string
	IsVideo             bool
	SentAt              string
	SentAtUnix          int64
	CreatedAt           string
	Title               string
	Body                string
	Priority            string
	Reason              string
	ReceiverID          int64
	CallType            string
	Status              string
	TargetUserID        int64
	AcceptedByUserID    int64
	AcceptedByDeviceID  string
	AcceptedBySessionID string
	ActiveDeviceID      string
	ExpiresAt           string
}

func (p callNotificationPayload) toStringMap() map[string]string {
	data := map[string]string{
		"type":              p.Type,
		"event":             p.Event,
		"action":            p.Action,
		"notification_type": p.NotificationType,
		"call_event":        p.CallEvent,
		"payload_type":      p.PayloadType,
		"event_id":          p.EventID,
		"notification_id":   p.NotificationID,
		"call_id":           p.CallID,
		"is_video":          strconv.FormatBool(p.IsVideo),
		"sent_at":           p.SentAt,
		"sent_at_unix":      strconv.FormatInt(p.SentAtUnix, 10),
	}
	if p.RoomID != "" {
		data["room_id"] = p.RoomID
	}
	if p.RoomName != "" {
		data["room_name"] = p.RoomName
	}
	if p.ChatID != "" {
		data["chat_id"] = p.ChatID
	}
	if p.FromUserID != 0 {
		data["from_user_id"] = strconv.FormatInt(p.FromUserID, 10)
	}
	if p.FromName != "" {
		data["from_name"] = p.FromName
	}
	if p.FromFullName != "" {
		data["from_full_name"] = p.FromFullName
	}
	if p.CallerID != 0 {
		data["caller_id"] = strconv.FormatInt(p.CallerID, 10)
	}
	if p.CallerName != "" {
		data["caller_name"] = p.CallerName
	}
	if p.CallerAvatar != "" || p.Event == "incoming_call" {
		data["caller_avatar"] = p.CallerAvatar
	}
	if p.LivekitURL != "" {
		data["livekit_url"] = p.LivekitURL
	}
	if p.LivekitToken != "" {
		data["livekit_token"] = p.LivekitToken
	}
	if p.CreatedAt != "" {
		data["created_at"] = p.CreatedAt
	}
	if p.Title != "" {
		data["title"] = p.Title
	}
	if p.Body != "" {
		data["body"] = p.Body
	}
	if p.Priority != "" {
		data["priority"] = p.Priority
	}
	if p.Reason != "" {
		data["reason"] = p.Reason
	}
	if p.ReceiverID != 0 {
		data["receiver_id"] = strconv.FormatInt(p.ReceiverID, 10)
	}
	if p.CallType != "" {
		data["call_type"] = p.CallType
	}
	if p.Status != "" {
		data["status"] = p.Status
	}
	if p.TargetUserID != 0 || p.CallType == "group" {
		data["target_user_id"] = strconv.FormatInt(p.TargetUserID, 10)
	}
	if p.AcceptedByUserID != 0 {
		data["accepted_by_user_id"] = strconv.FormatInt(p.AcceptedByUserID, 10)
	}
	if p.AcceptedByDeviceID != "" {
		data["accepted_by_device_id"] = p.AcceptedByDeviceID
	}
	if p.AcceptedBySessionID != "" {
		data["accepted_by_session_id"] = p.AcceptedBySessionID
	}
	if p.ActiveDeviceID != "" {
		data["active_device_id"] = p.ActiveDeviceID
	}
	if p.ExpiresAt != "" {
		data["expires_at"] = p.ExpiresAt
	}
	return data
}

func (p callNotificationPayload) toMap() map[string]interface{} {
	data := map[string]interface{}{
		"type":              p.Type,
		"event":             p.Event,
		"action":            p.Action,
		"notification_type": p.NotificationType,
		"call_event":        p.CallEvent,
		"payload_type":      p.PayloadType,
		"event_id":          p.EventID,
		"notification_id":   p.NotificationID,
		"call_id":           p.CallID,
		"is_video":          p.IsVideo,
		"sent_at":           p.SentAt,
		"sent_at_unix":      p.SentAtUnix,
	}
	if p.RoomID != "" {
		data["room_id"] = p.RoomID
	}
	if p.RoomName != "" {
		data["room_name"] = p.RoomName
	}
	if p.ChatID != "" {
		data["chat_id"] = p.ChatID
	}
	if p.FromUserID != 0 {
		data["from_user_id"] = p.FromUserID
	}
	if p.FromName != "" {
		data["from_name"] = p.FromName
	}
	if p.FromFullName != "" {
		data["from_full_name"] = p.FromFullName
	}
	if p.CallerID != 0 {
		data["caller_id"] = p.CallerID
	}
	if p.CallerName != "" {
		data["caller_name"] = p.CallerName
	}
	if p.CallerAvatar != "" || p.Event == "incoming_call" {
		data["caller_avatar"] = p.CallerAvatar
	}
	if p.LivekitURL != "" {
		data["livekit_url"] = p.LivekitURL
	}
	if p.LivekitToken != "" {
		data["livekit_token"] = p.LivekitToken
	}
	if p.CreatedAt != "" {
		data["created_at"] = p.CreatedAt
	}
	if p.Title != "" {
		data["title"] = p.Title
	}
	if p.Body != "" {
		data["body"] = p.Body
	}
	if p.Priority != "" {
		data["priority"] = p.Priority
	}
	if p.Reason != "" {
		data["reason"] = p.Reason
	}
	if p.ReceiverID != 0 {
		data["receiver_id"] = p.ReceiverID
	}
	if p.CallType != "" {
		data["call_type"] = p.CallType
	}
	if p.Status != "" {
		data["status"] = p.Status
	}
	if p.TargetUserID != 0 || p.CallType == "group" {
		data["target_user_id"] = p.TargetUserID
	}
	if p.AcceptedByUserID != 0 {
		data["accepted_by_user_id"] = p.AcceptedByUserID
	}
	if p.AcceptedByDeviceID != "" {
		data["accepted_by_device_id"] = p.AcceptedByDeviceID
	}
	if p.AcceptedBySessionID != "" {
		data["accepted_by_session_id"] = p.AcceptedBySessionID
	}
	if p.ActiveDeviceID != "" {
		data["active_device_id"] = p.ActiveDeviceID
	}
	if p.ExpiresAt != "" {
		data["expires_at"] = p.ExpiresAt
	}
	return data
}

func buildIncomingCallPayload(
	fromUserID int64,
	fromName string,
	callID uuid.UUID,
	roomID string,
	chatID string,
	livekitURL string,
	livekitToken string,
	priority string,
	isVideo bool,
	sentAt time.Time,
	callType string,
) callNotificationPayload {
	callType = strings.TrimSpace(callType)
	if callType == "" {
		callType = domain.CallTypePrivate
	}
	return callNotificationPayload{
		Type:             "incoming_call",
		Event:            "call.incoming",
		Action:           "incoming_call",
		NotificationType: "incoming_call",
		CallEvent:        "incoming_call",
		PayloadType:      "incoming_call",
		EventID:          fmt.Sprintf("%s:%s", callID.String(), "incoming_call"),
		NotificationID:   callID.String(),
		CallID:           callID.String(),
		RoomID:           roomID,
		RoomName:         roomID,
		ChatID:           chatID,
		FromUserID:       fromUserID,
		FromName:         fromName,
		FromFullName:     fromName,
		CallerID:         fromUserID,
		CallerName:       fromName,
		CallerAvatar:     "",
		LivekitURL:       livekitURL,
		LivekitToken:     strings.TrimSpace(livekitToken),
		IsVideo:          isVideo,
		SentAt:           sentAt.Format(time.RFC3339),
		SentAtUnix:       sentAt.Unix(),
		CreatedAt:        sentAt.Format(time.RFC3339),
		Title:            "Входящий звонок",
		Body:             fmt.Sprintf("%s звонит вам", fromName),
		Priority:         priority,
		CallType:         callType,
	}
}

func (s *NotificationService) SetLiveKitTokenIssuer(tokenIssuer livekitTokenIssuer) {
	s.tokenIssuer = tokenIssuer
}

func (s *NotificationService) resolvePreconnectParticipantName(ctx context.Context, userID int64) string {
	if s.userRepo == nil || userID == 0 {
		return fmt.Sprintf("User %d", userID)
	}

	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil || user == nil {
		return fmt.Sprintf("User %d", userID)
	}

	fullName := strings.TrimSpace(strings.TrimSpace(user.FirstName) + " " + strings.TrimSpace(user.LastName))
	if fullName != "" {
		return fullName
	}
	if username := strings.TrimSpace(user.Username); username != "" {
		return username
	}
	return fmt.Sprintf("User %d", userID)
}

func (s *NotificationService) issuePreconnectLiveKitToken(roomID string, userID int64, participantName, deviceID string) string {
	if s == nil || s.tokenIssuer == nil || strings.TrimSpace(roomID) == "" || userID == 0 {
		return ""
	}

	tokenCtx := LiveKitTokenContext{
		DeviceID: strings.TrimSpace(deviceID),
	}

	token, err := s.tokenIssuer.GenerateToken(roomID, userID, participantName, tokenCtx)
	if err != nil {
		s.logger.Warn("failed to issue preconnect livekit token",
			zap.Int64("user_id", userID),
			zap.String("room_id", roomID),
			zap.String("device_id", strings.TrimSpace(deviceID)),
			zap.Error(err),
		)
		return ""
	}

	return token
}

func buildCallSyncPayload(
	event string,
	callID uuid.UUID,
	roomName string,
	reason string,
	receiverID int64,
	isVideo bool,
	sentAt time.Time,
	callInfo *domain.CallHistory,
	acceptedByDeviceID string,
) callNotificationPayload {
	roomName = strings.TrimSpace(roomName)
	callType := "private"
	status := ""
	callerID := int64(0)
	targetUserID := int64(0)
	acceptedByUserID := int64(0)
	activeDeviceID := strings.TrimSpace(acceptedByDeviceID)
	acceptedBySessionID := ""
	createdAt := sentAt.UTC().Format(time.RFC3339)
	expiresAt := sentAt.Add(fcmCallTTL).UTC().Format(time.RFC3339)
	if callInfo != nil {
		if strings.EqualFold(strings.TrimSpace(callInfo.CallType), domain.CallTypeGroup) ||
			callInfo.ToUserID == 0 ||
			strings.HasPrefix(strings.TrimSpace(callInfo.RoomID), "call_group_") {
			callType = "group"
		}
		status = strings.TrimSpace(callInfo.Status)
		callerID = callInfo.FromUserID
		targetUserID = callInfo.ToUserID
		if callInfo.AcceptedByUserID != nil {
			acceptedByUserID = *callInfo.AcceptedByUserID
		}
		if callInfo.AcceptedByDeviceID != nil {
			if deviceID := strings.TrimSpace(*callInfo.AcceptedByDeviceID); deviceID != "" {
				activeDeviceID = deviceID
			}
		}
		if callInfo.AcceptedBySessionID != nil {
			acceptedBySessionID = strings.TrimSpace(*callInfo.AcceptedBySessionID)
		}
		createdAt = callInfo.CreatedAt.UTC().Format(time.RFC3339)
		expiresAt = callInfo.CreatedAt.Add(fcmCallTTL).UTC().Format(time.RFC3339)
	}
	return callNotificationPayload{
		Type:                "call_sync",
		Event:               event,
		Action:              event,
		NotificationType:    event,
		CallEvent:           event,
		PayloadType:         event,
		EventID:             fmt.Sprintf("%s:%s", callID.String(), strings.TrimSpace(event)),
		NotificationID:      callID.String(),
		CallID:              callID.String(),
		RoomID:              roomName,
		RoomName:            roomName,
		Reason:              strings.TrimSpace(reason),
		ReceiverID:          receiverID,
		CallType:            callType,
		Status:              status,
		CallerID:            callerID,
		TargetUserID:        targetUserID,
		AcceptedByUserID:    acceptedByUserID,
		IsVideo:             isVideo,
		SentAt:              sentAt.Format(time.RFC3339),
		SentAtUnix:          sentAt.Unix(),
		CreatedAt:           createdAt,
		AcceptedByDeviceID:  strings.TrimSpace(acceptedByDeviceID),
		AcceptedBySessionID: acceptedBySessionID,
		ActiveDeviceID:      activeDeviceID,
		ExpiresAt:           expiresAt,
	}
}

func trimBody(s string, max int) string {
	s = strings.TrimSpace(s)
	if len([]rune(s)) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max]) + "…"
}

func shouldDeleteToken(err error) bool {
	if err == nil {
		return false
	}
	if messaging.IsUnregistered(err) {
		return true
	}

	var gerr *googleapi.Error
	if errors.As(err, &gerr) {
		switch gerr.Code {
		case 404:
			return true
		}
	}

	msg := strings.ToLower(err.Error())

	switch {
	case strings.Contains(msg, "requested entity was not found"):
		return true
	case strings.Contains(msg, "not registered"):
		return true
	case strings.Contains(msg, "unregistered"):
		return true
	case strings.Contains(msg, "registration-token-not-registered"):
		return true
	case strings.Contains(msg, "invalid registration token"):
		return true
	case strings.Contains(msg, "invalid-registration-token"):
		return true
	case strings.Contains(msg, "baddevicetoken"):
		return true
	default:
		return false
	}
}

func classifyPushError(err error) string {
	if err == nil {
		return ""
	}
	if messaging.IsUnregistered(err) {
		return "registration-token-not-registered"
	}
	if messaging.IsInvalidArgument(err) {
		return "invalid-registration-token"
	}

	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "registration-token-not-registered"):
		return "registration-token-not-registered"
	case strings.Contains(msg, "invalid registration token"):
		return "invalid-registration-token"
	case strings.Contains(msg, "invalid-registration-token"):
		return "invalid-registration-token"
	case strings.Contains(msg, "baddevicetoken"):
		return "bad-device-token"
	case strings.Contains(msg, "unregistered"):
		return "unregistered"
	case strings.Contains(msg, "mismatched-credential"):
		return "mismatched-credential"
	case strings.Contains(msg, "third-party-auth-error"):
		return "third-party-auth-error"
	case strings.Contains(msg, "invalid-apns-credentials"):
		return "invalid-apns-credentials"
	case strings.Contains(msg, "apns-auth-error"):
		return "apns-auth-error"
	case strings.Contains(msg, "senderid mismatch"):
		return "senderid-mismatch"
	case strings.Contains(msg, "requested entity was not found"):
		return "requested-entity-not-found"
	default:
		return "unknown"
	}
}

func tokenPlatform(device domain.UserDevice) string {
	if platform := normalizeDeviceType(device.Platform); platform != devicePlatformUnknown {
		return platform
	}
	return normalizeDeviceType(device.DeviceType)
}

func maskedToken(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	if len(token) <= 12 {
		return token
	}
	return token[:6] + "..." + token[len(token)-4:]
}

func (s *NotificationService) resolvePushTargets(userID int64, devices []domain.UserDevice) pushTargets {
	targets := pushTargets{
		AndroidTokens:  make([]string, 0, len(devices)),
		IOSTokens:      make([]string, 0, len(devices)),
		FallbackTokens: make([]string, 0, len(devices)),
		VoIPTargets:    make([]voipPushTarget, 0, len(devices)),
	}

	for _, device := range devices {
		platform := tokenPlatform(device)
		if platform == devicePlatformUnknown {
			s.logger.Warn("Unknown device platform; using fallback push routing",
				zap.Int64("user_id", userID),
				zap.String("device_id", strings.TrimSpace(device.DeviceID)),
				zap.String("device_type", strings.TrimSpace(device.DeviceType)),
				zap.String("platform", strings.TrimSpace(device.Platform)),
			)
		}

		hasVoIPToken := false
		if device.VoIPToken != nil {
			vt := strings.TrimSpace(*device.VoIPToken)
			if vt != "" {
				hasVoIPToken = true
				targets.VoIPTargets = append(targets.VoIPTargets, voipPushTarget{
					UserID:    userID,
					DeviceID:  strings.TrimSpace(device.DeviceID),
					Platform:  platform,
					VoIPToken: vt,
				})
			}
		}

		token := strings.TrimSpace(device.FCMToken)
		if token == "" {
			continue
		}
		if platform == devicePlatformIOS && hasVoIPToken {
			continue
		}
		switch platform {
		case devicePlatformAndroid:
			targets.AndroidTokens = append(targets.AndroidTokens, token)
		case devicePlatformIOS:
			targets.IOSTokens = append(targets.IOSTokens, token)
		default:
			targets.FallbackTokens = append(targets.FallbackTokens, token)
		}
	}

	targets.AndroidTokens = sanitizeTokens(targets.AndroidTokens)
	targets.IOSTokens = sanitizeTokens(targets.IOSTokens)
	targets.FallbackTokens = sanitizeTokens(targets.FallbackTokens)
	targets.VoIPTargets = sanitizeVoIPTargets(targets.VoIPTargets)
	return targets
}

func sanitizeVoIPTargets(targets []voipPushTarget) []voipPushTarget {
	seen := make(map[string]struct{}, len(targets))
	sanitized := make([]voipPushTarget, 0, len(targets))
	for _, target := range targets {
		token := strings.TrimSpace(target.VoIPToken)
		if token == "" || token == "string" || strings.ContainsAny(token, " \n\t") {
			continue
		}
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		target.VoIPToken = token
		sanitized = append(sanitized, target)
	}
	return sanitized
}

func (s *NotificationService) sendVoIPIncomingCallBatch(
	ctx context.Context,
	userID int64,
	fromUserID int64,
	callID uuid.UUID,
	payload apnsVoIPPayload,
	expiration time.Time,
	targets []voipPushTarget,
) {
	if s.apnsVoip == nil || len(targets) == 0 {
		return
	}
	voipCtx, voipCancel := context.WithTimeout(ctx, 10*time.Second)
	defer voipCancel()

	for _, target := range targets {
		if err := s.apnsVoip.sendVoIP(voipCtx, target.VoIPToken, payload, expiration, s.logger); err != nil {
			tokenDeleted := false
			s.logger.Error("APNS VoIP push failed",
				zap.String("event", "push_send_failed"),
				zap.String("push_type", "incoming_call_voip"),
				zap.String("call_id", callID.String()),
				zap.Int64("caller_id", fromUserID),
				zap.Int64("callee_id", userID),
				zap.String("device_id", target.DeviceID),
				zap.String("platform", target.Platform),
				zap.String("token_type", "voip"),
				zap.String("token_prefix", maskedToken(target.VoIPToken)),
				zap.Bool("success", false),
				zap.String("firebase_message_id", ""),
				zap.String("error_code", classifyPushError(err)),
				zap.String("error_message", err.Error()),
				zap.Bool("token_invalidated", false),
			)
			if errors.Is(err, ErrAPNSVoIPTokenUnregistered) {
				if clearErr := s.deviceRepo.ClearVoIPToken(ctx, userID, target.DeviceID, target.VoIPToken); clearErr != nil {
					s.logger.Warn("Failed to clear expired VoIP token",
						zap.Int64("user_id", userID),
						zap.String("device_id", target.DeviceID),
						zap.String("token_prefix", maskedToken(target.VoIPToken)),
						zap.Error(clearErr),
					)
				} else {
					tokenDeleted = true
					s.logger.Warn("push_token_invalidated",
						zap.Int64("user_id", userID),
						zap.String("device_id", target.DeviceID),
						zap.String("platform", target.Platform),
						zap.String("push_type", "incoming_call_voip"),
						zap.String("token_type", "voip"),
						zap.Bool("token_invalidated", true),
					)
				}
			}
			s.logger.Warn("Incoming call push device result",
				zap.String("push_type", "incoming_call_voip"),
				zap.String("call_id", callID.String()),
				zap.Int64("caller_id", fromUserID),
				zap.Int64("callee_id", userID),
				zap.String("device_id", target.DeviceID),
				zap.String("platform", target.Platform),
				zap.String("token_type", "voip"),
				zap.String("token_prefix", maskedToken(target.VoIPToken)),
				zap.Bool("success", false),
				zap.String("firebase_message_id", ""),
				zap.String("error_code", classifyPushError(err)),
				zap.String("error_message", err.Error()),
				zap.Bool("token_invalidated", tokenDeleted),
			)
			continue
		}
		s.logger.Info("Incoming call push device result",
			zap.String("push_type", "incoming_call_voip"),
			zap.String("call_id", callID.String()),
			zap.Int64("caller_id", fromUserID),
			zap.Int64("callee_id", userID),
			zap.String("device_id", target.DeviceID),
			zap.String("platform", target.Platform),
			zap.String("token_type", "voip"),
			zap.String("token_prefix", maskedToken(target.VoIPToken)),
			zap.Bool("success", true),
			zap.String("firebase_message_id", ""),
			zap.String("error_code", ""),
			zap.String("error_message", ""),
			zap.Bool("token_invalidated", false),
		)
	}
}

func (s *NotificationService) SendIncomingCallPush(
	ctx context.Context,
	toUserID int64,
	fromUserID int64,
	fromName string,
	callID uuid.UUID,
	roomID string,
	chatID string,
	priority string,
	isVideo bool,
) error {
	return s.SendIncomingCallPushWithType(ctx, toUserID, fromUserID, fromName, callID, roomID, chatID, priority, isVideo, domain.CallTypePrivate)
}

func (s *NotificationService) SendIncomingCallPushWithType(
	ctx context.Context,
	toUserID int64,
	fromUserID int64,
	fromName string,
	callID uuid.UUID,
	roomID string,
	chatID string,
	priority string,
	isVideo bool,
	callType string,
) error {
	roomID = strings.TrimSpace(roomID)
	chatID = strings.TrimSpace(chatID)
	fromName = strings.TrimSpace(fromName)
	if fromName == "" {
		fromName = i18n.SystemNotification(s.recipientLocale(ctx, toUserID), "Коллега")
	}
	if roomID == "" {
		return apperrors.BadRequest("room_id is required")
	}

	if priority == "" {
		priority = "high"
	}

	devices, err := s.deviceRepo.GetUserDevices(ctx, toUserID)
	if err != nil {
		return err
	}
	targets := s.resolvePushTargets(toUserID, devices)
	targetLogIndex := buildPushTargetLogIndex(devices)
	s.logger.Info("Resolved incoming call target devices",
		zap.String("event", "push_send_started"),
		zap.Int64("receiver_id", toUserID),
		zap.String("call_id", callID.String()),
		zap.String("room_name", roomID),
		zap.Int("device_count", len(devices)),
		zap.Int("android_tokens", len(targets.AndroidTokens)),
		zap.Int("ios_tokens", len(targets.IOSTokens)),
		zap.Int("fallback_tokens", len(targets.FallbackTokens)),
		zap.Int("voip_token_count", len(targets.VoIPTargets)),
	)
	if len(targets.AndroidTokens) == 0 && len(targets.IOSTokens) == 0 && len(targets.FallbackTokens) == 0 && len(targets.VoIPTargets) == 0 {
		s.logger.Warn("No active tokens resolved for incoming call push",
			zap.Int64("receiver_id", toUserID),
			zap.String("call_id", callID.String()),
			zap.String("room_name", roomID),
		)
		return nil
	}

	sentAt := time.Now().UTC()
	expiration := sentAt.Add(fcmCallTTL)
	participantName := s.resolvePreconnectParticipantName(ctx, toUserID)
	preconnectToken := s.issuePreconnectLiveKitToken(roomID, toUserID, participantName, "")
	livekitURL := s.resolveLiveKitURLForUser(ctx, toUserID)
	payload := buildIncomingCallPayload(fromUserID, fromName, callID, roomID, chatID, livekitURL, preconnectToken, priority, isVideo, sentAt, callType)
	s.localizeCallPayload(ctx, toUserID, &payload)
	s.logger.Info("incoming_payload_built",
		zap.String("call_id", callID.String()),
		zap.Int64("caller_id", fromUserID),
		zap.Int64("callee_id", toUserID),
		zap.String("room_id", roomID),
		zap.String("status", domain.CallStatusRinging),
		zap.Bool("has_livekit_url", strings.TrimSpace(livekitURL) != ""),
		zap.Bool("has_livekit_token", strings.TrimSpace(preconnectToken) != ""),
	)
	data := payload.toStringMap()
	customData := payload.toMap()

	// Отправка VoIP пушей напрямую через APNS — единственный способ разбудить
	// убитое iOS-приложение и показать CallKit UI.
	if len(targets.VoIPTargets) > 0 {
		voipPayload := apnsVoIPPayload{
			Aps:          map[string]interface{}{"content-available": 1},
			Event:        "incoming_call",
			CallID:       callID.String(),
			CallerName:   fromName,
			FromFullName: fromName,
			FromUserID:   strconv.FormatInt(fromUserID, 10),
			CallerID:     strconv.FormatInt(fromUserID, 10),
			RoomName:     roomID,
			RoomID:       roomID,
			ChatID:       chatID,
			IsVideo:      isVideo,
			LivekitURL:   livekitURL,
			LivekitToken: preconnectToken,
		}
		if s.apnsVoip != nil {
			s.sendVoIPIncomingCallBatch(ctx, toUserID, fromUserID, callID, voipPayload, expiration, targets.VoIPTargets)
		} else {
			s.logger.Warn("APNS VoIP client not configured, skipping VoIP push for iOS terminated state",
				zap.Int64("user_id", toUserID),
				zap.Int("voip_token_count", len(targets.VoIPTargets)),
			)
		}
	}

	if s.fcmClient == nil {
		if len(targets.VoIPTargets) > 0 {
			s.logger.Info("FCM client not initialized; skipping FCM/APNS alert fallback and relying on VoIP push only",
				zap.Int64("receiver_id", toUserID),
				zap.String("call_id", callID.String()),
			)
			return nil
		}
		return nil
	}

	sendCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	dispatch := []struct {
		platform string
		tokens   []string
		msg      *messaging.MulticastMessage
	}{
		{
			platform: "android",
			tokens:   targets.AndroidTokens,
			msg: &messaging.MulticastMessage{
				Tokens: targets.AndroidTokens,
				Data:   data,
				Android: &messaging.AndroidConfig{
					Priority: priority,
					TTL:      ptrDuration(fcmCallTTL),
				},
			},
		},
		{
			platform: "ios",
			tokens:   targets.IOSTokens,
			msg: &messaging.MulticastMessage{
				Tokens: targets.IOSTokens,
				Data:   data,
				APNS: buildIncomingCallAPNSConfig(
					expiration,
					payload.Title,
					payload.Body,
					"INCOMING_CALL",
					customData,
				),
			},
		},
		{
			platform: "fallback",
			tokens:   targets.FallbackTokens,
			msg: &messaging.MulticastMessage{
				Tokens: targets.FallbackTokens,
				Data:   data,
				Android: &messaging.AndroidConfig{
					Priority: priority,
					TTL:      ptrDuration(fcmCallTTL),
				},
				APNS: buildIncomingCallAPNSConfig(
					expiration,
					payload.Title,
					payload.Body,
					"INCOMING_CALL",
					customData,
				),
			},
		},
	}

	if len(targets.VoIPTargets) > 0 {
		s.logger.Info("Incoming call target has stored VoIP tokens; keep foreground delivery additive across websocket/FCM/APNS paths",
			zap.Int64("user_id", toUserID),
			zap.Int("voip_token_count", len(targets.VoIPTargets)),
		)
	}
	for _, item := range dispatch {
		if len(item.tokens) == 0 {
			continue
		}
		s.logger.Info("Sending incoming call push batch",
			zap.Int64("receiver_id", toUserID),
			zap.String("call_id", callID.String()),
			zap.String("room_name", roomID),
			zap.String("platform", item.platform),
			zap.Int("token_count", len(item.tokens)),
		)
		br, err := sendCallMulticast(sendCtx, s.fcmClient, item.msg)
		if err != nil {
			s.logger.Error("Incoming call push batch failed",
				zap.Int64("receiver_id", toUserID),
				zap.String("call_id", callID.String()),
				zap.String("room_name", roomID),
				zap.String("platform", item.platform),
				zap.String("firebase_error_code", classifyPushError(err)),
				zap.Error(err),
			)
			for _, tok := range item.tokens {
				meta := targetLogIndex[tok]
				s.logger.Warn("Incoming call push device result",
					zap.String("push_type", "incoming_call"),
					zap.String("call_id", callID.String()),
					zap.Int64("caller_id", fromUserID),
					zap.Int64("callee_id", toUserID),
					zap.String("device_id", meta.DeviceID),
					zap.String("platform", meta.Platform),
					zap.String("token_prefix", maskedToken(tok)),
					zap.Bool("success", false),
					zap.String("firebase_message_id", ""),
					zap.String("error_code", classifyPushError(err)),
					zap.String("error_message", err.Error()),
					zap.Bool("token_deleted", false),
				)
			}
			return err
		}
		if br == nil {
			s.logger.Warn("Incoming call push batch returned nil response",
				zap.Int64("receiver_id", toUserID),
				zap.String("call_id", callID.String()),
				zap.String("room_name", roomID),
				zap.String("platform", item.platform),
			)
			continue
		}
		s.logger.Info("Incoming call push batch completed",
			zap.Int64("receiver_id", toUserID),
			zap.String("call_id", callID.String()),
			zap.String("room_name", roomID),
			zap.String("platform", item.platform),
			zap.Int("success_count", br.SuccessCount),
			zap.Int("failure_count", br.FailureCount),
		)
		if br.FailureCount == 0 {
			continue
		}

		deleted := make(map[string]struct{}, br.FailureCount)
		for i, resp := range br.Responses {
			tok := item.tokens[i]
			meta := targetLogIndex[tok]
			if resp.Success {
				s.logger.Info("Incoming call push device result",
					zap.String("push_type", "incoming_call"),
					zap.String("call_id", callID.String()),
					zap.Int64("caller_id", fromUserID),
					zap.Int64("callee_id", toUserID),
					zap.String("device_id", meta.DeviceID),
					zap.String("platform", meta.Platform),
					zap.String("token_prefix", maskedToken(tok)),
					zap.Bool("success", true),
					zap.String("firebase_message_id", resp.MessageID),
					zap.String("error_code", ""),
					zap.String("error_message", ""),
					zap.Bool("token_deleted", false),
				)
				continue
			}
			tokenDeleted := false
			if shouldDeleteToken(resp.Error) {
				if _, ok := deleted[tok]; ok {
					tokenDeleted = true
				} else {
					deleted[tok] = struct{}{}
					if err := s.deviceRepo.DeactivateTokenByValue(ctx, tok); err != nil {
						s.logger.Warn("Failed to deactivate bad token in CallPush", zap.Error(err))
					} else {
						tokenDeleted = true
						s.logger.Info("Deactivated bad FCM token in CallPush", zap.Int64("user_id", toUserID))
					}
				}
			}
			s.logger.Warn("Incoming call push device result",
				zap.String("push_type", "incoming_call"),
				zap.String("call_id", callID.String()),
				zap.Int64("caller_id", fromUserID),
				zap.Int64("callee_id", toUserID),
				zap.String("device_id", meta.DeviceID),
				zap.String("platform", meta.Platform),
				zap.String("token_prefix", maskedToken(tok)),
				zap.Bool("success", false),
				zap.String("firebase_message_id", ""),
				zap.String("error_code", classifyPushError(resp.Error)),
				zap.String("error_message", resp.Error.Error()),
				zap.Bool("token_deleted", tokenDeleted),
			)
		}
	}

	return nil
}

func (s *NotificationService) SendIncomingCallWS(
	toUserID int64,
	fromUserID int64,
	fromName string,
	callID uuid.UUID,
	roomID string,
	chatID string,
	isVideo bool,
) {
	s.SendIncomingCallWSWithType(toUserID, fromUserID, fromName, callID, roomID, chatID, isVideo, domain.CallTypePrivate)
}

func (s *NotificationService) SendIncomingCallWSWithType(
	toUserID int64,
	fromUserID int64,
	fromName string,
	callID uuid.UUID,
	roomID string,
	chatID string,
	isVideo bool,
	callType string,
) {
	if s.wsHub == nil {
		return
	}

	roomID = strings.TrimSpace(roomID)
	chatID = strings.TrimSpace(chatID)
	fromName = normalizeCallerDisplayName(fromName, i18n.SystemNotification(s.recipientLocale(context.Background(), toUserID), "Caller"))
	if roomID == "" {
		return
	}

	sentAt := time.Now().UTC()
	payload := buildIncomingCallPayload(fromUserID, fromName, callID, roomID, chatID, s.resolveLiveKitURLForUser(context.Background(), toUserID), "", "high", isVideo, sentAt, callType)
	s.localizeCallPayload(context.Background(), toUserID, &payload)
	wsMsg := &websocket.Message{
		Type:      "incoming_call",
		To:        toUserID,
		Content:   payload.toMap(),
		Timestamp: sentAt.Unix(),
	}
	s.wsHub.SendToUser(toUserID, wsMsg)
}

func (s *NotificationService) SendGroupCallInviteWS(
	toUserID int64,
	fromUserID int64,
	fromName string,
	groupID string,
	groupName string,
	roomName string,
	callID uuid.UUID,
) {
	if s.wsHub == nil {
		return
	}

	roomName = strings.TrimSpace(roomName)
	groupID = strings.TrimSpace(groupID)
	fromName = normalizeCallerDisplayName(fromName, i18n.SystemNotification(s.recipientLocale(context.Background(), toUserID), "Group participant"))
	if roomName == "" {
		return
	}

	sentAt := time.Now().UTC()
	payload := buildIncomingCallPayload(fromUserID, fromName, callID, roomName, groupID, s.resolveLiveKitURLForUser(context.Background(), toUserID), "", "high", true, sentAt, domain.CallTypeGroup)
	s.localizeCallPayload(context.Background(), toUserID, &payload)
	wsMsg := &websocket.Message{
		Type:      "incoming_call",
		To:        toUserID,
		Content:   payload.toMap(),
		Timestamp: sentAt.Unix(),
	}
	s.wsHub.SendToUser(toUserID, wsMsg)
}

func (s *NotificationService) SendCallAcceptedWS(
	toUserID int64,
	callID uuid.UUID,
	roomName string,
	acceptedByUserID int64,
	acceptedByName string,
) {
	if s.wsHub == nil {
		return
	}

	roomName = strings.TrimSpace(roomName)
	acceptedByName = normalizeCallerDisplayName(acceptedByName, i18n.SystemNotification(s.recipientLocale(context.Background(), toUserID), "Caller"))
	if roomName == "" {
		return
	}

	wsMsg := &websocket.Message{
		Type: "call_accepted",
		To:   toUserID,
		Content: map[string]interface{}{
			"call_id":             callID.String(),
			"room_name":           roomName,
			"accepted_by_user_id": acceptedByUserID,
			"accepted_by_name":    acceptedByName,
		},
		Timestamp: time.Now().Unix(),
	}
	s.wsHub.SendToUser(toUserID, wsMsg)
}

func sanitizeTokens(tokens []string) []string {
	seen := make(map[string]struct{}, len(tokens))
	sanitized := make([]string, 0, len(tokens))
	for _, t := range tokens {
		t = strings.TrimSpace(t)
		// Removed length check < 50 just in case. Also removed 'string' check? No, kept string check.
		// Removed onBad call to prevent deletion from validation logic
		if t == "" || t == "string" || strings.ContainsAny(t, " \n\t") {
			// if onBad != nil { onBad(t) } // DISABLED AUTO DELETE
			continue
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		sanitized = append(sanitized, t)
	}
	return sanitized
}

func (s *NotificationService) GetUserCallHistory(ctx context.Context, userID int64, limit, offset int, kind string) ([]domain.CallHistory, error) {
	if s.callRepo == nil {
		return []domain.CallHistory{}, nil
	}
	return s.callRepo.ListForUser(ctx, userID, limit, offset, kind)
}

func (s *NotificationService) GetUserActiveCalls(ctx context.Context, userID int64, limit int) ([]domain.CallHistory, error) {
	if s.callRepo == nil {
		return []domain.CallHistory{}, nil
	}
	return s.callRepo.ListActiveForUser(ctx, userID, limit)
}

func ptrDuration(d time.Duration) *time.Duration { return &d }

// SendCallEndedWS sends a targeted WebSocket `call_ended` event to a specific user
// for a SPECIFIC callID. This prevents the client from accidentally closing a NEW call
// when the event was intended for an OLD completed call.
func (s *NotificationService) SendCallEndedWS(toUserID int64, callID uuid.UUID, roomName, reason string) {
	if s.wsHub == nil {
		return
	}

	wsMsg := &websocket.Message{
		Type: "call_ended",
		To:   toUserID,
		Content: map[string]interface{}{
			"call_id":   callID.String(),
			"room_name": roomName,
			"reason":    reason,
		},
		Timestamp: time.Now().Unix(),
	}
	s.wsHub.SendToUser(toUserID, wsMsg)
}

func (s *NotificationService) SendCallSyncEvent(
	ctx context.Context,
	toUserID int64,
	callID uuid.UUID,
	roomName string,
	event string,
	reason string,
	isVideo bool,
	excludeDeviceID string,
	excludeSessionKey string,
	// P1.2: acceptedByDeviceID передаётся для call_answered_elsewhere
	// чтобы Flutter мог сравнить с currentDeviceId и не закрыть звонок на принявшем устройстве.
	// Для остальных событий передавайте пустую строку.
	acceptedByDeviceID ...string,
) error {
	acceptedDevice := ""
	if len(acceptedByDeviceID) > 0 {
		acceptedDevice = strings.TrimSpace(acceptedByDeviceID[0])
	}

	sentAt := time.Now().UTC()
	var callInfo *domain.CallHistory
	if s.callRepo != nil {
		resolvedCall, err := s.callRepo.GetByCallID(ctx, callID)
		if err != nil {
			s.logger.Warn("failed to load call info for sync payload",
				zap.String("call_id", callID.String()),
				zap.Error(err),
			)
		} else {
			callInfo = resolvedCall
		}
	}
	payload := buildCallSyncPayload(event, callID, roomName, reason, toUserID, isVideo, sentAt, callInfo, acceptedDevice)
	data := payload.toStringMap()
	content := payload.toMap()

	if s.wsHub != nil {
		wsMsg := &websocket.Message{
			Type:              event,
			To:                toUserID,
			Content:           content,
			Timestamp:         sentAt.Unix(),
			ExcludeSessionKey: strings.TrimSpace(excludeSessionKey),
		}
		s.wsHub.SendToUser(toUserID, wsMsg)
	}

	if s.fcmClient == nil {
		return nil
	}

	devices, err := s.deviceRepo.GetUserDevices(ctx, toUserID)
	if err != nil {
		return err
	}

	var androidTokens []string
	var iosTokens []string
	var fallbackTokens []string
	for _, device := range devices {
		if excludeDeviceID != "" && strings.TrimSpace(device.DeviceID) == strings.TrimSpace(excludeDeviceID) {
			continue
		}
		token := strings.TrimSpace(device.FCMToken)
		if token == "" {
			continue
		}
		switch tokenPlatform(device) {
		case "android":
			androidTokens = append(androidTokens, token)
		case "ios":
			iosTokens = append(iosTokens, token)
		default:
			fallbackTokens = append(fallbackTokens, token)
		}
	}

	androidTokens = sanitizeTokens(androidTokens)
	iosTokens = sanitizeTokens(iosTokens)
	fallbackTokens = sanitizeTokens(fallbackTokens)
	if len(androidTokens) == 0 && len(iosTokens) == 0 && len(fallbackTokens) == 0 {
		return nil
	}

	expiration := sentAt.Add(30 * time.Second)
	apnsCfg := buildBackgroundAPNSConfigWithData(expiration, content)

	sendCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	dispatch := []struct {
		platform string
		tokens   []string
		msg      *messaging.MulticastMessage
	}{
		{
			platform: "android",
			tokens:   androidTokens,
			msg: &messaging.MulticastMessage{
				Tokens: androidTokens,
				Data:   data,
				Android: &messaging.AndroidConfig{
					Priority: "high",
					TTL:      ptrDuration(30 * time.Second),
				},
			},
		},
		{
			platform: "ios",
			tokens:   iosTokens,
			msg: &messaging.MulticastMessage{
				Tokens: iosTokens,
				Data:   data,
				APNS:   apnsCfg,
			},
		},
		{
			platform: "fallback",
			tokens:   fallbackTokens,
			msg: &messaging.MulticastMessage{
				Tokens: fallbackTokens,
				Data:   data,
				Android: &messaging.AndroidConfig{
					Priority: "high",
					TTL:      ptrDuration(30 * time.Second),
				},
				APNS: apnsCfg,
			},
		},
	}

	for _, item := range dispatch {
		if len(item.tokens) == 0 {
			continue
		}
		br, err := sendCallMulticast(sendCtx, s.fcmClient, item.msg)
		if err != nil {
			s.logger.Error("Call sync push batch failed",
				zap.Int64("receiver_id", toUserID),
				zap.String("call_id", callID.String()),
				zap.String("room_name", roomName),
				zap.String("event", event),
				zap.String("platform", item.platform),
				zap.String("firebase_error_code", classifyPushError(err)),
				zap.Error(err),
			)
			return err
		}
		if br == nil || br.FailureCount == 0 {
			continue
		}
		for i, resp := range br.Responses {
			if resp.Success {
				continue
			}
			tok := item.tokens[i]
			s.logger.Warn("Call sync push delivery failed",
				zap.Int64("receiver_id", toUserID),
				zap.String("call_id", callID.String()),
				zap.String("room_name", roomName),
				zap.String("event", event),
				zap.String("platform", item.platform),
				zap.String("masked_token", maskedToken(tok)),
				zap.String("firebase_error_code", classifyPushError(resp.Error)),
				zap.Error(resp.Error),
			)
			if shouldDeleteToken(resp.Error) {
				if err := s.deviceRepo.DeactivateTokenByValue(ctx, tok); err != nil {
					s.logger.Warn("Failed to deactivate bad token in call sync push", zap.Error(err))
				}
			}
		}
	}

	return nil
}

func (s *NotificationService) SendIncomingCallCancelPush(ctx context.Context, toUserID int64, callID uuid.UUID, roomName, reason string) error {
	return s.SendIncomingCallCancelPushEx(ctx, toUserID, callID, roomName, reason, true, "", "")
}

func (s *NotificationService) SendIncomingCallCancelPushEx(
	ctx context.Context,
	toUserID int64,
	callID uuid.UUID,
	roomName string,
	reason string,
	isVideo bool,
	excludeDeviceID string,
	excludeSessionKey string,
) error {
	eventType := "incoming_call_cancel"

	switch reason {
	case "declined":
		eventType = "call_declined"
	case "busy":
		eventType = "call_busy"
	case "canceled":
		eventType = "call_cancelled"
	}

	// If roomName is missing, try to fetch it from DB
	if roomName == "" && s.callRepo != nil {
		if callInfo, err := s.callRepo.GetByCallID(ctx, callID); err == nil && callInfo != nil {
			roomName = callInfo.RoomID
		}
	}

	return s.SendCallSyncEvent(ctx, toUserID, callID, roomName, eventType, reason, isVideo, excludeDeviceID, excludeSessionKey)
}

func (s *NotificationService) SendGroupCallPush(
	ctx context.Context,
	fromUserID int64,
	fromName string,
	groupID string,
	groupName string,
	callID uuid.UUID,
	roomName string,
	participants []int64,
) error {
	fromName = normalizeCallerDisplayName(fromName, "Group participant")
	groupID = strings.TrimSpace(groupID)
	groupName = strings.TrimSpace(groupName)
	roomName = strings.TrimSpace(roomName)
	if roomName == "" {
		return apperrors.BadRequest("room_name is required")
	}

	sentAt := time.Now().UTC()

	for _, toUserID := range participants {
		if toUserID == fromUserID {
			continue
		}

		s.SendGroupCallInviteWS(toUserID, fromUserID, fromName, groupID, groupName, roomName, callID)

		devices, err := s.deviceRepo.GetUserDevices(ctx, toUserID)
		if err != nil {
			s.logger.Warn("Failed to get devices for group call", zap.Error(err), zap.Int64("user_id", toUserID))
			continue
		}
		targets := s.resolvePushTargets(toUserID, devices)
		participantName := s.resolvePreconnectParticipantName(ctx, toUserID)
		preconnectToken := s.issuePreconnectLiveKitToken(roomName, toUserID, participantName, "")
		livekitURL := s.resolveLiveKitURLForUser(ctx, toUserID)

		notificationLocale := s.recipientLocale(ctx, toUserID)
		data := map[string]string{
			"type":              "incoming_call",
			"event":             "call.incoming",
			"action":            "incoming_call",
			"notification_type": "incoming_call",
			"call_event":        "incoming_call",
			"payload_type":      "incoming_call",
			"event_id":          fmt.Sprintf("%s:%s", callID.String(), "incoming_call"),
			"notification_id":   callID.String(),
			"call_id":           callID.String(),
			"room_name":         roomName,
			"room_id":           roomName,
			"chat_id":           groupID,
			"group_id":          groupID,
			"group_name":        groupName,
			"from_user_id":      fmt.Sprintf("%d", fromUserID),
			"from_name":         fromName,
			"from_full_name":    fromName,
			"caller_id":         fmt.Sprintf("%d", fromUserID),
			"caller_name":       fromName,
			"caller_avatar":     "",
			"livekit_url":       livekitURL,
			"livekit_token":     preconnectToken,
			"is_video":          "true",
			"sent_at":           sentAt.Format(time.RFC3339),
			"sent_at_unix":      fmt.Sprintf("%d", sentAt.Unix()),
			"created_at":        sentAt.Format(time.RFC3339),
			"title":             i18n.SystemNotification(notificationLocale, "Групповой звонок"),
			"body":              i18n.SystemNotification(notificationLocale, "%s звонит в группу %s", fromName, groupName),
		}

		if len(targets.VoIPTargets) > 0 {
			voipPayload := apnsVoIPPayload{
				Aps:          map[string]interface{}{"content-available": 1},
				Event:        "incoming_call",
				CallID:       callID.String(),
				CallerName:   fromName,
				FromFullName: fromName,
				FromUserID:   strconv.FormatInt(fromUserID, 10),
				CallerID:     strconv.FormatInt(fromUserID, 10),
				RoomName:     roomName,
				RoomID:       roomName,
				ChatID:       groupID,
				IsVideo:      true,
				LivekitURL:   livekitURL,
				LivekitToken: preconnectToken,
			}
			if s.apnsVoip != nil {
				s.sendVoIPIncomingCallBatch(ctx, toUserID, fromUserID, callID, voipPayload, sentAt.Add(fcmCallTTL), targets.VoIPTargets)
			} else {
				s.logger.Warn("APNS VoIP client not configured, skipping group VoIP push for iOS terminated state",
					zap.Int64("user_id", toUserID),
					zap.Int("voip_token_count", len(targets.VoIPTargets)),
				)
			}
		}

		if s.fcmClient == nil {
			continue
		}
		fcmTokens := append(append([]string{}, targets.AndroidTokens...), targets.IOSTokens...)
		fcmTokens = append(fcmTokens, targets.FallbackTokens...)
		if len(fcmTokens) == 0 {
			continue
		}

		msg := &messaging.MulticastMessage{
			Tokens: fcmTokens,
			Data:   data,
			Android: &messaging.AndroidConfig{
				Priority: "high",
				TTL:      ptrDuration(fcmCallTTL),
			},
			APNS: buildIncomingCallAPNSConfig(
				sentAt.Add(fcmCallTTL),
				data["title"],
				data["body"],
				"INCOMING_CALL",
				map[string]interface{}{
					"call_id":           callID.String(),
					"room_name":         roomName,
					"room_id":           roomName,
					"group_id":          groupID,
					"group_name":        groupName,
					"caller_id":         fmt.Sprintf("%d", fromUserID),
					"caller_name":       fromName,
					"caller_avatar":     "",
					"livekit_url":       livekitURL,
					"livekit_token":     preconnectToken,
					"type":              "incoming_call",
					"event":             "call.incoming",
					"action":            "incoming_call",
					"notification_type": "incoming_call",
					"call_event":        "incoming_call",
					"event_id":          fmt.Sprintf("%s:%s", callID.String(), "incoming_call"),
					"notification_id":   callID.String(),
					"is_video":          true,
					"created_at":        sentAt.Format(time.RFC3339),
				},
			),
		}

		go func(m *messaging.MulticastMessage, uid int64, toks []string) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			s.logger.Info("Sending group call push batch",
				zap.Int64("receiver_id", uid),
				zap.String("call_id", callID.String()),
				zap.String("room_name", roomName),
				zap.Int("token_count", len(toks)),
			)
			br, err := s.fcmClient.SendEachForMulticast(ctx, m)
			if err != nil {
				s.logger.Error("Failed to send group push",
					zap.Error(err),
					zap.Int64("receiver_id", uid),
					zap.String("call_id", callID.String()),
					zap.String("room_name", roomName),
					zap.String("firebase_error_code", classifyPushError(err)),
				)
				return
			}
			s.logger.Info("Group call push batch completed",
				zap.Int64("receiver_id", uid),
				zap.String("call_id", callID.String()),
				zap.String("room_name", roomName),
				zap.Int("success_count", br.SuccessCount),
				zap.Int("failure_count", br.FailureCount),
			)
			if br.FailureCount > 0 {
				for i, resp := range br.Responses {
					if !resp.Success && shouldDeleteToken(resp.Error) {
						tok := toks[i]
						s.logger.Warn("Group call push delivery failed",
							zap.Int64("receiver_id", uid),
							zap.String("call_id", callID.String()),
							zap.String("room_name", roomName),
							zap.String("masked_token", maskedToken(tok)),
							zap.String("firebase_error_code", classifyPushError(resp.Error)),
							zap.Error(resp.Error),
						)
						if err := s.deviceRepo.DeactivateTokenByValue(ctx, tok); err != nil {
							s.logger.Warn("Failed to deactivate bad token in group call", zap.Error(err))
						} else {
							s.logger.Info("Deactivated bad FCM token in group call", zap.Int64("user_id", uid))
						}
					} else if !resp.Success {
						s.logger.Warn("Group call push delivery failed",
							zap.Int64("receiver_id", uid),
							zap.String("call_id", callID.String()),
							zap.String("room_name", roomName),
							zap.String("masked_token", maskedToken(toks[i])),
							zap.String("firebase_error_code", classifyPushError(resp.Error)),
							zap.Error(resp.Error),
						)
					}
				}
			}
		}(msg, toUserID, fcmTokens)
	}

	return nil
}

func normalizeCallerDisplayName(name string, fallback string) string {
	name = strings.TrimSpace(name)
	if name != "" {
		return name
	}
	fallback = strings.TrimSpace(fallback)
	if fallback != "" {
		return fallback
	}
	return "Caller"
}
