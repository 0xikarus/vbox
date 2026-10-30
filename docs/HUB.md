# Single-host hub

The hub manages multiple isolated Docker standalone boxes on one Linux host.
It reuses the standalone runtime, Docker lifecycle, portable profile import,
chat delivery receipts, MCP, desktop and TMUX. It has its own SQLite registry;
it does not need a fleet controller, PostgreSQL or provider pools.

## Install

From the repository checkout, install the CLI, then install the hub locally or
through your existing SSH alias. The host must already have native Linux Docker.

```sh
./install.sh
vbox hub install --host home-server
```

The wizard shows host resources and asks for the shared worker budget, host and
local-model reserves, maximum running boxes, default agent and hub password. OpenCode model
endpoint settings appear only when you choose its Custom configuration mode.
For explicit configuration (sizes without suffixes are MiB):

```sh
vbox hub install --host home-server \
  --listen 0.0.0.0:8091 --data /home/you/.local/share/vbox-hub \
  --cpus 4 --memory 8g --max-running 4 \
  --reserve-cpus 2 --reserve-memory 4g --model-memory 32768 \
  --model-url http://model-host:8000/v1 --model YOUR-MODEL --model-vision \
  --password-file /private/hub-password --no-dialog
```

Choose budgets that fit the actual host. `--model-memory` is an additional
explicit RAM reservation for an inference service running outside the workers;
the hub does not start or reconfigure that service. The default listener is
host loopback; choose a LAN binding intentionally and configure HTTPS separately
for public exposure. Passwords have no minimum length or complexity requirement.

Installation builds `Dockerfile.standalone` and `Dockerfile.hub` on the target.
The small trusted hub manager runs in its own container, capped at one CPU and
512 MiB RAM, with bounded logs. Autostart with Docker is **off by default**.
Installation starts the hub immediately; select autostart in the wizard or pass
`--autostart` to enable automatic restarts after crashes or Docker/host restart.
Reinstalling without that option disables autostart again. Only this manager
gets the Docker socket and read-only access to Docker's storage filesystem for
capacity observations. Agent boxes receive neither the socket nor the hub data
directory or API token. Docker access makes the hub manager a trusted host-admin
component; it must not execute agent workloads inside itself.

The private data directory contains `hub.json`, `hub.sqlite`, and owned instance
metadata. `hub.json` includes a password hash and API token; profile archives and
private upstream sessions are never included in public box JSON. Keep this
directory private. Re-running `install` retains the existing configuration and
worker files, builds the new images, and replaces only the matching hub manager.
Existing worker containers continue using their original image.

`vbox hub init` writes configuration without starting the manager. For an already
prepared Docker host, `vbox hub serve --data DIR` runs it directly. A directory
lock prevents two managers from allocating the same registry concurrently.

## Use the hub

Open the configured hub URL. The home page shows all boxes, actual CPU/RAM
observations, resource allocations and queue reasons. Create boxes with agent,
model, saved profile, tools, local proxy, CPU/RAM and an optional initial goal.
Open **Saved profiles** to upload Claude, Codex or OpenCode configuration/login
files from your computer. The file picker supports adding files from different
folders. It shows filenames and sizes, never credential contents. Select the
agent first; the box profile picker shows matching saved profiles only.

For OpenCode, **Saved provider / profile** uses the uploaded model, providers and
API keys as configured. Only **Custom endpoint** asks for a model endpoint, model
ID and image capability; those settings override the selected model for that box.
Claude and Codex use their native profiles and do not inherit OpenCode's endpoint.
OpenCode JSONC is normalized in the uploaded copy so the shared MCP setup can add
its tools while preserving provider settings. Host profile files remain unchanged.

The goal is submitted once through the existing idempotent chat-delivery path
when the agent is ready.

Opening a box shows chat beside Desktop with TMUX directly underneath. All
worker HTTP, image and WebSocket traffic goes through `/boxes/BOX_ID/` on the hub;
worker gateway ports bind only to host loopback. Live-view links remain read-only
and can be shared without signing into the hub. They do not authorize chat,
TMUX or desktop control. Session storage is scoped by box path.

Signing out or changing the password invalidates browser sessions and closes
active proxied control connections. Password changes require the old password.
The workspace's shutdown button records a stopped box in the hub, so the
scheduler does not immediately restart it. Files remain until explicit deletion.

The same operations are available through the CLI:

