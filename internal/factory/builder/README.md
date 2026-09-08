# Local feature builder

```go
type Request struct {
    Agent, Workspace, Prompt, Branch, BaseSHA, ResultPath string
    Images []string
}
func Run(context.Context, Request) (Result, error)
type Result struct {
    ExitCode *int
    Signal int
    CandidateSHA string
    Clean bool
    Summary string
    Truncated bool
    HEAD string
    Branch string
}
```

Linux adapter for the installed `codex` / `claude` on trusted PATH. The caller must supply an exclusively owned, privately staged checkout root at the exact full BaseSHA, a trusted factory-derived `factory/…` branch, an approved feature prompt, a deadline, and a fresh result path in a private directory outside the checkout. Git metadata must reside inside the checkout. No fetch, source staging, identity configuration, push, controller integration, or durable retry is performed here.

Run checks the exact HEAD and a clean baseline (including untracked, ignored and submodule changes), then creates a new branch with a no-replacement `update-ref` and points HEAD at it using `symbolic-ref`; the already matching worktree needs no checkout or smudge filters. Codex runs `exec --approve-for-me --disable multi_agent --color never -`; the installed CLI documents that approval review uses workspace-write. It rejects combining this flag with `--sandbox`. Claude runs `-p --permission-mode acceptEdits --permission-prompts none`, allows Bash/Edit/Write/Read/Glob/Grep, and exposes only those tools. No model override, authentication bypass or dangerous permission skip is used. Normal saved agent authentication/configuration remains available through HOME, XDG_CONFIG_HOME, CODEX_HOME and CLAUDE_CONFIG_DIR; agent API auth variables are retained. Inherited GIT_, VMBOX_, CONTROLLER_, FACTORY_, RAILWAY_, GH_ and GITHUB_ namespaces (including GH_TOKEN and GITHUB_TOKEN) are removed; system/global Git config is disabled, so staging must supply local commit identity. Authorization to implement does not authorize changing acceptance criteria or publishing.

The prompt requires implementation, optional exploratory checks, a commit on the assigned branch, and an honest summary. It forbids pushes, PRs, GitHub writes, deployment, credential access and spawning workers. These are agent instructions, not a network security boundary; deployment isolation and authorization remain caller responsibilities.

A candidate requires actual process exit 0, a new commit on the assigned branch, ancestry from BaseSHA, and a clean checkout. Actual HEAD/branch/clean state are inspected after the process, including failures/cancellation. Signals have nil ExitCode and the actual Signal; startup failures have neither. CandidateSHA remains empty on invalid builds. These facts are BUILD evidence only: an empty commit or incorrect feature can satisfy these mechanical conditions. Acceptance verification and review are separate stages and must inspect the candidate independently.

Trusted inspection uses fresh Git metadata with copied refs/index and read access to source objects, without loading checkout-local config/includes. Overriding attributes disable filters and byte normalization; diffs disable external drivers/textconv. Clean checks compare staged changes and rebuild a fresh index to avoid stat-cache/assume-unchanged/skip-worktree concealment. Submodules and linked-worktree metadata are rejected. Agent-invoked Git still uses its normal local repository configuration.

Summary contains trusted Git change counts or a fixed diagnostic category, never the agent's prose. stdout/stderr are drained and discarded; Truncated records crossing the combined 1 MiB transcript budget. No transcript, prompt, filenames, authored source, credentials or raw diagnostics are persisted in the result. Git outputs have a 64 KiB cap; excessive inspection output fails closed. Result JSON is bounded, mode 0600, fsynced, atomically renamed with NOREPLACE, and the parent directory is fsynced. Existing results are rejected. After path validation/reservation, failures also persist. Callers must check both error and result; persistence errors are returned. A temporary exclusive lock serializes a result path; a crash may leave the lock, and recovery belongs to the external durable job journal. Do not retry an attempt inside this adapter.

Linux `waitid(WNOWAIT)` observes leader exit without reaping it, pinning its PID during residual group cleanup. Cancellation and cleanup share a mutex; cancellation is disabled before `cmd.Wait` reaps the leader, so no later group kill can target a recycled ID. This ownership pattern also covers trusted Git subprocesses. Cancellation kills the process group. Residual group members are also killed on normal leader exit before Git inspection. Children that deliberately escape their process group require container/cgroup enforcement by the caller. The adapter is not an OS sandbox or hostile-code containment boundary. Environment filtering does not remove arbitrarily named secrets, credentials on disk, inherited file descriptors, or network access. Saved agent auth/config is intentionally accessible; the caller must separate controller/publication credentials and enforce filesystem/network/process isolation.

Codex PNG/JPEG inputs are validated and copied to private temporary files (8 images, 10 MiB each, 40 MiB total, 40 million pixels each), then passed with the real `--image` CLI flag. Claude images are explicitly rejected because this adapter has no verified Claude image-input protocol. Prompt text is passed on stdin, never through a shell.

Tests use real isolated Git repositories and explicitly named controlled executable fixtures. `BUILDER_LIVE_AGENT=codex go test ./internal/factory/builder -run '^TestLiveImplementation$' -v -count=1` opts into the real installed CLI and normal saved auth; `claude` selects the corresponding live trial. No live test has a remote. See EVIDENCE.md for this box's results.

CLI references checked 2026-09-08: installed Codex 0.153.0 `exec --help`, Claude Code 2.1.259 `--help`, and official OpenAI [CLI reference](https://developers.openai.com/codex/cli/reference) / [security](https://developers.openai.com/codex/security). Installed behavior is exercised by the live trial; flags may require revision for other CLI versions.
