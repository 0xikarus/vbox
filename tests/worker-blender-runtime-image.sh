#!/usr/bin/env bash
set -euo pipefail

image=${1:?usage: worker-blender-runtime-image.sh IMAGE}
docker run --rm --network none --user 10001:10001 \
  --tmpfs /data:rw,uid=10001,gid=10001,mode=0700 \
  --env HOME=/data/home --workdir /data --entrypoint /bin/bash "$image" -ceu '
set -eu
mkdir -p "$HOME" /data/workspace
root=/opt/vmbox/blender-5.1.2/5.1/python/lib
if test -e "$root/libpython3.13.a" ||
   test -e "$root/python3.13/config-3.13-x86_64-linux-gnu/libpython3.13.a"; then
  echo "Blender static Python build archives remain in the runtime image" >&2
  exit 1
fi
test "$(command -v blender)" = /usr/local/bin/blender
test "$(readlink -f /usr/local/bin/blender)" = /opt/vmbox/blender-5.1.2/blender
blender --version | sed -n "1p" | grep -Fx "Blender 5.1.2"
bash -lc '\''test "$(command -v blender)" = /usr/local/bin/blender && blender --version | sed -n "1p" | grep -Fx "Blender 5.1.2"'\''
blender --background --factory-startup \
  --python-expr "import bpy, mathutils; assert bpy.app.version_string == '\''5.1.2'\''; print('\''BLENDER_RUNTIME_OK'\'')"
'
