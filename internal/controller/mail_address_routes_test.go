package controller

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestMailGrantLookupIsLiveAndAccountScoped(t *testing.T) {
	store, mock := testStore(t)
	for _, allowed := range []bool{true, false} {
		mock.ExpectQuery(`SELECT EXISTS\(SELECT 1 FROM mail_addresses a LEFT JOIN mail_address_grants g`).
			WithArgs("account-a", panelTestBox, "shop@example.test").WillReturnRows(sqlmock.NewRows([]string{"allowed"}).AddRow(allowed))
		got, err := store.mailAddressGranted(httptest.NewRequest(http.MethodGet, "/", nil).Context(), "account-a", panelTestBox, "shop@example.test")
		if err != nil || got != allowed {
			t.Fatalf("grant=%t got=%t err=%v", allowed, got, err)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestUnknownRecipientRejectedWhenCatchallOff(t *testing.T) {
	t.Setenv("VMBOX_MAIL_DOMAIN", "example.test")
	t.Setenv("VMBOX_INBOUND_MAIL_SECRET", "synthetic-secret")
	store, mock := testStore(t)
	mock.ExpectQuery(`SELECT a.account_id::text,a.id::text,a.owning_box_id::text`).WithArgs("unknown@example.test").WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(`SELECT account_id::text FROM mail_account_settings WHERE keep_unknown=true`).WillReturnError(sql.ErrNoRows)
	body := []byte(`{"to":"unknown@example.test"}`)
	r := signedMailRequest(http.MethodPost, "/v1/inbound-mail/recipient", body, fmt.Sprint(time.Now().Unix()), "synthetic-secret")
	w := httptest.NewRecorder()
	(&Server{Store: store}).inboundMailRecipient(w, r)
	if w.Code != 404 {
		t.Fatalf("unknown recipient status=%d body=%s", w.Code, w.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCatchallAcceptsUnknownButNotDisabledKnownAddress(t *testing.T) {
	t.Setenv("VMBOX_MAIL_DOMAIN", "example.test")
	store, mock := testStore(t)
	mock.ExpectQuery(`SELECT a.account_id::text,a.id::text,a.owning_box_id::text`).WithArgs("new@example.test").WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(`SELECT account_id::text FROM mail_account_settings WHERE keep_unknown=true`).WillReturnRows(sqlmock.NewRows([]string{"account"}).AddRow("account-a"))
	destination, found, err := (&Server{Store: store}).resolveInboundMailRecipient(httptest.NewRequest(http.MethodGet, "/", nil).Context(), "new@example.test")
	if err != nil || !found || destination.accountID != "account-a" || destination.boxID != "" || destination.addressID != "" {
		t.Fatalf("catchall found=%t destination=%+v err=%v", found, destination, err)
	}
	mock.ExpectQuery(`SELECT a.account_id::text,a.id::text,a.owning_box_id::text`).WithArgs("disabled@example.test").WillReturnRows(sqlmock.NewRows([]string{"account", "address", "box", "name", "enabled", "state"}).AddRow("account-a", panelTestBox, panelTestBox, "Disabled", false, "running"))
	_, found, err = (&Server{Store: store}).resolveInboundMailRecipient(httptest.NewRequest(http.MethodGet, "/", nil).Context(), "disabled@example.test")
	if err != nil || found {
		t.Fatalf("disabled registered address accepted: found=%t err=%v", found, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestExtraAddressCollisionWithBoxAddressReturnsConflict(t *testing.T) {
	t.Setenv("VMBOX_MAIL_DOMAIN", "example.test")
	store, mock := testStore(t)
	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO mail_addresses`).WithArgs(sqlmock.AnyArg(), "account-a", "box", "box@example.test", "").WillReturnError(&pgconn.PgError{Code: "23505"})
	mock.ExpectRollback()
	s := &Server{Store: store}
	w := httptest.NewRecorder()
	s.ownerMailAddresses(w, httptest.NewRequest(http.MethodPost, "/v1/mail/addresses", strings.NewReader(`{"localPart":"box","boxIds":[]}`)), Principal{AccountID: "account-a", Role: "owner"})
	if w.Code != 409 {
		t.Fatalf("collision status=%d body=%s", w.Code, w.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAgentCannotReadAnotherAddressWithoutCurrentGrant(t *testing.T) {
	t.Setenv("VMBOX_MAIL_DOMAIN", "example.test")
	store, mock := testStore(t)
	policy, _ := json.Marshal(v1.AgentRoleCapabilities{Mail: v1.MailGrant{Read: true}, MCPTools: v1.MCPToolsGrant{Enabled: true, AllowedTools: []string{"read_email"}}})
	mock.ExpectQuery("agent_box_policy").WithArgs("account-a", panelTestBox).WillReturnRows(sqlmock.NewRows([]string{"permission", "config"}).AddRow("agent_box_policy", policy))
	mock.ExpectQuery(`SELECT enabled,COALESCE\(address`).WithArgs("account-a", panelTestBox).WillReturnRows(sqlmock.NewRows([]string{"enabled", "address", "subscribed", "sender_filter", "subject_filter"}).AddRow(true, "box@example.test", false, "", ""))
	mock.ExpectQuery(`(?s)FROM mail_messages m WHERE m.account_id=\$1.*mail_address_grants g`).WithArgs("account-a", panelTestBox, panelTestMail).WillReturnError(sql.ErrNoRows)
	r := httptest.NewRequest(http.MethodGet, "/v1/agent-desktop/mail/messages/"+panelTestMail, nil)
	r.SetPathValue("mid", panelTestMail)
	w := httptest.NewRecorder()
	(&Server{Store: store}).agentMailMessage(w, r, Principal{AccountID: "account-a", Role: "desktop-agent", Subject: "desktop-box:" + panelTestBox})
	if w.Code != 404 {
		t.Fatalf("ungranted mail status=%d body=%s", w.Code, w.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestBoxAddressCanGrantAnotherBoxWithoutRemovingItsOwner(t *testing.T) {
	t.Setenv("VMBOX_MAIL_DOMAIN", "example.test")
	const otherBox = "33333333-3333-4333-8333-333333333333"
	store, mock := testStore(t)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT local_part,address,label,enabled,COALESCE\(owning_box_id::text`).WithArgs("account-a", panelTestBox).WillReturnRows(sqlmock.NewRows([]string{"local", "address", "label", "enabled", "owner"}).AddRow("box", "box@example.test", "Box", true, panelTestBox))
	for _, id := range []string{panelTestBox, otherBox} {
		mock.ExpectQuery(`SELECT EXISTS\(SELECT 1 FROM logical_boxes`).WithArgs("account-a", id).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	}
	mock.ExpectExec(`DELETE FROM mail_address_grants`).WithArgs("account-a", panelTestBox).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`INSERT INTO mail_address_grants`).WithArgs("account-a", panelTestBox, otherBox).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	r := httptest.NewRequest(http.MethodPatch, "/v1/mail/addresses/"+panelTestBox, strings.NewReader(`{"boxIds":["`+panelTestBox+`","`+otherBox+`"]}`))
	r.SetPathValue("aid", panelTestBox)
	w := httptest.NewRecorder()
	(&Server{Store: store}).ownerMailAddressItem(w, r, Principal{AccountID: "account-a", Role: "owner"})
	if w.Code != 200 || !strings.Contains(w.Body.String(), panelTestBox) || !strings.Contains(w.Body.String(), otherBox) {
		t.Fatalf("grant status=%d body=%s", w.Code, w.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
