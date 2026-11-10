package service

import (
	"github.com/west6ide/workspace-backend/internal/domain"
	"testing"
)

func TestNotificationContentUsesSharedCatalog(t *testing.T) {
	for _, test := range []struct{ language, title, message string }{
		{"ru", "Заявление обработано", "Ваше заявление №42 успешно обработано. Результат доступен в разделе «Заявления»."},
		{"kk", "Өтініш өңделді", "Сіздің №42 өтінішіңіз сәтті өңделді. Нәтиже «Өтініштер» бөлімінде қолжетімді."},
		{"en", "Application processed", "Your application #42 has been processed successfully. The result is available in the Applications section."},
	} {
		result := buildNotificationContent(domain.InternalNotificationRequest{Language: test.language, NotificationType: "APPLICATION_STATUS", Status: "PROCESSED", ApplicationNumber: "42"})
		if result.Title != test.title || result.MessageText != test.message {
			t.Fatalf("%s: %+v", test.language, result)
		}
	}
}

func TestNotificationContentUsesRecipientLanguage(t *testing.T) {
	for _, test := range []struct{ requested, recipient, want string }{
		{"", "kk", "kk"}, {"", "ru", "ru"}, {"", "", "ru"},
		{"unsupported", "kk", "kk"}, {"ru", "kk", "kk"}, {"en", "kk", "kk"},
	} {
		result := buildRecipientNotificationContent(domain.InternalNotificationRequest{Language: test.requested}, test.recipient)
		if result.Language != test.want {
			t.Fatalf("request=%q recipient=%q: got=%q want=%q", test.requested, test.recipient, result.Language, test.want)
		}
	}
}
