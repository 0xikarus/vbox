# Linux VPS: isolated agent containers

This guide reproduces the host-managed setup verified on a Debian 13 KVM VPS.
One host supervisor manages several logical slots; each allocated box runs in
its own Docker container with private files, processes, IPC and networking.
Agents have passwordless sudo **inside their own container**. Changes to system
packages disappear on hibernation/container replacement; `/data` persists.

The containers share the host kernel and disk capacity. They are not separate
VMs, and this deployment does not remap container root through a user namespace.
See [the isolation contract and limitations](SHARED-CONTAINERS.md).

## Prerequisites and sizing

- A fresh Debian 13 VPS with root access, systemd, cgroup v2 and Docker support.
  Other distributions need equivalent packages and separate verification.
- An existing vmbox controller and an owner login on your workstation.
- Public inbound TCP 80 and 443, plus your SSH port; outbound DNS and HTTPS.
  Do not publish the Docker daemon or supervisor port 8081.
- Access to this repository and a digest-pinned desktop worker image containing
  `vmbox-runtime`, sudo, tmux and desktop packages. Authenticate privately if the
  repository or registry requires it; never put credentials in an image or guide.

Each box currently has fixed limits of **2 GiB RAM, up to 1 GiB swap, 1 CPU and
512 processes**. Configure host swap for the extra headroom; it cushions short
memory peaks but is slower than RAM and does not replace adequate host capacity.
For a 4-core/8-GB host, use at most three slots and reserve the remaining resources
for the host/supervisor. Existing workloads must count toward that budget.
There are no per-box disk quotas. Monitor free disk space and configure backups.

The commands below run **as root on the VPS**, except the workstation steps.
They assume a fresh installation; don't apply them blindly over an existing
worker. Keep existing users/volumes intact and plan migrations separately.

## 1. SSH and host packages

From your workstation, create a dedicated SSH key, install its public key using
`ssh-copy-id`, and verify a fresh key login. Only then disable password SSH login
if desired. Retain a working provider-console recovery path. Never share a
private key or paste a server password into chat.

On the VPS:

```sh
apt-get update
apt-get install -y docker.io iptables nginx ca-certificates curl openssl \
  git golang-go python3-venv
systemctl enable --now docker nginx
stat -fc %T /sys/fs/cgroup
cat /sys/fs/cgroup/cgroup.controllers
docker info --format 'cgroup={{.CgroupVersion}} driver={{.CgroupDriver}}'
```

Expect `cgroup2fs`, cgroup version `2`, and CPU/memory/PID controllers. The runtime
uses the local Unix Docker socket; the supervisor must run on the Docker host.
Do not mount that socket into an agent container.

## 2. Build the supervisor and install service definitions

Check out a reviewed revision containing `internal/sharedworker/container_linux.go`.
The commands below use `main`; pin a reviewed commit for repeatable deployments.

```sh
git clone --branch main https://github.com/0xikarus/vmbox-service.git /opt/vmbox-service
cd /opt/vmbox-service
GOTOOLCHAIN=auto go build -buildvcs=false -trimpath \
  -o /usr/local/bin/vmbox-shared-worker-isolated ./cmd/vmbox-shared-worker
install -m 755 scripts/shared-container-firewall.sh /usr/local/sbin/vmbox-container-firewall
install -m 644 deploy/linux-vps/vmbox-container-firewall.service /etc/systemd/system/
install -m 644 deploy/linux-vps/vmbox-isolated.service /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now vmbox-container-firewall.service
```

Go automatically obtains the toolchain required by `go.mod` if the distribution
compiler is older. Record the source revision and built binary checksum.

The firewall rules apply only to the runtime's reserved `vb*` bridge interfaces.
They deny container access to host services, sibling/private networks, metadata
addresses and IPv6 egress, while allowing public IPv4 internet and DNS. The
supervisor refuses container mode when the required rules are absent. This is
not a replacement for the VPS's general ingress firewall.

