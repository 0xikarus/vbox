package main

import (
	"context"
	"testing"

	"github.com/0xikarus/vmbox-service/internal/factory"
)

func TestPlanningRequiresExplicitCompleteConfiguration(t *testing.T) {
	t.Setenv("VMBOX_FACTORY_EXECUTION_ENABLED", "")
	s := &factory.Service{}
	if d, err := configurePlanning(context.Background(), s, nil); err != nil || d != nil || s.ExecutionReady {
		t.Fatal("execution enabled implicitly")
	}
	t.Setenv("VMBOX_FACTORY_EXECUTION_ENABLED", "true")
	if _, err := configurePlanning(context.Background(), s, nil); err == nil || s.ExecutionReady {
		t.Fatal("incomplete execution enabled")
	}
}
func TestResultEndpointRestrictions(t *testing.T) {
	for _, u := range []string{"https://factory.example/result", "http://127.0.0.1:8090/result", "http://[::1]:8090/result"} {
		if err := validateResultURL(u); err != nil {
			t.Fatal(err)
		}
	}
	for _, u := range []string{"", "http://factory.example/result", "https://token@factory.example/result", "https://factory.example/result?token=secret", "https://factory.example/elsewhere", "https://factory.example/result#fragment"} {
		if validateResultURL(u) == nil {
			t.Fatal("invalid result endpoint accepted")
		}
	}
}
