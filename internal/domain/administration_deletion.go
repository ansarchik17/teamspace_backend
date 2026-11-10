package domain

// Company owners must be reassigned before their individual account is deleted.
func CanDeleteEmployee(actor, target int64, role Role, owner *int64) bool {
	return actor > 0 && CanChangeEmployeeBlock(actor, target, role, owner)
}
