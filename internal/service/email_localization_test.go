package service

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/i18n"
	"go.uber.org/zap"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEmailNotificationTranslations(t *testing.T) {
	for _, tc := range []struct {
		locale                            i18n.Locale
		greeting, button, footer, created string
	}{
		{i18n.Russian, "Уважаемый(ая)", "Открыть уведомление", "Это письмо сформировано автоматически.", "Создано:"},
		{i18n.Kazakh, "Құрметті", "Хабарландыруды ашу", "Бұл хат автоматты түрде жасалды.", "Жасалған уақыты:"},
	} {
		result := BuildNotificationEmailTemplate(NotificationEmailTemplateInput{Language: tc.locale, Title: "User {{.Name}} <b>title</b>", Message: "User message 100% <script>secret</script>", GreetingName: "Алия", ActionURL: "https://example.com/open?a=1&b=2", CreatedAt: time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)})
		for _, want := range []string{tc.greeting, tc.button, tc.footer, tc.created, "Алия"} {
			if !strings.Contains(result.HTMLBody, want) || !strings.Contains(result.TextBody, want) {
				t.Errorf("%s missing %q", tc.locale, want)
			}
		}
		if strings.Contains(result.HTMLBody, "<script>") || !strings.Contains(result.HTMLBody, "&lt;script&gt;") {
			t.Fatal("user text must be escaped in HTML")
		}
		if result.Subject != "User {{.Name}} <b>title</b>" || !strings.Contains(result.TextBody, "User message 100% <script>secret</script>") {
			t.Fatal("user text changed")
		}
		if !strings.Contains(result.HTMLBody, `lang="`+string(tc.locale)+`"`) {
			t.Fatal("incorrect HTML language")
		}
	}
	empty := BuildNotificationEmailTemplate(NotificationEmailTemplateInput{Language: i18n.Kazakh})
	if !strings.Contains(empty.Subject, "жаңа хабарландыру") || !strings.Contains(empty.TextBody, "пайдаланушы") || strings.Contains(empty.TextBody, "Пожалуйста") {
		t.Fatal(empty)
	}
	fallback := BuildNotificationEmailTemplate(NotificationEmailTemplateInput{Language: "unsupported"})
	if fallback.Subject != "Новое уведомление в Team Workspace" {
		t.Fatal(fallback.Subject)
	}
}

func TestEmailCalendarVariantsTranslations(t *testing.T) {
	for _, locale := range []i18n.Locale{i18n.Russian, i18n.Kazakh} {
		for _, kind := range []string{"invite", "update", "cancel"} {
			input := CalendarInviteEmailInput{Language: locale, EventID: uuid.New(), EventTitle: "User <b>meeting</b>", AttendeeName: "Алия", OrganizerName: "Ерлан", StartTime: time.Date(2026, 10, 9, 7, 0, 0, 0, time.UTC), BaseURL: "https://example.com/", IsUpdate: kind == "update", IsCancel: kind == "cancel"}
			result := BuildCalendarInviteEmail(input, "accept&token", "decline", "tentative")
			if !strings.Contains(result.Subject, input.EventTitle) || strings.Contains(result.HTMLBody, "<b>meeting</b>") {
				t.Fatal("event title is not preserved or escaped")
			}
			for _, key := range []string{"date", "organizer", "location", "description", "no_description", "footer"} {
				want := i18n.Text(locale, "email.calendar."+key, nil)
				if !strings.Contains(result.HTMLBody, want) || !strings.Contains(result.TextBody, want) {
					t.Errorf("%s/%s missing %s", locale, kind, key)
				}
			}
			if strings.Contains(result.HTMLBody, "token=") == input.IsCancel {
				t.Fatal("incorrect cancellation actions")
			}
			if !input.IsCancel {
				for _, key := range []string{"accept", "decline", "tentative", "open"} {
					want := i18n.Text(locale, "email.calendar."+key, nil)
					if !strings.Contains(result.HTMLBody, want) || !strings.Contains(result.TextBody, want) {
						t.Errorf("missing %s", key)
					}
				}
				if !strings.Contains(result.TextBody, "token=accept%26token") {
					t.Fatal("action token changed")
				}
			}
		}
	}
}

