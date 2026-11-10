// Package i18n provides shared server translations and locale resolution.
package i18n

import (
	"context"
	"strings"

	"golang.org/x/text/language"
)

type Locale string

const (
	Russian Locale = "ru"
	Kazakh  Locale = "kk"
)

type localeContextKey struct{}

// Parse accepts the supported language codes and their regional variants.
func Parse(value string) (Locale, bool) {
	tag, err := language.Parse(strings.TrimSpace(value))
	if err != nil {
		return "", false
	}
	base, _ := tag.Base()
	switch base.String() {
	case "ru":
		return Russian, true
	case "kk":
		return Kazakh, true
	default:
		return "", false
	}
}

// RequestLocale selects the first supported positive-weight language, respecting
// Accept-Language quality weights. Wildcards do not override the profile.
func RequestLocale(header string) (Locale, bool) {
	tags, _, err := language.ParseAcceptLanguage(header)
	if err != nil {
		return "", false
	}
	for _, tag := range tags {
		if locale, ok := Parse(tag.String()); ok {
			return locale, true
		}
	}
	return "", false
}

func ResolveHTTP(header, profileLanguage string) Locale {
	if locale, ok := RequestLocale(header); ok {
		return locale
	}
	return ResolveRecipient(profileLanguage)
}

// ResolveRecipient deliberately ignores the initiating request's locale.
func ResolveRecipient(profileLanguage string) Locale {
	if locale, ok := Parse(profileLanguage); ok {
		return locale
	}
	return Russian
}

func WithLocale(ctx context.Context, locale Locale) context.Context {
	return context.WithValue(ctx, localeContextKey{}, ResolveRecipient(string(locale)))
}

func FromContext(ctx context.Context) Locale {
	if locale, ok := ctx.Value(localeContextKey{}).(Locale); ok {
		return locale
	}
	return Russian
}