```sh
vbox hub up --host home-server # manually start an installed, stopped hub
vbox hub status --host home-server
vbox hub create researcher --host home-server
vbox hub create tiny --host home-server --cpu .5 --memory 1g --no-dialog
vbox hub profile upload claude=/path/to/.claude --name 'My Claude profile' --host home-server
vbox hub profiles --host home-server
# After all referencing boxes have been deleted:
# vbox hub profile delete PROFILE_ID --yes --host home-server
vbox hub create coding --agent claude --profile-id PROFILE_ID --host home-server --no-dialog
vbox hub create local --agent opencode --model-source profile --profile-id OPENCODE_PROFILE_ID --host home-server --no-dialog
vbox hub list --host home-server
vbox hub set BOX_ID --cpu .5 --memory 1g --host home-server
vbox hub message BOX_ID --text 'Your goal' --key stable-message-id --host home-server
vbox hub messages BOX_ID --host home-server
vbox hub stop BOX_ID --host home-server
vbox hub start BOX_ID --host home-server
vbox hub delete BOX_ID --yes --host home-server
vbox hub shutdown --host home-server
```

Add `--data DIR` when using a separate registry, and `--url URL` to override API
URL discovery. Remote commands use SSH by default, supporting private listeners and `.local` names without exposing the API. An explicit `--url` opts into direct HTTP access. The creation wizard can discover local profiles and displays
source paths before importing. `--profile APP=PATH` supports explicit imports.
Profile archives are sanitized, stored privately, and selected later by opaque
IDs. Uploading a profile does not run its host hooks.

Shutdown stops the hub manager, retaining worker containers and data. Use
`vbox hub up --host home-server` to resume it without rebuilding or changing its
autostart policy. Deleting a box removes only resources whose ownership
labels match this hub and box: container, volume, network and instance directory.
A missing container does not prevent cleanup of its owned volume/network. The
registry keeps a deletion tombstone, clears the worker password, and does not
reuse that identity. Shared image caches and reusable saved profiles are retained.

## Resource and recovery rules

- Per-box Docker CPU, RAM, swap and PID limits enforce the allocated resources.
  There is no additional swap allowance. Combined enforcement is admission
  control over the sum of those hard per-box limits, rather than CPU pinning.
- Running, starting and stopping workers hold reservations. Admission is serialized
  and durable before Docker creation. Concurrent lifecycle operations are bounded. The oldest queued request holds its place
  when it cannot fit. All live worker identities are observed before new admission
  after a restart. Unverifiable inventory blocks new starts.
- The worker budget must leave the configured host/model reserves. Admission also
  checks current available RAM conservatively, including existing reservations.
  Severe RAM pressure stops managed workers while retaining their files.
  External host services can still consume their own resources; the hub does not
  impose quotas on an independently managed inference server.
- Live resource increases must fit the shared budget. Live RAM reduction is
  rejected; stop the box first. Edits reserve the larger old/new limits until
  Docker's effective settings are verified, so a crash cannot undercount them.
- Worker containers have Docker automatic restart disabled. The hub reconciles
  starts through admission again, with a bounded crash retry count. Repeated
  exits and a 15-minute startup deadline become visible failures retaining files.
- Hub restart retains workers and registry state. After Docker/host restart, the
  manager starts automatically only when installed with `--autostart`; otherwise
  start it manually with `vbox hub up`. Once running, it admits previously desired
  workers within the same budget. Explicitly stopped boxes remain stopped.
- Docker logs are capped at three 10 MiB files per container. The disk low-water
  guard blocks new starts and stops active workers while retaining files.
  This is a periodic free-space guard, **not a per-volume disk quota**.

Container mode shares the host kernel. VM provisioning, enforced host/LAN egress
isolation, automatic worker image upgrades and multi-host scheduling are outside
this implementation. A hub is a single-host manager, not a fleet provider pool.

## API reference

Use `Authorization: Bearer $HUB_TOKEN`. Retrieve the token privately with
`vbox hub token --host home-server --data DIR`; it is not printed during install.

| Method | Route | Purpose |
| --- | --- | --- |
| GET | `/api/status` | Host observations, policy, allocations and defaults |
| GET / POST | `/api/boxes` | List / create a box |
| GET | `/api/boxes/ID` | Poll box state and workspace URL |
| POST | `/api/boxes/ID/start`, `/stop`, `/delete` | Lifecycle operations |
| POST | `/api/boxes/ID/resources` | Edit `{ "cpu": 0.5, "memoryMiB": 1024 }` |
| GET / POST | `/api/boxes/ID/messages` | Read / send chat messages |
| GET | `/api/boxes/ID/messages/MSG/images/INDEX` | Read authenticated image results |
| GET / POST | `/api/profiles` | List metadata / upload portable profiles |
| PUT | `/api/policy` | Edit policy; reductions below live reservations fail |

