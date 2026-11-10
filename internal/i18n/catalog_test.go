package i18n

import (
	"strings"
	"testing"
)

func TestCatalogFallbackAndRendering(t *testing.T) {
	catalog, err := NewCatalog(map[Locale]map[string]string{
		Russian: {"greeting": "Привет, {{.Name}}", "fallback": "Русский текст"},
		Kazakh:  {"greeting": "Сәлем, {{.Name}}", "fallback": ""},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		locale    Locale
		key, want string
	}{
		{Kazakh, "greeting", "Сәлем, Алия"}, {Russian, "greeting", "Привет, Алия"},
		{Kazakh, "fallback", "Русский текст"}, {Locale("unknown"), "fallback", "Русский текст"},
	} {
		got, err := catalog.Render(test.locale, test.key, map[string]any{"Name": "Алия"})
		if err != nil || got != test.want {
			t.Fatalf("Render: got=%q err=%v want=%q", got, err, test.want)
		}
	}
	if _, err := catalog.Render(Kazakh, "unknown", nil); err == nil {
		t.Fatal("expected unknown key error")
	}
	if _, err := catalog.Render(Kazakh, "greeting", nil); err == nil {
		t.Fatal("expected missing parameter error")
	}
	if _, err := NewCatalog(map[Locale]map[string]string{Russian: {"bad": "{{"}}); err == nil {
		t.Fatal("expected invalid template error")
	}
}

func TestBundledCatalog(t *testing.T) {
	for _, locale := range []Locale{Russian, Kazakh, Locale("en")} {
		for key := range defaultCatalog.templates[Russian] {
			result, err := Render(locale, key, map[string]any{"ID": "42", "Source": "Team", "P0": "Алия", "P1": "B-42", "P2": "Board", "P3": "Task", "Name": "Алия", "Time": "12:00", "Title": "Meeting", "Organizer": "Алия", "Description": "Description", "Start": "12:00", "End": "13:00", "Sender": "user@example.com", "Importance": "Important"})
			if err != nil || result == "" || strings.Contains(result, "{{") {
				t.Fatalf("%s/%s: %q, %v", locale, key, result, err)
			}
			if locale == Kazakh && defaultCatalog.templates[Kazakh][key] == nil {
				t.Errorf("missing Kazakh translation: %s", key)
			}
		}
	}
}
