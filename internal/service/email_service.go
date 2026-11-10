package service

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/config"
	"go.uber.org/zap"
)

var (
	ErrSMTPNotConfigured     = errors.New("smtp not configured")
	ErrMailDisabled          = errors.New("mail disabled")
	ErrInvalidEmailRecipient = errors.New("invalid email recipient")
)

type smtpTransport interface {
	Send(ctx context.Context, cfg config.SMTPConfig, from string, recipients []string, message []byte) error
}

type defaultSMTPTransport struct{}

type EmailService struct {
	cfg       config.SMTPConfig
	from      string
	fromEmail string
	logger    *zap.Logger
	transport smtpTransport
}

func NewEmailService(cfg config.SMTPConfig, logger *zap.Logger) *EmailService {
	serviceLogger := logger.With(zap.String("service", "email"))
	fromEmail := strings.TrimSpace(cfg.FromEmail)
	if fromEmail == "" {
		fromEmail = strings.TrimSpace(cfg.From)
	}

	fromName := strings.TrimSpace(cfg.FromName)
	if fromName == "" {
		fromName = "Alem Workspace"
	}

	formattedFrom := fromEmail
	if parsed := strings.TrimSpace(fromEmail); parsed != "" {
		formattedFrom = (&mail.Address{Name: fromName, Address: parsed}).String()
	}

	if cfg.Enabled && strings.TrimSpace(cfg.Host) == "" && len(cfg.Hosts) == 0 {
		serviceLogger.Warn("SMTP mail enabled but host is not configured")
	}

	return &EmailService{
		cfg:       cfg,
		from:      formattedFrom,
		fromEmail: fromEmail,
		logger:    serviceLogger,
		transport: defaultSMTPTransport{},
	}
}

func (s *EmailService) Enabled() bool {
	return s != nil && s.cfg.Enabled && strings.EqualFold(strings.TrimSpace(s.cfg.Provider), "smtp")
}

func (s *EmailService) SendEmail(
	ctx context.Context,
	to string,
	subject string,
	htmlBody string,
	textBody string,
) error {
	return s.SendEmailToMany(ctx, []string{to}, subject, htmlBody, textBody)
}

func (s *EmailService) SendCalendarInvite(
	ctx context.Context,
	to string,
	subject string,
	htmlBody string,
	textBody string,
	icsData string,
) error {
	if s == nil {
		return ErrSMTPNotConfigured
	}
	if !s.Enabled() {
		s.logger.Debug("Skip email send: mail is disabled")
		return ErrMailDisabled
	}
	if strings.TrimSpace(s.cfg.Host) == "" {
		return ErrSMTPNotConfigured
	}

	to = strings.TrimSpace(to)
	if _, err := mail.ParseAddress(to); err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidEmailRecipient, to)
	}

	if strings.TrimSpace(textBody) == "" {
		textBody = stripHTMLTags(htmlBody)
	}

	message, err := s.buildCalendarMessage([]string{to}, subject, htmlBody, textBody, icsData)
	if err != nil {
		return err
	}

	if deadline, ok := ctx.Deadline(); ok {
		s.logger.Info("Sending calendar email",
			zap.String("recipient", to),
			zap.String("subject", subject),
			zap.Duration("time_remaining", time.Until(deadline)),
		)
	} else {
		s.logger.Info("Sending calendar email",
			zap.String("recipient", to),
			zap.String("subject", subject),
		)
	}

	if err := s.transport.Send(ctx, s.cfg, s.fromEmail, []string{to}, message); err != nil {
		return err
	}

	s.logger.Info("Calendar email sent",
		zap.String("recipient", to),
		zap.String("subject", subject),
	)
	return nil
}

