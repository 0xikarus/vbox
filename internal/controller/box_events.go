package controller

import (
	"context"
	"database/sql"
)

// appendBoxEvent belongs in the same transaction as the change it describes.
func appendBoxEvent(ctx context.Context, tx *sql.Tx, accountID, boxID, body, key string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO box_events(id,account_id,box_id,body,event_key)
		VALUES($1,$2,$3,$4,$5) ON CONFLICT(account_id,event_key) DO NOTHING`, uuid(), accountID, boxID, body, key)
	return err
}
