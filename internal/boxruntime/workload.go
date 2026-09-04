package boxruntime

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/0xikarus/vmbox-service/internal/provider"
)

// Railway's SSH data path connects as root, so anything the CLI installs into a
// box is created by root unless the runtime hands it back explicitly. Files that
// root owns are invisible to the agents, which run as the unprivileged workload
// user, and credential verification performed by root answers for root's own
// login instead of the workload user's.
const (
	// PrivateDirectoryMode is used for every directory the runtime creates for
	// profiles, credentials, and instructions.
	PrivateDirectoryMode os.FileMode = 0o700
	// CredentialMode is the only mode a synced credential may carry.
	CredentialMode os.FileMode = 0o600
)

// Ownership is the numeric identity that must own every remotely installed file.
type Ownership struct {
	UID int
	GID int
}

// Chowner applies ownership to one path. os.Chown satisfies it; tests substitute
// a recorder because unprivileged test processes cannot give files away.
type Chowner func(path string, uid, gid int) error

// LookupWorkloadOwnership resolves vmbox:vmbox for a process running with the
// given effective UID. It returns nil ownership for an unprivileged runtime,
// which already writes files it owns itself, and an error only when a root
// runtime cannot resolve the account it must hand its work to.
func LookupWorkloadOwnership(euid int, lookup func(string) (*user.User, error)) (*Ownership, error) {
	if euid != 0 {
		return nil, nil
	}
	if lookup == nil {
		lookup = user.Lookup
	}
	account, err := lookup(provider.WorkloadUser)
	if err != nil {
		return nil, fmt.Errorf("resolve workload user %s: %w", provider.WorkloadUser, err)
	}
	uid, err := strconv.Atoi(account.Uid)
	if err != nil {
		return nil, fmt.Errorf("resolve workload user %s UID: %w", provider.WorkloadUser, err)
	}
	gid, err := strconv.Atoi(account.Gid)
	if err != nil {
		return nil, fmt.Errorf("resolve workload user %s GID: %w", provider.WorkloadUser, err)
	}
	return &Ownership{UID: uid, GID: gid}, nil
}

// Apply hands one existing path to the workload user. A nil ownership is the
// unprivileged case and does nothing.
func (o *Ownership) Apply(path string, chown Chowner) error {
	if o == nil {
		return nil
	}
	if chown == nil {
		chown = os.Chown
	}
	if err := chown(path, o.UID, o.GID); err != nil {
		return fmt.Errorf("hand %s to the workload user: %w", path, err)
	}
	return nil
}

// EnsurePrivateDirectory creates every missing directory between root and dir.
// Only the directories it creates are touched: an existing directory keeps its
// mode and owner so that shared paths such as /data/workspace are never
// narrowed behind the user's back.
func EnsurePrivateDirectory(root, dir string, owner *Ownership, chown Chowner) error {
	missing, err := missingDirectories(root, dir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, PrivateDirectoryMode); err != nil {
		return err
	}
	for _, created := range missing {
		if err := os.Chmod(created, PrivateDirectoryMode); err != nil {
			return fmt.Errorf("secure %s: %w", created, err)
		}
		if err := owner.Apply(created, chown); err != nil {
			return err
		}
	}
	return nil
}

// missingDirectories lists the directories between root and dir, outermost
// first, that do not exist yet.
func missingDirectories(root, dir string) ([]string, error) {
	root = filepath.Clean(root)
	dir = filepath.Clean(dir)
	relative, err := filepath.Rel(root, dir)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return nil, fmt.Errorf("directory %s must be below %s", dir, root)
	}
	var missing []string
	for current := dir; current != root && current != "/" && current != "."; current = filepath.Dir(current) {
		if _, err := os.Lstat(current); err == nil {
			break
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		missing = append([]string{current}, missing...)
	}
	return missing, nil
}

// WorkloadArgv rewrites a command so that it always runs as the unprivileged
// workload user with HOME=/data/home.
//
// This is the single guard against a false-positive authentication check: a
// root process reading its own HOME reports "logged in" from credentials the
// agents can never see, and `gh auth login` run by root writes a hosts.yml the
// agents can never read.
func WorkloadArgv(euid int, name string, args []string) (string, []string) {
	if euid == 0 {
		full := provider.AsWorkloadUser(append([]string{name}, args...))
		return full[0], full[1:]
	}
	full := append([]string{"env", "HOME=" + provider.WorkloadHome}, append([]string{name}, args...)...)
	return full[0], full[1:]
}

// RunsAsWorkloadUser reports whether argv was produced by WorkloadArgv, so that
// tests and callers can assert a command never reaches the box as root.
func RunsAsWorkloadUser(argv []string) bool {
	home := "HOME=" + provider.WorkloadHome
	switch {
	case len(argv) == 0:
		return false
	case argv[0] == "env":
		return len(argv) > 1 && argv[1] == home
	case argv[0] == "sudo":
		user := false
		for index := 1; index < len(argv); index++ {
			if argv[index] == "-u" && index+1 < len(argv) && argv[index+1] == provider.WorkloadUser {
				user = true
			}
			if argv[index] == home {
				return user
			}
		}
	}
	return false
}

// CheckSyncedFileMode rejects a mode that would leave a file installed below the
// workload home readable or writable by anyone but the workload user. Agent
// profiles and GitHub configuration are credentials; nothing else may see them.
func CheckSyncedFileMode(destination string, mode os.FileMode) error {
	if mode&0o222 != mode&0o200 {
		return fmt.Errorf("synced file %s must not be group- or world-writable", destination)
	}
	if !withinWorkloadHome(destination) {
		return nil
	}
	if mode.Perm()&^CredentialMode != 0 {
		return fmt.Errorf("synced credential %s must use mode %04o or stricter", destination, CredentialMode)
	}
	return nil
}

func withinWorkloadHome(path string) bool {
	path = filepath.Clean(path)
	home := filepath.Clean(provider.WorkloadHome)
	return path == home || strings.HasPrefix(path, home+string(os.PathSeparator))
}

// SecureGitHubConfigScript narrows the GitHub CLI configuration written by
// `gh auth login` to the workload user only. It runs as vmbox, so it can only
// ever tighten files that user already owns.
const SecureGitHubConfigScript = `set -eu
config="${HOME}/.config/gh"
[ -d "$HOME" ] && chmod 0700 "$HOME"
[ -d "$config" ] || exit 0
chmod 0700 "${HOME}/.config" "$config"
[ -f "$config/hosts.yml" ] && chmod 0600 "$config/hosts.yml"
[ -f "${HOME}/.gitconfig" ] && chmod 0600 "${HOME}/.gitconfig"
exit 0`

// GitIdentityArguments returns the `git config --global` invocations that record
// the commit identity belonging to a synced GitHub account. An account without a
// resolved identity contributes nothing rather than writing a placeholder.
func GitIdentityArguments(github *GitHubSetup) [][]string {
	if github == nil {
		return nil
	}
	var arguments [][]string
	if name := strings.TrimSpace(github.Name); name != "" {
		arguments = append(arguments, []string{"config", "--global", "user.name", name})
	}
	if email := strings.TrimSpace(github.Email); email != "" {
		arguments = append(arguments, []string{"config", "--global", "user.email", email})
	}
	return arguments
}
