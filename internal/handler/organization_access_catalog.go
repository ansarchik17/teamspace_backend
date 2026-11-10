package handler

import (
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/i18n"
)

// Catalog text is server-owned. Names of companies, positions and employees
// never pass through this translation boundary. Untranslated legacy entries
// retain their existing text until their catalog translations are supplied.
func localizeAccessCatalog(locale i18n.Locale, catalog map[string][]domain.AccessPermission) map[string][]domain.AccessPermission {
	if catalog == nil {
		return nil
	}
	out := make(map[string][]domain.AccessPermission, len(catalog))
	for app, permissions := range catalog {
		items := append([]domain.AccessPermission{}, permissions...)
		for index := range items {
			permission := &items[index]
			prefix := "access.catalog." + app + "." + permission.Code
			for field, target := range map[string]*string{
				"group": &permission.Group, "name": &permission.Name, "description": &permission.Description,
			} {
				if text, err := i18n.Render(locale, prefix+"."+field, nil); err == nil {
					*target = text
				}
			}
		}
		out[app] = items
	}
	return out
}
