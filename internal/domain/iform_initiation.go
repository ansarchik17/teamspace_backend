package domain

import "github.com/google/uuid"

// Authenticated iForm supplies the immutable published policy, never form data.
type IFormInitiation struct {
	Mode        string   `json:"mode"`
	PositionIDs []string `json:"positionIds,omitempty"`
}

func (p IFormInitiation) Valid() bool {
	switch p.Mode {
	case "HR", "ALL_EMPLOYEES":
		return len(p.PositionIDs) == 0
	case "POSITIONS":
		if len(p.PositionIDs) == 0 || len(p.PositionIDs) > 1000 {
			return false
		}
		seen := map[string]bool{}
		for _, id := range p.PositionIDs {
			parsed, err := uuid.Parse(id)
			if err != nil || parsed == uuid.Nil || parsed.String() != id || seen[id] {
				return false
			}
			seen[id] = true
		}
		return true
	}
	return false
}

func (p IFormInitiation) Allows(member IFormMember) bool {
	if !member.Active || !p.Valid() {
		return false
	}
	roles := map[string]bool{}
	for _, r := range member.Roles {
		roles[r] = true
	}
	if roles["TENANT_ADMIN"] {
		return true
	}
	switch p.Mode {
	case "HR":
		return roles["HR"]
	case "ALL_EMPLOYEES":
		return roles["EMPLOYEE"]
	case "POSITIONS":
		if !roles["EMPLOYEE"] {
			return false
		}
		for _, id := range p.PositionIDs {
			if member.PositionID == id {
				return true
			}
		}
	}
	return false
}
