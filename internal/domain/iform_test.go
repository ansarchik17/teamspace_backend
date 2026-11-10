package domain

import (
	"reflect"
	"sort"
	"testing"
)

func TestIFormPermissionsDoNotEscalateJobTitlesOrPlatformRoles(t *testing.T) {
	got := IFormRoles([]string{"admin", "TENANT_ADMIN", "Руководитель", "iform.use", "iform.finance"}, false)
	if !reflect.DeepEqual(got, []string{"EMPLOYEE", "FINANCE_APPROVER"}) {
		t.Fatalf("unexpected roles: %v", got)
	}
	if len(IFormRoles(nil, false)) != 0 {
		t.Fatal("unconfigured employee received permissions")
	}
	owner := IFormRoles(nil, true)
	wantOwner := append([]string{"TENANT_ADMIN"}, IFormRoles(OwnerAccessPermissions("iform", true), false)...)
	sort.Strings(wantOwner)
	if !reflect.DeepEqual(owner, wantOwner) {
		t.Fatalf("live owner missing company roles: %v", owner)
	}
	if got := IFormRoles([]string{"iform.finance"}, true); !reflect.DeepEqual(got, owner) {
		t.Fatalf("position narrowed live owner's company roles: %v", got)
	}
	if _, err := ValidateAccessPermissions("iform", []string{"iform.use", "iform.approve"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateAccessPermissions("iform", []string{"TENANT_ADMIN"}); err == nil {
		t.Fatal("admin role delegated directly")
	}
}
