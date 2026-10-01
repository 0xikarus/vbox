package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

type contactProfileRef struct{ application, name string }

func (s *Server) contactUsage(ctx context.Context, accountID, selfID string, contacts []v1.ContactEntry) (v1.ContactUsage, error) {
	needed := map[string]bool{}
	for _, contact := range contacts {
		needed[contact.Name] = true
	}
	profiles := map[string]contactProfileRef{}
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT id::text,name,default_agent,COALESCE(metadata->'importedLoginProfiles',metadata->'loginProfiles','[]'::jsonb)
		FROM logical_boxes WHERE account_id=$1 AND state<>'deleting'`, accountID)
	if err != nil {
		return v1.ContactUsage{}, err
	}
	for rows.Next() {
		var id, name, agent string
		var raw []byte
		if err := rows.Scan(&id, &name, &agent, &raw); err != nil {
			rows.Close()
			return v1.ContactUsage{}, err
		}
		if id != selfID && !needed[name] {
			continue
		}
		var refs []v1.LoginProfileRef
		if json.Unmarshal(raw, &refs) != nil {
			continue
		}
		for _, ref := range refs {
			if ref.Application == agent && ref.Name != "" {
				profiles[id] = contactProfileRef{agent, ref.Name}
				profiles[name] = contactProfileRef{agent, ref.Name}
				break
			}
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return v1.ContactUsage{}, err
	}
	rows.Close()

	usage := map[contactProfileRef]v1.ContactUsage{}
	rows, err = s.Store.DB.QueryContext(ctx, `SELECT application,profile_name,observed_at,COALESCE(snapshot,'null'::jsonb)
		FROM profile_usage_snapshots WHERE account_id=$1`, accountID)
	if err != nil {
		return v1.ContactUsage{}, err
	}
	now := time.Now().UTC()
	for rows.Next() {
		var ref contactProfileRef
		var observed sql.NullTime
		var raw []byte
		if err := rows.Scan(&ref.application, &ref.name, &observed, &raw); err != nil {
			rows.Close()
			return v1.ContactUsage{}, err
		}
		if !observed.Valid || string(raw) == "null" {
			continue
		}
		var snapshot profileUsageSnapshot
		if json.Unmarshal(raw, &snapshot) != nil {
			continue
		}
		usage[ref] = summarizeContactUsage(snapshot, observed.Time, now)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return v1.ContactUsage{}, err
	}
	rows.Close()
	resolve := func(ref contactProfileRef) v1.ContactUsage {
		if ref.name == "" {
			return v1.ContactUsage{Status: "no_profile"}
		}
		if value, ok := usage[ref]; ok {
			return value
		}
		return v1.ContactUsage{Status: "unknown"}
	}
	for index := range contacts {
		contacts[index].Usage = resolve(profiles[contacts[index].Name])
	}
	return resolve(profiles[selfID]), nil
}

func summarizeContactUsage(snapshot profileUsageSnapshot, observedAt, now time.Time) v1.ContactUsage {
	result := v1.ContactUsage{Status: "unknown", ObservedAt: &observedAt}
	if observedAt.After(now.Add(5*time.Minute)) || now.Sub(observedAt) > 2*profileUsageFreshness {
		result.Status = "stale"
		return result
	}
	for _, window := range snapshot.Windows {
		if window.UsedPercent == nil || math.IsNaN(*window.UsedPercent) || math.IsInf(*window.UsedPercent, 0) {
			continue
		}
		remaining := max(0, min(100, 100-*window.UsedPercent))
		if result.RemainingPercent == nil || remaining < *result.RemainingPercent {
			result.RemainingPercent = &remaining
		}
	}
	if result.RemainingPercent != nil {
		result.Status = "available"
		return result
	}
	if spend := snapshot.Spend; spend != nil {
		amount := spend.Remaining
		if amount == nil && spend.Limit != nil && spend.Used != nil {
			value := max(0, *spend.Limit-*spend.Used)
			amount = &value
		}
		if amount != nil && !math.IsNaN(*amount) && !math.IsInf(*amount, 0) {
			result.Status, result.RemainingAmount, result.Unit = "available", amount, spend.Currency
			return result
		}
	}
	if len(snapshot.Balances) > 0 {
		balance := snapshot.Balances[0]
		if !math.IsNaN(balance.Amount) && !math.IsInf(balance.Amount, 0) {
			result.Status, result.RemainingAmount, result.Unit = "available", &balance.Amount, balance.Unit
		}
	}
	return result
}
