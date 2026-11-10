package service

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/i18n"
)

type notificationLanguageUsers map[int64]string

func (users notificationLanguageUsers) GetByID(_ context.Context, id int64) (*domain.User, error) {
	return &domain.User{ID: id, Language: users[id]}, nil
}

func TestSystemNotificationRecipientLocalization(t *testing.T) {
	s := &NotificationService{userRepo: notificationLanguageUsers{1: "ru", 2: "kk", 3: "unsupported"}}
	ctx := i18n.WithLocale(context.Background(), i18n.Kazakh)
	req := domain.CreateNotificationRequest{Type: "calendar_invite", TitleTemplate: &domain.NotificationTemplate{Source: "Приглашение на встречу"}, ContentTemplate: &domain.NotificationTemplate{Source: "%s пригласил вас на встречу: %s", Args: []any{"Алия", "Budget {{.P0}} 100%"}}}
	for _, tc := range []struct {
		id          int64
		locale      i18n.Locale
		title, body string
	}{
		{1, i18n.Russian, "Приглашение на встречу", "Алия пригласил вас на встречу: Budget {{.P0}} 100%"},
		{2, i18n.Kazakh, "Кездесуге шақыру", "Алия сізді кездесуге шақырды: Budget {{.P0}} 100%"},
		{3, i18n.Russian, "Приглашение на встречу", "Алия пригласил вас на встречу: Budget {{.P0}} 100%"},
	} {
		req.UserID = tc.id
		title, body, locale := s.localizeNotification(ctx, req)
		if title != tc.title || body != tc.body || locale != tc.locale {
			t.Fatalf("recipient %d: %q %q %q", tc.id, title, body, locale)
		}
	}
}

func TestSystemNotificationPreservesUserText(t *testing.T) {
	s := &NotificationService{userRepo: notificationLanguageUsers{1: "kk"}}
	// Even a user string identical to a server label is not translated.
	req := domain.CreateNotificationRequest{UserID: 1, Type: "publication_alert", Title: "Новое сообщение", Content: "Встреча отменена"}
	title, body, _ := s.localizeNotification(context.Background(), req)
	// Legacy generic title handling remains unchanged; explicit template parameters
	// always preserve user content.
	if body != req.Content {
		t.Fatalf("user body changed: %q", body)
	}
	title = renderNotificationTemplate(i18n.Kazakh, &domain.NotificationTemplate{Source: "%s", Args: []any{req.Title}}, title)
	if title != req.Title {
		t.Fatalf("user title changed: %q", title)
	}
}

func TestSystemNotificationDefaultsAndLabels(t *testing.T) {
	s := &NotificationService{userRepo: notificationLanguageUsers{1: "kk"}}
	for _, kind := range []string{"calendar_invite", "calendar_event_updated", "calendar_event_deleted", "calendar_reminder", "drive_access_granted", "web_login", "system", "kanban_task_created", "kanban_task_assigned"} {
		req := domain.CreateNotificationRequest{UserID: 1, Type: kind, Metadata: map[string]string{"task_key": "B-42", "task_title": "Hello", "event_title": "Meeting", "board_name": "Board"}}
		title, body, _ := s.localizeNotification(context.Background(), req)
		ruTitle, ruBody := normalizeNotificationText(kind, "", "", req.Metadata)
		if title == ruTitle || body == ruBody {
			t.Errorf("%s has untranslated defaults: %s / %s", kind, title, body)
		}
	}
	text := renderNotificationTemplate(i18n.Kazakh, &domain.NotificationTemplate{Source: "Вам открыли доступ (%s) на %s: %s", Args: []any{domain.NotificationLabel("редактирование"), domain.NotificationLabel("папку"), "папку"}}, "")
	if !strings.Contains(text, "өңдеу") || !strings.Contains(text, "бума") || !strings.HasSuffix(text, "папку") {
		t.Fatal(text)
	}
	if localizedCountdown(i18n.Russian, 61) != FormatCountdownRU(61) || localizedCountdown(i18n.Kazakh, 61) != "1 сағат 1 минуттан кейін" {
		t.Fatal("invalid countdown")
	}
}

func TestSystemNotificationCallPayloadLanguages(t *testing.T) {
	s := &NotificationService{userRepo: notificationLanguageUsers{1: "ru", 2: "kk"}}
	for _, id := range []int64{1, 2} {
		payload := buildIncomingCallPayload(7, "Алия", [16]byte{}, "room", "chat", "url", "", "high", false, time.Time{}, "")
		s.localizeCallPayload(context.Background(), id, &payload)
		want := "Входящий звонок"
		if id == 2 {
			want = "Кіріс қоңырау"
		}
		if payload.Title != want || payload.toStringMap()["title"] != want || !strings.Contains(payload.Body, "Алия") {
			t.Fatalf("%+v", payload)
		}
	}
}

// Audit explicitly marked server templates and push labels in production code.
func TestSystemNotificationTemplatesHaveBothLanguages(t *testing.T) {
	data, err := os.ReadFile("../i18n/system_notifications.json")
	if err != nil {
		t.Fatal(err)
	}
	var messages map[string]map[string]string
	if err = json.Unmarshal(data, &messages); err != nil {
		t.Fatal(err)
	}
	sources := map[string]bool{}
	err = filepath.Walk("..", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		add := func(expr ast.Expr) {
			if lit, ok := expr.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				source, _ := strconv.Unquote(lit.Value)
				sources[source] = true
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if cl, ok := n.(*ast.CompositeLit); ok {
				if sel, ok := cl.Type.(*ast.SelectorExpr); ok && sel.Sel.Name == "NotificationTemplate" {
					for _, e := range cl.Elts {
						if kv, ok := e.(*ast.KeyValueExpr); ok {
							if name, ok := kv.Key.(*ast.Ident); ok && name.Name == "Source" {
								add(kv.Value)
							}
						}
					}
				}
			}
			if call, ok := n.(*ast.CallExpr); ok && len(call.Args) >= 2 {
				if fn, ok := call.Fun.(*ast.SelectorExpr); ok && fn.Sel.Name == "SystemNotification" {
					add(call.Args[1])
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	params := map[string]any{}
	for i := 0; i < 12; i++ {
		params[fmt.Sprintf("P%d", i)] = "X"
	}
	for source := range sources {
		for _, locale := range []i18n.Locale{i18n.Russian, i18n.Kazakh} {
			key := i18n.SystemNotificationKey(source)
			if messages[string(locale)][key] == "" {
				t.Errorf("missing %s template %q", locale, source)
				continue
			}
			if _, err := i18n.Render(locale, key, params); err != nil {
				t.Error(err)
			}
		}
	}
	if len(sources) < 30 {
		t.Fatalf("unexpectedly small audit: %d", len(sources))
	}
}
