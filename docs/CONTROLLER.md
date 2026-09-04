# Controller operations

`vmbox-controller` uses PostgreSQL for accounts, exactly two human roles (`owner`, `user`), hashed access tokens, encrypted-provider-credential slots, runs, events, questions, host capacity, and immutable audit entries. Run and question queries are always account-scoped.

Required configuration:

```text
DATABASE_URL
VMBOX_CONTROLLER_LISTEN=:8080
VMBOX_CONTROLLER_URL=https://controller.example
VMBOX_IMAGE=ghcr.io/owner/vmbox-service@sha256:...
VMBOX_ENCRYPTION_KEY=<base64-encoded-32-byte-key>
```

Provider credentials remain in environment variables in standalone mode. Controller mode requires an account-scoped credential in the encrypted vault; scheduling resolves the selected credential for that account and never sends it to a workload. Never put credentials into container images, CLI contexts, reusable profiles, logs, or workload environment. `vmbox-controller bootstrap` prints its owner token once.

For a single-account Railway controller, a scoped `RAILWAY_TOKEN` environment variable is imported at startup as the encrypted `railway/primary` vault credential. Railway's injected `RAILWAY_PROJECT_ID` and `RAILWAY_ENVIRONMENT_ID` fence that credential to the current project and environment. The plaintext is never returned by the API or copied into a worker.

Controller-initiated Railway OpenSSH requires a dedicated Railway-registered key. Store its standard-base64 private key as the sealed controller variable `VMBOX_RAILWAY_SSH_PRIVATE_KEY_B64`; startup materializes it with mode `0600`, OpenSSH uses only that identity, and it is never copied into a slot or workload image.

`VMBOX_INITIAL_COMPUTE_BOX_SLOTS` optionally seeds Railway fleet capacity for
that single account during first startup. It is ignored after an owner has
configured the fleet, so later changes in the web UI or CLI are never
overwritten by a redeploy.

Provision or repair a Railway controller from a Railway context. The command prints the billable plan before requiring explicit confirmation, reuses the exact `vmbox-controller` and `vmbox-postgres` service names, preserves existing secrets and accounts, and waits for `/healthz` before saving the endpoint. When `--endpoint` is omitted, it reuses or generates a Railway domain. Local source deployment stages only `Dockerfile`, `.dockerignore`, `entrypoint.sh`, `go.mod`, `go.sum`, `cmd/`, and `internal/`; other workspace files are never uploaded.

```bash
vmbox controller init \
  --source . \
  --box-image ghcr.io/owner/vmbox-service@sha256:... \
  --yes

# A healthy controller is returned immediately; an unhealthy one is repaired
# by redeploying the same service and database.
vmbox controller ensure --yes
```

Use `--controller-image IMAGE@sha256:DIGEST` instead of `--source` when a published controller image is available. The controller image and default box image are stored separately so later repair never deploys the box image as the controller. A newly generated owner token is printed once; save it in the context's configured token environment.

Before accepting a box command, the controller bootstraps its bundled runtime and selected tools into generic Debian/Ubuntu-compatible provider images. Project-built images advertise their preinstalled components, allowing bootstrap to skip the slow apt/npm install and refresh only the small runtime payload.

Owner/user and provider credential management:

```bash
vmbox users add alice --role user       # prints the new token once
vmbox users list
vmbox users remove <user-id>

export RAILWAY_VAULT_JSON='{"token":"..."}'
vmbox credentials set railway primary --secret-env RAILWAY_VAULT_JSON \
  --config '{"projectId":"...","environmentId":"..."}'
vmbox credentials list
vmbox credentials remove railway primary
```

Set `--provider-credential primary` on the controller context to select a credential when an account has more than one credential for that provider.

Webhook, Telegram, and Discord secrets are accepted only through an environment variable containing a JSON object. Telegram and Discord require both user and chat/channel allowlists. Destinations can be independently tested and removed:

```bash
export WEBHOOK_JSON='{"url":"https://hooks.example/vmbox","signingSecret":"..."}'
vmbox notifications setup webhook ops --secret-env WEBHOOK_JSON
vmbox notifications test webhook ops
vmbox notifications remove webhook ops

export TELEGRAM_JSON='{"token":"...","webhookSecret":"random-secret"}'
vmbox notifications setup telegram team --secret-env TELEGRAM_JSON \
  --config '{"chatId":"123","userMap":{"456":"CONTROLLER-USER-UUID"}}' \
  --allow-user 456 --allow-chat 123
```

