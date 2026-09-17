#!/usr/bin/env bash
set -uo pipefail

image="${VMBOX_TEST_SHARED_IMAGE:-vmbox-shared-blender:disposable}"
name="vmbox-shared-probe-$(date +%s)-$$"

cleanup() {
  docker rm -f "$name" >/dev/null 2>&1 || true
}
trap cleanup EXIT

die() { printf 'probe.sh: %s\n' "$*" >&2; exit 1; }

require_docker() {
  command -v docker >/dev/null 2>&1 || die "docker not found on PATH; run this on a host with Docker"
  docker image inspect "$image" >/dev/null 2>&1 || die "image '$image' not present locally; build or pull it first"
}

print_image_info() {
  printf 'image: %s\n' "$image"
  printf 'image-id: %s\n' "$(docker image inspect --format '{{.Id}}' "$image" 2>/dev/null || echo unknown)"
  printf 'container: %s\n' "$name"
  printf 'mode: disposable --rm container, no host volumes, no published ports\n'
}

run_probe() {
  local payload status
  payload="$(cat <<'PROBE'
echo "===== vmbox shared-worker isolation probe ====="

section() { printf '\n----- %s -----\n' "$1"; }
probe() {
  printf '\n>>> %s\n$ %s\n' "$1" "$2"
  if out=$(sh -c "$2" 2>&1); then rc=0; else rc=$?; fi
  printf '%s\n' "$out"
  printf '[exit %s]\n' "$rc"
  return "$rc"
}

r_user=no; r_mount=no; r_pid=no; r_bwrap=no
r_chroot=no; r_pivot=no; r_ro=no; r_tmp=no

if [ "$(id -u)" = 0 ]; then
  PROD="setpriv --reuid 30000 --regid 30000 --clear-groups --no-new-privs --inh-caps=-all --ambient-caps=-all --bounding-set=-all --"
  RUN_AS="uid 30000 via setpriv"
else
  PROD=""
  RUN_AS="current uid $(id -u) (no setpriv; already unprivileged)"
fi
BWRAP="bwrap --die-with-parent --unshare-user --unshare-pid --unshare-ipc --unshare-uts --ro-bind / / --dev /dev --proc /proc --tmpfs /dev/shm"

section "1. identity and capabilities"
probe "uname" 'uname -a'
probe "uid" 'id -u'
probe "pid 1" 'cat /proc/1/comm 2>/dev/null || echo unknown'
probe "capability set" 'grep Cap /proc/self/status'
probe "no-new-privs and seccomp" 'grep -E "NoNewPrivs|Seccomp" /proc/self/status'
probe "image version manifest" 'if [ -r /usr/local/lib/vmbox-image-version ]; then cat /usr/local/lib/vmbox-image-version; else echo "(absent)"; fi'
printf '\n[workload identity: %s]\n' "$RUN_AS"

section "2. user namespace sysctls"
probe "unprivileged_userns_clone" 'if [ -r /proc/sys/kernel/unprivileged_userns_clone ]; then cat /proc/sys/kernel/unprivileged_userns_clone; else echo "(absent)"; fi'
probe "max_user_namespaces" 'if [ -r /proc/sys/user/max_user_namespaces ]; then cat /proc/sys/user/max_user_namespaces; else echo "(absent)"; fi'

section "3. namespace tooling"
probe "tool paths" 'for t in bwrap unshare setpriv newuidmap newgidmap; do p=$(command -v "$t" 2>/dev/null || true); if [ -n "$p" ]; then echo "$t: $p"; else echo "$t: MISSING"; fi; done'

section "4. root namespace probes"
probe "unshare --mount true" 'unshare --mount true'
probe "unshare --user true" 'unshare --user true'
probe "unshare --user --map-root-user true" 'unshare --user --map-root-user true'

section "5. production pattern as workspace user (uid 30000)"
probe "create uid 30000" 'id -u 30000 >/dev/null 2>&1 || { command -v useradd >/dev/null 2>&1 && useradd -u 30000 -M -s /bin/sh probeuser; }; id -u 30000'
probe "ordered #1: unshare --user true" "$PROD sh -c 'unshare --user true'"
if probe "ordered #2: unshare --user --mount --pid --fork true" "$PROD sh -c 'unshare --user --mount --pid --fork true'"; then
  r_mount=yes; r_pid=yes
fi
if probe "ordered #3: bwrap smoke test" "$PROD sh -c '$BWRAP true'"; then
  r_bwrap=yes
fi
if probe "split: unshare --user true" "$PROD sh -c 'unshare --user true'"; then r_user=yes; fi
if probe "split: unshare --user --mount true" "$PROD sh -c 'unshare --user --mount true'"; then r_mount=yes; fi
if probe "split: unshare --user --pid --fork true" "$PROD sh -c 'unshare --user --pid --fork true'"; then r_pid=yes; fi

section "6. bubblewrap isolation smoke tests"
if probe "bwrap base shape" "$PROD sh -c '$BWRAP true'"; then
  r_bwrap=yes
  if probe "bwrap read-only root: touch /usr/vmbox-probe-ro (expected to fail)" "$PROD sh -c '$BWRAP touch /usr/vmbox-probe-ro'"; then r_ro=no; else r_ro=yes; fi
  if probe "bwrap private /tmp write" "$PROD sh -c '$BWRAP --tmpfs /tmp sh -c \"printf private > /tmp/vmbox-probe-tmp && cat /tmp/vmbox-probe-tmp\"'" \
    && test ! -e /tmp/vmbox-probe-tmp; then
    r_tmp=yes
  fi
  probe "private /tmp assertion: marker absent outside" 'test ! -e /tmp/vmbox-probe-tmp'
else
  printf '\n[skipped read-only root and private /tmp checks: bubblewrap could not create a namespace]\n'
fi

section "7. chroot and pivot_root as workspace user"
if probe "chroot / true (expected to fail)" "$PROD sh -c 'chroot / true'"; then r_chroot=yes; fi
if probe "pivot_root inside userns+mountns" "$PROD sh -c 'unshare --user --map-root-user --mount sh -c \"mkdir -p /tmp/vmbox-pivot/old && mount -t tmpfs none /tmp/vmbox-pivot && pivot_root /tmp/vmbox-pivot /tmp/vmbox-pivot/old && echo pivot-root-ok\"'"; then
  r_pivot=yes
fi

section "summary"
if command -v bwrap >/dev/null 2>&1; then r_installed=yes; else r_installed=no; fi
printf 'PROBE_RESULT user_namespace=%s mount_namespace=%s pid_namespace=%s bubblewrap_installed=%s bubblewrap_works=%s\n' "$r_user" "$r_mount" "$r_pid" "$r_installed" "$r_bwrap"
printf 'PROBE_DETAIL chroot=%s pivot_root=%s read_only_root=%s private_tmp=%s\n' "$r_chroot" "$r_pivot" "$r_ro" "$r_tmp"
printf 'PROBE_HINT namespace tier requires bubblewrap_works=yes; otherwise the supervisor must report the uid tier.\n'
exit 0
PROBE
)"
  if [ "${VMBOX_TEST_SHARED_LOCAL:-}" = "1" ]; then
    printf 'probe: running locally on %s (no container)\n\n' "$(uname -n)"
    sh -c "$payload"
  else
    printf 'probe: starting disposable container\n\n'
    docker run --rm --name "$name" --user 0 --entrypoint sh "$image" -c "$payload"
  fi
  status=$?
  printf '\nprobe: exited %s\n' "$status"
  return "$status"
}

if [ "${VMBOX_TEST_SHARED_LOCAL:-}" = "1" ]; then
  run_probe
else
  require_docker
  print_image_info
  run_probe
fi
