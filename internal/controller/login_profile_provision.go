package controller

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/0xikarus/vmbox-service/internal/boxruntime"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

func (s *Server) provisionCreationProfiles(ctx context.Context, prov provider.Provider, creation logicalBoxCreation) error {
	if len(creation.Request.LoginProfiles) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	request := boxruntime.SyncRequest{}
	defer func() {
		for _, f := range request.Files {
			clear(f.Data)
		}
	}()
	for _, ref := range creation.Request.LoginProfiles {
		profile, err := s.Store.LoadLoginProfile(ctx, Principal{AccountID: creation.AccountID}, ref.Application, ref.Name)
		if err != nil {
			return fmt.Errorf("selected login profile unavailable; no credentials provisioned")
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
			request.Files = append(request.Files, boxruntime.SyncFile{Path: path, Mode: "0600", Data: profile.Files[name]})
		}
	}
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
	if err := verifyCredentialVolume(ctx, prov, a.Slot.ServiceID, a.Box.VolumeID); err != nil {
		return err
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("could not encode selected credentials")
	}
	defer clear(payload)
	result, err := prov.Exec(ctx, a.Slot.ServiceID, []string{"vmbox-runtime", "sync-files"}, provider.ExecOptions{Stdin: bytes.NewReader(payload)})
	if err != nil || result.ExitCode != 0 || strings.TrimSpace(result.Stdout) != fmt.Sprintf("%x", sha256.Sum256(payload)) {
		return fmt.Errorf("selected credential transfer failed integrity verification")
	}
	result, err = prov.Exec(ctx, a.Slot.ServiceID, []string{"vmbox-runtime", "prepare-hibernate"}, provider.ExecOptions{})
	if err != nil || result.ExitCode != 0 {
		return fmt.Errorf("could not flush provisioned workspace")
	}
	return tx.Commit()
}

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
	if err != nil || storage == nil || storage.ID != volumeID {
		return fmt.Errorf("credential destination volume mismatch")
	}
	return nil
}
