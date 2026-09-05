// Package transport implements controller-resolved data transport, without provider clients.
package transport

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/0xikarus/vmbox-service/internal/procexec"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

var endpointPattern = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_.-]*@[a-zA-Z0-9][a-zA-Z0-9.-]*$`)

type SSH struct {
	Runner         procexec.Runner
	IdentityFile   string
	KnownHostsFile string
}

func (s SSH) ExecConnection(ctx context.Context, conn provider.Connection, remote []string, opts provider.ExecOptions) (provider.ExecResult, error) {
	if conn.Transport != "openssh" {
		return provider.ExecResult{}, fmt.Errorf("unsupported controller transport %q", conn.Transport)
	}
	if !endpointPattern.MatchString(conn.Endpoint) {
		return provider.ExecResult{}, fmt.Errorf("invalid SSH endpoint")
	}
	if len(remote) == 0 {
		return provider.ExecResult{}, fmt.Errorf("empty remote command")
	}
	quoted := make([]string, len(remote))
	for i, arg := range remote {
		if strings.ContainsRune(arg, 0) {
			return provider.ExecResult{}, fmt.Errorf("NUL in remote argument")
		}
		quoted[i] = "'" + strings.ReplaceAll(arg, "'", "'\"'\"'") + "'"
	}
	// No shared master, retries, key deletion, or server-supplied local options.
	args := []string{"ssh", "-F", "/dev/null", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=accept-new", "-o", "ConnectTimeout=15", "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=2", "-o", "ControlMaster=no", "-o", "ControlPath=none", "-o", "ForwardAgent=no", "-o", "ClearAllForwardings=yes"}
	if s.IdentityFile != "" {
		args = append(args, "-o", "IdentitiesOnly=yes", "-i", s.IdentityFile)
	}
	if s.KnownHostsFile != "" {
		args = append(args, "-o", "UserKnownHostsFile="+s.KnownHostsFile)
	}
	if opts.Interactive {
		args = append(args, "-tt")
	} else {
		args = append(args, "-T")
	}
	args = append(args, "--", conn.Endpoint, strings.Join(quoted, " "))
	runner := s.Runner
	if runner == nil {
		runner = procexec.OSRunner{}
	}
	var result procexec.Result
	var err error
	if attached, ok := runner.(procexec.AttachedRunner); ok && opts.Interactive {
		result, err = attached.RunAttached(ctx, args, opts.Stdin, opts.Stdout, opts.Stderr)
	} else {
		result, err = runner.Run(ctx, args, opts.Stdin, opts.Stdout, opts.Stderr)
	}
	return provider.ExecResult{Stdout: string(result.Stdout), Stderr: string(result.Stderr), ExitCode: result.ExitCode}, err
}
