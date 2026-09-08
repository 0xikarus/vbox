package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/0xikarus/vmbox-service/internal/factory"
	"github.com/0xikarus/vmbox-service/internal/factory/resultinbox"
	"github.com/0xikarus/vmbox-service/internal/taskflow"
	"github.com/0xikarus/vmbox-service/internal/taskflowruntime"
	"github.com/0xikarus/vmbox-service/internal/transport"
)

func configureTaskRuntime(ctx context.Context, s *factory.Service, inbox *resultinbox.Inbox) (*taskflowruntime.Runner, error) {
	if os.Getenv("VMBOX_TASKS_EXECUTION_ENABLED") != "true" {
		return nil, nil
	}
	if inbox == nil || s.Profiles == nil {
		return nil, fmt.Errorf("tasks require controller profiles and a result inbox")
	}
	binary := os.Getenv("VMBOX_TASKS_RUNNER_BINARY")
	identity := os.Getenv("VMBOX_FACTORY_SSH_IDENTITY")
	knownHosts := os.Getenv("VMBOX_FACTORY_SSH_KNOWN_HOSTS")
	for _, p := range []string{binary, identity, knownHosts} {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p {
			return nil, fmt.Errorf("tasks require absolute runner binary, SSH identity and known-hosts paths")
		}
	}
	for _, p := range []string{binary, identity} {
		info, err := os.Lstat(p)
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			return nil, fmt.Errorf("task runner binary or SSH identity unavailable")
		}
		if p == identity && info.Mode().Perm()&0077 != 0 {
			return nil, fmt.Errorf("task SSH identity must be private")
		}
	}
	controller := &factory.ControllerClient{URL: os.Getenv("VMBOX_FACTORY_CONTROLLER_URL"), Token: os.Getenv("VMBOX_FACTORY_CONTROLLER_TOKEN"), AccountID: os.Getenv("VMBOX_FACTORY_ACCOUNT_ID")}
	if err := controller.Authorize(ctx, controller.AccountID); err != nil {
		return nil, fmt.Errorf("task controller binding unavailable")
	}
	return taskflowruntime.New(taskflowruntime.Config{Controller: controller, Inbox: inbox, Assets: s.Assets, SSH: transport.SSH{IdentityFile: identity, KnownHostsFile: knownHosts}, BinaryPath: binary, CallbackURL: os.Getenv("VMBOX_FACTORY_RESULT_URL")})
}

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
