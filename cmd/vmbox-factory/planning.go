package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/0xikarus/vmbox-service/internal/factory"
	"github.com/0xikarus/vmbox-service/internal/factory/remoteplan"
	"github.com/0xikarus/vmbox-service/internal/factory/resultinbox"
	"github.com/0xikarus/vmbox-service/internal/factory/staging"
	"github.com/0xikarus/vmbox-service/internal/transport"
)

func configurePlanning(ctx context.Context, s *factory.Service, inbox *resultinbox.Inbox) (*factory.Dispatcher, error) {
	if os.Getenv("VMBOX_FACTORY_EXECUTION_ENABLED") != "true" {
		return nil, nil
	}
	repositories, ok := s.Repositories.(remoteplan.Repositories)
	if !ok || inbox == nil || s.Profiles == nil {
		return nil, fmt.Errorf("planning requires configured GitHub App, controller profile binding and result inbox")
	}
	callback := os.Getenv("VMBOX_FACTORY_RESULT_URL")
	if err := validateResultURL(callback); err != nil {
		return nil, err
	}
	binary := os.Getenv("VMBOX_FACTORY_PLANNER_BINARY")
	identity := os.Getenv("VMBOX_FACTORY_SSH_IDENTITY")
	knownHosts := os.Getenv("VMBOX_FACTORY_SSH_KNOWN_HOSTS")
	for _, p := range []string{binary, identity, knownHosts} {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p {
			return nil, fmt.Errorf("configure absolute planner binary, SSH identity and dedicated known-hosts paths")
		}
	}
	for _, p := range []string{binary, identity} {
		info, err := os.Lstat(p)
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			return nil, fmt.Errorf("planner binary or SSH identity unavailable")
		}
		if p == identity && info.Mode().Perm()&0077 != 0 {
			return nil, fmt.Errorf("factory SSH identity must be private")
		}
	}
	controller := &factory.ControllerClient{URL: os.Getenv("VMBOX_FACTORY_CONTROLLER_URL"), Token: os.Getenv("VMBOX_FACTORY_CONTROLLER_TOKEN"), AccountID: os.Getenv("VMBOX_FACTORY_ACCOUNT_ID")}
	if err := controller.Authorize(ctx, controller.AccountID); err != nil {
		return nil, fmt.Errorf("planning controller binding unavailable")
	}
	stager := staging.Stager{SSH: transport.SSH{IdentityFile: identity, KnownHostsFile: knownHosts}, BinaryPath: binary}
	runner := &remoteplan.Runner{Controller: controller, Repositories: repositories, Inbox: inbox, Assets: s.Assets, CallbackURL: callback, Stage: func(ctx context.Context, input remoteplan.Input) error {
		images := make([]staging.Image, len(input.Images))
		for i, image := range input.Images {
			images[i] = staging.Image{ID: image.ID, Data: image.Data}
		}
		return stager.Stage(ctx, staging.Input{Connection: input.Connection, AttemptID: input.AttemptID, Repository: input.Repository, BaseSHA: input.BaseSHA, SourceToken: input.SourceToken, Job: input.Job, Images: images})
	}}
	s.ExecutionReady = true
	s.Images = map[string]bool{"codex": true, "claude": false}
	return &factory.Dispatcher{Store: s.Store, Runner: runner, MaxWorkers: s.WorkerLimit}, nil
}

func validateResultURL(value string) error {
	u, err := url.Parse(value)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "/result" || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"))) {
		return fmt.Errorf("factory result URL must be an HTTPS /result endpoint reachable from workers (literal loopback only for isolated tests)")
	}
	return nil
}
