package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Uses the disposable PostgreSQL fixture, the real router/auth middleware and
// real TCP requests. It does not start agents or emulate their answers.
func testCoworkerHTTP(t *testing.T, ctx context.Context, store *Store, owner, outsider Principal, first, firstToken string) {
	t.Helper()
	enroll := func(p Principal, name string) (string, string) {
		t.Helper()
		id := uuid()
		_, err := store.DB.ExecContext(ctx, `INSERT INTO logical_boxes(id,account_id,owner_user_id,name,provider,state,volume_id,volume_name) VALUES($1,$2,$3,$4,'railway','running',$5,$5)`, id, p.AccountID, p.UserID, name, "test-volume-"+id)
		if err != nil {
			t.Fatal(err)
		}
		if err = store.EnableCoworkers(ctx, p, true); err != nil {
			t.Fatal(err)
		}
		if err = store.EnrollCoworker(ctx, p, id); err != nil {
			t.Fatal(err)
		}
		var sealed string
		if err = store.DB.QueryRowContext(ctx, `SELECT encrypted_token FROM coworkers WHERE account_id=$1 AND box_id=$2`, p.AccountID, id).Scan(&sealed); err != nil {
			t.Fatal(err)
		}
		token, err := store.Envelope.Open(p.AccountID+":coworker:"+id, sealed)
		if err != nil {
			t.Fatal(err)
		}
		defer clear(token)
		return id, string(token)
	}
	second, secondToken := enroll(owner, "coworker-http-second")
	foreign, foreignToken := enroll(outsider, "coworker-http-foreign")
	controller := NewServer(store, nil)
	t.Run("Telegram correlated reply", func(t *testing.T) { testTelegramCorrelatedReply(t, ctx, controller, owner, first, second) })
	hibernateStarted := make(chan string, 1)
	controller.StartHibernate = func(_ context.Context, p Principal, id string) error {
		if p.AccountID != owner.AccountID {
			return fmt.Errorf("incorrect hibernate account")
		}
		hibernateStarted <- id
		return nil
	}
	server := httptest.NewServer(controller.Handler())
	defer server.Close()
	request := func(token, method, path string, body any, want int) json.RawMessage {
		t.Helper()
		var input io.Reader
		if body != nil {
			data, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			input = bytes.NewReader(data)
		}
		r, err := http.NewRequestWithContext(ctx, method, server.URL+path, input)
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json, text/event-stream")
		r.Header.Set("MCP-Protocol-Version", "2025-11-25")
		res, err := server.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		data, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		if err != nil || res.StatusCode != want {
			t.Fatalf("%s %s: status=%d want=%d error=%v", method, path, res.StatusCode, want, err)
		}
		return data
	}
	call := func(token, tool string, args any, wantError bool, out any) {
		t.Helper()
		data := request(token, "POST", "/mcp/coworkers", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": args}}, 200)
		var response struct {
			Error  json.RawMessage
			Result struct {
				IsError bool
				Content []struct{ Type, Text string }
			}
		}
		if json.Unmarshal(data, &response) != nil || len(response.Error) > 0 || response.Result.IsError != wantError || len(response.Result.Content) != 1 {
			t.Fatalf("unexpected %s result: %s", tool, data)
		}
		if out != nil && json.Unmarshal([]byte(response.Result.Content[0].Text), out) != nil {
			t.Fatalf("invalid %s tool content", tool)
		}
	}
	var discovery []map[string]string
	call(firstToken, "coworkers_list", map[string]any{}, false, &discovery)
	if len(discovery) != 2 {
		t.Fatalf("active account discovery: %d", len(discovery))
	}
	for _, box := range discovery {
		if box["id"] == foreign {
			t.Fatal("cross-account discovery leaked")
		}
	}
	key, text := uuid(), "generated durable message "+uuid()
	args := map[string]any{"recipient": second, "key": key, "text": text}
	var sent, retry struct{ Sequence int64 }
	call(firstToken, "message_send", args, false, &sent)
	call(firstToken, "message_send", args, false, &retry)
	if sent.Sequence < 1 || sent.Sequence != retry.Sequence {
		t.Fatal("HTTP retry was not idempotent")
	}
	var inbox []CoworkerEvent
	if json.Unmarshal(request(secondToken, "GET", "/v1/coworker/events", nil, 200), &inbox) != nil || len(inbox) != 1 || inbox[0].Sender != first || inbox[0].Sequence != sent.Sequence {
		t.Fatal("second box did not receive exact durable event")
	}
	var message map[string]string
	if json.Unmarshal(inbox[0].Data, &message) != nil || message["text"] != text {
		t.Fatal("durable message content changed")
	}
	call(foreignToken, "message_send", args, true, nil)
	request(secondToken, "GET", "/v1/provider-credentials", nil, 401)
	call(secondToken, "hibernate_self", map[string]any{"completed": true, "boxId": first}, true, nil)
	call(secondToken, "hibernate_self", map[string]any{"completed": false}, true, nil)
	var board CoworkerBoard
	call(firstToken, "board_read", map[string]any{}, false, &board)
	call(firstToken, "board_edit", CoworkerBoardEdit{Revision: board.Revision, Action: "create", TaskID: "http-task", Title: "HTTP shared task"}, false, &board)
	call(secondToken, "board_edit", CoworkerBoardEdit{Revision: board.Revision, Action: "assign", TaskID: "http-task", Assignee: second}, false, &board)
	call(secondToken, "board_edit", CoworkerBoardEdit{Revision: board.Revision, Action: "comment", TaskID: "http-task", Comment: "Second coworker acknowledged"}, false, &board)
	call(firstToken, "board_edit", CoworkerBoardEdit{Revision: board.Revision - 1, Action: "move", TaskID: "http-task", Status: "done"}, true, nil)
	call(firstToken, "board_read", map[string]any{}, false, &board)
	last := board.Tasks[len(board.Tasks)-1]
	if last.ID != "http-task" || last.Assignee != second || len(last.Comments) != 1 || last.Comments[0].Author != second {
		t.Fatal("shared board attribution or assignment lost")
	}
	var foreignBoard CoworkerBoard
	call(foreignToken, "board_read", map[string]any{}, false, &foreignBoard)
	if len(foreignBoard.Tasks) != 0 {
		t.Fatal("cross-account board leaked")
	}
	var events []CoworkerEvent
	if json.Unmarshal(request(secondToken, "GET", fmt.Sprintf("/v1/coworker/events?after=%d", sent.Sequence), nil, 200), &events) != nil || len(events) != 3 {
		t.Fatal("board events missing from second coworker inbox")
	}
	slot, fence := uuid(), uuid()
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO compute_slots(id,account_id,provider,ordinal,state,service_id,health,assignment_generation,fencing_token) VALUES($1,$2,'railway',3,'occupied','coworker-http-service','healthy',1,$3)`, slot, owner.AccountID, fence); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.ExecContext(ctx, `UPDATE logical_boxes SET slot_id=$2,assignment_generation=1,fencing_token=$3 WHERE id=$1`, second, slot, fence); err != nil {
		t.Fatal(err)
	}
	var accepted struct {
		Accepted, VolumeRetained bool
		BoxID                    string
	}
	call(secondToken, "hibernate_self", map[string]any{"completed": true}, false, &accepted)
	if !accepted.Accepted || !accepted.VolumeRetained || accepted.BoxID != second {
		t.Fatal("wrong self-hibernate result")
	}
	select {
	case id := <-hibernateStarted:
		if id != second {
			t.Fatal("sibling hibernate scheduled")
		}
	case <-ctx.Done():
		t.Fatal("hibernate not handed to lifecycle worker")
	}
	var state, volume, slotState, siblingState string
	if err := store.DB.QueryRowContext(ctx, `SELECT b.state,b.volume_id,s.state FROM logical_boxes b JOIN compute_slots s ON s.id=b.slot_id WHERE b.id=$1`, second).Scan(&state, &volume, &slotState); err != nil {
		t.Fatal(err)
	}
	if state != "hibernating" || volume != "test-volume-"+second || slotState != "draining" {
		t.Fatal("self-hibernate did not durably preserve volume and fence compute")
	}
	if err := store.DB.QueryRowContext(ctx, `SELECT state FROM logical_boxes WHERE id=$1`, first).Scan(&siblingState); err != nil || siblingState != "running" {
		t.Fatal("sibling affected by self-hibernate")
	}
	request(secondToken, "GET", "/v1/coworker/events", nil, 401)
	call(firstToken, "coworkers_list", map[string]any{}, false, &discovery)
	if len(discovery) != 1 || discovery[0]["id"] != first {
		t.Fatal("inactive coworker still advertised")
	}
}
