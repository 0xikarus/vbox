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
	Role       string `json:"role,omitempty"`
	Agent      string `json:"agent,omitempty"`
	State      string `json:"state,omitempty"`
	CanMessage bool   `json:"canMessage"`
	CanReceive bool   `json:"canReceive"`
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

// requireContact rejects a send before it reaches the outbox when the controller
// does not list the target. The controller still re-validates when it drains the
// event, so a revoked edge cannot be bypassed by a stale in-box check.
func requireContact(ctx context.Context, assignment, ref string) error {
	contacts, err := DesktopContacts(ctx, assignment)
	if err != nil {
		return err
	}
	for _, contact := range contacts {
		if contact.ID == ref || strings.EqualFold(contact.Name, ref) {
			if !contact.CanMessage {
				return fmt.Errorf("messaging contact %q is not permitted", ref)
			}
			return nil
		}
	}
	return fmt.Errorf("contact %q is not in your contact list; call get_contacts first", ref)
}
