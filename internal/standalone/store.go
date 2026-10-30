// Package standalone hosts one persistent agent without a fleet controller.
package standalone

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/0xikarus/vmbox-service/internal/boxruntime"
	_ "modernc.org/sqlite"
)

var ErrConflict = errors.New("idempotency key already used for different content")

type Message struct {
	ID        string            `json:"id"`
	Direction string            `json:"direction"`
	Text      string            `json:"text"`
	State     string            `json:"state"`
	Created   string            `json:"created"`
	Images    []ImageAttachment `json:"images,omitempty"`
}

type ImageAttachment struct {
	Index     int    `json:"index"`
	Name      string `json:"name"`
	MediaType string `json:"mediaType"`
	URL       string `json:"url,omitempty"`
}
type Store struct{ db *sql.DB }

func OpenStore(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	f.Close()
	if err = os.Chmod(path, 0600); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	var version int
	if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		db.Close()
		return nil, err
	}
	if version > 1 {
		db.Close()
		return nil, fmt.Errorf("standalone database version %d is newer than this release", version)
	}
	_, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;
 CREATE TABLE IF NOT EXISTS messages(id TEXT PRIMARY KEY,direction TEXT NOT NULL,body TEXT NOT NULL,state TEXT NOT NULL,created TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS events(id TEXT PRIMARY KEY,payload BLOB NOT NULL);
 PRAGMA user_version=1;`)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db}, nil
}
func (s *Store) Close() error { return s.db.Close() }

// StoreEvent archives the entire envelope, including images and questions,
// atomically with its history entry before the worker outbox can be acknowledged.
func (s *Store) StoreEvent(event boxruntime.ChatEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO events VALUES(?,?) ON CONFLICT(id) DO NOTHING`, event.ID, data); err != nil {
		return err
	}
	var existing []byte
	if err = tx.QueryRow(`SELECT payload FROM events WHERE id=?`, event.ID).Scan(&existing); err != nil {
		return err
	}
	if string(existing) != string(data) {
		return ErrConflict
	}
	text := event.Text
	if event.Question != nil {
		text = event.Question.Text
	}
	_, err = tx.Exec(`INSERT INTO messages VALUES(?,?,?,?,?) ON CONFLICT(id) DO NOTHING`, "event_"+event.ID, "agent", text, "answered", time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) Insert(id, direction, body, state string) (Message, error) {
	if id == "" || len(id) > 128 || len(body) > 140000 {
		return Message{}, fmt.Errorf("invalid message")
	}
	_, err := s.db.Exec(`INSERT INTO messages VALUES(?,?,?,?,?) ON CONFLICT(id) DO NOTHING`, id, direction, body, state, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return Message{}, err
	}
	var m Message
	err = s.db.QueryRow(`SELECT id,direction,body,state,created FROM messages WHERE id=?`, id).Scan(&m.ID, &m.Direction, &m.Text, &m.State, &m.Created)
	if err == nil && (m.Text != body || m.Direction != direction) {
		return Message{}, ErrConflict
	}
	return m, err
}
func (s *Store) List() (messages []Message, err error) {
	rows, err := s.db.Query(`SELECT m.id,m.direction,m.body,m.state,m.created,
 (SELECT json_group_array(json_object('index',j.key,'name',json_extract(j.value,'$.name'),'mediaType',json_extract(j.value,'$.mediaType'))) FROM json_each(e.payload,'$.images') j)
 FROM (SELECT rowid,id,direction,body,state,created FROM messages ORDER BY rowid DESC LIMIT 200) m
 LEFT JOIN events e ON m.id='event_'||e.id ORDER BY m.rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	messages = []Message{}
	for rows.Next() {
		var m Message
		var images string
		if err = rows.Scan(&m.ID, &m.Direction, &m.Text, &m.State, &m.Created, &images); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(images), &m.Images); err != nil {
			return nil, err
		}
		for i := range m.Images {
			m.Images[i].URL = "/v1/standalone/messages/" + url.PathEscape(m.ID) + "/images/" + strconv.Itoa(m.Images[i].Index)
		}
		messages = append(messages, m)
	}
	return messages, rows.Err()
}
func (s *Store) Pending() (messages []Message, err error) {
	rows, err := s.db.Query(`SELECT id,direction,body,state,created FROM messages WHERE direction='user' AND state IN ('queued','uncertain') ORDER BY rowid LIMIT 20`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var m Message
		if err = rows.Scan(&m.ID, &m.Direction, &m.Text, &m.State, &m.Created); err != nil {
			return nil, err
		}
		messages = append(messages, m)
	}
	return messages, rows.Err()
}
func (s *Store) State(id, state string) error {
	_, err := s.db.Exec(`UPDATE messages SET state=? WHERE id=?`, state, id)
	return err
}

func (s *Store) Image(messageID string, index int) ([]byte, string, error) {
	if index < 0 || index > 15 {
		return nil, "", sql.ErrNoRows
	}
	var payload []byte
	if err := s.db.QueryRow(`SELECT e.payload FROM events e JOIN messages m ON m.id='event_'||e.id WHERE m.id=?`, messageID).Scan(&payload); err != nil {
		return nil, "", err
	}
	var event boxruntime.ChatEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return nil, "", err
	}
	if index >= len(event.Images) {
		return nil, "", sql.ErrNoRows
	}
	image := event.Images[index]
	if len(image.Data) > 36<<20 {
		return nil, "", fmt.Errorf("image too large")
	}
	data, err := base64.StdEncoding.DecodeString(image.Data)
	return data, image.Name, err
}
