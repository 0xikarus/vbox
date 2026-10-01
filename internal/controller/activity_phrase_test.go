package controller

import (
	"bufio"
	"encoding/json"
	"os"
	"sort"
	"testing"
	"time"
)

func TestActivityPhrasePrefersNewestToolLabel(t *testing.T) {
	text := "assistant: I am inspecting the build.\ntool: Running go test"
	if got := activityPhrase(text); got != "Running go test" {
		t.Fatalf("latest tool label = %q", got)
	}
	if got := activityPhrase("assistant: Sending a reply.\ntool: Using chat_message"); got != "Messaging a box" {
		t.Fatalf("old MCP tool label = %q", got)
	}
}

func TestActivityPhraseDropsTrivialToolOnlyEvidence(t *testing.T) {
	if got := activityPhrase("tool: Running cd /tmp\ntool: Running ls -la"); got != "" {
		t.Fatalf("trivial shell activity = %q", got)
	}
}

func TestActivityPhraseRejectsInvalidGenerations(t *testing.T) {
	for _, phrase := range []string{
		"fixing the build",                      // Must start with a capital.
		"Fixing fixing the build",               // Repeated token.
		"Checking one two three four five six",  // More than six words.
		"Editing <unk>",                         // Decoder control token.
		"Reviewing extraordinarilylongfilename", // More than 32 characters.
		"Services mascot state machine",         // First word is not a verb form.
		"Merging deepseek merge",                // Repeated verb stem.
		"Waking image-6.png",                    // Non-file verb before filename.
		"Running cd",                            // Trivial shell command.
		"Using a tool",                          // Generic tool name.
		"Planning mascot plan",                  // Repeated stem after doubled consonant.
		"Holding deepseek screenshots",          // Garbled screenshot action.
	} {
		if validActivityPhrase(phrase) {
			t.Errorf("accepted invalid phrase %q", phrase)
		}
	}
	if !validActivityPhrase("Fixing the mobile layout") {
		t.Fatal("rejected normal activity phrase")
	}
	if !validActivityPhrase("Merging README.md") {
		t.Fatal("rejected a valid file action")
	}
}

func TestActivityPhraseMatchesPythonFinalOutput(t *testing.T) {
	file, err := os.Open("../activityphrase/testdata/activity_golden.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	count := 0
	for scanner.Scan() {
		var row struct {
			ID     string `json:"id"`
			Text   string `json:"text"`
			Output string `json:"output"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			t.Fatal(err)
		}
		if got := activityPhrase(row.Text); got != row.Output {
			t.Errorf("%s: Go activity %q, Python %q", row.ID, got, row.Output)
		}
		count++
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if count != 50 {
		t.Fatalf("want 50 activity goldens, got %d", count)
	}
}

func BenchmarkActivityPhrase20Real(b *testing.B) {
	file, err := os.Open("../activityphrase/testdata/activity_benchmark.jsonl")
	if err != nil {
		b.Fatal(err)
	}
	defer file.Close()
	var texts []string
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	for scanner.Scan() {
		var row struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			b.Fatal(err)
		}
		texts = append(texts, row.Text)
	}
	if err := scanner.Err(); err != nil {
		b.Fatal(err)
	}
	if len(texts) < 20 {
		b.Fatal("need 20 real inputs")
	}
	durations := make([]time.Duration, 0, b.N*20)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, text := range texts[:20] {
			started := time.Now()
			_ = activityPhrase(text)
			durations = append(durations, time.Since(started))
		}
	}
	b.StopTimer()
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	b.ReportMetric(float64(durations[len(durations)/2])/float64(time.Millisecond), "p50_ms")
	b.ReportMetric(float64(durations[(len(durations)*95+99)/100-1])/float64(time.Millisecond), "p95_ms")
}
