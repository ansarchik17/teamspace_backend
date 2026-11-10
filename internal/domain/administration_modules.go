package domain

// Module availability is distinct from the application's managed permissions mode.
type AdministrationModule struct {
	Application string `json:"application"`
	Available   bool   `json:"available"`
	Enabled     bool   `json:"enabled"`
	Effective   bool   `json:"effective"`
	Revision    int64  `json:"revision"`
}

type AdministrationModuleRequest struct {
	Available *bool  `json:"available"`
	Enabled   *bool  `json:"enabled"`
	Revision  *int64 `json:"revision"`
}

func AdministrationModules() []string { return []string{"iform", "odo", "sd", "growy", "crm"} }
func IsAdministrationModule(app string) bool {
	for _, name := range AdministrationModules() {
		if name == app {
			return true
		}
	}
	return false
}
