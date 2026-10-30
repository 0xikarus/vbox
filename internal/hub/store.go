package hub

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
	_ "modernc.org/sqlite"
)

type Store struct {
	db   *sql.DB
	lock *os.File
}

func OpenStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "hub.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("hub data directory is already in use")
	}
	path := filepath.Join(dir, "hub.sqlite")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		f.Close()
		return nil, err
	}
	file.Close()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		f.Close()
		return nil, err
	}
	db.SetMaxOpenConns(1)
	var version int
	if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version > 1 {
		db.Close()
		f.Close()
		return nil, fmt.Errorf("unsupported hub database version")
	}

	_, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;
 CREATE TABLE IF NOT EXISTS boxes(id TEXT PRIMARY KEY,spec TEXT NOT NULL,state TEXT NOT NULL,desired TEXT NOT NULL,reason TEXT NOT NULL DEFAULT '',created TEXT NOT NULL,updated TEXT NOT NULL,port INTEGER NOT NULL DEFAULT 0,password TEXT NOT NULL,attempts INTEGER NOT NULL DEFAULT 0,goal_sent INTEGER NOT NULL DEFAULT 0,booted TEXT NOT NULL DEFAULT '');
 CREATE TABLE IF NOT EXISTS requests(key TEXT PRIMARY KEY,payload TEXT NOT NULL,box_id TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS profiles(id TEXT PRIMARY KEY,name TEXT NOT NULL,agent TEXT NOT NULL,archive BLOB NOT NULL);
 PRAGMA user_version=1;`)
	if err != nil {
		db.Close()
		f.Close()
		return nil, err
	}
	return &Store{db, f}, nil
}
func (s *Store) Close() error { err := s.db.Close(); s.lock.Close(); return err }

const columns = `id,spec,state,desired,reason,created,updated,port,password,attempts,goal_sent,booted`

func scanBox(row interface{ Scan(...any) error }) (Box, error) {
	var b Box
	var data string
	err := row.Scan(&b.ID, &data, &b.State, &b.Desired, &b.Reason, &b.Created, &b.Updated, &b.Port, &b.Password, &b.Attempts, &b.GoalSent, &b.Booted)
	if err == nil {
		err = json.Unmarshal([]byte(data), &b.Spec)
	}
	b.URL = "/boxes/" + b.ID + "/"
	return b, err
}
func (s *Store) Get(id string) (Box, error) {
	return scanBox(s.db.QueryRow(`SELECT `+columns+` FROM boxes WHERE id=?`, id))
}
func (s *Store) List() ([]Box, error) {
	rows, err := s.db.Query(`SELECT ` + columns + ` FROM boxes WHERE state!='deleted' ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Box{}
	for rows.Next() {
		b, e := scanBox(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
func (s *Store) Create(spec Spec) (Box, error) { return s.CreateWithKey(spec, "") }
func (s *Store) CreateWithKey(spec Spec, key string) (Box, error) {
	data, _ := json.Marshal(spec)
	tx, err := s.db.Begin()
	if err != nil {
		return Box{}, err
	}
	defer tx.Rollback()
	if key != "" {
		var payload, id string
		err = tx.QueryRow(`SELECT payload,box_id FROM requests WHERE key=?`, key).Scan(&payload, &id)
		if err == nil {
			if payload != string(data) {
				return Box{}, fmt.Errorf("idempotency key already used for different settings")
			}
			tx.Rollback()
			return s.Get(id)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return Box{}, err
		}
	}
	id := "hb-" + Token()[:24]
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = tx.Exec(`INSERT INTO boxes(id,spec,state,desired,created,updated,password) VALUES(?,?,'queued','running',?,?,?)`, id, string(data), now, now, Token())
	if err != nil {
		return Box{}, err
	}
	if key != "" {
		if _, err = tx.Exec(`INSERT INTO requests VALUES(?,?,?)`, key, string(data), id); err != nil {
			return Box{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return Box{}, err
	}
	return s.Get(id)
}

func (s *Store) Update(b Box) error {
	b.Updated = time.Now().UTC().Format(time.RFC3339Nano)
	data, _ := json.Marshal(b.Spec)
	_, err := s.db.Exec(`UPDATE boxes SET spec=?,state=?,desired=?,reason=?,updated=?,port=?,password=?,attempts=?,goal_sent=?,booted=? WHERE id=?`, string(data), b.State, b.Desired, b.Reason, b.Updated, b.Port, b.Password, b.Attempts, b.GoalSent, b.Booted, b.ID)
	return err
}
func (s *Store) Desired(id, want string) error {
	res, err := s.db.Exec(`UPDATE boxes SET desired=?,updated=? WHERE id=? AND state!='deleted'`, want, time.Now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}
func (s *Store) Archive(ids []string) ([][]byte, error) {
	var out [][]byte
	for _, id := range ids {
		var b []byte
		if err := s.db.QueryRow(`SELECT archive FROM profiles WHERE id=?`, id).Scan(&b); err != nil {
			return nil, fmt.Errorf("profile not found")
		}
		out = append(out, b)
	}
	return out, nil
}

type Profile struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Agent string `json:"agent"`
	Model string `json:"model,omitempty"`
}

func (s *Store) Profiles() ([]Profile, error) {
	rows, err := s.db.Query(`SELECT id,name,agent,archive FROM profiles ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Profile{}
	for rows.Next() {
		var p Profile
		var archive []byte
		if err = rows.Scan(&p.ID, &p.Name, &p.Agent, &archive); err != nil {
			return nil, err
		}
		p.Model = profileModel(p.Agent, archive)
		clear(archive)
		out = append(out, p)
	}
	return out, rows.Err()
}
func (s *Store) SaveProfile(name, agent string, data []byte) (Profile, error) {
	if len(name) < 1 || len(name) > 100 || (agent != "claude" && agent != "codex" && agent != "opencode" && agent != "github") {
		return Profile{}, errors.New("invalid profile")
	}
	clean, err := sanitizeArchive(data)
	if err != nil {
		return Profile{}, err
	}
	p := Profile{ID: "hp-" + Token()[:24], Name: name, Agent: agent, Model: profileModel(agent, clean)}
	_, err = s.db.Exec(`INSERT INTO profiles VALUES(?,?,?,?)`, p.ID, p.Name, p.Agent, clean)
	return p, err
}

func (s *Store) DeleteProfile(id string) error {
	boxes, err := s.List()
	if err != nil {
		return err
	}
	for _, b := range boxes {
		for _, p := range b.Spec.Profiles {
			if p == id {
				return fmt.Errorf("profile is still referenced by a box")
			}
		}
	}
	_, err = s.db.Exec(`DELETE FROM profiles WHERE id=?`, id)
	return err
}
