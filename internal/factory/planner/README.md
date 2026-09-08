# Local planning adapter (Linux)

`Run(context.Context, Request) (Result, error)` invokes installed `codex` or
`claude` directly via argv in the validated workspace. PATH, installed CLI,
provider configuration, hooks and authentication are trusted box configuration.
No model/auth overrides or approval bypass flags are supplied. This is not an
OS isolation boundary against a hostile CLI or another process of the same UID.

Codex: `exec --sandbox read-only -c approval_policy="never" --ephemeral
--output-schema ... --output-last-message ...`, with multi-agent features disabled.
Images are attached with repeatable `--image` flags after decoding and private
staging. Claude: `-p --permission-mode plan --permission-prompts none --tools
Read,Glob,Grep --strict-mcp-config --no-session-persistence --output-format json
--json-schema ...`; consumes only the `structured_output` of a success envelope.
Claude images are explicitly unsupported pending live capability proof. PNG/JPEG
only, max 8 images, 10 MiB/25 MP each, 40 MiB total; WebP is unsupported here.

All paths must be absolute, clean and contain no symlink components. Workspace
and destination directory are pinned by file descriptors. ResultPath must be a
fresh filename in an existing directory, allocated per attempt. Persistence uses
a private 0600 temporary file, fsync, atomic no-replace rename and directory fsync.
A directory-fsync failure can return an error after the result became visible.
Existing files (including stale output) are never replaced; simultaneous writers
cannot clobber the winner. Image bytes are copied from validated file descriptors.

Only a complete, validated agent document from a successful CLI is persisted.
Document is bounded to 300000 bytes; Claude's transport envelope is capped at
2 MiB. Oversized documents return `Truncated=true` and an error. CLI logs are
discarded, never parsed as plans or exposed in errors. Codex's temporary output
file is read with a bound after exit; disk quotas remain a box responsibility.
Execution is limited to ten minutes or the earlier caller deadline. Cancellation
kills only the new process group; descendants that deliberately escape that group
require box-level containment. ExitCode is nil for signal termination or failure
to start; Signal records the actual terminating signal. No exit is synthesized.

The schema admits only proposal fields and future checks. Core controller-owned
metadata and feature execution state are excluded and rejected locally. Free text
remains an untrusted agent claim, never test evidence. Repository/user context
cannot extend the required schema. The adapter validates against the core
`factory.ParsePlannerResult` as well as its restricted shape.

Integration seam: this synchronous API intentionally does not implement
`factory.PlannerRunner.Start/Observe`. The caller must stage authorized inputs,
apply saved profiles through trusted box provisioning, allocate attempt-specific
result paths, own idempotency/leases, and map returned evidence into Observation.
The core fills revision/attempt/base metadata. No shared files changed.

Sources inspected alongside local CLI help:
- https://developers.openai.com/codex/noninteractive
- https://code.claude.com/docs/en/headless

Tests named `Controlled` use explicitly fake shell executables to verify transport
and failure handling; they establish no model capability. `TestLive*` uses the
actual installed CLI only when PLANNER_LIVE_CODEX=1 / PLANNER_LIVE_CLAUDE=1.
The Codex live test generates visual pixels at runtime; its prompt and attachment
name do not contain ground truth. No fixture supplies the live response.
