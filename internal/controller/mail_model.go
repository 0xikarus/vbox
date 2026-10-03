package controller

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/mail"
	"os"
	"regexp"
	"strings"
	"time"
)

var mailUUIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
var mailDomainPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]{0,251}[a-z0-9])?$`)

func mailDomain() string {
	domain := strings.ToLower(strings.TrimSpace(os.Getenv("VMBOX_MAIL_DOMAIN")))
	if !mailDomainPattern.MatchString(domain) || !strings.Contains(domain, ".") || strings.Contains(domain, "..") {
		return ""
	}
	return domain
}

func validMailAddress(value string) bool {
	address, err := mail.ParseAddress(strings.TrimSpace(value))
	return err == nil && address.Address == value && !strings.ContainsAny(value, "\r\n") && len(value) <= 254
}

func normalizeMailRecipients(values []string) ([]string, error) {
	if len(values) < 1 || len(values) > 5 {
		return nil, fmt.Errorf("choose 1–5 recipients")
	}
	result := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if !validMailAddress(value) {
			return nil, fmt.Errorf("invalid recipient address")
		}
		key := strings.ToLower(value)
		if !seen[key] {
			result = append(result, value)
			seen[key] = true
		}
	}
	return result, nil
}

type mailFilters struct {
	Sender  string `json:"sender"`
	Subject string `json:"subject"`
}
type mailSettings struct {
	Enabled    bool        `json:"enabled"`
	Address    string      `json:"address"`
	Subscribed bool        `json:"subscribed"`
	Filters    mailFilters `json:"filters"`
	Unread     int         `json:"unread"`
	Pending    int         `json:"pending"`
}
type mailMessageRow struct {
	ID             string    `json:"id"`
	From           string    `json:"from"`
	FromName       string    `json:"fromName"`
	Subject        string    `json:"subject"`
	Preview        string    `json:"preview"`
	ReceivedAt     time.Time `json:"receivedAt"`
	Unread         bool      `json:"unread"`
	HasAttachments bool      `json:"hasAttachments"`
	Quarantined    bool      `json:"quarantined"`
	SPF            string    `json:"spf"`
	DKIM           string    `json:"dkim"`
}
type mailAttachmentRow struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	ContentType string `json:"contentType"`
	Size        int    `json:"size"`
}
type mailMessageDetail struct {
	mailMessageRow
	Text        string              `json:"text"`
	Attachments []mailAttachmentRow `json:"attachments"`
}
type mailOutboxRow struct {
	OutboxID    string     `json:"outboxId"`
	To          []string   `json:"to"`
	Subject     string     `json:"subject"`
	Text        string     `json:"text"`
	Status      string     `json:"status"`
	Reason      string     `json:"reason"`
	Version     int        `json:"version"`
	SubmittedAt time.Time  `json:"submittedAt"`
	DecidedAt   *time.Time `json:"decidedAt,omitempty"`
	SentAt      *time.Time `json:"sentAt,omitempty"`
}

type mailCursor struct {
	At time.Time `json:"at"`
	ID string    `json:"id"`
}

func encodeMailCursor(at time.Time, id string) string {
	raw, _ := json.Marshal(mailCursor{At: at, ID: id})
	return base64.RawURLEncoding.EncodeToString(raw)
}
func decodeMailCursor(value string) (mailCursor, error) {
	if value == "" {
		return mailCursor{}, nil
	}
	if len(value) > 256 {
		return mailCursor{}, fmt.Errorf("invalid cursor")
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return mailCursor{}, fmt.Errorf("invalid cursor")
	}
	var cursor mailCursor
	if json.Unmarshal(raw, &cursor) != nil || cursor.At.IsZero() || !mailUUIDPattern.MatchString(cursor.ID) {
		return mailCursor{}, fmt.Errorf("invalid cursor")
	}
	return cursor, nil
}
