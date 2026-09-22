package boxruntime

import "testing"

func TestValidateContactRefAcceptsBoxIdentities(t *testing.T) {
	for _, value := range []string{"builder", "box-1", "team.box_2", "00000000-0000-4000-8000-000000000000"} {
		if err := validateContactRef(value); err != nil {
			t.Fatalf("validateContactRef(%q)=%v", value, err)
		}
	}
	for _, value := range []string{"", "with space", "slash/name", "quote\"name", "line\nbreak"} {
		if err := validateContactRef(value); err == nil {
			t.Fatalf("validateContactRef(%q) accepted an invalid contact", value)
		}
	}
}

func TestResolveContactAcceptsCompactIDOrExactName(t *testing.T) {
	contacts := []ContactSummary{{ID: "a1b2c3d4", Name: "CodeChecker", CanMessage: true}}
	for _, ref := range []string{"a1b2c3d4", "CodeChecker", "codechecker"} {
		name, err := resolveContactFromList(contacts, ref)
		if err != nil || name != "CodeChecker" {
			t.Fatalf("resolveContactFromList(%q)=(%q,%v)", ref, name, err)
		}
	}
	if _, err := resolveContactFromList(contacts, "unknown"); err == nil {
		t.Fatal("unknown contact was accepted")
	}
}
