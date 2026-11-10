package domain

// Transport state stays compatible with published processes; outcome is the
// business observation shown in iForm. A return remains a poll of the same ID.
func IFormDocumentObservation(status, milestone, phase string, configured bool) (state, outcome, message string, valid bool) {
	switch status {
	case "draft", "registered", "on_approval", "approved", "in_execution", "waiting_for_signature", "signed", "completed", "closed", "archived", "rejected", "cancelled", "withdrawn", "revision_required", "executed":
	default:
		return "", "", "", false
	}
	state, valid = IFormDocumentRouteCompletion(status, milestone, phase, configured)
	if !valid {
		return
	}
	switch {
	case status == "rejected" || configured && phase == "rejected":
		return "FAILED", "REJECTED", "Документ ЭДО отклонён. Приём остановлен", true
	case status == "cancelled" || status == "withdrawn" || configured && phase == "cancelled":
		return "FAILED", "CANCELLED", "Документ ЭДО отменён или отозван. Приём остановлен", true
	case status == "revision_required" || configured && phase == "revision_required":
		return "WAITING", "RETURNED", "Документ возвращён на доработку в ЭДО. Ожидаем исправления того же документа", true
	case state == "SUCCEEDED":
		return state, "COMPLETED", "Завершение документа ЭДО подтверждено", true
	case state == "FAILED":
		return state, "BLOCKED", "Документ закрыт до подтверждения согласованного статуса. Требуется сверка", true
	default:
		return "WAITING", "WAITING", "Ожидаем фактического завершения документа ЭДО", true
	}
}
