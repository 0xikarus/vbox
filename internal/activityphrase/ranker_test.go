package activityphrase

import (
	"bytes"
	"testing"
)

func TestRankerLearnsCandidateChoiceAndRoundTrips(t *testing.T) {
	var examples []Example
	for i := 0; i < 24; i++ {
		examples = append(examples, Example{Candidates: []Candidate{
			{Text: "Checking old logs", Recency: 5, Line: 1},
			{Text: "Editing service.go", Recency: 0, Line: 7, Tool: true},
		}, Best: 1})
	}
	model := Train(examples, 15)
	model.Calibrate(examples[:6])
	if got := model.Best(examples[0].Candidates); got != "Editing service.go" {
		t.Fatalf("best=%q", got)
	}
	encoded := model.Encode()
	decoded, err := Decode(encoded)
	if err != nil || decoded.Best(examples[0].Candidates) != "Editing service.go" {
		t.Fatalf("decoded ranker differs: %v", err)
	}
	// Recalibration changes only the threshold; the trained weights are stable.
	other := Train(examples, 15).Encode()
	if !bytes.Equal(encoded[12:], other[12:]) {
		t.Fatal("training is not deterministic")
	}
	if _, err := Decode(encoded[:len(encoded)-1]); err == nil {
		t.Fatal("truncated ranker was accepted")
	}
}

func TestTokenOverlap(t *testing.T) {
	if got := TokenOverlap("Fixing the mobile layout", "fixing mobile layout"); got < .8 {
		t.Fatalf("overlap=%f", got)
	}
	if got := TokenOverlap("Reading image.png", "Reviewing screenshots"); got != 0 {
		t.Fatalf("unrelated overlap=%f", got)
	}
}
