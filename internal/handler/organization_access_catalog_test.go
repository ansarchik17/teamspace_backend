package handler

import (
	"reflect"
	"strings"
	"testing"

	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/i18n"
)

func TestAccessCatalogArchiveLocalizationPreservesPermissionCodesAndSource(t *testing.T) {
	source := domain.AccessCatalog()
	for _, locale := range []i18n.Locale{i18n.Russian, i18n.Kazakh} {
		localized := localizeAccessCatalog(locale, source)
		for app, permissions := range source {
			if len(permissions) != len(localized[app]) {
				t.Fatalf("localization changed %s permission count", app)
			}
			for index, permission := range permissions {
				got := localized[app][index]
				if got.Code != permission.Code {
					t.Fatalf("localization changed code: %s -> %s", permission.Code, got.Code)
				}
				if app != "odo" || !strings.HasPrefix(permission.Code, "archive.") {
					_, translated := i18n.Render(locale, "access.catalog."+app+"."+permission.Code+".description", nil)
					if translated != nil && got != permission {
						t.Fatalf("untranslated legacy permission changed: %s", permission.Code)
					}
					if translated == nil && (got.Description == "" || got.Name != permission.Name || got.Group != permission.Group) {
						t.Fatalf("invalid conditional role guidance: %+v", got)
					}
					continue
				}
				prefix := "access.catalog.odo." + permission.Code
				for field, value := range map[string]string{"group": got.Group, "name": got.Name, "description": got.Description} {
					text, err := i18n.Render(locale, prefix+"."+field, nil)
					if err != nil || text != value || value == "" {
						t.Fatalf("missing %s translation for %s.%s: %v", locale, permission.Code, field, err)
					}
				}
				if locale == i18n.Kazakh && (got.Group != "Мұрағат" || got.Name == permission.Name || got.Description == permission.Description) {
					t.Fatalf("archive text was not translated into Kazakh: %+v", got)
				}
			}
		}
	}
	if !reflect.DeepEqual(source, domain.AccessCatalog()) {
		t.Fatal("localization mutated the source catalog")
	}
	if localizeAccessCatalog(i18n.Kazakh, nil) != nil {
		t.Fatal("nil catalog was not preserved")
	}
}
