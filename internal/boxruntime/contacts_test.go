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
