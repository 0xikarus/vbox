// Package hub manages resource-bounded standalone workers on one Docker host.
package hub

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"github.com/0xikarus/vmbox-service/internal/standalone"
)

type Policy struct {
	CPUs             float64 `json:"cpus"`
	MemoryMiB        int64   `json:"memoryMiB"`
	ReserveCPUs      float64 `json:"reserveCPUs"`
	ReserveMemoryMiB int64   `json:"reserveMemoryMiB"`
	ModelMemoryMiB   int64   `json:"modelMemoryMiB"`
	MinDiskFreeMiB   int64   `json:"minDiskFreeMiB"`
	MaxRunning       int     `json:"maxRunning"`
}
type Config struct {
	ID       string                  `json:"id"`
	Listen   string                  `json:"listen"`
	Image    string                  `json:"image"`
	Password standalone.PasswordHash `json:"password"`
	APIToken string                  `json:"apiToken"`
	Policy   Policy                  `json:"policy"`
	Defaults Spec                    `json:"defaults"`
}
type Spec struct {
	Name                 string   `json:"name"`
	Agent                string   `json:"agent"`
	CPU                  float64  `json:"cpu"`
	MemoryMiB            int64    `json:"memoryMiB"`
	ModelSource          string   `json:"modelSource,omitempty"`
	ModelURL             string   `json:"modelURL,omitempty"`
	Model                string   `json:"model,omitempty"`
	AgentModel           string   `json:"agentModel,omitempty"`
	AgentReasoningEffort string   `json:"agentReasoningEffort,omitempty"`
	Vision               *bool    `json:"vision,omitempty"`
	Tools                []string `json:"tools,omitempty"`
	Profiles             []string `json:"profiles,omitempty"`
	Goal                 string   `json:"goal,omitempty"`
}
type Box struct {
	Usage    *Usage `json:"usage,omitempty"`
	ID       string `json:"id"`
	Spec     Spec   `json:"spec"`
	State    string `json:"state"`
	Desired  string `json:"desired"`
	Reason   string `json:"reason,omitempty"`
	Created  string `json:"created"`
	Updated  string `json:"updated"`
	Port     int    `json:"-"`
	Password string `json:"-"`
	Attempts int    `json:"attempts"`
	GoalSent bool   `json:"goalSent"`
	Booted   string `json:"booted,omitempty"`
	URL      string `json:"url"`
}
type Host struct {
	CPUs         float64 `json:"cpus"`
	MemoryMiB    int64   `json:"memoryMiB"`
	AvailableMiB int64   `json:"availableMiB"`
	DiskFreeMiB  int64   `json:"diskFreeMiB"`
}

func Token() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func SaveConfig(dir string, c Config) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".config-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	f.Close()
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(dir, "hub.json"))
}
func LoadConfig(dir string) (Config, error) {
	var c Config
	b, err := os.ReadFile(filepath.Join(dir, "hub.json"))
	if err == nil {
		err = json.Unmarshal(b, &c)
	}
	return c, err
}
func (p Policy) Validate(h Host) error {
	if math.IsNaN(p.CPUs) || math.IsInf(p.CPUs, 0) || math.IsNaN(p.ReserveCPUs) || math.IsInf(p.ReserveCPUs, 0) || p.CPUs <= 0 || p.MemoryMiB < 256 || p.ReserveCPUs < 0 || p.ReserveMemoryMiB < 256 || p.ModelMemoryMiB < 0 || p.MinDiskFreeMiB < 64 || p.MaxRunning < 1 || p.MaxRunning > 100 {
		return fmt.Errorf("invalid hub resource policy")
	}
	if p.ReserveMemoryMiB > h.MemoryMiB || p.ModelMemoryMiB > h.MemoryMiB || p.CPUs+p.ReserveCPUs > h.CPUs || p.MemoryMiB > h.MemoryMiB-p.ReserveMemoryMiB-p.ModelMemoryMiB {
		return fmt.Errorf("worker budget would consume reserved host/model capacity")
	}
	return nil
}
