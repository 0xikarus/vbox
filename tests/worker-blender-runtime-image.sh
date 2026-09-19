#!/usr/bin/env bash
set -euo pipefail

image=${1:?usage: worker-blender-runtime-image.sh IMAGE}
docker run --rm --entrypoint sh "$image" -c '
set -eu
root=/opt/vmbox/blender-5.1.2/5.1/python/lib
if test -e "$root/libpython3.13.a" ||
   test -e "$root/python3.13/config-3.13-x86_64-linux-gnu/libpython3.13.a"; then
  echo "Blender static Python build archives remain in the runtime image" >&2
  exit 1
fi
/opt/vmbox/blender-5.1.2/blender --background --factory-startup \
  --python-expr "import bpy, mathutils; assert bpy.app.version_string == '\''5.1.2'\''; print('\''BLENDER_RUNTIME_OK'\'')"
'
