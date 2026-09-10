# Using Railway only for controller hosting

## Decision

Keep the controller and PostgreSQL on Railway, and move worker lifecycle, storage,
terminal, and desktop operations behind an outbound `vmbox-hostd` connection from
one or more separately operated Incus hosts.

The host agent should initiate its connection to the controller. This avoids a
public Incus or Docker control socket, works when the host is behind NAT, and
removes Railway API and Railway SSH from the worker control and data paths.

## Why configuration alone is not enough

The provider abstraction is broad, but the current controller fleet is only
complete with the Railway provider:

- Logical-box creation requires `AttachedStorageProvider` and
  `DetachableStorageProvider`. Only the Railway provider implements both.
- Web terminal and desktop streams require `ConnectionStreamer`. Only Railway
  implements it.
- Native attachment requires an OpenSSH connection and validates Railway's
  `deploymentInstanceId` metadata.
- Docker and Incus providers execute their CLIs in the controller process. The
  production controller image includes the Railway CLI, but not Docker or Incus.
- `vmbox-hostd` currently advertises capacity and exposes only list, create,
  exec, and delete endpoints. The controller records its heartbeat but does not
  allocate work to it. It has no start/stop, volume, bootstrap, reconciliation,
  or streaming protocol.

Selecting a Docker or Incus provider alias on today's Railway-hosted controller
therefore does not produce a working replacement fleet.

## Target architecture

```text
browser / CLI
      |
      | HTTPS / WebSocket
      v
Railway: controller + PostgreSQL
      ^
      | one authenticated outbound multiplexed WebSocket
      |
external host: vmbox-hostd -> local Incus -> containers + volumes
```

Railway remains ordinary application hosting. It does not receive an API token,
manage worker services or volumes, resolve Railway deployment instances, or carry
Railway SSH credentials.

The first host implementation should use Incus. It already has separate custom
volumes and attach/detach operations that match the controller's logical-box
model. Docker can be added later, but attaching a different named volume normally
requires replacing a container, which makes it a less direct fit for reusable
compute slots.

## Required work

### 1. Make connection fencing provider-neutral

Replace controller checks for `deploymentInstanceId` and
`ID@ssh.railway.com` with a provider-neutral connection revision. Store the
revision on the compute-slot assignment and require an exact match before native,
terminal, or desktop attachment.

The revision must change whenever a host agent replaces the underlying instance
or loses its assignment fence. A reconnect to the same instance keeps it stable.

### 2. Extend the host protocol

Use one outbound, authenticated WebSocket from hostd to the controller. Multiplex:

- host registration, capabilities, health, and available slot inventory;
- leased lifecycle commands and results;
- exact-argv bootstrap and exec;
- start, stop, inspect, resize, and reconcile;
- create, inspect, attach, detach, and delete volume;
- bidirectional terminal and desktop byte streams.

Every command needs an operation ID, account and resource ownership, assignment
generation, fencing token, deadline, and idempotency key. Hostd must persist
claimed destructive operations before execution and return the saved result after
reconnect. A lost connection cannot imply success and cannot cause an automatic
replay of ambiguous input or deletion.

Enrollment should exchange a one-time secret for a host identity. Store only a
hash of the long-lived credential in the controller, support rotation/revocation,
and scope a host to one account and host pool.

### 3. Add an agent-backed provider

Implement a controller-side provider that satisfies the existing provider
contract by queueing commands for a connected host agent. It must implement:

- `Provider` and `Bootstrapper`;
- `AttachedStorageProvider` and `DetachableStorageProvider`;
- `ConnectionExecutor`, `ConnectionStreamer`, and session attachment where
  the CLI still needs it.

The provider should never accept an executable or shell fragment from stored host
metadata. Commands remain typed operations or exact argv arrays.

### 4. Preserve storage locality

Add a host or storage-domain ID to compute slots and logical volumes. A hibernated
box can resume only on a compatible slot in the same storage domain unless an
explicit volume migration has completed.

For the first release, one Incus host can be one region and one storage domain.
This keeps hibernate/resume safe without pretending that an Incus volume can move
between hosts.

### 5. Proxy interactive data through the agent

Hostd should open an Incus exec stream for `vmbox-runtime web-terminal` or
`desktop-stream`, then multiplex raw bytes over its existing outbound connection.
The controller continues to authorize the browser or CLI, validate the assignment
fence, and apply stream limits.

This also gives the CLI a provider-neutral controller WebSocket transport. Direct
OpenSSH can remain an optional fast path for providers that expose it.

### 6. Migrate without moving existing volumes implicitly

1. Deploy the protocol additively while the Railway provider remains available.
2. Enroll one disposable Incus host and pass lifecycle, hibernate/resume,
   terminal, desktop, reconnect, and controller-restart tests.
3. Create an agent-backed provider alias and a separate fleet.
4. Direct new boxes to that alias.
5. Leave existing Railway boxes on Railway until each is explicitly exported and
   imported, or deleted by its owner. Do not retarget their stored provider.
6. After the Railway worker fleet is empty, scale its desired slots to zero.
7. Remove the Railway provider credential, Railway SSH key, and worker-related
   Railway environment variables. Railway continues hosting the controller and
   database.

Workspace migration should be a named, observable operation using a snapshot or
archive with checksum verification. It must never be hidden inside provider
retargeting.

## Acceptance gates

- Controller starts and remains fully usable with no Railway API token or Railway
  SSH key.
- Creating, hibernating, resuming, and deleting a disposable box uses only hostd
  and Incus.
- Desktop and TMUX work through the web UI; CLI attachment has a
  provider-neutral path.
- Controller restart and hostd reconnect preserve tmux sessions and do not repeat
  destructive commands.
- A dropped connection during create, attach, detach, or delete resolves from a
  persisted operation result or stays explicitly unknown.
- Assignment and ownership mismatches fail closed on both controller and host.
- An offline host does not cause its volumes to be scheduled on another host.
- Railway audit logs show no worker-service, worker-volume, deployment-instance,
  or Railway SSH operations.

## Shorter alternative and its limit

The controller could run the Incus CLI against an Internet-reachable Incus API
protected by mutual TLS. This needs fewer protocol changes and removes Railway API
usage, but it places a host-wide infrastructure credential in the controller and
requires inbound control-plane exposure. Web/CLI streaming and provider-neutral
connection fencing still need implementation.

An outbound host agent is the safer long-term boundary and reuses the existing
`vmbox-hostd` direction instead of creating a second temporary architecture.
