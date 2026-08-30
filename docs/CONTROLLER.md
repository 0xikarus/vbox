# Controller operations

`vmbox-controller` uses PostgreSQL for accounts, exactly two human roles (`owner`, `user`), hashed access tokens, encrypted-provider-credential slots, runs, events, questions, host capacity, and immutable audit entries. Run and question queries are always account-scoped.

Required configuration:

```text
DATABASE_URL
VMBOX_CONTROLLER_LISTEN=:8080
VMBOX_CONTROLLER_URL=https://controller.example
VMBOX_IMAGE=ghcr.io/owner/vmbox-service@sha256:...
```

Provider credentials remain in environment variables for a standalone deployment or should be inserted into the encrypted account vault by an owner. Never put them into container images, CLI contexts, reusable profiles, logs, or workload environment. `vmbox-controller bootstrap` prints its owner token once.

The API requires bearer authentication and `Idempotency-Key` for run creation. Controller-created OCI runs require an immutable `@sha256:` image. It applies an account rate limit and records state, ordered events, heartbeats, last visible output, questions, answers, and audit data. The controller contains no GitHub, repository, agent, model, prompt, or source-indexing logic.

Use TLS at the controller ingress. A configured context never silently falls back to standalone mode. The compatibility contract for external schedulers is `v1alpha1`; see `openapi.yaml`.
