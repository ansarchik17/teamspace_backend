package domain

// IFormDocumentCompletion resolves an explicit milestone without treating later
// terminal statuses as proof that the required milestone was observed.
// An empty milestone preserves the contract of existing published processes.
func IFormDocumentCompletion(status, milestone string) (string, bool) {
	if milestone != "" && milestone != "completed" && milestone != "closed" && milestone != "signed" && milestone != "archived" {
		return "", false
	}
	switch status {
	case "rejected", "cancelled", "withdrawn":
		return "FAILED", true
	}
	if milestone != "" {
		if status == milestone {
			return "SUCCEEDED", true
		}
		if status == "archived" || status == "closed" && milestone != "archived" {
			return "FAILED", true
		}
		return "WAITING", true
	}
	switch status {
	case "completed", "closed", "archived":
		return "SUCCEEDED", true
	default:
		return "WAITING", true
	}
}

// A milestone reached between stages is not completion of the configured route.
func IFormDocumentRouteCompletion(status, milestone, phase string, configured bool) (string, bool) {
	state, valid := IFormDocumentCompletion(status, milestone)
	if !valid || milestone == "" || !configured {
		return state, valid
	}
	if phase == "rejected" || phase == "cancelled" {
		return "FAILED", true
	}
	if state == "SUCCEEDED" && phase != "completed" {
		return "WAITING", true
	}
	return state, true
}
