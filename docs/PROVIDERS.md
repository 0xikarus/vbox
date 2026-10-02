# Worker providers

The controller can run wherever it has PostgreSQL, HTTPS, and the matching
runtime binary. Worker hosting is a separate choice. A provider alias names a
worker pool; changing its configuration does not move an existing box or volume.

Add and edit aliases from the controller's Providers page, where each provider
type has its own fields, or with `vbox providers schema|list|show|create|update|validate`.
Secrets are separate from non-secret config. Updates use the alias's revision;
omitted secrets are preserved and replacement is explicit. Target fields are
immutable after creation, so create a new alias for a new endpoint or project.
An alias update does not deploy, restart, or migrate a worker.

| Worker path | Use it for | Guide |
| --- | --- | --- |
| `shared-worker` on a Linux host | Self-hosted persistent boxes; the container tier gives each box its own container | [Linux VPS setup](LINUX-VPS-SETUP.md) and [shared-worker isolation](SHARED-WORKERS.md) |
| `railway` | Dedicated Railway service and volume per compute slot | [Railway notes below](#railway) |
| `docker` or `incus` | Provider adapter workflows with their own engine or remote | [Adapter notes below](#docker-and-incus) |

The Docker and Incus adapters do not have the same documented controller-backed
persistent-box coverage as the two paths above. Verify the lifecycle and
transport features you need before choosing one for a controller fleet.

Controller-managed operations use ownership and assignment checks. A box's
files can outlive its compute slot, but its processes cannot survive hibernation.
Never infer an operation succeeded from a disconnected transport.

## Worker image

The repository Dockerfile builds the full desktop worker image. It includes
`vmbox-runtime`, the agent CLIs, tmux, browser, and optional tools. Build it
locally with `make box-image`. A registry-hosted provider needs a published
digest-pinned image; set `VMBOX_IMAGE=REGISTRY/IMAGE:TAG` and use
`make box-image-push` to publish one. The Linux container worker also requires
a locally pulled digest-pinned image. Agent logins, provider tokens, and other
credentials are synchronized at runtime and must never be baked into an image.
Building a new image does not upgrade existing boxes or hosts.

## Shared worker

A `shared-worker` alias points to a worker endpoint and a controller-stored
authentication token. On a self-hosted Linux server, the supervisor can run
each box in its own Docker container with its own files, processes, and network.
The trusted process modes are also available, with weaker isolation. See
[shared-worker isolation](SHARED-WORKERS.md) before putting different workloads
on the same physical host. The worker needs neither the controller database
connection nor another provider's management token.

A shared worker's slots and new-box size are changed remotely, bounded by its
machine: see [changing slots and box size remotely](SHARED-WORKERS.md#changing-slots-and-box-size-remotely).

## Railway

Railway is one optional worker provider; the controller itself need not run
there. A `railway` alias needs `projectId` and `environmentId` in non-secret
config JSON, and a token in secret JSON. Select `RAILWAY_API_TOKEN` or
`RAILWAY_TOKEN` with `tokenEnvironment` according to the token type. For
single-account startup, the controller can seed an absent encrypted
`railway/primary` alias from those environment variables; it does not overwrite
an existing owner-managed credential.

Railway needs a registry-published worker image. It creates a service and
attached `/data` volume for a compute slot, then waits for the submitted
deployment to become ready. A stopped box retains its storage. The optional
`VMBOX_DIRECT_WORKERS=1` path enrolls a worker agent so ordinary terminal,
desktop, file, and runtime traffic uses its authenticated outbound WebSocket.
Railway's API and SSH remain for infrastructure operations and initial
installation. An enrolled worker that disconnects does not silently fall back
to SSH for ordinary box traffic.

For an existing running slot, an owner can POST to
`/v1/worker-slots/{slot}/enrollment`, wait for its agent connection, then POST
to `/v1/worker-slots/{slot}/activate`. Installation records native session
identities and activation verifies they survived. If installation is ambiguous,
use `/v1/worker-slots/{slot}/recover` to probe it; do not blindly repeat setup
or restart the worker. New allocations enroll automatically when direct workers
are enabled.

Controller-initiated Railway SSH needs a separately registered key supplied
through `VMBOX_RAILWAY_SSH_PRIVATE_KEY_B64`. Its private material is stored
outside worker images and volumes. API requests share a PostgreSQL-backed
budget; `VMBOX_RAILWAY_API_HOURLY_LIMIT` and `VMBOX_RAILWAY_API_INTERVAL`
adjust its defaults. An optional `VMBOX_RAILWAY_WEBHOOK_SECRET` enables
deployment events as inventory refresh hints at
`/v1/railway-webhooks/SECRET`; treat the full webhook URL as a credential.
Webhook events do not authorize destructive actions or replace reconciliation.

## Docker and Incus

The Docker adapter accepts a local, rootless, named, SSH, or mTLS engine
context. It rejects unauthenticated TCP. Each box gets a labeled container,
internal network, and `/data` volume; cleanup verifies those labels before
deleting resources. `docker exec` is the default transport.

The Incus adapter uses a configured remote and project. Unprivileged system
containers are the default; `--incus-vm` selects a VM. It applies limits and a
custom `/data` volume. Direct standalone operation uses the configured remote;
controller hosts use `vmbox-hostd` so agent workloads do not receive the Incus
socket.
