# Shell-first release verification

Verified against controller revision `0bed1dafda7d4870d2015d21169086cfc47239b5`
on 2026-09-05. This supersedes the old implementation report's rollout status,
not its unrelated untested coverage.

| Requirement | Evidence |
|---|---|
| One persistent creation dialog | Real PTY form tests cover Unicode paste, inline choices, retry, cancellation, terminal restoration and one alternate-screen lifetime. Creation tests cover deferred uploads, retained profile state and no writes on cancel. |
| Create and connect; shell-first access | Prior live `shell-check-0905` creation automatically attached, printed specs and executed shell input. Plain reconnect retained shell PID 181 and background PID 231. README documents explicit leave-running/hibernated alternatives. |
| Optional arbitrary startup command | Live `lifecycle-proof-0905b --start-cli ...` wrote one marker and exited 7, leaving a functional shell. After hibernate/resume the marker still had exactly one line. No automatic Claude/Codex launch occurred. |
| One-shot agent selection | Real PTY selector tests cover Codex, Claude and shell. CLI tests cover explicit positional/flag arguments. These selector tests do not claim live execution of every agent. |
| Actual exit code and automatic hibernate | Live shell task `7c5b7bc5-c0be-49b2-8c6d-f7fe00000000` computed `37 * 19`: stored output `lifecycle-proof-0905b=703`, state `exited`, exit code 7. Box reached `hibernated / saved` at 21:38:48 UTC without a manual hibernate request. |
| Resume requested during hibernate | Plain CLI open showed `hibernating · detaching-volume (resume queued)`, then verifying/sanitizing phases, allocation and restoration. Independently observed `running / restored` at 21:41:09 UTC. |
| Remembered shell and volume | Session `shell-eefb92148b8d` and volume `e4b0806e-c5b7-4681-9399-f54c9d2bafd4` retained across hibernation. Restored shell executed `41 * 17` as 697. PID changed from 251 to 106, as expected after compute release; hibernation is not live-process checkpointing. |
| Telegram real-agent delivery | Owner-message sequences 1–5 each have correlated `replyState=sent`: Telegram accepted actual agent replies. Worker checkpoint `after=5,pending=false` survived controller rollout. This does not prove messages were read. |

Full Go tests and vet passed with PostgreSQL integration enabled. Historical
uncommitted startup/reconciliation drafts were preserved and excluded from
commits/deployment. README describes current commands and limitations.

## Scope and cleanup

The user approved a temporary third slot. Only disposable box
`lifecycle-proof-0905b` (ID `6a253f7b-a454-4879-8a7c-e53500000000`) was mutated
for these final lifecycle checks. User box `test` and running
`coworker-telegram` were not interrupted. Cleanup is pending final confirmation.

Known cosmetic issue: the tmux footer can render a literal `nobold]` and initially
empty metadata. The specs welcome and shell access work; this issue is not claimed
fixed. Telegram controller slash-command expansion remains in [TODO](TODO.md).
