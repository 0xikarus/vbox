#!/usr/bin/env bash
set -euo pipefail

image="${1:?Usage: bash tests/worker-tools.sh IMAGE}"
docker run --rm --name "vmbox-tools-disposable-$$" \
  --network none --user 10001:10001 \
  --tmpfs /data:rw,uid=10001,gid=10001,mode=0700 \
  --env HOME=/data/home --workdir /data --entrypoint /bin/bash "$image" -ceu '
    mkdir -p "$HOME" /data/workspace
    cd /data/workspace
    python --version
    python3 --version
    python -c "import sys; assert sys.version_info >= (3, 11)"
    python -m pip --version
    pipx --version
    uv --version
    uvx --version
    python -m venv .python-venv
    .python-venv/bin/python -m pip --version
    uv venv --offline --python /usr/bin/python3 .uv-venv
    .uv-venv/bin/python -c "import sys; assert sys.prefix != sys.base_prefix"
    node -e "console.log(\"node \" + process.version)"
    npm --version
    npx --version
    bun -e "console.log(\"bun \" + Bun.version)"
    ffmpeg -hide_banner -version | sed -n "1p"
    ffprobe -hide_banner -version | sed -n "1p"
    ffmpeg -hide_banner -loglevel error -f lavfi -i color=c=black:s=32x32:d=0.1 -frames:v 1 -f null -
    printf "Worker development tools verified offline as UID %s\n" "$(id -u)"
  '
