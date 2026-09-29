package boxruntime

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLocalHeartbeatSchedulesAndConsumesExactTicks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if _, err := startLocalHeartbeat("managed-one", 4, 1); err == nil {
		t.Fatal("interval under five minutes was accepted")
	}
	if _, err := startLocalHeartbeat("managed-one", 5, 3); err != nil {
		t.Fatal(err)
	}
	state, err := readLocalHeartbeat(home)
	if err != nil || state == nil || state.TicksLeft != 3 {
		t.Fatalf("persisted heartbeat=%+v err=%v", state, err)
	}
	var messages []string
	var ids []string
	for remaining := 2; remaining >= 0; remaining-- {
		state.NextAt = time.Now().Add(-time.Second)
		if err := writeLocalHeartbeat(home, *state); err != nil {
			t.Fatal(err)
		}
		err = advanceLocalHeartbeat(context.Background(), home, time.Now(), func(context.Context) (bool, error) { return true, nil }, func(_ context.Context, _ localHeartbeat, text, id string) error {
			messages = append(messages, text)
			ids = append(ids, id)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(messages[len(messages)-1], "[Ticks left:"+string(rune('0'+remaining))+"]") {
			t.Fatalf("unexpected prompt %q", messages[len(messages)-1])
		}
		state, err = readLocalHeartbeat(home)
		if err != nil {
			t.Fatal(err)
		}
	}
	if state != nil || len(messages) != 3 || ids[0] == ids[1] || ids[1] == ids[2] {
		t.Fatalf("heartbeat did not complete: state=%+v messages=%v ids=%v", state, messages, ids)
	}
}

func TestLocalHeartbeatRetainsDueTickOnDeliveryFailureAndStops(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if _, err := startLocalHeartbeat("managed-one", 5, 1); err != nil {
		t.Fatal(err)
	}
	state, _ := readLocalHeartbeat(home)
	state.NextAt = time.Now().Add(-time.Second)
	if err := writeLocalHeartbeat(home, *state); err != nil {
		t.Fatal(err)
	}
	err := advanceLocalHeartbeat(context.Background(), home, time.Now(), func(context.Context) (bool, error) { return true, nil }, func(context.Context, localHeartbeat, string, string) error { return os.ErrDeadlineExceeded })
	if err == nil {
		t.Fatal("failed delivery was treated as a completed tick")
	}
	still, _ := readLocalHeartbeat(home)
	if still == nil || still.TicksLeft != 1 || !still.NextAt.Equal(state.NextAt) {
		t.Fatalf("failed tick was consumed: %+v", still)
	}
	stopped, err := stopLocalHeartbeat()
	if err != nil || stopped["stopped"] != true {
		t.Fatalf("stop result=%v err=%v", stopped, err)
	}
	stopped, err = stopLocalHeartbeat()
	if err != nil || stopped["stopped"] != false {
		t.Fatalf("second stop result=%v err=%v", stopped, err)
	}
}

func TestLocalHeartbeatCountOneOmitsRemainingSuffix(t *testing.T) {
	message := heartbeatMessage(time.Date(2026, 9, 30, 1, 2, 3, 0, time.Local), 0, false)
	if message != "[Heartbeat] 30.09.2026 01:02:03" {
		t.Fatalf("message=%q", message)
	}
}

func TestHeartbeatMCPToolsStartAndStopLocalTimer(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("VMBOX_CHAT_SESSION", "managed-one")
	if _, err := callDesktopTool(context.Background(), "assignment", "start_heartbeat", json.RawMessage(`{"intervalMinutes":5}`)); err != nil {
		t.Fatal(err)
	}
	state, err := readLocalHeartbeat(home)
	if err != nil || state == nil || state.TicksLeft != 1 || state.Session != "managed-one" {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	if _, err := callDesktopTool(context.Background(), "assignment", "stop_heartbeat", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	state, err = readLocalHeartbeat(home)
	if err != nil || state != nil {
		t.Fatalf("stopped state=%+v err=%v", state, err)
	}
}