For two-way `needs_input` replies, point Telegram's webhook or Discord's Interaction Endpoint URL at `https://CONTROLLER/v1/integrations/KIND/ACCOUNT_ID/NAME`. Telegram verifies `X-Telegram-Bot-Api-Secret-Token`; Discord verifies every Ed25519 signature. Each external user ID must be both allowlisted and mapped in `config.userMap` to an active controller user UUID, so accepted answers retain tenant boundaries and audit identity. Telegram users reply with `/answer QUESTION_ID TEXT`; Discord notifications include an Answer button and modal. Discord secrets require `webhookUrl` and `publicKey`; optional `config.allowedGuilds` narrows accepted guilds.

The API requires bearer authentication and `Idempotency-Key` for run creation. Reusing a key with a different request is rejected. Controller-created OCI runs require an immutable `@sha256:` image. It applies an account rate limit and records state, ordered events, heartbeats, last visible output, questions, answers, and audit data. On every startup and then periodically, the controller reconciles durable runs with providers, resumes interrupted provisioning/cleanup, detects missing or failed boxes, and enforces every run's absolute `maxTtl`. The controller contains no GitHub, repository, agent, model, prompt, or source-indexing logic.

## Fleet UI and persistent logical boxes

Open the controller URL in a browser and enter an owner or user token (the production operator may provision a temporary password-shaped token). The credential is kept in browser session storage, never local storage. The responsive Scandinavian-style interface supports desktop and phone layouts, persistent per-box chats, durable groups with per-message recipients, provider-visible external boxes, fleet capacity, notification destinations, and a read-only terminal mirror. Sending to an offline logical box allocates a slot and starts Claude by default; the conversation records an online system message when the agent is ready. Closing or backgrounding the page aborts pending requests and stops polling; it does not hibernate a box or leave the UI stuck in a reconnect loop.

Owners can set the number of reusable compute slots from the Fleet view. Slot services are capacity, not boxes: idle slots appear only in Fleet, and an occupied slot appears in the conversation roster exactly once through its assigned logical box. Provider services outside the fleet are shown separately as external and not controller-managed. Persistent logical boxes own their Railway volume independently of a slot: allocation mounts that volume into a free service, while hibernation snapshots tmux state, unmounts the volume, sanitizes the service, and returns the slot to the pool. When every slot is busy, allocation remains queued. Deleting a logical box requires typing its exact name and deletes only its volume; the fleet service count is unchanged.

```bash
vmbox fleet status
vmbox fleet slots 4
vmbox boxes create dev
vmbox dev
vmbox resume
vmbox auth dev
vmbox task dev --agent codex --prompt 'Review the repository and report findings'
vmbox boxes hibernate dev
vmbox boxes delete-volume dev
```

`vmbox NAME` resolves the currently fenced deployment through the controller and then uses direct OpenSSH with a reusable control connection. `vmbox resume` selects from the same logical boxes, and `vmbox boxes open NAME` remains an explicit equivalent for scripts. `vmbox task` interactively chooses a logical box and agent, or accepts scriptable box, agent, session, and prompt arguments; task creation automatically allocates compute when the logical box is hibernated and leaves the tmux session running. `vmbox auth NAME` explicitly uploads the selected or active local Codex, Claude, and OpenCode profiles plus the active GitHub CLI credential, selected Git protocol, and Git identity directly over that SSH connection; `--no-github` is an explicit agent-only override. Opening a box never uploads local credentials implicitly, and the controller never receives those files. The files remain on the logical box's persistent volume through hibernation, are owned by `vmbox:vmbox`, and both agent and GitHub authentication are verified as that unprivileged user.

Leaving the interactive client keeps both the logical box and tmux session running by default. The exit prompt separately offers hibernation or exact-name volume deletion. Logical-box identity and compute-slot metadata are persisted with the tmux snapshot, so restored sessions show the current provider, resources, workspace, box, and slot instead of fallback values. Tmux uses `Ctrl-a` as its prefix and the bottom guide lists writing, scrolling, detaching, and QWERTZ-safe keys.

Use TLS at the controller ingress. Interactive first use prompts for the controller connection without storing its token. A missing controller fails closed in noninteractive use, and standalone provider management requires an explicit `--standalone`. The compatibility contract for external schedulers is `v1alpha1`; see `openapi.yaml`.
