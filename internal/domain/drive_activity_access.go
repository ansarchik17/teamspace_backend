package domain

import "strings"

var driveActivityLeadershipPositionTokens = []string{
	"министр",
	"вице-министр",
	"первый вице-министр",
	"заместитель министра",
	"руководитель аппарата",
	"глава аппарата",
	"председатель",
	"заместитель председателя",
	"head of staff",
	"minister",
	"vice minister",
	"deputy minister",
	"chairman",
	"chairperson",
}

func NormalizeDriveActivityPosition(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	return strings.Join(strings.Fields(value), " ")
}

func PositionGrantsFullDriveActivity(value string) bool {
	normalized := NormalizeDriveActivityPosition(value)
	if normalized == "" {
		return false
	}
	for _, token := range driveActivityLeadershipPositionTokens {
		if strings.Contains(normalized, token) {
			return true
		}
	}
	return false
}
