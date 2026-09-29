package boxruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ContactSummary is one addressable contact as reported by the controller.
type ContactSummary struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Group      string `json:"group,omitempty"`
	Agent      string `json:"agent,omitempty"`
	State      string `json:"state,omitempty"`
	CanMessage bool   `json:"canMessage"`
	Reason     string `json:"reason,omitempty"`
}

// DesktopContacts fetches the contact directory for this box. The controller
// derives it from the authenticated assignment, so the box cannot widen its own
// reach by naming a different identity.
func DesktopContacts(ctx context.Context, assignment string) ([]ContactSummary, error) {
	config, err := readDesktopAgentConfig(assignment)
	if err != nil {
		return nil, err
	}
	endpoint := strings.TrimRight(config.Controller, "/") + "/v1/agent-desktop/contacts"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("contact directory unavailable")
	}
	request.Header.Set("Authorization", "DesktopAgent "+config.Token)
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("contact directory request failed")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil || response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("contact directory rejected; check the box assignment")
	}
	var payload struct {
		Contacts []ContactSummary `json:"contacts"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("contact directory unavailable")
	}
	return payload.Contacts, nil
}

// validateContactRef accepts a box id or a box name without letting arbitrary
// characters reach the controller route or the outbox file name.
func validateContactRef(value string) error {
	if value == "" || len(value) > 128 {
		return fmt.Errorf("contact is empty or too long")
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '-' || character == '_' || character == '.' {
			continue
		}
		return fmt.Errorf("contact contains unsupported character %q", character)
	}
	return nil
}

// resolveContact rejects a send before it reaches the outbox when the controller
// does not list the target. The controller still re-validates when it drains the
// event, so a revoked edge cannot be bypassed by a stale in-box check.
func resolveContact(ctx context.Context, assignment, ref string) (string, error) {
	contacts, err := DesktopContacts(ctx, assignment)
	if err != nil {
		return "", err
	}
	return resolveContactFromList(contacts, ref)
}

func resolveContactFromList(contacts []ContactSummary, ref string) (string, error) {
	for _, contact := range contacts {
		if contact.ID == ref || strings.EqualFold(contact.Name, ref) || fullBoxIDMatchesCompact(contact.ID, ref) {
			if !contact.CanMessage {
				return "", fmt.Errorf("messaging contact %q is not permitted", ref)
			}
			if contact.State != "" && contact.State != "running" {
				return "", fmt.Errorf("contact %q is %s; wake it and retry after it is running", contact.Name, contact.State)
			}
			// The controller accepts exact box names. Normalize compact IDs,
			// incoming full box IDs, and case-insensitive names before enqueueing.
			return contact.Name, nil
		}
	}
	return "", fmt.Errorf("contact %q is not in your contact list; call get_contacts first", ref)
}

// Incoming contact prompts carry a full UUID. Match it against the unique
// compact ID from the authorized contact directory, never against a box that
// is absent from that directory.
func fullBoxIDMatchesCompact(compactID, ref string) bool {
	if len(ref) != 36 || ref[8] != '-' || ref[13] != '-' || ref[18] != '-' || ref[23] != '-' {
		return false
	}
	for index, character := range ref {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			continue
		}
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f') || (character >= 'A' && character <= 'F')) {
			return false
		}
	}
	return strings.HasPrefix(strings.ReplaceAll(strings.ToLower(ref), "-", ""), strings.ToLower(compactID))
}
