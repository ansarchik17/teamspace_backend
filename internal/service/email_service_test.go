package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

type fakeSMTPTransport struct {
	sendCalls int
	lastFrom  string
	lastTo    []string
	lastMsg   string
	sendErr   error
}

func (f *fakeSMTPTransport) Send(_ context.Context, _ config.SMTPConfig, from string, recipients []string, message []byte) error {
	f.sendCalls++
	f.lastFrom = from
	f.lastTo = append([]string{}, recipients...)
	f.lastMsg = string(message)
	return f.sendErr
}

func TestResolveEmailRecipientUsesSpecialDomain(t *testing.T) {
	resolver := NewEmailRecipientResolver(config.SMTPConfig{
		CDNKindyDomain: "cdn-kindy.kz",
		CDNKindyUsers:  []string{"sayan.ashirbekov"},
	})

	recipient, err := resolver.ResolveEmailRecipient(&domain.User{
		Username: "sayan.ashirbekov",
		Email:    "fallback@example.com",
	})
	if err != nil {
		t.Fatalf("ResolveEmailRecipient error = %v", err)
	}
	if recipient != "sayan.ashirbekov@cdn-kindy.kz" {
		t.Fatalf("recipient = %q", recipient)
	}
}

func TestResolveEmailRecipientUsesUserEmailForRegularUser(t *testing.T) {
	resolver := NewEmailRecipientResolver(config.SMTPConfig{
		CDNKindyDomain: "cdn-kindy.kz",
		CDNKindyUsers:  []string{"special.user"},
	})

	recipient, err := resolver.ResolveEmailRecipient(&domain.User{
		Username: "regular.user",
		Email:    "regular@example.com",
	})
	if err != nil {
		t.Fatalf("ResolveEmailRecipient error = %v", err)
	}
	if recipient != "regular@example.com" {
		t.Fatalf("recipient = %q", recipient)
	}
}

func TestResolveEmailRecipientMatchesCaseInsensitiveUsername(t *testing.T) {
	resolver := NewEmailRecipientResolver(config.SMTPConfig{
		CDNKindyDomain: "cdn-kindy.kz",
		CDNKindyUsers:  []string{"Maden.Abdelmanov"},
	})

	recipient, err := resolver.ResolveEmailRecipient(&domain.User{
		Username: "maden.abdelmanov",
		Email:    "fallback@example.com",
	})
	if err != nil {
		t.Fatalf("ResolveEmailRecipient error = %v", err)
	}
	if recipient != "Maden.Abdelmanov@cdn-kindy.kz" {
		t.Fatalf("recipient = %q", recipient)
	}
}

func TestResolveEmailRecipientRejectsEmptyEmail(t *testing.T) {
	resolver := NewEmailRecipientResolver(config.SMTPConfig{})

	_, err := resolver.ResolveEmailRecipient(&domain.User{Username: "regular.user"})
	if !errors.Is(err, ErrInvalidEmailRecipient) {
		t.Fatalf("expected ErrInvalidEmailRecipient, got %v", err)
	}
}

func TestResolveEmailRecipientRejectsInvalidEmail(t *testing.T) {
	resolver := NewEmailRecipientResolver(config.SMTPConfig{})

	_, err := resolver.ResolveEmailRecipient(&domain.User{
		Username: "regular.user",
		Email:    "not-an-email",
	})
	if !errors.Is(err, ErrInvalidEmailRecipient) {
		t.Fatalf("expected ErrInvalidEmailRecipient, got %v", err)
	}
}

func TestSendEmailSkipsWhenMailDisabled(t *testing.T) {
	service := NewEmailService(config.SMTPConfig{
		Enabled:   false,
		Provider:  "smtp",
		Host:      "mail.cdn-kindy.kz",
		Port:      587,
		FromEmail: "info@cdn-kindy.kz",
		FromName:  "Alem Workspace",
	}, zap.NewNop())
	fake := &fakeSMTPTransport{}
	service.transport = fake

	err := service.SendEmail(context.Background(), "user@example.com", "Subject", "<p>Hi</p>", "Hi")
	if !errors.Is(err, ErrMailDisabled) {
		t.Fatalf("expected ErrMailDisabled, got %v", err)
	}
	if fake.sendCalls != 0 {
		t.Fatalf("sendCalls = %d, want 0", fake.sendCalls)
	}
}

func TestBuildNotificationEmailTemplateIncludesContent(t *testing.T) {
	createdAt := time.Date(2026, 5, 24, 15, 4, 0, 0, time.UTC)
	template := BuildNotificationEmailTemplate(NotificationEmailTemplateInput{
		Title:        "Новое уведомление в Alem Workspace",
		GreetingName: "Иван Иванов",
		Message:      "Для вас создано новое уведомление.",
		ActionURL:    "https://alem.example/notifications/1",
		ActionText:   "Открыть уведомление",
		FooterText:   "Не отвечайте на это письмо.",
		CreatedAt:    createdAt,
		AppName:      "Alem Workspace",
	})

	if !strings.Contains(template.HTMLBody, "Для вас создано новое уведомление.") {
		t.Fatal("expected HTMLBody to contain message")
	}
	if !strings.Contains(template.HTMLBody, "Открыть уведомление") {
		t.Fatal("expected HTMLBody to contain action text")
	}
	if !strings.Contains(template.HTMLBody, "Не отвечайте на это письмо.") {
		t.Fatal("expected HTMLBody to contain footer")
	}
	if !strings.Contains(template.TextBody, "https://alem.example/notifications/1") {
		t.Fatal("expected TextBody to contain action URL")
	}
}

func TestSendEmailBuildsMultipartMessage(t *testing.T) {
	service := NewEmailService(config.SMTPConfig{
		Enabled:   true,
		Provider:  "smtp",
		Host:      "mail.cdn-kindy.kz",
		Port:      587,
		FromEmail: "info@cdn-kindy.kz",
		FromName:  "Alem Workspace",
		UseTLS:    true,
	}, zap.NewNop())
	fake := &fakeSMTPTransport{}
	service.transport = fake

	if err := service.SendEmail(context.Background(), "user@example.com", "Subject", "<p>Hello</p>", "Hello"); err != nil {
		t.Fatalf("SendEmail error = %v", err)
	}
	if fake.sendCalls != 1 {
		t.Fatalf("sendCalls = %d, want 1", fake.sendCalls)
	}
	if fake.lastFrom != "info@cdn-kindy.kz" {
		t.Fatalf("lastFrom = %q", fake.lastFrom)
	}
	if !strings.Contains(fake.lastMsg, "multipart/alternative") {
		t.Fatal("expected multipart/alternative in message")
	}
	if !strings.Contains(fake.lastMsg, "text/plain; charset=UTF-8") {
		t.Fatal("expected text/plain part")
	}
	if !strings.Contains(fake.lastMsg, "text/html; charset=UTF-8") {
		t.Fatal("expected text/html part")
	}
}
