package workerprotocol

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestOperationJournalPreventsReplayAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	j := Journal{Directory: dir}
	req := request("op")
	req.Argv = []string{"example", "private-value"}
	if err := j.Claim(req); err != nil {
		t.Fatal(err)
	}
	j = Journal{Directory: dir}
	if err := j.Claim(req); !errors.Is(err, ErrOperationExists) {
		t.Fatalf("replayed claim: %v", err)
	}
	op, err := j.Inspect(req)
	if err != nil || op.ExitCode != nil {
		t.Fatalf("unfinished result: %+v %v", op, err)
	}
	if err := j.Complete(req, 7); err != nil {
		t.Fatal(err)
	}
	op, err = j.Inspect(req)
	if err != nil || op.ExitCode == nil || *op.ExitCode != 7 {
		t.Fatalf("lost exit: %+v %v", op, err)
	}
	data, err := os.ReadFile(j.path(req.OperationID))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "private-value") {
		t.Fatal("secret in journal")
	}
	req.Argv = []string{"different"}
	if _, err = j.Inspect(req); err == nil {
		t.Fatal("accepted changed request")
	}
}

func TestOperationJournalReconcilesAcrossAgentIncarnations(t *testing.T) {
	j := Journal{Directory: t.TempDir()}
	req := request("surviving-operation")
	req.Argv = []string{"example", "same-command"}
	if err := j.Claim(req); err != nil {
		t.Fatal(err)
	}
	if err := j.Complete(req, 23); err != nil {
		t.Fatal(err)
	}
	restarted := req
	restarted.Binding.Incarnation = "replacement-agent"
	op, err := j.Inspect(restarted)
	if err != nil || op.ExitCode == nil || *op.ExitCode != 23 {
		t.Fatalf("agent restart lost durable result: %+v %v", op, err)
	}
	reassigned := restarted
	reassigned.Binding.Assignment = "different-assignment"
	if _, err := j.Inspect(reassigned); err == nil {
		t.Fatal("operation crossed an assignment boundary")
	}
}
