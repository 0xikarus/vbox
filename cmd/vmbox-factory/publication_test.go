package main

import (
	"context"
	"github.com/0xikarus/vmbox-service/internal/factory"
	"testing"
)

func TestPublicationRequiresExplicitCompleteConfiguration(t *testing.T) {
	s := &factory.Service{ExecutionReady: true}
	t.Setenv("VMBOX_FACTORY_ISSUES_WRITE", "")
	if c, err := configurePublication(context.Background(), s); err != nil || c != nil || s.PublicationReady {
		t.Fatal("publication enabled by default")
	}
	t.Setenv("VMBOX_FACTORY_ISSUES_WRITE", "true")
	if _, err := configurePublication(context.Background(), s); err == nil || s.PublicationReady {
		t.Fatal("incomplete publication enabled")
	}
}