## 3. Configure the worker without printing its secret

On your **workstation**, `vmbox whoami --json` supplies your controller account ID.
Only that ID belongs on the worker—not your controller login token, database
credentials or another provider's management credentials.

On the VPS, enter the account ID, desired capacity and selected worker image
when this script prompts. The script refuses to overwrite an existing config.

```sh
install -d -m 700 /etc/vmbox /srv/vmbox-isolated/data
python3 -c '
import os, secrets
account = input("Controller account UUID: ").strip()
image = input("Worker image, including @sha256:digest: ").strip()
slots = int(input("Slot count (1-3 on a 4-core/8-GB host): "))
assert 1 <= slots <= 3 and "@sha256:" in image
assert account and all("\n" not in v and "\r" not in v for v in (account,image))
values = {
  "VMBOX_SHARED_ISOLATION": "container",
  "VMBOX_SHARED_SLOTS": str(slots),
  "VMBOX_SHARED_ACCOUNT_ID": account,
  "VMBOX_SHARED_CONTAINER_IMAGE": image,
  "VMBOX_SHARED_ROOT": "/srv/vmbox-isolated/data",
  "VMBOX_SHARED_BIND": "127.0.0.1",
  "PORT": "8081",
  "VMBOX_SHARED_TOKEN": secrets.token_hex(32),
}
fd = os.open("/etc/vmbox/isolated-worker.env", os.O_WRONLY|os.O_CREAT|os.O_EXCL, 0o600)
with os.fdopen(fd,"w") as f:
  f.write("".join(k+"="+v+"\n" for k,v in values.items()))
print("Worker configuration saved; secret was not displayed.")
'
python3 -c '
import subprocess
values = dict(line.rstrip("\n").split("=",1) for line in open("/etc/vmbox/isolated-worker.env"))
subprocess.run(["docker","pull",values["VMBOX_SHARED_CONTAINER_IMAGE"]],check=True)
'
systemctl enable --now vmbox-isolated.service
curl --fail http://127.0.0.1:8081/healthz
```

The account and image values must be ordinary single-line identifiers, not shell
expressions. No configuration secret is inherited by agent containers.

## 4. HTTPS using the public IP, without a domain

The provided nginx examples use port 443. If another service already owns it,
choose an unused TLS port (for example 8443), allow that port through your ingress
firewall, adjust the redirect, and include the port in the controller endpoint.

```sh
install -d -m 755 /var/lib/vmbox-acme
install -m 644 deploy/linux-vps/nginx-http.conf.example /etc/nginx/conf.d/vmbox-http.conf
```

Edit `/etc/nginx/conf.d/vmbox-http.conf`, replacing every `YOUR_PUBLIC_IP` with
the actual public IPv4 address. Then:

```sh
nginx -t
systemctl reload nginx
python3 -m venv /opt/vmbox/certbot
/opt/vmbox/certbot/bin/pip install certbot==5.4.0
```

Request the IP certificate, replacing `YOUR_PUBLIC_IP`. Supply an operator email
at the prompt, or explicitly choose Certbot's no-email registration option.
Port 80 must be reachable for validation.

```sh
/opt/vmbox/certbot/bin/certbot certonly --agree-tos \
  --preferred-profile shortlived --webroot -w /var/lib/vmbox-acme \
  --ip-address YOUR_PUBLIC_IP
install -m 644 deploy/linux-vps/nginx-https.conf.example /etc/nginx/conf.d/vmbox-https.conf
```

Edit the HTTPS config, replacing all `YOUR_PUBLIC_IP` placeholders, including
certificate paths. Do not reload nginx with unresolved placeholders.

```sh
nginx -t
systemctl reload nginx
install -m 644 deploy/linux-vps/vmbox-cert-renew.service /etc/systemd/system/
install -m 644 deploy/linux-vps/vmbox-cert-renew.timer /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now vmbox-cert-renew.timer
/opt/vmbox/certbot/bin/certbot renew --dry-run --no-random-sleep-on-renewal \
  --run-deploy-hooks --deploy-hook '/usr/bin/systemctl reload nginx'
```

