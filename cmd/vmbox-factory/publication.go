package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/0xikarus/vmbox-service/internal/factory"
	"github.com/0xikarus/vmbox-service/internal/factory/githubapp"
	"github.com/0xikarus/vmbox-service/internal/factory/publishing"
)

func configurePublication(ctx context.Context, service *factory.Service) (*publishing.Coordinator, error) {
	if os.Getenv("VMBOX_FACTORY_ISSUES_WRITE") != "true" {
		return nil, nil
	}
	client, ok := service.Repositories.(*githubapp.Client)
	account := os.Getenv("VMBOX_FACTORY_ACCOUNT_ID")
	if !ok || client == nil || service.Store == nil || service.Store.DB == nil || account == "" || !service.ExecutionReady {
		return nil, fmt.Errorf("issue publication requires configured planning, GitHub App, database and operator account")
	}
	c := publishing.New(service.Store.DB, client, publishing.Policy{AccountID: account, IssuesWrite: true})
	if err := c.Migrate(ctx); err != nil {
		return nil, fmt.Errorf("publication database migration failed")
	}
	service.PublicationReady = true
	return c, nil
}

func runPublication(ctx context.Context, c *publishing.Coordinator) {
	for ctx.Err() == nil {
		step, done := context.WithTimeout(ctx, 90*time.Second)
		err := c.Step(step)
		done()
		if err != nil && !errors.Is(err, sql.ErrNoRows) && ctx.Err() == nil {
			slog.Warn("publication deferred; durable work item retains outcome")
		}
		timer := time.NewTimer(2 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
