#!/usr/bin/env bash
# Disposable worker-image smoke test for managed agent instructions.
#
# Verifies, inside a throwaway container and as the unprivileged workload user:
#   1. `vmbox-runtime sync-instructions` writes one canonical Markdown file and
#      links it into the Codex, Claude, and OpenCode global instruction slots
#   2. re-applying identical content is a no-op and changing content updates it
#   3. selecting none removes only vmbox-owned links
#   4. a pre-existing user file is never overwritten (reported as a conflict)
#   5. the real Codex binary loads the synced instructions into its
#      model-visible prompt (`codex debug prompt-input`, no provider call)
#   6. Claude and OpenCode resolve the linked global instruction file
#
# Usage: bash tests/worker-instructions.sh IMAGE
set -euo pipefail

image="${1:?Usage: bash tests/worker-instructions.sh IMAGE}"
marker="VMBX_INSTRUCTIONS_SMOKE_MARKER"

docker run --rm --name "vmbox-instructions-disposable-$$" \
  --network none --user 10001:10001 \
  --tmpfs /data:rw,uid=10001,gid=10001,mode=0700 \
  --env HOME=/data/home --env VMBOX_WORKSPACE_ROOT=/data --workdir /data \
  --entrypoint /bin/bash "$image" -ceu '
    home="$HOME"
    mkdir -p "$home" /data/workspace
    export PATH="$home/bin:$PATH"

    payload() { printf "{\"markdown\":%s}" "$(printf %s "$1" | python3 -c "import json,sys;print(json.dumps(sys.stdin.read()))")"; }

    apply() {
      payload "$1" | vmbox-runtime sync-instructions > /tmp/digest
      printf "  applied digest %s\n" "$(cat /tmp/digest)"
    }

    canonical="$home/.config/vmbox/instructions.md"
    codex_slot="$home/.codex/AGENTS.md"
    claude_slot="$home/.claude/CLAUDE.md"
    opencode_slot="$home/.config/opencode/AGENTS.md"

    echo "[1] link one canonical source into every agent slot"
    apply "# Worker smoke\nAlways answer with the marker '"$marker"'.\n"
    test -f "$canonical"
    for slot in "$codex_slot" "$claude_slot" "$opencode_slot"; do
      test "$(readlink "$slot")" = "$canonical" || { echo "slot $slot does not link the canonical file"; exit 1; }
      grep -q "$marker" "$slot" || { echo "slot $slot does not resolve the markdown"; exit 1; }
    done
    test "$(stat -c %a "$canonical")" = "600"
    test "$(stat -c %a "$(dirname "$canonical")")" = "700"
    test "$(id -u)" = "$(stat -c %u "$canonical")" || { echo "canonical file is not owned by the workload user"; exit 1; }

    echo "[2] reapplying identical content is a no-op; new content replaces it"
    before="$(stat -c %Y:%s "$canonical")"
    apply "# Worker smoke\nAlways answer with the marker '"$marker"'.\n"
    test "$(stat -c %Y:%s "$canonical")" = "$before" || { echo "identical re-apply rewrote the canonical file"; exit 1; }
    apply "# Worker smoke\nAlways answer with the marker '"$marker"' plus a new line.\n"
    grep -q "plus a new line" "$canonical" || { echo "content change was not applied"; exit 1; }

    echo "[3] a pre-existing user file is preserved, never overwritten"
    rm -f "$claude_slot"
    printf "user-managed claude memory\n" > "$claude_slot"
    apply "# Worker smoke\nAlways answer with the marker '"$marker"'.\n"
    grep -q "user-managed claude memory" "$claude_slot" || { echo "user file was overwritten"; exit 1; }
    test "$(readlink "$codex_slot")" = "$canonical" || { echo "codex slot lost its link after a sibling conflict"; exit 1; }

    echo "[4] selecting none removes only vmbox-owned links"
    apply ""
    test ! -e "$canonical" || { echo "canonical file survived none"; exit 1; }
    test ! -e "$codex_slot" || { echo "owned codex link survived none"; exit 1; }
    test ! -e "$opencode_slot" || { echo "owned opencode link survived none"; exit 1; }
    grep -q "user-managed claude memory" "$claude_slot" || { echo "none deleted a user-managed file"; exit 1; }

    echo "[5] the real Codex binary loads synced instructions into its prompt input"
    apply "# Worker smoke\nAlways answer with the marker '"$marker"'.\n"
    cd /data/workspace
    codex debug prompt-input "smoke test" > /tmp/prompt.json
    grep -q "$marker" /tmp/prompt.json || { echo "Codex did not receive the instructions"; exit 1; }

    echo "[6] Claude and OpenCode resolve their global instruction slots"
    test -r "$claude_slot" && grep -q "$marker" "$claude_slot"
    test -r "$opencode_slot" && grep -q "$marker" "$opencode_slot"

    echo "Managed agent instructions verified offline as UID $(id -u)"
  '
