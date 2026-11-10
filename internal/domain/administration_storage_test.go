package domain

import "testing"

func TestAdministrationStorageRequestValidation(t *testing.T) {
	zero, negative, tooLarge := int64(0), int64(-1), MaxAdministrationStorageBytes+1
	for _, r := range []AdministrationStorageRequest{{}, {QuotaBytes: &zero}, {Revision: &zero}, {QuotaBytes: &negative, Revision: &zero}, {QuotaBytes: &zero, Revision: &negative}, {QuotaBytes: &tooLarge, Revision: &zero}} {
		if r.Valid() {
			t.Fatalf("invalid request accepted: %+v", r)
		}
	}
	if !(AdministrationStorageRequest{QuotaBytes: &zero, Revision: &zero}).Valid() {
		t.Fatal("zero quota should disable growth")
	}
}
