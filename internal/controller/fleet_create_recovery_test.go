package controller

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

func TestLogicalBoxCreationRunsOncePerBox(t *testing.T) {
	server := NewServer(nil, nil)
	creation := logicalBoxCreation{AccountID: "account-a", Assignment: fleetAssignment{Box: v1.LogicalBox{ID: "box-1"}}}
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	var calls atomic.Int32
	go func() {
		done <- server.runLogicalBoxCreationOnce(creation, func() error {
			calls.Add(1)
			close(started)
			<-release
			return nil
		})
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first creation did not start")
	}
	if err := server.runLogicalBoxCreationOnce(creation, func() error {
		calls.Add(1)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("overlapping creation ran %d provider workflows", got)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestLogicalBoxCreationRecoveryDoesNotStarveSiblings(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	siblingStarted := make(chan struct{})
	creations := []logicalBoxCreation{
		{Request: v1.CreateLogicalBoxRequest{Name: "slow"}},
		{Request: v1.CreateLogicalBoxRequest{Name: "sibling"}},
	}
	errors := finishLogicalBoxCreationRecoveries(ctx, creations, func(ctx context.Context, creation logicalBoxCreation) error {
		if creation.Request.Name == "sibling" {
			close(siblingStarted)
			return nil
		}
		select {
		case <-siblingStarted:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	for _, err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
}
