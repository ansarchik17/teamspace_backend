package i18n

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"text/template"
)

//go:embed *.json
var catalogFiles embed.FS

// Catalog is immutable after construction and safe for concurrent readers.
type Catalog struct {
	templates map[Locale]map[string]*template.Template
}

func NewCatalog(messages map[Locale]map[string]string) (*Catalog, error) {
	catalog := &Catalog{templates: make(map[Locale]map[string]*template.Template)}
	for locale, entries := range messages {
		catalog.templates[locale] = make(map[string]*template.Template)
		for key, value := range entries {
			if value == "" {
				continue
			}
			parsed, err := template.New(key).Option("missingkey=error").Parse(value)
			if err != nil {
				return nil, fmt.Errorf("translation %s/%s: %w", locale, key, err)
			}
			catalog.templates[locale][key] = parsed
		}
	}
	return catalog, nil
}

func (c *Catalog) Render(locale Locale, key string, params map[string]any) (string, error) {
	translated := c.templates[locale][key]
	if translated == nil {
		translated = c.templates[Russian][key]
	}
	if translated == nil {
		return "", fmt.Errorf("unknown translation key %q", key)
	}
	var output bytes.Buffer
	if err := translated.Execute(&output, params); err != nil {
		return "", fmt.Errorf("render translation %q: %w", key, err)
	}
	return output.String(), nil
}

var defaultCatalog = loadCatalog()

func loadCatalog() *Catalog {
	messages := make(map[Locale]map[string]string)
	files, err := catalogFiles.ReadDir(".")
	if err != nil {
		panic(err)
	}
	for _, file := range files {
		data, err := catalogFiles.ReadFile(file.Name())
		if err != nil {
			panic(err)
		}
		var entries map[Locale]map[string]string
		if err := json.Unmarshal(data, &entries); err != nil {
			panic(err)
		}
		for locale, values := range entries {
			if messages[locale] == nil {
				messages[locale] = make(map[string]string)
			}
			for key, value := range values {
				if _, exists := messages[locale][key]; exists {
					panic("duplicate translation key: " + key)
				}
				messages[locale][key] = value
			}
		}
	}
	catalog, err := NewCatalog(messages)
	if err != nil {
		panic(err)
	}
	return catalog
}

func Render(locale Locale, key string, params map[string]any) (string, error) {
	return defaultCatalog.Render(locale, key, params)
}

// Text returns the key on a programmer error rather than an empty UI string.
// Use Render when the caller needs to handle an unknown key or missing parameter.
func Text(locale Locale, key string, params map[string]any) string {
	text, err := Render(locale, key, params)
	if err != nil {
		return key
	}
	return text
}

func TextContext(ctx context.Context, key string, params map[string]any) string {
	return Text(FromContext(ctx), key, params)
}
