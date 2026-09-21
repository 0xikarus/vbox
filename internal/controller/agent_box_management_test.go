package controller

import (
	"encoding/json"
	"strings"
	"testing"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

func TestAgentManagedBoxRedactsInfrastructureAndAccessDetails(t *testing.T) {
	box := v1.LogicalBox{ID: "box-b", Name: "builder", State: v1.LogicalBoxRunning, DefaultAgent: "codex", Provider: "railway", ProviderCredential: "secret-ref", VolumeID: "volume-secret", SlotID: "slot-secret"}
	got := safeAgentManagedBox(box)
	if got.ID != box.ID || got.Name != box.Name || got.State != box.State || got.DefaultAgent != box.DefaultAgent {
		t.Fatalf("safe box=%+v", got)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"secret-ref", "volume-secret", "slot-secret"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("safe response exposed %q: %s", secret, encoded)
		}
	}
}

func TestValidateAgentBoxDeletionRequiresOtherUnprotectedBoxAndExactName(t *testing.T) {
	box := v1.LogicalBox{ID: "box-b", Name: "builder"}
	if err := validateAgentBoxDeletion("box-a", box, false, "builder"); err != nil {
		t.Fatal(err)
	}
	for name, test := range map[string]struct {
		actor        string
		protected    bool
		confirmation string
		want         string
	}{
		"self":      {actor: "box-b", confirmation: "builder", want: "cannot delete itself"},
		"protected": {actor: "box-a", protected: true, confirmation: "builder", want: "protected"},
		"name":      {actor: "box-a", confirmation: "Builder", want: "exactly match"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateAgentBoxDeletion(test.actor, box, test.protected, test.confirmation); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want %q", err, test.want)
			}
		})
	}
}
