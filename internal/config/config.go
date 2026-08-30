package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type Context struct {
	Name            string `json:"name"`
	Provider        string `json:"provider"`
	Controller      string `json:"controller,omitempty"`
	Account         string `json:"account,omitempty"`
	TokenEnv        string `json:"tokenEnv,omitempty"`
	Project         string `json:"project,omitempty"`
	Environment     string `json:"environment,omitempty"`
	Company         string `json:"company,omitempty"`
	Cluster         string `json:"cluster,omitempty"`
	ResourceType    string `json:"resourceType,omitempty"`
	Image           string `json:"image,omitempty"`
	DockerContext   string `json:"dockerContext,omitempty"`
	DockerHost      string `json:"dockerHost,omitempty"`
	DockerTLSVerify bool   `json:"dockerTLSVerify,omitempty"`
	DockerCertPath  string `json:"dockerCertPath,omitempty"`
	IncusRemote     string `json:"incusRemote,omitempty"`
	IncusProject    string `json:"incusProject,omitempty"`
	IncusVM         bool   `json:"incusVm,omitempty"`
	PreAttachedDisk string `json:"preAttachedDisk,omitempty"`
}
type File struct {
	Current  string             `json:"current"`
	Contexts map[string]Context `json:"contexts"`
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
	return file, nil
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
