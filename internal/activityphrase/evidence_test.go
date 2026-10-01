package activityphrase

import "testing"

func TestNormalizeEvidenceUsesHarnessLabels(t *testing.T) {
	input := "assistant: Reviewing build.\n" +
		"tool: Running S=private; export PATH=/tmp; cd /tmp && go test ./...\n" +
		"tool: Running go test\n" +
		"tool: Running cd /tmp\n" +
		"tool: Running ls -la\n" +
		"tool: Using [redacted]\n" +
		"tool: Using chat_message\n" +
		"tool: Reading image-1.png\n" +
		"tool: Reading image-2.png\n" +
		"assistant: Next I will check the result."
	want := "assistant: Reviewing build.\n" +
		"tool: Running go test\n" +
		"tool: Using a tool\n" +
		"tool: Messaging a box\n" +
		"tool: Reviewing screenshots\n" +
		"assistant: Next I will check the result."
	if got := NormalizeEvidence(input); got != want {
		t.Fatalf("normalized evidence = %q, want %q", got, want)
	}
	if got := NormalizeEvidence(want); got != want {
		t.Fatalf("second normalization changed evidence to %q", got)
	}
}
