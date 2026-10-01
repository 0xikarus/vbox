package controller

import (
	"bufio"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestClassifyMascotText(t *testing.T) {
	cases := []struct {
		name, text, mood, activity string
	}{
		{"neutral", "The repository contains source files.", "idle", "idle"},
		{"inspection", "Inspecting the repository layout.", "idle", "working"},
		{"work", "Running the build now", "idle", "working"},
		{"failure", "error: compilation failed", "angry", "idle"},
		{"recovery", "error: compilation failed\nFixed the issue. All tests passed.", "happy", "idle"},
		{"approval", "Please confirm which option to use", "waiting", "waiting"},
		{"humor", "Haha, that was funny 😂", "laughing", "idle"},
		{"humor emoji", "😂", "laughing", "idle"},
		{"question", "Would you like me to run the build?", "waiting", "waiting"},
		{"active tool", "Running tool", "idle", "working"},
		{"paraphrased failure", "The type checker rejected the change.", "angry", "idle"},
		{"paraphrased progress", "The validation task is underway.", "idle", "working"},
		{"paraphrased completion", "The correction did the trick.", "happy", "idle"},
		{"old failure followed by recovery", "The service crashed during startup. The repair worked and the app is healthy again.", "happy", "idle"},
		{"long raw paragraph", strings.Repeat("This module contains configuration and route details. ", 25) + "The compiler rejected the change.", "angry", "idle"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyMascotText(tc.text)
			if got.Mood != tc.mood || got.Activity != tc.activity {
				t.Fatalf("got %+v, want %s/%s", got, tc.mood, tc.activity)
			}
		})
	}
}

func TestMascotTranscriptEvidenceFiltersUserAndCode(t *testing.T) {
	sample := "user: Please fix the error\nassistant: I am inspecting it.\nassistant: ```text\nassistant: error: demonstration\nassistant: ```\nassistant: I am checking the patch."
	evidence := mascotTranscriptEvidence(sample)
	if strings.Contains(evidence, "Please fix") || strings.Contains(evidence, "demonstration") {
		t.Fatalf("transcript context leaked into evidence: %q", evidence)
	}
	if got := classifyMascotText(evidence); got.Activity != "working" {
		t.Fatalf("got %+v from %q", got, evidence)
	}
	if got := classifyMascotText(mascotTranscriptEvidence("assistant: Tests passed.\ntool: Running tool")); got.Activity != "working" {
		t.Fatalf("active native tool got %+v", got)
	}
}

func TestMascotModelHeldoutExamples(t *testing.T) {
	file, err := os.Open("../../scripts/mascot-data/holdout.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	correct, total := 0, 0
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		label, sample, ok := strings.Cut(scanner.Text(), "\t")
		if !ok {
			t.Fatal("invalid held-out example")
		}
		predicted, _ := mascotModel.Predict(sample)
		if predicted == label {
			correct++
		}
		total++
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if total < 30 || correct*100 < total*90 {
		t.Fatalf("held-out accuracy %d/%d is below 90%%", correct, total)
	}
}

func TestMascotObservationScopesSessionAndStoresOnlyState(t *testing.T) {
	store, mock := testStore(t)
	mock.ExpectExec(`UPDATE box_tasks SET mascot_mood`).
		WithArgs("account-a", "box-a", "codex-chat", "angry", "idle").
		WillReturnResult(sqlmock.NewResult(0, 1))
	server := chatTestServer(store)
	request := httptest.NewRequest(http.MethodPost, "/v1/agent-desktop/mascot-observation", bytes.NewBufferString(`{"session":"codex-chat","text":"error: build failed"}`))
	request.SetPathValue("id", "box-a")
	response := httptest.NewRecorder()
	server.mascotObservationHandler(response, request, Principal{AccountID: "account-a", Role: "desktop-agent", Subject: "desktop-box:box-a"})
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"mood":"angry"`) {
		t.Fatalf("response %d: %s", response.Code, response.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestMascotStateExpiresAndFollowsActiveTask(t *testing.T) {
	store, mock := testStore(t)
	mock.ExpectQuery(`SELECT mascot_mood,mascot_activity,mascot_observed_at FROM box_tasks`).WithArgs("account-a", "box-a").
		WillReturnRows(sqlmock.NewRows([]string{"mascot_mood", "mascot_activity", "mascot_observed_at"}).AddRow("happy", "idle", time.Now().Add(-time.Minute)))
	if _, fresh, err := store.boxMascotState(context.Background(), "account-a", "box-a"); err != nil || fresh {
		t.Fatalf("expired state: fresh=%t err=%v", fresh, err)
	}
	mock.ExpectQuery(`SELECT mascot_mood,mascot_activity,mascot_observed_at FROM box_tasks`).WithArgs("account-a", "box-a").
		WillReturnRows(sqlmock.NewRows([]string{"mascot_mood", "mascot_activity", "mascot_observed_at"}).AddRow("happy", "idle", time.Now()))
	state, fresh, err := store.boxMascotState(context.Background(), "account-a", "box-a")
	if err != nil || !fresh || state.Mood != "happy" {
		t.Fatalf("fresh state: %+v fresh=%t err=%v", state, fresh, err)
	}
}

func BenchmarkClassifyMascotText(b *testing.B) {
	sample := strings.Repeat("I am tracing the request and checking the code. ", 200)
	sample = sample[len(sample)-8192:]
	b.ReportAllocs()
	b.StopTimer()
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	baseRSS := mascotBenchmarkProcessMiB("VmRSS:")
	b.StartTimer()
	for i := 0; i < b.N; i++ {
		_ = classifyMascotText(sample)
	}
	b.StopTimer()
	peakRSS := mascotBenchmarkProcessMiB("VmHWM:")
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	b.ReportMetric(float64(len(sample)), "sample_B")
	b.ReportMetric(float64(before.HeapAlloc)/(1<<20), "heap_base_MiB")
	b.ReportMetric(float64(after.HeapAlloc)/(1<<20), "heap_after_MiB")
	if baseRSS > 0 {
		b.ReportMetric(baseRSS, "rss_base_MiB")
		b.ReportMetric(peakRSS, "rss_peak_MiB")
	}
}

// Linux reports process resident memory in KiB. This includes the Go runtime,
// controller package initialization, and the classifier's allocations.
func mascotBenchmarkProcessMiB(field string) float64 {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, field) {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) != 3 || parts[2] != "kB" {
			return 0
		}
		kib, err := strconv.ParseUint(parts[1], 10, 64)
		if err == nil {
			return float64(kib) / 1024
		}
	}
	return 0
}
