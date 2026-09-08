# Independent review adapter

`Run(ctx, Request)` runs installed Codex or Claude with saved CLI authentication
and default/saved model selection. Supply `Agent` (`codex` or `claude`), an absolute
clean `Workspace`, full lowercase `BaseSHA` / `CandidateSHA`, the coordinator's
`ApprovedFeature`, independently collected `verification.Report` as
`CheckEvidence`, and a fresh absolute `ResultPath` in an existing private directory
outside the source checkout. No environment API keys are passed.

The adapter checks the approved argv/cwd/deadlines and complete passing verification
report, independently observes HEAD and raw checkout cleanliness, and supplies the
actual Git diff **and every candidate file's committed contents** to the model.
It does not rely on builder prose or on the model claiming it read a file. It audits
HEAD and cleanliness again after termination, including cancellation. Repository
Git hooks, filters, fsmonitor and external diff configuration are excluded from
these audits using verification's temporary Git-directory pattern.

Codex uses `exec --sandbox read-only`, approval policy `never`, ephemeral execution
and a strict output schema; multi-agent features are disabled. Claude uses safe
mode, plan permissions, no permission prompts, only Read/Glob/Grep, strict MCP
configuration and structured JSON. Neither uses bypass flags or a model override.
The review prompt forbids running the candidate, dependency installation, mutation,
pushes and publication. This package has no publication operation.

`Result` records OS exit code **or** numeric signal, start/finish/effective deadline,
timeout/cancellation/truncation, independently observed SHAs and prompt digest.
The ten-minute adapter deadline is shortened by the caller's context. Owned process
groups are killed before reaping their leader, using `waitid(WNOWAIT)` as in
verification, avoiding signaling a reused group identity. Detached descendants
that deliberately escape the group are outside this mechanism.

A valid review has exactly `candidateSha`, substantive `summary`, an explicit
`blockingFindings` array and boolean `approved`. Duplicate/unknown/missing/null
fields, trailing documents, contradictory approval and wrong SHA are rejected.
CLI streams are capped at 2 MiB each and structured output at 64 KiB; overflow is
rejected. A valid negative review returns **nil error, exit 0, Accepted=false**.
Infrastructure/validation failures return an error while preserving observed
process evidence. Consumers must require `Accepted`, `SourceUnchanged`, actual
exit 0, and no signal/deadline/cancellation/truncation; never use exit 0 alone.

After path validation, both positive and negative attempt reports are written as
0600 JSON, fsynced and atomically published using `RENAME_NOREPLACE` through a
pinned directory FD. Existing names and symlink components are rejected. The model
never receives ResultPath; its temporary output is separate and removed. Raw CLI
diagnostics are bounded in memory and not returned or persisted.

Limits and trust boundaries:

- Linux, SHA-1 repositories with a real `.git` directory; no linked worktrees or
  submodules. Full source+diff must fit 512 KiB; non-text files are rejected rather
  than silently omitted. Approved context has a separate 512 KiB cap.
- The coordinator must authenticate approved-plan and verifier artifacts and
  preserve attempt/box/task identities. This adapter validates their shape and SHA
  binding, not provenance, and does not implement scheduling or execution-store
  integration. Source must be exclusively owned for the attempt; before/after
  audits cannot detect a concurrent mutation that is later restored.
- The installed CLI, its saved authentication/model configuration and user home
  are trusted. The environment is an allowlist of runtime locations and Git
  noninteractive settings: no GitHub/controller/cloud/provider keys, SSH agent,
  proxy settings or ambient Git configuration. Saved CLI auth stays in its normal
  home. Do not configure publishing connectors or hooks for this reviewer user.
  Read-only agent permissions and process cleanup are **not a hostile-code
  sandbox**, filesystem credential isolation or an adversarial egress boundary.
- Claude's real missing-auth path is verified; authenticated Claude success has
  only synthetic protocol coverage here. See EVIDENCE.md.

Run in the vmbox only (Go 1.26):

```sh
go test -count=1 ./internal/factory/reviewer
CGO_ENABLED=1 go test -race -count=1 ./internal/factory/reviewer
FACTORY_LIVE_REVIEW=codex go test -count=1 -run '^TestLiveReview$' -v ./internal/factory/reviewer
FACTORY_LIVE_REVIEW=claude go test -count=1 -run '^TestLiveReview$' -v ./internal/factory/reviewer
```

`TestLiveReview` creates real baseline/candidate commits and runs a real independent
verification check before invoking the installed CLI. Its assertions check the
result contract and blocking findings, not canned model wording; inspect the
logged findings. Claude unavailability is explicitly logged, never an AI pass.
All tests named Synthetic use shell fixtures, not model evidence.

CLI permissions reference: [official Codex CLI documentation](https://developers.openai.com/codex/cli/reference/).
Installed CLI help was also inspected for both agents' exact supported flags.
