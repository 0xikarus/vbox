# Factory feature-branch staging

Not a production rollout guide. Do not merge/deploy the factory without approval.
Build and test these binaries inside an isolated vmbox:

```sh
go build -o /data/workspace/bin/vmbox-factory ./cmd/vmbox-factory
go build -o /data/workspace/bin/vmbox-controller ./cmd/vmbox-controller
bash scripts/test-factory-in-box.sh
```

The existing controller serves the Factory tab and authenticates browser cookies.
A separate factory process owns its PostgreSQL tables and private asset directory.
Give staging its own DB, server identity and storage. Do not point a test controller
at the production DB or turn on its fleet reconciler against production resources.

Configuration (names only; supply values through secure environment):

| Process | Variable | Purpose |
|---|---|---|
| Controller | `VMBOX_FACTORY_URL` | Trusted factory HTTP endpoint; same host loopback recommended |
| Both | `VMBOX_FACTORY_GATEWAY_TOKEN` | Shared random server-only token, at least 32 characters |
| Factory | `VMBOX_FACTORY_DATABASE_URL` | Dedicated PostgreSQL database |
| Factory | `VMBOX_FACTORY_DATA_DIR` | Dedicated absolute persistent private storage directory |
| Factory | `VMBOX_FACTORY_LISTEN` | Defaults to `127.0.0.1:8090` |
| Factory | `VMBOX_FACTORY_MAX_WORKERS` | Defaults to 3; current approved maximum is 6 |
| Factory | `VMBOX_FACTORY_CONTROLLER_URL` | Execution controller HTTPS URL |
| Factory | `VMBOX_FACTORY_CONTROLLER_TOKEN` | Server-only configured account credential; never forwarded to boxes |
| Factory | `VMBOX_FACTORY_ACCOUNT_ID` | Expected controller account; checked against whoami |
| Factory | `VMBOX_FACTORY_GITHUB_APP_ID` | Registered GitHub App identifier |
| Factory | `VMBOX_FACTORY_GITHUB_INSTALLATIONS` | Operator-approved JSON mapping controller account IDs to installation IDs |
| Factory | `VMBOX_FACTORY_ENCRYPTION_KEY` | Separate secret of at least 32 bytes for the App key envelope |
| Factory | `VMBOX_FACTORY_GITHUB_KEY_FILE` | Encrypted key envelope in a regular 0600 file |

With the App ID and encryption key securely configured, `vmbox-factory
seal-github-key` reads a PEM private key on stdin and emits only its encrypted
envelope on stdout. Save that envelope privately and set the key-file path.
Do not commit either plaintext keys, encryption keys, tokens, or real environment
files. Changing the App ID changes the authenticated encryption scope; re-encrypt
the key intentionally. App repository grants are rechecked on access.

The trusted operator allowlist is currently the installation onboarding mechanism;
an interactive GitHub installation callback/management UI remains to be added.
The factory currently refuses new planning/reply submissions until a real planner
runner is wired. Repository lists and persisted records are not proof of agent
execution. Image capability must remain false until runtime adapters are verified.
Worker GitHub fixture tests are not proof of access to a live installation.

Current proof: core factory PostgreSQL tests passed in a worker. Combined tests
found and prompted a fix for route registration. Remaining tmux compatibility
failure and later integration verification are tracked in FACTORY-PROGRESS.md.
