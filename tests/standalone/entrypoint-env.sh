#!/usr/bin/env bash
# Opt-in, disposable image check. No agent, desktop, network or credentials used.
set -euo pipefail
image="${1:?Usage: entrypoint-env.sh IMAGE}"
docker run --rm -i --label dev.vmbox.test=standalone-entrypoint-env \
 --network none --cpus .25 --memory 256m --memory-swap 256m --pids-limit 128 \
 -e VMBOX_WEB_BASE_PATH=/boxes/hb-012345678901234567890123 \
 -e VMBOX_HUB_MANAGED=true -e VMBOX_UNRELATED_TEST_SETTING=not-forwarded \
 --entrypoint /bin/sh "$image" -s <<'SCRIPT'
cat > /tmp/vmbox-standalone <<'PY'
#!/usr/bin/env python3
import os
assert os.getuid()==10001
assert os.environ['VMBOX_WEB_BASE_PATH']=='/boxes/hb-012345678901234567890123'
assert os.environ['VMBOX_HUB_MANAGED']=='true'
assert 'VMBOX_UNRELATED_TEST_SETTING' not in os.environ
print('Unprivileged standalone retains hub routing settings and drops unrelated environment: PASS')
PY
chmod 755 /tmp/vmbox-standalone
exec /usr/local/bin/vmbox-entrypoint /tmp/vmbox-standalone
SCRIPT
