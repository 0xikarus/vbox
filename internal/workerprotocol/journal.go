package workerprotocol

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

var ErrOperationExists = errors.New("worker operation already claimed; reconcile its result instead of replaying")

// Journal durably claims operations before starting processes. Only request
// hashes and exit status are recorded: command arguments and output may contain
// secrets. An incomplete claim remains ambiguous across an agent restart.
type Journal struct{ Directory string }
type Operation struct {
	RequestHash string     `json:"requestHash"`
	StartedAt   time.Time  `json:"startedAt"`
	FinishedAt  *time.Time `json:"finishedAt,omitempty"`
	ExitCode    *int       `json:"exitCode,omitempty"`
}

func operationHash(req Request) (string, error) {
	// The agent incarnation fences live execution, but a new agent process must
	// still be able to reconcile an operation claimed by its predecessor. Keep
	// every durable assignment and command field in the hash while excluding
	// only that process-local connection identity.
	req.Binding.Incarnation = ""
	data, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}
func (j Journal) path(id string) string {
	hash := sha256.Sum256([]byte(id))
	return filepath.Join(j.Directory, hex.EncodeToString(hash[:])+".json")
}
func (j Journal) Claim(req Request) error {
	if req.OperationID == "" || j.Directory == "" {
		return errors.New("operation ID and journal directory required")
	}
	hash, err := operationHash(req)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(j.Directory, 0700); err != nil {
		return err
	}
	file, err := os.OpenFile(j.path(req.OperationID), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		return ErrOperationExists
	}
	if err != nil {
		return err
	}
	defer file.Close()
	if err = json.NewEncoder(file).Encode(Operation{RequestHash: hash, StartedAt: time.Now().UTC()}); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	return syncDirectory(j.Directory)
}
func (j Journal) Inspect(req Request) (Operation, error) {
	var op Operation
	data, err := os.ReadFile(j.path(req.OperationID))
	if err != nil {
		return op, err
	}
	if err = json.Unmarshal(data, &op); err != nil {
		return op, err
	}
	hash, err := operationHash(req)
	if err != nil {
		return op, err
	}
	if op.RequestHash != hash {
		return Operation{}, errors.New("operation request mismatch")
	}
	return op, nil
}
func (j Journal) Complete(req Request, code int) error {
	op, err := j.Inspect(req)
	if err != nil {
		return err
	}
	if op.FinishedAt != nil {
		return errors.New("operation already completed")
	}
	now := time.Now().UTC()
	op.FinishedAt = &now
	op.ExitCode = &code
	file, err := os.CreateTemp(j.Directory, ".result-")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	defer file.Close()
	if err = json.NewEncoder(file).Encode(op); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	if err = os.Rename(name, j.path(req.OperationID)); err != nil {
		return err
	}
	return syncDirectory(j.Directory)
}
func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
