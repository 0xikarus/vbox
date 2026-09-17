package controller

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/boxruntime"
	"github.com/0xikarus/vmbox-service/internal/loginprofile"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

func (s *Server) provisionCreationProfiles(ctx context.Context, prov provider.Provider, creation logicalBoxCreation) error {
	if len(creation.Request.LoginProfiles) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	// Keep assignment locked throughout secret transport so reassignment cannot
	// send account credentials into another box's workspace.
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("could not lock credential destination")
	}
	defer tx.Rollback()
	var id string
	a := creation.Assignment
	err = tx.QueryRowContext(ctx, `SELECT id::text FROM logical_boxes WHERE account_id=$1 AND id=$2 AND state='attaching' AND slot_id=$3 AND assignment_generation=$4 AND fencing_token=$5 AND volume_id=$6 FOR UPDATE`, creation.AccountID, a.Box.ID, a.Slot.ID, a.Box.AssignmentGeneration, a.FencingToken, a.Box.VolumeID).Scan(&id)
	if err != nil {
		return fmt.Errorf("credential destination assignment changed")
	}
	if err := s.transferProfileFiles(ctx, tx, prov, creation.AccountID, a.Box.ID, a.Slot.ServiceID, a.Box.VolumeID, creation.Request.LoginProfiles); err != nil {
		return err
	}
	result, err := prov.Exec(ctx, a.Slot.ServiceID, []string{"vmbox-runtime", "prepare-hibernate"}, provider.ExecOptions{})
	if err != nil || result.ExitCode != 0 {
		return fmt.Errorf("could not flush provisioned workspace")
	}
	return tx.Commit()
}

// profileSyncRequest builds the integrity-checked file set for the selected
// login profiles. It returns the GitHub host/user so the caller can verify the
// GitHub login after the transfer.
func (s *Server) profileSyncRequest(ctx context.Context, accountID string, refs []v1.LoginProfileRef) (boxruntime.SyncRequest, string, string, error) {
	request := boxruntime.SyncRequest{}
	var githubHost, githubUser string
	for _, ref := range refs {
		profile, err := s.Store.LoadLoginProfile(ctx, Principal{AccountID: accountID}, ref.Application, ref.Name)
		if err != nil {
			return boxruntime.SyncRequest{}, "", "", fmt.Errorf("selected login profile unavailable; no credentials provisioned")
		}
		if ref.Application == "github" {
			if err := loginprofile.Validate(ref.Application, profile.Files, time.Now()); err != nil {
				return boxruntime.SyncRequest{}, "", "", err
			}
			var credential loginprofile.GitHub
			_ = json.Unmarshal(profile.Files["credential.json"], &credential)
			githubHost, githubUser = credential.Host, credential.User
			// JSON quoted strings are valid YAML scalars. Only the selected
			// account is exported, never the local multi-account credential store.
			data := []byte(fmt.Sprintf("%q:\n    user: %q\n    oauth_token: %q\n    git_protocol: https\n", credential.Host, credential.User, credential.Token))
			for _, value := range profile.Files {
				clear(value)
			}
			request.Files = append(request.Files, boxruntime.SyncFile{Path: "/data/home/.config/gh/hosts.yml", Mode: "0600", Data: data})
			continue
		}
		names := make([]string, 0, len(profile.Files))
		for name := range profile.Files {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			path := "/data/home/." + ref.Application + "/" + name
			if ref.Application == "claude" && name == ".claude.json" {
				path = "/data/home/.claude.json"
			}
			if ref.Application == "opencode" {
				path = "/data/home/.config/opencode/" + name
				if name == "auth.json" {
					path = "/data/home/.local/share/opencode/auth.json"
				}
			}
			request.Files = append(request.Files, boxruntime.SyncFile{Path: path, Mode: "0600", Data: profile.Files[name]})
		}
	}
	return request, githubHost, githubUser, nil
}

// transferProfileFiles writes and verifies one profile set inside an attached
// box workspace, then records the references. The caller owns the transaction
// and must hold the assignment lock. Credential transfers never touch
// instruction files or any other box configuration.
func (s *Server) transferProfileFiles(ctx context.Context, tx *sql.Tx, prov provider.Provider, accountID, boxID, serviceID, volumeID string, refs []v1.LoginProfileRef) error {
	request, githubHost, githubUser, err := s.profileSyncRequest(ctx, accountID, refs)
	if err != nil {
		return err
	}
	defer func() {
		for _, f := range request.Files {
			clear(f.Data)
		}
	}()
	if err := waitForCredentialVolume(ctx, prov, serviceID, volumeID, 30*time.Second); err != nil {
		return err
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("could not encode selected credentials")
	}
	defer clear(payload)
	result, err := prov.Exec(ctx, serviceID, []string{"vmbox-runtime", "sync-files"}, provider.ExecOptions{Stdin: bytes.NewReader(payload)})
	if err != nil || result.ExitCode != 0 || strings.TrimSpace(result.Stdout) != fmt.Sprintf("%x", sha256.Sum256(payload)) {
		return fmt.Errorf("selected credential transfer failed integrity verification")
	}
	for _, ref := range refs {
		if err := verifyProvisionedLogin(ctx, prov, serviceID, ref.Application, githubHost, githubUser); err != nil {
			return err
		}
	}
	encoded, err := json.Marshal(refs)
	if err != nil {
		return fmt.Errorf("could not record imported credential references")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE logical_boxes SET metadata=jsonb_set(jsonb_set(metadata,'{importedLoginProfiles}',$3::jsonb),'{pendingLoginProfiles}','[]'::jsonb) WHERE account_id=$1 AND id=$2`, accountID, boxID, encoded); err != nil {
		return fmt.Errorf("could not record imported credential references")
	}
	return nil
}

var errCredentialVolumeUnavailable = errors.New("credential destination volume unavailable")

// Call only while holding the logical-box assignment lock, before any secret
// transport. A valid SSH endpoint alone does not prove workspace ownership.
func verifyCredentialVolume(ctx context.Context, prov provider.Provider, serviceID, volumeID string) error {
	if serviceID == "" || volumeID == "" || pendingVolume(volumeID) {
		return fmt.Errorf("credential destination identity incomplete")
	}
	inspector, ok := prov.(provider.AttachedStorageProvider)
	if !ok {
		return fmt.Errorf("provider cannot verify credential destination")
	}
	storage, err := inspector.AttachedStorage(ctx, serviceID)
	if err != nil || storage == nil {
		return errCredentialVolumeUnavailable
	}
	if storage.ID != volumeID {
		return fmt.Errorf("credential destination volume mismatch")
	}
	return nil
}

// Railway can briefly omit a volume's service attachment while replacing a
// deployment. Wait only when attachment evidence is unavailable. An observed
// different volume is a definitive mismatch and must never be retried into a
// credential write.
func waitForCredentialVolume(ctx context.Context, prov provider.Provider, serviceID, volumeID string, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		err := verifyCredentialVolume(ctx, prov, serviceID, volumeID)
		if err == nil || !errors.Is(err, errCredentialVolumeUnavailable) {
			return err
		}
		select {
		case <-ctx.Done():
			return err
		case <-timer.C:
			return err
		case <-ticker.C:
		}
	}
}
