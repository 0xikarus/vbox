# Controller on the def1dashboard host

The production controller is **https://vbox.def1.sh/** on the def1dashboard host.
The existing market-monitor Caddy terminates HTTPS; its named tracker/market
sites remain independent. `vbox.caddy` adds this hostname and its upstream on the
shared `edge` Docker network. DNS is a DNS-only A record to `109.94.169.84`.
Caddy manages and renews its public certificate automatically.

To move to another domain, update the Caddy site, `VMBOX_CONTROLLER_URL`, running
boxes' private `desktop-agent.json` controller field and the inbound mail
Worker's `CONTROLLER_URL` together. Existing Railway-generated domains cannot be
moved with DNS; clients must connect to the new URL.

## Layout and isolation

- `/opt/vbox-controller/src`: public `0xikarus/vbox`, branch `main`.
- `/opt/vbox-controller/controller.env`: original `VMBOX_*` settings and the new
  private `DATABASE_URL`; root-owned mode 0600. Preserve the encryption key.
- `/opt/vbox-controller/database.env`: new database login; root-owned mode 0600.
- `/opt/vbox-controller/deployed`: last healthy source commit.
- `/opt/vbox-controller/backups`: private custom-format daily dumps, seven-day
  retention; copy these off-host separately for disaster recovery.

The compose project is `vbox-controller`. PostgreSQL 18 has a dedicated volume
mounted at `/var/lib/postgresql` and a private internal network; it publishes no
port and does not share the market database. The controller also joins `edge`
and publishes only a loopback diagnostic port, 43120. It has no Docker socket,
no worker volumes, a read-only root filesystem and private temporary files.
The controller is capped at half a CPU and 1 GiB RAM, the database at half a CPU
and 768 MiB. Image builds are capped at half a CPU and 1 GiB RAM.

## Installation and updates

1. Stage root-only environment files; never add them to the checkout or image.
2. Clone `https://github.com/0xikarus/vbox.git` into the source directory.
3. Build `Dockerfile.controller`; it includes the controller, matching worker
   transport binaries and the native agent CLIs used for profile login/catalogs.
4. Start **only** the new database and restore a source backup with
   `pg_restore --no-owner --no-acl --exit-on-error`. Verify counts, encrypted
   profile readability and archive integrity before cutover.
5. Stop the old controller; take a final quiesced dump, restore it into a clean
   destination database, then start the new controller. Never run both
   reconcilers against separate database copies. Retain the old database for
   rollback until the complete cutover is verified.
6. Install the Caddy site; validate before reloading.
   Update running worker callback configurations without changing tokens or
   fences, and update the inbound-mail Worker URL.
7. Install the service/timer files in `/etc/systemd/system`, reload systemd and
   enable both timers. Auto-deploy checks main every two minutes, builds before
   touching the running controller, backs up before schema migration, and rolls
   the application image back on failed health checks. It never restarts boxes.
   Database changes are additive; image rollback does not restore the database.

`systemctl start vbox-controller-deploy` checks for updates now. The backup
service runs daily and before an upgrade. Inspect `journalctl -u
vbox-controller-deploy` for build/deploy results. Do not delete the PostgreSQL
volume when updating the controller.
