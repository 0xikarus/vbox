#!/usr/bin/env bash
set -euo pipefail
root="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"
bin="${VMBOX_INSTALL_DIR:-$HOME/.local/bin}"
rc="${VMBOX_SHELL_RC:-$HOME/.bashrc}"
update_rc=1
uninstall=0
build_go_binary() {
  local output="$1" package="$2" target_os="$3" target_arch="$4" ldflags="${5:-}"
  local -a build_args=(build -buildvcs=false -trimpath)
  [[ -n "$ldflags" ]] && build_args+=("-ldflags=$ldflags")
  build_args+=(-o "$output" "$package")
  if command -v go >/dev/null 2>&1; then
    (cd "$root" && CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" go "${build_args[@]}")
    return
  fi
  command -v docker >/dev/null 2>&1 || {
    echo "Go 1.26 or Docker is required." >&2
    return 1
  }
  local output_dir output_name
  output_dir="$(dirname -- "$output")"
  output_name="$(basename -- "$output")"
  build_args=(build -buildvcs=false -trimpath)
  [[ -n "$ldflags" ]] && build_args+=("-ldflags=$ldflags")
  build_args+=(-o "/out/$output_name" "$package")
  docker run --rm \
    --user "$(id -u):$(id -g)" \
    -e CGO_ENABLED=0 -e "GOOS=$target_os" -e "GOARCH=$target_arch" \
    -e GOCACHE=/tmp/go-build -e GOMODCACHE=/tmp/go-mod \
    -v "$root:/src:ro" -v "$output_dir:/out" -w /src \
    golang:1.26-bookworm /usr/local/go/bin/go "${build_args[@]}"
}


while (($#)); do
 case "$1" in
 --no-shell-update) update_rc=0 ;;
 --shell-rc) shift; rc="${1:?Missing shell rc path}" ;;
 --uninstall) uninstall=1 ;;
 -h|--help) echo 'Usage: install.sh [--no-shell-update] [--shell-rc PATH] [--uninstall]'; exit ;;
 *) echo "Unknown option: $1" >&2; exit 2 ;;
 esac
 shift
done
if ((uninstall)); then
 rm -f -- "$bin/vmbox"
 echo 'Removed vmbox CLI. Contexts, credentials, legacy bundles, and remote resources were preserved.'
 exit
fi
case "$(uname -s)" in Linux) cli_os=linux ;; Darwin) cli_os=darwin ;; *) echo 'Unsupported OS' >&2; exit 1 ;; esac
case "$(uname -m)" in x86_64|amd64) cli_arch=amd64 ;; arm64|aarch64) cli_arch=arm64 ;; *) echo 'Unsupported architecture' >&2; exit 1 ;; esac
mkdir -p "$bin"
output="$(mktemp "$bin/.vmbox-build.XXXXXX")"
trap 'rm -f -- "$output"' EXIT
build_go_binary "$output" ./cmd/vmbox "$cli_os" "$cli_arch"
chmod 755 "$output"
mv -f -- "$output" "$bin/vmbox"
if ((update_rc)); then
 mkdir -p "$(dirname -- "$rc")"
 touch "$rc"
 if ! grep -Fq '# vmbox-service' "$rc"; then
  printf -v quoted_bin '%q' "$bin"
  printf '\nexport PATH=%s:$PATH # vmbox-service\n' "$quoted_bin" >> "$rc"
 fi
fi
echo "Installed $bin/vmbox (controller-only; no provider tools or deployment bundle)"
echo 'Configure: vmbox connect https://YOUR-CONTROLLER'
echo 'Provide controller authentication securely through VMBOX_CONTROLLER_TOKEN.'
