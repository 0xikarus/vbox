package events

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

var ansi = regexp.MustCompile(`(?:\x1B\[[0-?]*[ -/]*[@-~])|[\x00-\x08\x0B\x0C\x0E-\x1F\x7F]`)

type Store struct {
	mu       sync.Mutex
	dir      string
	sequence uint64
	secrets  []string
	preview  int
}

func NewStore(dir string, secrets []string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	store := &Store{dir: dir, preview: 512}
	for _, secret := range secrets {
		if len(secret) >= 4 {
			store.secrets = append(store.secrets, secret)
		}
	}
	if prior, err := ReadAll(filepath.Join(dir, "events.ndjson"), 1); err == nil && len(prior) == 1 {
		store.sequence = prior[0].Sequence
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return store, nil
}

func (s *Store) Append(event v1.Event) (v1.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sequence++
	event.Sequence = s.sequence
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	event.Message = s.Sanitize(event.Message)
	data, err := json.Marshal(event)
	if err != nil {
		return event, err
	}
	path := filepath.Join(s.dir, "events.ndjson")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return event, err
	}
	defer file.Close()
	if _, err := file.Write(append(data, '\n')); err != nil {
		return event, err
	}
	return event, file.Sync()
}

func (s *Store) WriteStatus(run v1.Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	run.LastActivity = s.Sanitize(run.LastActivity)
	data, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, ".status-*.json")
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
	return os.Rename(name, filepath.Join(s.dir, "status.json"))
}

func (s *Store) ReadStatus() (v1.Run, error) {
	var run v1.Run
	data, err := os.ReadFile(filepath.Join(s.dir, "status.json"))
	if err != nil {
		return run, err
	}
	err = json.Unmarshal(data, &run)
	return run, err
}

func (s *Store) Sanitize(value string) string {
	value = strings.ReplaceAll(value, "\r", "\n")
	parts := strings.Split(value, "\n")
	for i := range parts {
		parts[i] = ansi.ReplaceAllString(parts[i], "")
	}
	value = strings.TrimSpace(strings.Join(parts, "\n"))
	for _, secret := range s.secrets {
		value = strings.ReplaceAll(value, secret, "[REDACTED]")
	}
	if !utf8.ValidString(value) {
		value = strings.ToValidUTF8(value, "�")
	}
	if len(value) > s.preview {
		value = value[len(value)-s.preview:]
	}
	return value
}

func ReadAll(path string, limit int) ([]v1.Event, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 2<<20)
	events := []v1.Event{}
	for scanner.Scan() {
		var event v1.Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return nil, fmt.Errorf("event log: %w", err)
		}
		events = append(events, event)
		if limit > 0 && len(events) > limit {
			events = events[len(events)-limit:]
		}
	}
	return events, scanner.Err()
}
