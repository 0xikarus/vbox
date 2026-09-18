package controller

import "testing"

// The fence repair must fire for the worker-side failures that actually mean the
// tmux server lost the assignment, and for nothing else — repairing on an
// unrelated error would re-bind a worker for reasons nobody checked.
func TestWorkerFenceLostRecognisesOnlyFenceFailures(t *testing.T) {
	for _, detail := range []string{
		"vmbox-runtime: worker assignment unavailable or changed",
		"server incarnation unavailable; enable native sessions",
		"desktop startup requires a worker assignment",
		"WORKER ASSIGNMENT UNAVAILABLE",
	} {
		if !workerFenceLost(detail) {
			t.Fatalf("fence loss not recognised: %q", detail)
		}
	}
	for _, detail := range []string{
		"desktop components unavailable; enable the desktop worker image first",
		"desktop startup timed out; inspect vmbox-desktop session",
		"chromium: pthread_create: Resource temporarily unavailable",
		"assignment changed while applying instructions",
		"",
	} {
		if workerFenceLost(detail) {
			t.Fatalf("unrelated failure treated as fence loss: %q", detail)
		}
	}
}
