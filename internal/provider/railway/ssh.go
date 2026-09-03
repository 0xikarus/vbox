package railway

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/0xikarus/vmbox-service/internal/procexec"
)

const railwaySSHHost = "ssh.railway.com"

func (p *Provider) deploymentTarget(ctx context.Context, service service) (string, error) {
	p.sshMu.Lock()
	defer p.sshMu.Unlock()
	if instance := p.deploymentByService[service.ID]; instance != "" {
		return instance + "@" + railwaySSHHost, nil
	}
	instance, err := p.serviceInstanceID(ctx, service.ID)
	if err != nil {
		return "", err
	}
	p.deploymentByService[service.ID] = instance
	return instance + "@" + railwaySSHHost, nil
}

func (p *Provider) invalidateSSHForServiceKey(key string) {
	p.cacheMu.RLock()
	service, ok := p.servicesByKey[key]
	p.cacheMu.RUnlock()
	if ok {
		p.invalidateServiceSSH(service)
	}
}

func (p *Provider) invalidateServiceSSH(service service) {
	p.sshMu.Lock()
	defer p.sshMu.Unlock()
	instance := p.deploymentByService[service.ID]
	delete(p.deploymentByService, service.ID)
	if instance != "" {
		delete(p.masterByTarget, instance+"@"+railwaySSHHost)
	}
}

func (p *Provider) controlPath(target string) (string, error) {
	if p.cfg.SSHControlDir == "" {
		return "", nil
	}
	instance, host, ok := strings.Cut(target, "@")
	if !ok || instance == "" || host != railwaySSHHost {
		return "", fmt.Errorf("invalid Railway SSH target %q", target)
	}
	for _, character := range instance {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '-' || character == '_' {
			continue
		}
		return "", fmt.Errorf("invalid Railway deployment instance %q", instance)
	}
	path := filepath.Join(p.cfg.SSHControlDir, instance+".sock")
	if len(path) > 100 {
		digest := sha256.Sum256([]byte(target))
		path = filepath.Join(p.cfg.SSHControlDir, fmt.Sprintf("%x.sock", digest[:16]))
	}
	return path, nil
}

func (p *Provider) sshOptions(controlPath string) []string {
	args := []string{p.cfg.SSHBinary,
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "ConnectTimeout=15",
		"-o", "ServerAliveInterval=30",
		"-o", "ServerAliveCountMax=2",
	}
	if p.cfg.SSHKnownHostsFile != "" {
		args = append(args, "-o", "UserKnownHostsFile="+p.cfg.SSHKnownHostsFile)
	}
	if controlPath != "" {
		args = append(args, "-o", "ControlPath="+controlPath)
	}
	return args
}

func (p *Provider) ensureSSHMaster(ctx context.Context, target string) error {
	controlPath, err := p.controlPath(target)
	if err != nil || controlPath == "" {
		return err
	}
	p.sshMu.Lock()
	defer p.sshMu.Unlock()
	if p.masterByTarget[target] {
		return nil
	}
	if err := os.MkdirAll(p.cfg.SSHControlDir, 0700); err != nil {
		return fmt.Errorf("create Railway SSH control directory: %w", err)
	}
	check := append(p.sshOptions(controlPath), "-O", "check", "--", target)
	checked, checkErr := p.runSSHHandshake(ctx, check)
	if checkErr == nil && checked.ExitCode == 0 {
		p.masterByTarget[target] = true
		return nil
	}
	if err := removeStaleControlSocket(controlPath); err != nil {
		return err
	}
	start := append(p.sshOptions(controlPath),
		"-M", "-N", "-f",
		"-o", "ControlMaster=yes",
		"-o", "ControlPersist=120",
		"--", target,
	)
	started, startErr := p.runSSHHandshake(ctx, start)
	if startErr != nil {
		return fmt.Errorf("start Railway SSH control connection: %w", startErr)
	}
	if started.ExitCode != 0 {
		return fmt.Errorf("start Railway SSH control connection exited with status %d: %s", started.ExitCode, strings.TrimSpace(string(started.Stderr)))
	}
	p.masterByTarget[target] = true
	return nil
}

func removeStaleControlSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect stale SSH control socket: %w", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("refusing to remove non-socket SSH control path %s", path)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove stale SSH control socket: %w", err)
	}
	return nil
}

func (p *Provider) runSSHHandshake(ctx context.Context, argv []string) (procexec.Result, error) {
	result, err := p.runner.Run(ctx, argv, nil, nil, nil)
	if p.cfg.SSHKnownHostsFile == "" || !hostKeyChanged(result) {
		return result, err
	}
	removed, removeErr := p.runner.Run(ctx, []string{"ssh-keygen", "-f", p.cfg.SSHKnownHostsFile, "-R", railwaySSHHost}, nil, nil, nil)
	if removeErr != nil {
		return result, fmt.Errorf("repair Railway SSH host key: %w", removeErr)
	}
	if removed.ExitCode != 0 {
		return result, fmt.Errorf("repair Railway SSH host key exited with status %d", removed.ExitCode)
	}
	return p.runner.Run(ctx, argv, nil, nil, nil)
}

func hostKeyChanged(result procexec.Result) bool {
	return strings.Contains(strings.ToUpper(string(result.Stderr)), "REMOTE HOST IDENTIFICATION HAS CHANGED")
}

func shellCommand(argv []string) (string, error) {
	if len(argv) == 0 {
		return "", fmt.Errorf("remote command argv cannot be empty")
	}
	quoted := make([]string, len(argv))
	for index, value := range argv {
		if strings.IndexByte(value, 0) >= 0 {
			return "", fmt.Errorf("remote command argument %d contains NUL", index)
		}
		quoted[index] = "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
	}
	return strings.Join(quoted, " "), nil
}

func (p *Provider) directSSH(ctx context.Context, service service, remote []string, interactive bool, stdin io.Reader, stdout, stderr io.Writer) (procexec.Result, error) {
	target, err := p.deploymentTarget(ctx, service)
	if err != nil {
		return procexec.Result{}, err
	}
	if err := p.ensureSSHMaster(ctx, target); err != nil {
		return procexec.Result{}, err
	}
	controlPath, err := p.controlPath(target)
	if err != nil {
		return procexec.Result{}, err
	}
	command, err := shellCommand(remote)
	if err != nil {
		return procexec.Result{}, err
	}
	args := p.sshOptions(controlPath)
	if interactive {
		args = append(args, "-tt")
	} else {
		args = append(args, "-T")
	}
	args = append(args, "-o", "ControlMaster=no", "--", target, command)
	var result procexec.Result
	if interactive {
		if attached, ok := p.runner.(procexec.AttachedRunner); ok {
			result, err = attached.RunAttached(ctx, args, stdin, stdout, stderr)
		} else {
			result, err = p.runner.Run(ctx, args, stdin, stdout, stderr)
		}
	} else {
		result, err = p.runner.Run(ctx, args, stdin, stdout, stderr)
	}
	if result.ExitCode == 255 {
		p.sshMu.Lock()
		delete(p.masterByTarget, target)
		p.sshMu.Unlock()
	}
	return result, err
}
