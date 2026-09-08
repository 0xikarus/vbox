#!/usr/bin/env bash
# Real Chromium UI fixtures, executed inside a disposable vmbox only.
set -euo pipefail
test -d /data/workspace || { echo 'Run browser verification inside a vmbox.' >&2; exit 1; }
if ! command -v chromium >/dev/null; then
  sudo apt-get update -qq
  sudo env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends chromium
fi
if ! node -e "require.resolve('puppeteer-core',{paths:['/data/workspace/factory-browser-tools']})" >/dev/null 2>&1; then
  npm install --prefix /data/workspace/factory-browser-tools puppeteer-core
fi
export VMBOX_CHROMIUM
VMBOX_CHROMIUM=$(command -v chromium)
export VMBOX_FACTORY_PUPPETEER
VMBOX_FACTORY_PUPPETEER=$(node -e "console.log(require.resolve('puppeteer-core',{paths:['/data/workspace/factory-browser-tools']}))")
node --test tests/browser/factory-ui.test.mjs