func (s *EmailService) SendEmailToMany(
	ctx context.Context,
	recipients []string,
	subject string,
	htmlBody string,
	textBody string,
) error {
	if s == nil {
		return ErrSMTPNotConfigured
	}
	if !s.Enabled() {
		s.logger.Debug("Skip email send: mail is disabled")
		return ErrMailDisabled
	}
	if strings.TrimSpace(s.cfg.Host) == "" {
		return ErrSMTPNotConfigured
	}

	normalizedRecipients := make([]string, 0, len(recipients))
	for _, recipient := range recipients {
		recipient = strings.TrimSpace(recipient)
		if recipient == "" {
			continue
		}
		if _, err := mail.ParseAddress(recipient); err != nil {
			return fmt.Errorf("%w: %s", ErrInvalidEmailRecipient, recipient)
		}
		normalizedRecipients = append(normalizedRecipients, recipient)
	}
	if len(normalizedRecipients) == 0 {
		return ErrInvalidEmailRecipient
	}
	if strings.TrimSpace(textBody) == "" {
		textBody = stripHTMLTags(htmlBody)
	}

	message, err := s.buildMessage(normalizedRecipients, subject, htmlBody, textBody)
	if err != nil {
		return err
	}

	if deadline, ok := ctx.Deadline(); ok {
		s.logger.Info("Sending email",
			zap.Strings("recipients", normalizedRecipients),
			zap.String("subject", subject),
			zap.Duration("time_remaining", time.Until(deadline)),
		)
	} else {
		s.logger.Info("Sending email",
			zap.Strings("recipients", normalizedRecipients),
			zap.String("subject", subject),
		)
	}

	if err := s.transport.Send(ctx, s.cfg, s.fromEmail, normalizedRecipients, message); err != nil {
		return err
	}

	s.logger.Info("Email sent",
		zap.Strings("recipients", normalizedRecipients),
		zap.String("subject", subject),
	)
	return nil
}

func (s *EmailService) buildMessage(recipients []string, subject, htmlBody, textBody string) ([]byte, error) {
	if strings.TrimSpace(s.fromEmail) == "" {
		return nil, ErrSMTPNotConfigured
	}

	boundary := "alem-workspace-" + uuid.NewString()
	encodedSubject := mime.QEncoding.Encode("utf-8", strings.TrimSpace(subject))
	var buf bytes.Buffer

	headers := map[string]string{
		"From":         s.from,
		"To":           strings.Join(recipients, ", "),
		"Subject":      encodedSubject,
		"MIME-Version": "1.0",
		"Date":         time.Now().UTC().Format(time.RFC1123Z),
		"Message-ID":   fmt.Sprintf("<%s@%s>", uuid.NewString(), messageIDDomain(s.fromEmail)),
		"Content-Type": fmt.Sprintf("multipart/alternative; boundary=%q", boundary),
	}
	for key, value := range headers {
		buf.WriteString(key)
		buf.WriteString(": ")
		buf.WriteString(value)
		buf.WriteString("\r\n")
	}
	buf.WriteString("\r\n")
	buf.WriteString("--" + boundary + "\r\n")
	buf.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	buf.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
	buf.WriteString(textBody)
	buf.WriteString("\r\n")
	buf.WriteString("--" + boundary + "\r\n")
	buf.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	buf.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
	buf.WriteString(htmlBody)
	buf.WriteString("\r\n")
	buf.WriteString("--" + boundary + "--\r\n")

	return buf.Bytes(), nil
}

func (s *EmailService) buildCalendarMessage(recipients []string, subject, htmlBody, textBody, icsData string) ([]byte, error) {
	if strings.TrimSpace(s.fromEmail) == "" {
		return nil, ErrSMTPNotConfigured
	}

	mixedBoundary := "alem-mixed-" + uuid.NewString()
	altBoundary := "alem-alt-" + uuid.NewString()
	encodedSubject := mime.QEncoding.Encode("utf-8", strings.TrimSpace(subject))
	var buf bytes.Buffer

	headers := map[string]string{
		"From":         s.from,
		"To":           strings.Join(recipients, ", "),
		"Subject":      encodedSubject,
		"MIME-Version": "1.0",
		"Date":         time.Now().UTC().Format(time.RFC1123Z),
		"Message-ID":   fmt.Sprintf("<%s@%s>", uuid.NewString(), messageIDDomain(s.fromEmail)),
		"Content-Type": fmt.Sprintf("multipart/mixed; boundary=%q", mixedBoundary),
	}
	for key, value := range headers {
		buf.WriteString(key)
		buf.WriteString(": ")
		buf.WriteString(value)
		buf.WriteString("\r\n")
	}
	buf.WriteString("\r\n")

	// Multipart mixed start
	buf.WriteString("--" + mixedBoundary + "\r\n")
	buf.WriteString(fmt.Sprintf("Content-Type: multipart/alternative; boundary=%q\r\n\r\n", altBoundary))

	// Plain text
	buf.WriteString("--" + altBoundary + "\r\n")
	buf.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	buf.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
	buf.WriteString(textBody)
	buf.WriteString("\r\n")

	// HTML
	buf.WriteString("--" + altBoundary + "\r\n")
	buf.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	buf.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
	buf.WriteString(htmlBody)
	buf.WriteString("\r\n")

	// Inline calendar for some clients (Apple Mail etc.)
	buf.WriteString("--" + altBoundary + "\r\n")
	buf.WriteString("Content-Type: text/calendar; charset=UTF-8; method=REQUEST\r\n")
	buf.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
	buf.WriteString(icsData)
	buf.WriteString("\r\n")

	// End alternative
	buf.WriteString("--" + altBoundary + "--\r\n\r\n")

	// ICS Attachment
	buf.WriteString("--" + mixedBoundary + "\r\n")
	buf.WriteString("Content-Type: text/calendar; charset=UTF-8; name=\"invite.ics\"\r\n")
	buf.WriteString("Content-Disposition: attachment; filename=\"invite.ics\"\r\n")
	buf.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
	buf.WriteString(icsData)
	buf.WriteString("\r\n")

	// End mixed
	buf.WriteString("--" + mixedBoundary + "--\r\n")

	return buf.Bytes(), nil
}

