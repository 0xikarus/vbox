# Shared-worker disk guardrails

Shared-worker boxes share one host filesystem. A box's configured disk size is
informational; it is not a quota. The worker now reports usable filesystem free
bytes (`statfs.Bavail`) separately from total and used bytes. Providers and
Capacity show the free amount, and the management page displays an owner warning
when a shared worker has **less than 10% free**. The warning is based on a fresh
worker observation, not the box's nominal disk size.

New shared-worker workspaces are refused below **5% usable free space** at the
worker's `CreateStorage` call. Retrying or attaching an existing workspace is
still allowed. Managed agent CLI, preset, desktop package, and custom tool
installs check the persistent home and container root filesystems immediately
before installation. The error reports the filesystem and free/total bytes.
Arbitrary shell commands issued by a box are outside this admission boundary;
filesystem quotas are needed to bound those writes.

Per-box resources now report `diskWorkspaceBytes` for the persistent `/data`
tree and `diskWritableBytes` for the container's writable layer. `diskUsedBytes`
is their sum. The layer is measured with Docker `container inspect --size`
(`SizeRw`); the persistent tree uses a bounded, low-priority `du` scan. Both
run asynchronously and are cached for five minutes so resource polling does
not walk every box's files. `diskObservedAt` exposes the measurement age. If
Docker cannot report the layer, the API marks the disk value partial and shows
the workspace component with a reason. Docker image layers, logs, and other
shared host overhead remain unattributed, so host free space is the admission
signal. See [Docker's inspect size documentation](https://docs.docker.com/reference/cli/docker/container/inspect/).

The shared-worker supervisor asks each **running** box once at startup and
hourly to run `vmbox-runtime cleanup-temp` as its own unprivileged UID. The
command removes Chromium/Puppeteer temporary directories in the private
`TMPDIR` and `/tmp`, plus core dumps in those directories and the box's home
and workspace, only when every file in a temp tree is at least **24 hours old**.
It checks ownership, rejects symlinks as roots, does not follow symlinks while
walking, and skips trees containing open files or a live working directory.
Each pass checks at most 128 temp candidates, 10,000 entries per tree, and
reclaims at most 2 GiB; oversized or changing trees are skipped for review.
The worker logs counts and reclaimed bytes without logging file contents.
Stopped boxes are not started solely for cleanup.

Deploy the worker and box runtime together. Older workers omit the new free
space and layer fields, so the UI will not infer a low-disk warning from stale
totals. The thresholds are fixed starting values for this worker; adjust them
after observing actual write rates and host size. No production data was read
or changed while implementing these guardrails.
