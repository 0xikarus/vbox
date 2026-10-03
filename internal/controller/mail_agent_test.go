package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/DATA-DOG/go-sqlmock"
)

func TestAgentMailRuntimeGateRequiresGrantAndEnabledInbox(t *testing.T) {
	for _, tc := range []struct {
		name         string
		domain       string
		grant        v1.MailGrant
		selected     []string
		inboxEnabled bool
		wantAllowed  bool
	}{
		{"global feature off", "", v1.MailGrant{Read: true}, []string{"read_email"}, true, false},
		{"typed grant off", "example.test", v1.MailGrant{}, []string{"read_email"}, true, false},
		{"tool not selected", "example.test", v1.MailGrant{Read: true}, nil, true, false},
		{"inbox disabled", "example.test", v1.MailGrant{Read: true}, []string{"read_email"}, false, false},
		{"read enabled", "example.test", v1.MailGrant{Read: true}, []string{"read_email"}, true, true},
		{"compose cannot use read", "example.test", v1.MailGrant{Compose: true}, []string{"send_email", "read_email"}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("VMBOX_MAIL_DOMAIN", tc.domain)
			store, mock := testStore(t)
			if tc.domain != "" {
				policy, _ := json.Marshal(v1.AgentRoleCapabilities{Mail: tc.grant, MCPTools: v1.MCPToolsGrant{Enabled: true, AllowedTools: tc.selected}})
				mock.ExpectQuery("agent_box_policy").WithArgs("account-a", "box-a").WillReturnRows(sqlmock.NewRows([]string{"permission", "config"}).AddRow("agent_box_policy", policy))
				if tc.grant.Read && len(tc.selected) > 0 {
					mock.ExpectQuery("SELECT enabled,COALESCE\\(address").WithArgs("account-a", "box-a").WillReturnRows(sqlmock.NewRows([]string{"enabled", "address", "subscribed", "sender_filter", "subject_filter"}).AddRow(tc.inboxEnabled, "box@example.test", false, "", ""))
				}
			}
			s := &Server{Store: store}
			response := httptest.NewRecorder()
			p := Principal{AccountID: "account-a", Role: "desktop-agent", Subject: "desktop-box:box-a"}
			allowed := s.agentMailAllowed(response, httptest.NewRequest(http.MethodGet, "/v1/agent-desktop/mail/messages", nil), p, "read_email")
			if allowed != tc.wantAllowed || (!allowed && !strings.Contains(response.Body.String(), "disabled")) {
				t.Fatalf("allowed=%t response=%d %s", allowed, response.Code, response.Body.String())
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
