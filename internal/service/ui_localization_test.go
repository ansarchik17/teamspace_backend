package service

import (
	"context"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/i18n"
	"github.com/west6ide/workspace-backend/internal/repository"
	"strings"
	"testing"
	"time"
)

func TestLocalizedAICacheIdentityAndInvalidation(t *testing.T) {
	base := AITasksSummaryKey(7)
	ru := AILocalizedKey(context.Background(), base)
	kk := AILocalizedKey(i18n.WithLocale(context.Background(), i18n.Kazakh), base)
	if ru == kk {
		t.Fatal("different languages share cached response")
	}
	keys := aiCacheLocaleKeys(base)
	joined := strings.Join(keys, "|")
	if !strings.Contains(joined, ru) || !strings.Contains(joined, kk) || keys[0] != base {
		t.Fatal("cache invalidation misses language or legacy entries")
	}
}

func TestAssistantUILocalization(t *testing.T) {
	s := &AIAssistantService{}
	ru := context.Background()
	kk := i18n.WithLocale(ru, i18n.Kazakh)
	if s.buildCalendarSpeech(nil, time.Now(), kk) == s.buildCalendarSpeech(nil, time.Now(), ru) {
		t.Fatal("calendar speech not localized")
	}
	items := []AITaskItem{{Title: "User title", Priority: "high"}}
	text := s.buildTasksSpeech(items, kk)
	if !strings.Contains(text, "белсенді тапсырма") || !strings.Contains(text, "User title") {
		t.Fatal(text)
	}
	result, err := s.Execute(kk, 1, AIExecuteRequest{Intent: "greeting"})
	if err != nil || !strings.Contains(result.SpeechText, "Сәлеметсіз") {
		t.Fatalf("%+v %v", result, err)
	}
}
func TestDocumentUIErrorLanguageAndSafety(t *testing.T) {
	code := "read_error"
	secret := "SQLSTATE provider password=secret"
	doc := &repository.AIDocument{ErrorCode: &code, ErrorMessage: &secret}
	kk := localizedAIDocument(i18n.WithLocale(context.Background(), i18n.Kazakh), doc)
	ru := localizedAIDocument(context.Background(), doc)
	if *kk.ErrorCode != *ru.ErrorCode || *kk.ErrorMessage == *ru.ErrorMessage || strings.Contains(*kk.ErrorMessage, "secret") || *doc.ErrorMessage != secret {
		t.Fatal("document localization contract failed")
	}
}
func TestCalendarPrivacyLabelsLocalized(t *testing.T) {
	ctx := i18n.WithLocale(context.Background(), i18n.Kazakh)
	item := redactFreeBusyInterval(domain.CalendarBusyIntervalItem{EventTitle: "private title"}, 2, 1, ctx)
	if item.EventTitle != "Бос емес" {
		t.Fatal(item.EventTitle)
	}
}
