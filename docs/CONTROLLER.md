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

Provision or repair a Railway controller from a digest-pinned Railway context. The command prints the billable plan before requiring explicit confirmation, creates the controller and PostgreSQL services idempotently, configures a hash of the one-time bootstrap token, and saves the endpoint in the local context:

```bash
vmbox controller init --endpoint https://controller.example --yes
vmbox controller ensure --endpoint https://controller.example --yes
```

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

export TELEGRAM_JSON='{"token":"..."}'
vmbox notifications setup telegram team --secret-env TELEGRAM_JSON \
  --config '{"chatId":"123"}' --allow-user 456 --allow-chat 123
```

The API requires bearer authentication and `Idempotency-Key` for run creation. Reusing a key with a different request is rejected. Controller-created OCI runs require an immutable `@sha256:` image. It applies an account rate limit and records state, ordered events, heartbeats, last visible output, questions, answers, and audit data. On every startup and then periodically, the controller reconciles durable runs with providers, resumes interrupted provisioning/cleanup, detects missing or failed boxes, and enforces every run's absolute `maxTtl`. The controller contains no GitHub, repository, agent, model, prompt, or source-indexing logic.

Use TLS at the controller ingress. A configured context never silently falls back to standalone mode. The compatibility contract for external schedulers is `v1alpha1`; see `openapi.yaml`.
