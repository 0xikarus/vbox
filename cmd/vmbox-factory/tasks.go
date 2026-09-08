package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/0xikarus/vmbox-service/internal/factory"
	"github.com/0xikarus/vmbox-service/internal/taskflow"
)

// General tasks share the controller's account gateway, saved profiles and
// private attachments. GitHub configuration is deliberately not a prerequisite.
func configureTasks(ctx context.Context, db *sql.DB, source *factory.Service, runner taskflow.Runner, images map[string]bool) (*taskflow.Service, error) {
	store := &taskflow.Store{DB: db}
	if err := store.Migrate(ctx); err != nil {
		return nil, fmt.Errorf("general task storage unavailable")
	}
	s := &taskflow.Service{Store: store, GatewayToken: source.GatewayToken, Runner: runner, MaxWorkers: source.WorkerLimit, Images: images}
	if source.Profiles != nil {
		s.Profiles = func(ctx context.Context, account string) ([]taskflow.Profile, error) {
			profiles, err := source.Profiles(ctx, account)
			if err != nil {
				return nil, fmt.Errorf("saved task profiles unavailable")
			}
			out := make([]taskflow.Profile, 0, len(profiles))
			for _, p := range profiles {
				out = append(out, taskflow.Profile{Application: p.Application, Name: p.Name})
			}
			return out, nil
		}
	}
	s.ValidateAssets = func(ctx context.Context, account string, ids []string) error {
		if len(ids) > 8 {
			return fmt.Errorf("at most eight task images allowed")
		}
		seen := map[string]bool{}
		var total int64
		for _, id := range ids {
			if source.Assets == nil || id == "" || seen[id] {
				return fmt.Errorf("task image unavailable")
			}
			seen[id] = true
			f, a, err := source.Assets.Open(ctx, account, id)
			if err != nil {
				return fmt.Errorf("task image unavailable")
			}
			if err = f.Close(); err != nil {
				return fmt.Errorf("task image unavailable")
			}
			total += a.Size
			if a.Size <= 0 || a.Size > 10<<20 || total > 40<<20 {
				return fmt.Errorf("task images exceed size limit")
			}
		}
		return nil
	}
	return s, nil
}

func runTasks(ctx context.Context, s *taskflow.Service) {
	for ctx.Err() == nil {
		step, done := context.WithTimeout(ctx, 6*time.Minute)
		err := s.Step(step)
		done()
		if err != nil && ctx.Err() == nil {
			slog.Warn("task coordination step needs recovery; persisted attempt retained")
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
