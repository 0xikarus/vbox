package provider

import "testing"

func TestValidateNameAndOwnership(t *testing.T) {
	if err := ValidateName("worker-1.ok"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateName("bad/name"); err == nil {
		t.Fatal("expected invalid name")
	}
	actual := Owner{AccountID: "a", BoxID: "b", Lease: "new"}
	if err := VerifyOwner(actual, Owner{AccountID: "a", BoxID: "b", Lease: "old"}); err == nil {
		t.Fatal("stale lease accepted")
	}
	if err := VerifyOwner(actual, actual); err != nil {
		t.Fatal(err)
	}
}
