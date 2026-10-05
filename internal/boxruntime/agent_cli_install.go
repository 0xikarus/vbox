package boxruntime

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var agentCLIVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?$`)

func ValidAgentCLIVersion(version string) bool {
	return len(version) <= 64 && agentCLIVersionPattern.MatchString(version)
}

func AgentCLIPackage(agent string) (string, error) {
	switch agent {
	case "claude":
		return "@anthropic-ai/claude-code", nil
	case "codex":
		return "@openai/codex", nil
	case "opencode":
		return "opencode-ai", nil
	default:
		return "", fmt.Errorf("unsupported agent %q", agent)
	}
}

// InstallAgentCLI places the selected CLI on the retained box volume. The
// workload PATH prefers home/.local/bin over the image's bundled CLI, so the
// selected version survives hibernation and compute replacement.
func InstallAgentCLI(ctx context.Context, home, agent, version string, output io.Writer) error {
	if !filepath.IsAbs(home) || home == "/" {
		return fmt.Errorf("agent CLI installation requires a persistent absolute home")
	}
	if !ValidAgentCLIVersion(version) {
		return fmt.Errorf("agent CLI version must be an exact release version")
	}
	packageName, err := AgentCLIPackage(agent)
	if err != nil {
		return err
	}
	if err := requireInstallDiskSpace(home); err != nil {
		return err
	}
	prefix := filepath.Join(home, ".local")
	if err := os.MkdirAll(prefix, 0o700); err != nil {
		return err
	}
	command := exec.CommandContext(ctx, "npm", "install", "--global", "--no-audit", "--no-fund", "--prefix", prefix, packageName+"@"+version)
	command.Dir = home
	command.Env = append(os.Environ(), "HOME="+home)
	command.Stdout, command.Stderr = output, output
	if err := command.Run(); err != nil {
		return fmt.Errorf("install %s %s: %w", agent, version, err)
	}
	installed := exec.CommandContext(ctx, filepath.Join(prefix, "bin", agent), "--version")
	installed.Dir = home
	installed.Env = append(os.Environ(), "HOME="+home)
	actual, err := installed.CombinedOutput()
	if err != nil || !strings.Contains(string(actual), version) {
		return fmt.Errorf("installed %s did not report requested version %s: %s", agent, version, strings.TrimSpace(string(actual)))
	}
	fmt.Fprintf(output, "%s %s installed in persistent box home\n", agent, version)
	return nil
}