func TestEmailPublicationRecipientLanguages(t *testing.T) {
	service := &PublicationService{appPublicURL: "https://example.com"}
	content := "User body 100%"
	pub := &domain.Publication{ID: uuid.New(), Title: "User title", Content: &content, Importance: domain.PublicationImportanceUrgent}
	ru := service.buildPublicationEmail(pub, i18n.Russian)
	kk := service.buildPublicationEmail(pub, i18n.Kazakh)
	if ru.Subject != "[СРОЧНО] User title" || kk.Subject != "[ШҰҒЫЛ] User title" {
		t.Fatalf("%s / %s", ru.Subject, kk.Subject)
	}
	for _, item := range []struct {
		result NotificationEmailTemplateData
		button string
	}{{ru, "Перейти к публикации"}, {kk, "Жарияланымға өту"}} {
		if !strings.Contains(item.result.HTMLBody, item.button) || !strings.Contains(item.result.TextBody, item.button) || !strings.Contains(item.result.HTMLBody, content) || !strings.Contains(item.result.TextBody, content) {
			t.Fatal(item.result)
		}
	}
}

type emailLanguageProfiles map[int64]*domain.User

func (profiles emailLanguageProfiles) GetByID(_ context.Context, id int64) (*domain.User, error) {
	if user := profiles[id]; user != nil {
		return user, nil
	}
	return nil, errors.New("missing profile")
}
func (profiles emailLanguageProfiles) GetByEmail(_ context.Context, email string) (*domain.User, error) {
	for _, user := range profiles {
		if user.Email == email {
			return user, nil
		}
	}
	return nil, errors.New("missing profile")
}

func TestEmailDeliveryUsesRecipientProfile(t *testing.T) {
	cfg := config.SMTPConfig{Enabled: true, Provider: "smtp", Host: "mail.example.com", Port: 587, FromEmail: "no-reply@example.com"}
	mail := NewEmailService(cfg, zap.NewNop())
	transport := &fakeSMTPTransport{}
	mail.transport = transport
	users := emailLanguageProfiles{1: {ID: 1, Email: "ru@example.com", Language: "ru", FirstName: "Алия"}, 2: {ID: 2, Email: "kk@example.com", Language: "kk", FirstName: "Алия"}}
	service := &NotificationService{userRepo: users, emailService: mail, emailResolver: NewEmailRecipientResolver(cfg), logger: zap.NewNop()}
	// HTTP initiator language is deliberately opposite to the first recipient.
	ctx := i18n.WithLocale(context.Background(), i18n.Kazakh)
	for _, id := range []int64{1, 2} {
		req := domain.CreateNotificationRequest{UserID: id, Type: "calendar_invite", TitleTemplate: &domain.NotificationTemplate{Source: "Приглашение на встречу"}, ContentTemplate: &domain.NotificationTemplate{Source: "%s пригласил вас на встречу: %s", Args: []any{"Ерлан", "User meeting"}}, Metadata: map[string]string{"action_url": "https://example.com/event"}}
		stored := &domain.Notification{UserID: id, Title: "Stored Russian title", Content: "Stored Russian text", CreatedAt: time.Now()}
		if err := service.sendNotificationEmail(ctx, stored, req); err != nil {
			t.Fatal(err)
		}
		want := "Уважаемый(ая)"
		if id == 2 {
			want = "Құрметті"
		}
		if !strings.Contains(transport.lastMsg, want) || strings.Contains(transport.lastMsg, "Stored Russian text") || transport.lastTo[0] != users[id].Email {
			t.Fatal("wrong recipient language or stale content")
		}
	}
	if service.recipientLocaleByEmail(ctx, "kk@example.com") != i18n.Kazakh || service.recipientLocaleByEmail(ctx, "external@example.com") != i18n.Russian {
		t.Fatal("email address locale resolution failed")
	}
}

func TestEmailTemplateCallersSpecifyLanguage(t *testing.T) {
	count := 0
	err := filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(node ast.Node) bool {
			cl, ok := node.(*ast.CompositeLit)
			if !ok {
				return true
			}
			name, ok := cl.Type.(*ast.Ident)
			if !ok || (name.Name != "NotificationEmailTemplateInput" && name.Name != "CalendarInviteEmailInput") {
				return true
			}
			count++
			found := false
			for _, e := range cl.Elts {
				if kv, ok := e.(*ast.KeyValueExpr); ok {
					if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "Language" {
						found = true
					}
				}
			}
			if !found {
				t.Errorf("%s: email template has no recipient language", path)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if count < 5 {
		t.Fatalf("unexpectedly few email senders: %d", count)
	}
	data, err := os.ReadFile("../i18n/emails.json")
	if err != nil {
		t.Fatal(err)
	}
	var catalog map[string]map[string]string
	if err = json.Unmarshal(data, &catalog); err != nil {
		t.Fatal(err)
	}
	for key := range catalog["ru"] {
		if catalog["kk"][key] == "" {
			t.Errorf("missing kk email translation: %s", key)
		}
	}
}
