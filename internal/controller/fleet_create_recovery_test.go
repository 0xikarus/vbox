package controller

import (
	"context"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

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