IP certificates are short-lived, so successful automatic renewal and nginx reload
are essential. See [Let's Encrypt's IP certificate instructions](https://letsencrypt.org/2026/03/11/shorter-certs-certbot).

## 5. Register the pool from your workstation

Verify `vmbox whoami --json` reports the intended account and owner role. Create
a local **non-secret** JSON file `vps-provider.json` containing:

```json
{"endpoint":"https://YOUR_PUBLIC_IP"}
```

This workstation script obtains the worker secret through your authenticated SSH
connection and passes it to the CLI through a transient environment variable.
It does not print or save the secret locally. Set the SSH target, alias and config
path first. Use an SSH config entry if a custom port or identity key is needed.

```sh
python3 -c '
import json, os, subprocess
target = input("VPS SSH target (root@IP or SSH alias): ").strip()
alias = input("New controller pool alias (for example linux-vps): ").strip()
config = input("Provider JSON path: ").strip()
assert target and not target.startswith("-") and alias and not alias.startswith("-")
raw = subprocess.check_output(["ssh","-o","BatchMode=yes",target,"cat /etc/vmbox/isolated-worker.env"],text=True)
values = dict(line.split("=",1) for line in raw.splitlines())
env = dict(os.environ, VMBOX_VPS_SECRET=json.dumps({"token":values["VMBOX_SHARED_TOKEN"]}))
subprocess.run(["vmbox","providers","create","shared-worker",alias,"--config-file",config,"--secret-env","VMBOX_VPS_SECRET"],env=env,check=True)
subprocess.run(["vmbox","providers","validate","shared-worker",alias],check=True)
'
```

In the controller's Capacity page, select **this new pool** and set its desired
slot count to the configured capacity. Do not change another pool or the account
default unintentionally. The CLI's `fleet slots set` targets the default provider;
use the selected-pool UI when preserving an existing default.

## 6. Verify and operate

Confirm the pool shows its expected healthy/free slots. Create two explicitly
disposable boxes in that pool and verify their desktops, `sudo -n id -u` (returns
`0`), and an ordinary package installation. Verify file and network separation
even when commands run through sudo. Delete only those named test boxes afterward.

For the repository's disposable host/protocol tests, run as root from the checkout
with `VMBOX_TEST_CONTAINER_IMAGE` set to the already-pulled digest-pinned image:

```sh
export VMBOX_TEST_CONTAINER_IMAGE='YOUR_IMAGE@sha256:DIGEST'
GOTOOLCHAIN=auto go test ./internal/sharedworker -run TestDisposableContainerBoundary -v -count=1
GOTOOLCHAIN=auto bash tests/shared-worker/container.sh
```

The protocol test reserves loopback port 18082; confirm it is unused first.
These tests create and remove only their own disposable containers/networks.
They are not exhaustive hostile-tenant or resource-saturation testing.

Operational checks:

```sh
systemctl status vmbox-isolated vmbox-container-firewall --no-pager
systemctl list-timers vmbox-cert-renew.timer --no-pager
docker ps --filter label=io.vmbox.root=/srv/vmbox-isolated/data
curl --fail https://YOUR_PUBLIC_IP/healthz
df -h /srv/vmbox-isolated/data
```

Back up `/srv/vmbox-isolated/data` and root-only `/etc/vmbox` securely. Stop or
hibernate affected boxes before taking a consistency-sensitive backup. Do not
restore one data root under multiple simultaneous supervisors.

Upgrades must preserve the pinned image and policy expected by running containers.
An image/policy mismatch fails closed; don't fix it by deleting user storage.
Drain/hibernate affected boxes before changing their image or policy, then rebuild
and restart the supervisor. A host reboot stops processes; files survive and
active assignments are reconstructed. Existing UID-mode boxes require a separate,
planned migration and are not converted by these installation steps.
