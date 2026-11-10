package i18n

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestCatalogCompletenessAndParameters(t *testing.T) {
	fields := regexp.MustCompile(`\.([A-Za-z][A-Za-z0-9_]*)`)
	parameters := func(text string) []string {
		seen := map[string]bool{}
		for _, action := range regexp.MustCompile(`{{[^}]*}}`).FindAllString(text, -1) {
			for _, match := range fields.FindAllStringSubmatch(action, -1) {
				seen[match[1]] = true
			}
		}
		out := []string{}
		for name := range seen {
			out = append(out, name)
		}
		sort.Strings(out)
		return out
	}
	files, err := catalogFiles.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, file := range files {
		data, err := catalogFiles.ReadFile(file.Name())
		if err != nil {
			t.Fatal(err)
		}
		var entries map[Locale]map[string]string
		if err = json.Unmarshal(data, &entries); err != nil {
			t.Fatal(err)
		}
		for key, ru := range entries[Russian] {
			if strings.TrimSpace(ru) == "" || strings.TrimSpace(entries[Kazakh][key]) == "" {
				t.Errorf("%s: missing ru/kk %s", file.Name(), key)
			}
			if seen[key] {
				t.Errorf("duplicate catalog key %s", key)
			}
			seen[key] = true
			if !reflect.DeepEqual(parameters(ru), parameters(entries[Kazakh][key])) {
				t.Errorf("%s has different template parameters", key)
			}
		}
		for key := range entries[Kazakh] {
			if _, ok := entries[Russian][key]; !ok {
				t.Errorf("%s: kk-only key %s", file.Name(), key)
			}
		}
	}
	if len(seen) < 300 {
		t.Fatalf("unexpectedly small catalog audit: %d", len(seen))
	}
}

func TestUsedTranslationKeysExistInBothLocales(t *testing.T) {
	used := map[string]bool{}
	literal := func(expr ast.Expr) (string, bool) {
		lit, ok := expr.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return "", false
		}
		value, err := strconv.Unquote(lit.Value)
		return value, err == nil
	}
	err := filepath.Walk("..", func(path string, info os.FileInfo, err error) error {
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
			if call, ok := node.(*ast.CallExpr); ok {
				if selector, ok := call.Fun.(*ast.SelectorExpr); ok && len(call.Args) >= 2 {
					if pkg, ok := selector.X.(*ast.Ident); ok && pkg.Name == "i18n" {
						if source, ok := literal(call.Args[1]); ok {
							switch selector.Sel.Name {
							case "Text", "TextContext", "Render":
								used[source] = true
							case "SystemNotification":
								used[SystemNotificationKey(source)] = true
							case "UIMessage":
								used[UIMessageKey(source)] = true
							case "APIMessage":
								used[APIMessageKey(source)] = true
							}
						}
					}
				}
				if name, ok := call.Fun.(*ast.Ident); ok {
					switch name.Name {
					case "apiMessage":
						if len(call.Args) > 1 {
							if source, ok := literal(call.Args[1]); ok {
								used[APIMessageKey(source)] = true
							}
						}
					case "translate":
						if strings.HasSuffix(path, "notification_content_builder.go") && len(call.Args) > 0 {
							if suffix, ok := literal(call.Args[0]); ok {
								used["notification."+suffix] = true
							}
						}
					case "text":
						if len(call.Args) > 0 {
							if suffix, ok := literal(call.Args[0]); ok {
								switch filepath.Base(path) {
								case "email_template_service.go":
									used["email."+suffix] = true
								case "calendar_email_template.go":
									used["email.calendar."+suffix] = true
								}
							}
						}
					}
				}
			}
			if cl, ok := node.(*ast.CompositeLit); ok {
				if typ, ok := cl.Type.(*ast.SelectorExpr); ok && typ.Sel.Name == "NotificationTemplate" {
					for _, e := range cl.Elts {
						if kv, ok := e.(*ast.KeyValueExpr); ok {
							if name, ok := kv.Key.(*ast.Ident); ok && name.Name == "Source" {
								if source, ok := literal(kv.Value); ok {
									used[SystemNotificationKey(source)] = true
								}
							}
						}
					}
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for key := range used {
		for _, locale := range []Locale{Russian, Kazakh} {
			if defaultCatalog.templates[locale][key] == nil {
				t.Errorf("used key missing from %s: %s", locale, key)
			}
		}
	}
	if len(used) < 150 {
		t.Fatalf("unexpectedly small source audit: %d", len(used))
	}
}
