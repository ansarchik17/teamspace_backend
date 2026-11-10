package domain

// AIReadiness is a read-only snapshot, not authorization to deploy.
type AIReadiness struct {
	MissingMigrations      []int64 `json:"missing_migrations"`
	ActiveCompanies        int     `json:"active_companies"`
	UnconfiguredCompanies  int     `json:"unconfigured_companies"`
	UnconfiguredEmployees  int     `json:"unconfigured_employees"`
	OvercommittedCompanies int     `json:"overcommitted_companies"`
	ActiveReservations     int64   `json:"active_reservations"`
	UnknownReservations    int64   `json:"unknown_reservations"`
	StaleReservations      int64   `json:"stale_reservations"`
}

func (r AIReadiness) ReadyForEnforcement() bool {
	// Missing personal overrides use defaults. Legacy allocated company totals
	// do not constrain personal admission. Service company budgets still apply.
	return len(r.MissingMigrations) == 0 && r.UnconfiguredCompanies == 0 && r.StaleReservations == 0 && r.UnknownReservations == 0
}

var AIAccountingMigrationVersions = []int64{
	20261020100100, 20261020100200, 20261020100300,
	20261020100400, 20261020100500, 20261020100600,
	20261020100700, 20261020100800, 20261020100900, 20261020101000,
	20261022100100, 20261022100200, 20261022100400, 20261022100500,
}
