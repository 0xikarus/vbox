// Package instance contains the worker lifecycle shared by standalone CLI and hub.
package instance

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/0xikarus/vmbox-service/internal/procexec"
	"github.com/0xikarus/vmbox-service/internal/provider"
	dockerprovider "github.com/0xikarus/vmbox-service/internal/provider/docker"
	"github.com/0xikarus/vmbox-service/internal/standalone"
)

type Manager struct {
	*dockerprovider.Provider
	runner procexec.Runner
	host   string
}

func New(cfg dockerprovider.Config, runner procexec.Runner) *Manager {
	if runner == nil {
		runner = procexec.OSRunner{}
	}
	return &Manager{dockerprovider.New(cfg, runner), runner, cfg.Host}
}
func (m *Manager) Provision(ctx context.Context, req provider.CreateRequest, archives ...[]byte) (provider.Box, error) {
	b, err := m.Create(ctx, req)
	if err != nil {
		return b, err
	}
	if b.State == provider.StateRunning {
		return b, nil
	}
	for _, data := range archives {
		if len(data) > 0 {
			if err = m.Import(ctx, req.Name, data); err != nil {
				return b, err
			}
		}
	}
	return m.Start(ctx, req.Name)
}
func (m *Manager) Import(ctx context.Context, id string, data []byte) error {
	argv := []string{"docker"}
	if m.host != "" {
		argv = append(argv, "--host", m.host)
	}
	argv = append(argv, "cp", "-a", "-", "vmbox-"+id+":/data/home")
	r, err := m.runner.Run(ctx, argv, bytes.NewReader(data), nil, nil)
	if err != nil || r.ExitCode != 0 {
		return fmt.Errorf("could not copy private setup to stopped instance")
	}
	return nil
}
func PasswordArchive(password string) ([]byte, error) {
	h, err := standalone.HashPassword(password)
	if err != nil {
		return nil, err
	}
	data, _ := json.Marshal(h)
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, name := range []string{".config/", ".config/vmbox/"} {
		if err = tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeDir, Mode: 0700, Uid: 10001, Gid: 10001}); err != nil {
			return nil, err
		}
	}
	if err = tw.WriteHeader(&tar.Header{Name: ".config/vmbox/web-password.json", Mode: 0600, Size: int64(len(data)), Uid: 10001, Gid: 10001}); err == nil {
		_, err = tw.Write(data)
	}
	if err == nil {
		err = tw.Close()
	}
	return buf.Bytes(), err
}
