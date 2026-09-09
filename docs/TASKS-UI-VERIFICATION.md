# Tasks UI integration — 2026-09-08

Branch: `proposal/software-factory`. Main remains unmerged. Production rollout
was explicitly approved on 2026-09-09; deployment evidence is recorded separately
in `TASKS-ROLLOUT.md`.

Implemented: Tasks navigation, idea/profile/images form, coordinator questions
and versioned plans, explicit Run approval, dependency scheduling, worker results,
actual exits/signals, retries, cancellation observation and final semantic verdict.
The general backend does not require a repository or GitHub App.

## Evidence

All builds/tests below run inside isolated vmboxes, not on the local machine.

| Check | Evidence | Result |
| --- | --- | --- |
| Tasks browser fixtures + gateway tests/vet | `d2bc557a-18d9-48da-8507-b06500000000`, `e827273` | Exit 0; 14/14 Chromium tests |
| Real PostgreSQL coordination and runtime process/TLS fixtures, race enabled | `d62b2792-5c94-403f-86d6-8fc500000000`, `7125c83` | Both taskflow packages passed |
| Full Go suite in that run | Same task | Failed: legacy remote-build SQL mock lacked the new shared-capacity query; corrected in `7fcc32e` |
| Final Go/PostgreSQL/race/vet/build run | `db0314c9-897c-4241-8687-373100000000`, `7fcc32e` | PASS: terminal exit 0, verified 2026-09-09 |
| Complete UI → real agents → final synthesis | Not run | Requires isolated preview backend and reachable HTTPS callback |

Earlier bootstrap attempts exited 127 (missing PostgreSQL) and 2 (CGO disabled).
The in-box script now installs PostgreSQL/GCC and explicitly enables CGO for race
tests. These failures occurred before tests, not as product assertions.

The final run was queued at 21:45:57 UTC. The box's reported state was
`hibernating`, last updated 21:44:37 UTC. A normal allocate/resume request was
accepted as queued but did not start verification during this observation.
The same task subsequently ran and exited 0; it was not cancelled or duplicated.
All Go tests, real PostgreSQL tests, both taskflow race suites, vet and build
passed. The later changes through `2f30265` only add deployment packaging and its
Docker context allowlist; Go application source is unchanged from this test.

The browser suite uses API fixtures. Runtime tests use real synthetic CLI
processes and TLS callbacks, not real model answers. Neither is a claim of a
completed live UI-to-agent journey. The earlier manual non-software model trial
is separately documented in `GENERAL-TASKS.md`.

## Scope and remaining gaps

- Source-free adapters currently use read-only agent tools. Software editing and
  builds remain in the separate software workflow; recursive delegation is absent.
- Codex image transport is implemented and fixture-tested; Claude images are
  explicitly unavailable. No live Claude or image acceptance claim.
- Lost receipts are recovered using read-only lookups; unknown outcomes do not
  trigger another agent run. Missing results still require operator reconciliation.
- Startup configuration is documented in `GENERAL-TASKS.md`. The secure local
  environment has no isolated factory DB/gateway/result endpoint configuration.
  Provisioning a preview must not silently change the production controller.
- Existing user drafts and unrelated work were preserved. Temporary test tooling
  and PostgreSQL artifacts are confined to the disposable test box.