Create returns HTTP 202 with an ID, state and URL. Supply `Idempotency-Key` for
safe creation retries; reusing a key with different settings fails. A request
that fits a single worker budget can remain queued until shared capacity frees.
Poll its state, then read messages. There are no completion webhooks.

```json
{
  "name": "research",
  "cpu": 0.5,
  "memoryMiB": 1024,
  "profiles": ["hp-OPAQUE_ID"],
  "tools": [],
  "goal": "Your task, including any callback the agent itself must perform."
}
```

The agent defaults to hub configuration. For OpenCode use `modelSource: "profile"`
with a saved OpenCode profile, or `modelSource: "custom"` with `modelURL`, `model`
and `vision`. Omitting `modelSource` uses profile mode when an OpenCode profile
is selected; otherwise it retains the configured hub default. Only custom mode
inherits default endpoint settings. Claude and Codex require an appropriate
imported login and ignore all custom model fields. Tools use the same IDs as
standalone, including `blender`, `foundry`,
`tor-socks5` or `cloudflare-warp` (the proxies are mutually exclusive).

Sending a message requires `Idempotency-Key` and `{ "text": "Your message" }`.
It returns 202 for durable submission, not proof that inference has finished.
Message history includes authenticated image URLs when the agent attaches files.
Profile upload accepts `{ "name": "Saved profile", "agent": "claude",
"archive": "BASE64_TAR" }`; use the CLI to build the portable archive safely.
The browser alternative is `"files": { "auth.json": "BASE64_FILE", ... }` instead
of `archive`. Supported filenames are Claude `.credentials.json`, `settings.json`
and `.claude.json`; Codex `auth.json` and `config.toml`; OpenCode `opencode.json`
(or `.jsonc`) and `auth.json`. Uploads are limited to 512 KiB per file and a 4 MiB
archive; paths, links and unrelated files are rejected. API responses return
profile metadata only. Delete an unused profile with `DELETE /api/profiles/ID`.

## Verification

The 2026-10-30 pgx ARM64 verification used a separate hub registry, two active
0.5-CPU / 1536-MiB workers and a third queued request. It verified:

- Actual Docker CPU/RAM/swap limits, private gateway bindings and bounded logs.
- Concurrent creation, stable idempotency keys, resource-edit rejection and queue
  advancement after a stop.
- Portable profile import, local Qwen native chat delivery, a real MCP reply and
  an initial goal submitted once across manager restart.
- Visible failed startup, released reservations and recovery after explicit retry.
- Unexpected hub process death with Docker auto-restart, and simulated host
  stop/start of only the owned hub/workers. The pgx host itself was not rebooted.
- Persistent workspace files, native conversation identity and stopped-box intent.
- Real Chromium login, proxied Desktop/TMUX, matching viewer widths, mobile
  layout, public read-only sharing, private chat and logout closing a live viewer.
- Removal of every disposable worker, volume, network, instance directory and
  fixture profile. The empty installed hub was retained.

One GiB was sufficient for initial idle checks but one OpenCode worker hit its
memory cap; full UI/recovery checks therefore used 1.5 GiB. These are test sizes,
not a promise that every tool or model workload fits that amount of RAM.

The reusable focused checks are `go test -race ./internal/hub ./internal/cli`,
`tests/standalone/hub-browser.mjs` (explicit isolated hub and CDP variables), and
`tests/standalone/entrypoint-env.sh IMAGE` (disposable, network-disabled image).
`tests/standalone/hub-profiles-browser.mjs` additionally verifies agent-filtered
profiles, custom-only endpoint controls, a real browser JSONC upload, native
OpenCode startup from that saved provider and an MCP chat reply. Set the explicit
hub/CDP/password-file variables plus `VMBOX_HUB_TEST_MODEL_URL` and
`VMBOX_HUB_TEST_MODEL` for a disposable local-model run. Set
`VMBOX_HUB_TEST_CONTEXT` to match a smaller backend context (default: 32768).
The fixture permits only the chat MCP tool. It deletes only its own
identified box and profile, retaining existing saved profiles.

The shell check ensures routing settings survive the worker privilege drop while
unrelated environment values are stripped. Full `go vet ./...`, internal Go tests
and the maintained shell checks also passed. Database-backed fleet integration
checks retain their normal disposable-database requirements.

The profile follow-up passed the full browser upload/provider/MCP flow against
pgx's then-active Qwen 3.5 4B backend with its 8192-token context. The former Qwen
27B endpoint on port 8000 was no longer running; its service was not restarted or
reconfigured. Both imported Codex profiles also started native sessions with
managed MCP and auto mode in a separate disposable registry, without inference.
The test registry, copied credentials, workers and temporary bridge-only model
forwarder were removed afterwards; the three owner profiles remained installed.