func (defaultSMTPTransport) Send(
	ctx context.Context,
	cfg config.SMTPConfig,
	from string,
	recipients []string,
	message []byte,
) error {
	address := net.JoinHostPort(strings.TrimSpace(cfg.Host), fmt.Sprintf("%d", cfg.Port))
	dialer := &net.Dialer{Timeout: 10 * time.Second}

	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}
	defer conn.Close()
	// Bound greeting, TLS, AUTH and DATA as well as the initial TCP dial.
	deadline := time.Now().Add(30 * time.Second)
	if requested, ok := ctx.Deadline(); ok && requested.Before(deadline) {
		deadline = requested
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return fmt.Errorf("smtp deadline: %w", err)
	}
	rawConn := conn
	stopCancel := context.AfterFunc(ctx, func() { _ = rawConn.Close() })
	defer stopCancel()
	serverName := strings.TrimSpace(cfg.TLSServerName)
	if serverName == "" {
		serverName = strings.TrimSpace(cfg.Host)
	}
	tlsConfig := &tls.Config{
		ServerName:         serverName,
		InsecureSkipVerify: cfg.SkipTLSVerify,
		MinVersion:         tls.VersionTLS12,
	}
	if cfg.ImplicitTLS {
		secure := tls.Client(conn, tlsConfig)
		if err := secure.HandshakeContext(ctx); err != nil {
			return fmt.Errorf("smtp tls: %w", err)
		}
		conn = secure
	}

	client, err := smtp.NewClient(conn, serverName)
	if err != nil {
		return fmt.Errorf("smtp client: %w", err)
	}
	defer client.Close()

	if cfg.UseTLS && !cfg.ImplicitTLS {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return fmt.Errorf("smtp starttls not supported by host %s", cfg.Host)
		}
		if err := client.StartTLS(tlsConfig); err != nil {
			return fmt.Errorf("smtp starttls: %w", err)
		}
	}

	if strings.TrimSpace(cfg.Username) != "" {
		auth := smtp.PlainAuth("", cfg.Username, cfg.Password, serverName)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}

	if err := client.Mail(from); err != nil {
		return fmt.Errorf("smtp mail from: %w", err)
	}
	for _, recipient := range recipients {
		if err := client.Rcpt(recipient); err != nil {
			return fmt.Errorf("smtp rcpt to %s: %w", recipient, err)
		}
	}

	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := writer.Write(message); err != nil {
		_ = writer.Close()
		return fmt.Errorf("smtp write message: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("smtp close writer: %w", err)
	}
	if err := client.Quit(); err != nil {
		return fmt.Errorf("smtp quit: %w", err)
	}
	return nil
}

func messageIDDomain(fromEmail string) string {
	at := strings.LastIndex(fromEmail, "@")
	if at == -1 || at == len(fromEmail)-1 {
		return "alem-workspace.local"
	}
	return strings.TrimSpace(fromEmail[at+1:])
}

func stripHTMLTags(input string) string {
	replacer := strings.NewReplacer(
		"<br>", "\n",
		"<br/>", "\n",
		"<br />", "\n",
		"</p>", "\n\n",
		"</div>", "\n",
	)
	input = replacer.Replace(input)
	var out strings.Builder
	inTag := false
	for _, r := range input {
		switch r {
		case '<':
			inTag = true
		case '>':
			inTag = false
		default:
			if !inTag {
				out.WriteRune(r)
			}
		}
	}
	return strings.TrimSpace(out.String())
}
