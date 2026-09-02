package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/0xikarus/vmbox-service/internal/provider"
)

type ApplicationProfile struct {
	Application string `json:"application"`
	Path        string `json:"path"`
}

type GitHubCredential struct {
	Host     string `json:"host"`
	User     string `json:"user"`
	Protocol string `json:"protocol"`
}

// CreationSetup is deliberately secret-free. It remembers only explicit
// local profile identities and paths; credential material is rediscovered and
// uploaded from the laptop when the setup is used.
type CreationSetup struct {
	Save                bool                 `json:"-"`
	Version             int                  `json:"version"`
	SavedAt             time.Time            `json:"savedAt"`
	Region              string               `json:"region,omitempty"`
	Resources           provider.Resources   `json:"resources"`
	Components          []string             `json:"components,omitempty"`
	ApplicationProfiles []ApplicationProfile `json:"applicationProfiles,omitempty"`
	GitHub              *GitHubCredential    `json:"github,omitempty"`
	Instructions        []string             `json:"instructions,omitempty"`
	Workspace           string               `json:"workspace,omitempty"`
	NotificationPolicy  string               `json:"notificationPolicy,omitempty"`
	OnSuccess           string               `json:"onSuccess,omitempty"`
	OnFailure           string               `json:"onFailure,omitempty"`
	MaxTTL              string               `json:"maxTtl,omitempty"`
}

type Context struct {
	Name               string `json:"name"`
	Provider           string `json:"provider"`
	Controller         string `json:"controller,omitempty"`
	Account            string `json:"account,omitempty"`
	TokenEnv           string `json:"tokenEnv,omitempty"`
	Project            string `json:"project,omitempty"`
	Environment        string `json:"environment,omitempty"`
	RailwayCLIAuth     bool   `json:"railwayCliAuth,omitempty"`
	Cluster            string `json:"cluster,omitempty"`
	Image              string `json:"image,omitempty"`
	DockerContext      string `json:"dockerContext,omitempty"`
	DockerHost         string `json:"dockerHost,omitempty"`
	DockerTLSVerify    bool   `json:"dockerTLSVerify,omitempty"`
	DockerCertPath     string `json:"dockerCertPath,omitempty"`
	IncusRemote        string `json:"incusRemote,omitempty"`
	IncusProject       string `json:"incusProject,omitempty"`
	IncusVM            bool   `json:"incusVm,omitempty"`
	ProviderCredential string `json:"providerCredential,omitempty"`
}
type File struct {
	Current      string                   `json:"current"`
	Contexts     map[string]Context       `json:"contexts"`
	LastSetups   map[string]CreationSetup `json:"lastSetups,omitempty"`
	MigratedFrom string                   `json:"-"`
}

func DefaultPath() string {
	if value := os.Getenv("VMBOX_CONFIG"); value != "" {
		return value
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "vmbox", "config.json")
}
func Load(path string) (File, error) {
	if path == "" {
		path = DefaultPath()
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if migrated, ok, migrateErr := migrateLegacyRailway(filepath.Join(filepath.Dir(path), "config")); migrateErr != nil {
			return File{}, migrateErr
		} else if ok {
			if saveErr := Save(path, migrated); saveErr != nil {
				return File{}, fmt.Errorf("save migrated Railway setup: %w", saveErr)
			}
			migrated.MigratedFrom = filepath.Join(filepath.Dir(path), "config")
			return migrated, nil
		}
		return File{Contexts: make(map[string]Context)}, nil
	}
	if err != nil {
		return File{}, err
	}
	var file File
	if err := json.Unmarshal(data, &file); err != nil {
		return file, err
	}
	if file.Contexts == nil {
		file.Contexts = make(map[string]Context)
	}
	if file.LastSetups == nil {
		file.LastSetups = make(map[string]CreationSetup)
	}
	return file, nil
}

// migrateLegacyRailway reads only the non-secret target identifiers understood
// by the old shell CLI. It deliberately does not source the file or import any
// credential value.
func migrateLegacyRailway(path string) (File, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return File{}, false, nil
	}
	if err != nil {
		return File{}, false, fmt.Errorf("read legacy vmbox config: %w", err)
	}
	values := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := legacyAssignment(line)
		if !ok {
			continue
		}
		switch key {
		case "VMBOX_PROJECT_ID", "VMBOX_ENVIRONMENT_ID", "VMBOX_DEFAULT_REGION":
			if safeLegacyIdentifier(value) {
				values[key] = value
			}
		}
	}
	project, environment := values["VMBOX_PROJECT_ID"], values["VMBOX_ENVIRONMENT_ID"]
	if project == "" || environment == "" {
		return File{}, false, nil
	}
	legacyRegion := values["VMBOX_DEFAULT_REGION"]
	if legacyRegion == "" {
		legacyRegion = "us-east"
	}
	region := map[string]string{
		"eu-west": "ams", "us-west": "sfo", "us-east": "iad", "southeast-asia": "sin",
	}[legacyRegion]
	if region == "" {
		region = legacyRegion
	}
	return File{
		Current: "railway",
		Contexts: map[string]Context{"railway": {
			Name: "railway", Provider: "railway", Project: project, Environment: environment,
			RailwayCLIAuth: true, Cluster: region,
		}},
		LastSetups: make(map[string]CreationSetup),
	}, true, nil
}

func legacyAssignment(line string) (string, string, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}
	line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
	key, raw, ok := strings.Cut(line, "=")
	if !ok {
		return "", "", false
	}
	key, raw = strings.TrimSpace(key), strings.TrimSpace(raw)
	if len(raw) >= 2 && raw[0] == '\'' && raw[len(raw)-1] == '\'' {
		if strings.Contains(raw[1:len(raw)-1], "'") {
			return "", "", false
		}
		return key, raw[1 : len(raw)-1], true
	}
	if strings.HasPrefix(raw, `"`) {
		value, err := strconv.Unquote(raw)
		return key, value, err == nil
	}
	value, _, _ := strings.Cut(raw, "#")
	return key, strings.TrimSpace(value), true
}

func safeLegacyIdentifier(value string) bool {
	if value == "" || len(value) > 200 {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '-' || character == '_' || character == '.' {
			continue
		}
		return false
	}
	return true
}
func Save(path string, file File) error {
	if path == "" {
		path = DefaultPath()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.json")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
func (f File) Active(name string) (Context, error) {
	if name == "" {
		name = f.Current
	}
	if name == "" {
		return Context{}, fmt.Errorf("no active context; use: vmbox context add NAME --provider PROVIDER")
	}
	ctx, ok := f.Contexts[name]
	if !ok {
		return ctx, fmt.Errorf("context %q does not exist", name)
	}
	ctx.Name = name
	return ctx, nil
}
