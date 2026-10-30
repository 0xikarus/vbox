package standalone

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// Serve runs one local controller against the shared native box runtime.
func Serve(ctx context.Context, dir, listen string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	c, err := LoadConfig(dir)
	if err != nil {
		return err
	}
	c.PasswordRequired = true
	c.Name = os.Getenv("VMBOX_BOX_NAME")
	c.BasePath = os.Getenv("VMBOX_WEB_BASE_PATH")
	if c.BasePath != "" && !regexp.MustCompile(`^/boxes/hb-[0-9a-f]{24}$`).MatchString(c.BasePath) {
		return fmt.Errorf("invalid standalone base path")
	}
	c.HubManaged = os.Getenv("VMBOX_HUB_MANAGED") == "true"
	store, err := OpenStore(filepath.Join(dir, "chat.sqlite"))
	if err != nil {
		return err
	}
	defer store.Close()
	listener, err := net.Listen("tcp", listen)
	if err != nil {
		return err
	}
	defer listener.Close()
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if err = WriteAgentConfig(home, "http://127.0.0.1:"+port, c.Assignment, c.AgentToken); err != nil {
		return err
	}
	if endpoint := os.Getenv("VMBOX_MODEL_URL"); endpoint != "" {
		if err := saveThinkingCapabilities(home, discoverThinking(ctx, endpoint, os.Getenv("VMBOX_MODEL"))); err != nil {
			return err
		}
		if err := writeLocalModelConfig(home, endpoint, os.Getenv("VMBOX_MODEL"), os.Getenv("VMBOX_MODEL_VISION") == "true"); err != nil {
			return err
		}
	}
	agent := os.Getenv("VMBOX_AGENT")
	if agent == "" {
		agent = "opencode"
	}
	if agent != "opencode" && agent != "claude" && agent != "codex" {
		return fmt.Errorf("standalone agent must be opencode, claude or codex")
	}
	if err := configureGitHub(ctx, home); err != nil {
		return err
	}
	if err := applyAgentModel(home, agent, os.Getenv("VMBOX_AGENT_MODEL"), os.Getenv("VMBOX_AGENT_REASONING_EFFORT")); err != nil {
		return err
	}
	if err := installSelectedTools(ctx, home); err != nil {
		return err
	}
	if err := cleanupChromiumLocks(home, "/proc"); err != nil {
		return err
	}
	if err := writeSessionLauncher(home); err != nil {
		return err
	}
	runtime := &BoxRuntime{Assignment: c.Assignment, Home: home, Session: agent + "-standalone", Agent: agent}
	service := NewServer(store, c, runtime)
	service.Shutdown = cancel
	server := &http.Server{Handler: service.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
	runtimeDone := make(chan struct{})
	go func() { defer close(runtimeDone); service.Run(ctx) }()
	go func() {
		<-ctx.Done()
		shutdown, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		// A blocked native capture must not keep the instance alive forever.
		select {
		case <-runtimeDone:
		case <-shutdown.Done():
		}
		_ = server.Shutdown(shutdown)
		_ = server.Close()
	}()
	fmt.Fprintln(os.Stderr, "standalone gateway listening on", listener.Addr())
	err = server.Serve(listener)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}
