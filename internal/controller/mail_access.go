package controller

import (
	"context"
	"database/sql"
	"strings"
)

// mailAgentScopeSQL assumes $1=account and $2=box. A grant is checked against
// the current table on every read, so revocation takes effect immediately.
const mailAgentScopeSQL = `(m.box_id=$2 AND (m.address_id IS NULL OR m.address_id=$2) OR EXISTS (
 SELECT 1 FROM mail_address_grants g JOIN mail_addresses a ON a.id=g.address_id AND a.account_id=g.account_id
 WHERE g.account_id=$1 AND g.box_id=$2 AND g.address_id=m.address_id AND a.enabled))`

func (s *Store) mailAddressGranted(ctx context.Context, accountID, boxID, address string) (bool, error) {
	var allowed bool
	err := s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM mail_addresses a LEFT JOIN mail_address_grants g ON g.address_id=a.id AND g.account_id=a.account_id AND g.box_id=$2 WHERE a.account_id=$1 AND lower(a.address)=lower($3) AND a.enabled AND (a.owning_box_id=$2 OR g.box_id=$2))`, accountID, boxID, strings.TrimSpace(address)).Scan(&allowed)
	return allowed, err
}

func (s *Store) ownMailAddress(ctx context.Context, accountID, boxID string) (string, error) {
	var address string
	err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(address,'') FROM box_mail_settings WHERE account_id=$1 AND box_id=$2 AND enabled`, accountID, boxID).Scan(&address)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return address, err
}
