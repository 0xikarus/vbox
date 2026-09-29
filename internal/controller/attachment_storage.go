package controller

import (
	"context"
	"fmt"
	"net/http"
)

type boxAttachmentStorage struct {
	BoxBytes       int64 `json:"boxBytes"`
	BoxCount       int64 `json:"boxCount"`
	ClearableCount int64 `json:"clearableCount"`
	AccountBytes   int64 `json:"accountBytes"`
	UnusedBytes    int64 `json:"unusedBytes"`
	UnusedCount    int64 `json:"unusedCount"`
	LimitBytes     int64 `json:"limitBytes"`
}

func (s *Store) boxAttachmentStorage(ctx context.Context, accountID, boxID string) (boxAttachmentStorage, error) {
	value := boxAttachmentStorage{LimitBytes: maxAccountAttachmentBytes}
	err := s.DB.QueryRowContext(ctx, `SELECT count(*),COALESCE(sum(bytes),0),count(*) FILTER (WHERE clearable) FROM (
		SELECT i.id,octet_length(i.data) AS bytes,bool_or(m.state='delivered') AS clearable
		FROM box_message_images j
		JOIN box_messages m ON m.id=j.message_id AND m.account_id=j.account_id
		JOIN box_tasks t ON t.id=m.task_id AND t.account_id=m.account_id
		JOIN run_once_images i ON i.id=j.image_id AND i.account_id=j.account_id
		WHERE j.account_id=$1 AND t.logical_box_id=$2
		GROUP BY i.id
	) AS media`, accountID, boxID).Scan(&value.BoxCount, &value.BoxBytes, &value.ClearableCount)
	if err != nil {
		return value, err
	}
	err = s.DB.QueryRowContext(ctx, `SELECT COALESCE(sum(octet_length(i.data)),0),
		COALESCE(sum(octet_length(i.data)) FILTER (WHERE j.image_id IS NULL),0),
		count(*) FILTER (WHERE j.image_id IS NULL)
		FROM run_once_images i
		LEFT JOIN (SELECT DISTINCT image_id FROM box_message_images WHERE account_id=$1) j ON j.image_id=i.id
		WHERE i.account_id=$1`, accountID).Scan(&value.AccountBytes, &value.UnusedBytes, &value.UnusedCount)
	return value, err
}

func (s *Server) boxAttachmentStorageHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	box, err := s.Store.LogicalBox(r.Context(), p, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("box unavailable"))
		return
	}
	if r.Method == http.MethodGet {
		value, err := s.Store.boxAttachmentStorage(r.Context(), p.AccountID, box.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, value)
		return
	}
	var request struct {
		Confirmation string `json:"confirmation"`
	}
	if err := decodeJSON(r, &request); err != nil || request.Confirmation != box.Name {
		writeError(w, http.StatusConflict, fmt.Errorf("confirmation must exactly match box name %q", box.Name))
		return
	}
	freed, removed, err := s.Store.clearBoxAttachments(r.Context(), p, box.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"freedBytes": freed, "removedReferences": removed})
}

// clearBoxAttachments removes media only from delivered messages. An in-flight
// message can still need its original attachment for native agent delivery.
func (s *Store) clearBoxAttachments(ctx context.Context, p Principal, boxID string) (int64, int64, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	var accountID string
	if err := tx.QueryRowContext(ctx, `SELECT id::text FROM accounts WHERE id=$1 FOR UPDATE`, p.AccountID).Scan(&accountID); err != nil {
		return 0, 0, err
	}
	rows, err := tx.QueryContext(ctx, `DELETE FROM box_message_images j
		USING box_messages m,box_tasks t
		WHERE j.account_id=$1 AND m.id=j.message_id AND m.account_id=j.account_id
		AND t.id=m.task_id AND t.account_id=m.account_id
		AND t.logical_box_id=$2 AND m.state='delivered'
		RETURNING j.image_id::text`, accountID, boxID)
	if err != nil {
		return 0, 0, err
	}
	var imageIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, 0, err
		}
		imageIDs = append(imageIDs, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, 0, err
	}
	rows.Close()
	var freed int64
	if len(imageIDs) > 0 {
		freedRows, err := tx.QueryContext(ctx, `DELETE FROM run_once_images i
			WHERE i.account_id=$1 AND i.id=ANY($2::uuid[])
			AND NOT EXISTS (SELECT 1 FROM box_message_images j WHERE j.image_id=i.id)
			RETURNING octet_length(i.data)`, accountID, imageIDs)
		if err != nil {
			return 0, 0, err
		}
		for freedRows.Next() {
			var size int64
			if err := freedRows.Scan(&size); err != nil {
				freedRows.Close()
				return 0, 0, err
			}
			freed += size
		}
		if err := freedRows.Err(); err != nil {
			freedRows.Close()
			return 0, 0, err
		}
		freedRows.Close()
	}
	removed := int64(len(imageIDs))
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_log(account_id,user_id,action,target_type,target_id,detail)
		VALUES($1,$2,'logical_box.chat_attachments.clear','logical_box',$3,jsonb_build_object('removed_references',$4::bigint,'freed_bytes',$5::bigint))`, accountID, p.UserID, boxID, removed, freed); err != nil {
		return 0, 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	return freed, removed, nil
}
