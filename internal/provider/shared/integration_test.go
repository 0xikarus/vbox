package shared

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/0xikarus/vmbox-service/internal/provider"
)

func TestDisposableTwoSlotWorker(t *testing.T) {
	endpoint := os.Getenv("VMBOX_TEST_SHARED_DISPOSABLE_ENDPOINT")
	if endpoint == "" {
		t.Skip("requires explicitly disposable shared worker")
	}
	client, err := New(endpoint, os.Getenv("VMBOX_TEST_SHARED_TOKEN"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	type fixture struct {
		box     provider.Box
		storage provider.Storage
		owner   provider.Owner
	}
	var fixtures []fixture
	defer func() {
		cleanup, finish := context.WithTimeout(context.Background(), 30*time.Second)
		defer finish()
		for _, item := range fixtures {
			if err := client.DetachStorage(cleanup, item.box.ID, item.storage); err != nil {
				t.Error(err)
				continue
			}
			if err := client.DeleteStorage(cleanup, item.storage, item.owner); err != nil {
				t.Error(err)
			}
			if err := client.Delete(cleanup, item.box.ID, item.box.Owner); err != nil {
				t.Error(err)
			}
		}
	}()
	for index := range 2 {
		name := fmt.Sprintf("disposable-%d-%d", time.Now().UnixNano(), index)
		owner := provider.Owner{AccountID: "00000000-0000-4000-8000-000000000001", BoxID: name}
		box, err := client.Create(ctx, provider.CreateRequest{Name: name, Owner: owner})
		if err != nil {
			t.Fatal(err)
		}
		storage, err := client.CreateWorkspaceStorage(ctx, box.ID, owner, provider.Resources{DiskGiB: 1})
		if err != nil {
			t.Fatal(err)
		}
		box, err = client.Inspect(ctx, box.ID)
		if err != nil {
			t.Fatal(err)
		}
		fixtures = append(fixtures, fixture{box, storage, owner})
	}
	execute := func(index int, argv ...string) provider.ExecResult {
		t.Helper()
		result, err := client.Exec(ctx, fixtures[index].box.ID, argv, provider.ExecOptions{})
		if err != nil || result.ExitCode != 0 {
			t.Fatalf("slot %d: %+v, %v", index, result, err)
		}
		return result
	}
	first := execute(0, "sh", "-c", `printf '%s\n' "$(id -u)" "$HOME" "$VMBOX_DESKTOP_DISPLAY"; test -z "${VMBOX_SHARED_TOKEN:-}"; printf first > "$HOME/marker"; tmux new-session -d -s persistent 'sleep 300'`)
	second := execute(1, "sh", "-c", `printf '%s\n' "$(id -u)" "$HOME" "$VMBOX_DESKTOP_DISPLAY"; test -z "${VMBOX_SHARED_TOKEN:-}"; printf second > "$HOME/marker"; tmux new-session -d -s persistent 'sleep 300'`)
	firstFields, secondFields := strings.Fields(first.Stdout), strings.Fields(second.Stdout)
	if len(firstFields) != 3 || len(secondFields) != 3 {
		t.Fatalf("unexpected identities: %q, %q", first.Stdout, second.Stdout)
	}
	for index := range firstFields {
		if firstFields[index] == secondFields[index] {
			t.Fatalf("shared identity: %q", firstFields[index])
		}
	}
	execute(0, "sh", "-c", `test ! -r "$1/marker"; test "$(cat "$HOME/marker")" = first; test "$(tmux list-sessions -F '#{session_name}')" = persistent`, "isolation-test", secondFields[1])
	execute(1, "sh", "-c", `test ! -r "$1/marker"; test "$(cat "$HOME/marker")" = second`, "isolation-test", firstFields[1])
	for index := range 2 {
		assignment := strings.Repeat(fmt.Sprintf("%x", index+1), 64)
		execute(index, "tmux", "set-option", "-g", "@vmbox_assignment", assignment)
		execute(index, "tmux", "set-option", "-g", "@vmbox_server_incarnation", strings.Repeat("b", 24))
		execute(index, "vmbox-runtime", "desktop-start", assignment)
		capture := execute(index, "vmbox-runtime", "desktop-screenshot", assignment)
		if !strings.HasPrefix(capture.Stdout, "\x89PNG\r\n\x1a\n") {
			t.Fatalf("slot %d desktop screenshot is not PNG", index)
		}
	}
	blenderEnabled := os.Getenv("VMBOX_TEST_SHARED_BLENDER") == "1"
	checkBlender := func(index int) {
		t.Helper()
		execute(index, "/opt/vmbox/blender-mcp-1.9.1/bin/python", "-c", sharedBlenderMCPProbe, fmt.Sprintf("workspace-%d", index))
	}
	if blenderEnabled {
		for index := range 2 {
			execute(index, "vmbox-runtime", "install-tools", "blender")
			execute(index, "vmbox-runtime", "restore-tools")
			execute(index, "sh", "-c", fmt.Sprintf(`tmux new-session -d -s blender-test 'DISPLAY="$VMBOX_DESKTOP_DISPLAY" blender --python-expr "import bpy; bpy.context.scene.name=\"workspace-%d\"; bpy.context.scene.blendermcp_port=9876; bpy.ops.blendermcp.start_server()" > "$HOME/blender-test.log" 2>&1'`, index))
		}
		checkBlender(0)
		checkBlender(1)
	}
	if _, err := client.Stop(ctx, fixtures[0].box.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ExecConnection(ctx, fixtures[0].box.Connection, []string{"true"}, provider.ExecOptions{}); err == nil {
		t.Fatal("stale connection accepted")
	}
	execute(1, "tmux", "has-session", "-t", "persistent")
	if blenderEnabled {
		checkBlender(1)
	}
	if _, err := client.Start(ctx, fixtures[0].box.ID); err != nil {
		t.Fatal(err)
	}
	execute(0, "sh", "-c", `test "$(cat "$HOME/marker")" = first; if tmux has-session -t persistent 2>/dev/null; then exit 1; fi`)
	execute(0, "/usr/local/bin/vmbox-runtime", "health")
}

func TestDisposableTwoSlotIsolation(t *testing.T) {
	endpoint := os.Getenv("VMBOX_TEST_SHARED_DISPOSABLE_ENDPOINT")
	if endpoint == "" {
		t.Skip("requires explicitly disposable shared worker")
	}
	client, err := New(endpoint, os.Getenv("VMBOX_TEST_SHARED_TOKEN"))
	if err != nil {
		t.Fatal(err)
	}
	expectedTier := os.Getenv("VMBOX_TEST_SHARED_EXPECT_TIER")
	if expectedTier != "" && expectedTier != "uid" && expectedTier != "namespace" && expectedTier != "container" {
		t.Fatalf("VMBOX_TEST_SHARED_EXPECT_TIER must be uid, namespace or container, got %q", expectedTier)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	type fixture struct {
		box     provider.Box
		storage provider.Storage
		owner   provider.Owner
	}
	var fixtures []fixture
	defer func() {
		cleanup, finish := context.WithTimeout(context.Background(), 30*time.Second)
		defer finish()
		for _, item := range fixtures {
			if err := client.DetachStorage(cleanup, item.box.ID, item.storage); err != nil {
				t.Error(err)
				continue
			}
			if err := client.DeleteStorage(cleanup, item.storage, item.owner); err != nil {
				t.Error(err)
			}
			if err := client.Delete(cleanup, item.box.ID, item.box.Owner); err != nil {
				t.Error(err)
			}
		}
	}()
	for index := range 2 {
		name := fmt.Sprintf("isolation-%d-%d", time.Now().UnixNano(), index)
		owner := provider.Owner{AccountID: "00000000-0000-4000-8000-000000000001", BoxID: name}
		box, err := client.Create(ctx, provider.CreateRequest{Name: name, Owner: owner})
		if err != nil {
			t.Fatal(err)
		}
		storage, err := client.CreateWorkspaceStorage(ctx, box.ID, owner, provider.Resources{DiskGiB: 1})
		if err != nil {
			t.Fatal(err)
		}
		box, err = client.Inspect(ctx, box.ID)
		if err != nil {
			t.Fatal(err)
		}
		fixtures = append(fixtures, fixture{box, storage, owner})
	}
	execute := func(index int, argv ...string) provider.ExecResult {
		t.Helper()
		result, err := client.Exec(ctx, fixtures[index].box.ID, argv, provider.ExecOptions{})
		if err != nil || result.ExitCode != 0 {
			t.Fatalf("slot %d: %+v, %v", index, result, err)
		}
		return result
	}

	tier := fixtures[0].box.Connection.Metadata["isolationTier"]
	label := fixtures[0].box.Labels["isolation.tier"]
	if tier == "" || label == "" {
		t.Fatalf("box 0 did not report isolation tier: metadata %+v labels %+v", fixtures[0].box.Connection.Metadata, fixtures[0].box.Labels)
	}
	if tier != label {
		t.Fatalf("inconsistent isolation tier: metadata %q label %q", tier, label)
	}
	for index := 1; index < len(fixtures); index++ {
		if reported := fixtures[index].box.Connection.Metadata["isolationTier"]; reported != tier {
			t.Fatalf("box %d reported tier %q want %q", index, reported, tier)
		}
		if reported := fixtures[index].box.Labels["isolation.tier"]; reported != label {
			t.Fatalf("box %d reported label %q want %q", index, reported, label)
		}
	}
	if tier == "uid" && strings.TrimSpace(fixtures[0].box.Connection.Metadata["isolationReason"]) == "" {
		t.Fatal("uid tier must report a non-empty isolationReason")
	}
	if expectedTier != "" && tier != expectedTier {
		t.Fatalf("reported isolation tier %q does not match VMBOX_TEST_SHARED_EXPECT_TIER=%q", tier, expectedTier)
	}

	siblingWorkspace := fixtures[1].box.Connection.Metadata["workspaceId"]
	if siblingWorkspace == "" {
		t.Fatal("box 1 did not report workspaceId metadata")
	}
	siblingHome := "/data/workspaces/" + siblingWorkspace + "/home"

	execute(0, "sh", "-c", `set -eu
test -z "${VMBOX_SHARED_TOKEN:-}"
printf first > "$HOME/marker"
tmux new-session -d -s isolation-smoke 'sleep 60'`)
	execute(1, "sh", "-c", `set -eu
test -z "${VMBOX_SHARED_TOKEN:-}"
printf second > "$HOME/marker"
printf sibling > "$TMPDIR/isolation-sibling-marker"
printf sibling > "$XDG_RUNTIME_DIR/isolation-sibling-marker"
tmux new-session -d -s isolation-smoke 'sleep 60'
tmux new-session -d -s isolation-sleeper "bash -c 'sleep 300' isolation-sibling-process"`)

	execute(0, "sh", "-c", `set -eu
test "$(cat "$HOME/marker")" = first
test ! -r "$1/marker"
test ! -r "$1"
if ls /data/workspaces >/dev/null 2>&1; then exit 1; fi
test ! -r /data/.shared-worker/state.json
if ls /data/.shared-worker >/dev/null 2>&1; then exit 1; fi
test ! -e /tmp/isolation-sibling-marker
test ! -e /run/isolation-sibling-marker`, "isolation-test", siblingHome)

	if tier == "namespace" || tier == "container" {
		execute(0, "sh", "-c", `set -eu
test ! -w /usr
test ! -w /etc
test ! -e /data/workspaces
test -z "${VMBOX_SHARED_TOKEN:-}"
found=0
self=$$
for entry in /proc/[0-9]*; do
  pid=${entry#/proc/}
  if [ "$pid" = "$self" ] || [ "$pid" = "$PPID" ]; then continue; fi
  if [ -r "$entry/cmdline" ] && tr '\000' ' ' < "$entry/cmdline" 2>/dev/null | grep -q isolation-sibling-process; then found=1; fi
done
test "$found" = 0`)
	}

	execute(0, "sh", "-c", `test "$(cat "$HOME/marker")" = first; test "$(tmux list-sessions -F '#{session_name}' | grep -c isolation-smoke)" = 1`)
	execute(1, "sh", "-c", `test "$(cat "$HOME/marker")" = second; test "$(tmux list-sessions -F '#{session_name}' | grep -c isolation-smoke)" = 1`)
	execute(0, "/usr/local/bin/vmbox-runtime", "health")
	execute(1, "/usr/local/bin/vmbox-runtime", "health")
}

const sharedBlenderMCPProbe = `
import asyncio, os, socket, sys, time
from mcp import ClientSession, StdioServerParameters
from mcp.client.stdio import stdio_client

async def main():
    port = os.getuid()
    for attempt in range(60):
        try:
            with socket.create_connection(('127.0.0.1', port), timeout=1):
                break
        except OSError:
            await asyncio.sleep(1)
    else:
        raise RuntimeError('Blender did not listen on workspace port')
    env = dict(os.environ, BLENDER_HOST='127.0.0.1', BLENDER_PORT=str(port), BLENDER_MCP_SAFE_MODE='1', DISABLE_TELEMETRY='true')
    params = StdioServerParameters(command=os.path.expanduser('~/.local/bin/blender-mcp'), env=env)
    async with stdio_client(params) as streams:
        async with ClientSession(*streams) as session:
            await session.initialize()
            result = await session.call_tool('get_scene_info', {'user_prompt': 'Verify isolated disposable workspace scene'})
            assert not result.isError, result
            text = '\n'.join(getattr(item, 'text', '') for item in result.content)
            assert sys.argv[1] in text, text
            print('Blender MCP workspace verified:', sys.argv[1], port)

asyncio.run(main())
`
