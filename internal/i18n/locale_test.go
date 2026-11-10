package i18n

import (
	"context"
	"testing"
)

func TestResolveHTTP(t *testing.T) {
	for _, test := range []struct {
		header, profile string
		want            Locale
	}{
		{"kk", "ru", Kazakh}, {"ru-RU", "kk", Russian},
		{"ru;q=0.2,kk-KZ;q=0.9", "ru", Kazakh},
		{"en-US,kk;q=0.5", "ru", Kazakh},
		{"kk;q=0,ru;q=0.1", "kk", Russian},
		{"kk;q=0", "ru", Russian},
		{"en", "kk", Kazakh}, {"*", "kk", Kazakh},
		{"invalid;q=no", "kk", Kazakh}, {"", "kk", Kazakh},
		{"", "en", Russian}, {"", "", Russian},
	} {
		if got := ResolveHTTP(test.header, test.profile); got != test.want {
			t.Errorf("ResolveHTTP(%q, %q) = %q, want %q", test.header, test.profile, got, test.want)
		}
	}
}

func TestRecipientLocaleIsIndependentOfRequest(t *testing.T) {
	ctx := WithLocale(context.Background(), Kazakh)
	if FromContext(ctx) != Kazakh {
		t.Fatal("request locale missing")
	}
	if ResolveRecipient("ru") != Russian {
		t.Fatal("recipient locale must win over sender")
	}
	if FromContext(context.Background()) != Russian {
		t.Fatal("locale leaked between contexts")
	}
}
