package domain

import (
	"fmt"
	"sort"

	"github.com/google/uuid"
)

// Delegated application permissions become iForm roles. A verified live company
// owner receives all tenant roles; platform roles and job titles are not copied.
var IFormPermissionRoles = map[string]string{
	"iform.use": "EMPLOYEE", "iform.design": "PROCESS_DESIGNER",
	"iform.publish": "PROCESS_PUBLISHER", "iform.approve": "APPROVER",
	"iform.hr": "HR", "iform.finance": "FINANCE_APPROVER",
	"iform.accountant": "ACCOUNTANT", "iform.director": "DIRECTOR",
	"iform.sign": "SIGNER", "iform.operate": "OPERATOR", "iform.audit": "AUDITOR",
}

// Live ownership grants the company's iForm roles; job titles and platform roles
// are never accepted as proof of ownership.
func IFormRoles(permissions []string, owner bool) []string {
	roles := map[string]bool{}
	if owner {
		roles["TENANT_ADMIN"] = true
		for _, role := range IFormPermissionRoles {
			roles[role] = true
		}
	}
	for _, permission := range permissions {
		if role, ok := IFormPermissionRoles[permission]; ok {
			roles[role] = true
		}
	}
	out := make([]string, 0, len(roles))
	for role := range roles {
		out = append(out, role)
	}
	sort.Strings(out)
	return out
}

func IFormSubject(userID int64) string { return fmt.Sprintf("workspace:%d", userID) }

type IFormMember struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Position   string   `json:"position"`
	PositionID string   `json:"position_id,omitempty"`
	UnitID     string   `json:"unit_id"`
	ManagerID  string   `json:"manager_id"`
	Roles      []string `json:"roles"`
	Active     bool     `json:"active"`
}
type IFormUnit struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ParentID  string `json:"parent_id"`
	ManagerID string `json:"manager_id"`
}
type IFormPosition struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type IFormContext struct {
	OrganizationID uuid.UUID       `json:"organization_id"`
	Name           string          `json:"name"`
	Actor          string          `json:"actor"`
	Members        []IFormMember   `json:"members"`
	Units          []IFormUnit     `json:"units"`
	Positions      []IFormPosition `json:"positions"`
}
