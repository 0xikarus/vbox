#!/usr/bin/env python3
"""Measure the real visible bridge in fresh homes, without importing logins.

Run serially on the same machine with and without --baseline. On a box, use
--temp-dir /data/tmp: OpenTUI needs an executable temporary filesystem.
This creates no controller resources and sends no model prompts.
"""

import argparse
import http.client
import json
import os
import pathlib
import shutil
import socket
import subprocess
import tempfile
import time


def health(path):
    connection = http.client.HTTPConnection("localhost", timeout=1)
    connection.sock = socket.socket(socket.AF_UNIX)
    connection.sock.settimeout(1)
    try:
        connection.sock.connect(str(path))
        connection.request("GET", "/health")
        response = connection.getresponse()
        identity = json.loads(response.read(4096))
        if response.status == 200 and identity.get("instance") and identity.get("pid", 0) > 1:
            return identity
    finally:
        connection.close()
    return None


def measure(args, sample):
    with tempfile.TemporaryDirectory(prefix="vbox-bridge-bench-", dir=args.temp_dir) as directory:
        root = pathlib.Path(directory)
        home = root / "home"
        config = home / ".config/opencode"
        config.mkdir(parents=True, mode=0o700)
        plugin = root / "bridge.mjs"
        shutil.copyfile(args.plugin, plugin)
        (config / "tui.json").write_text(json.dumps({"plugin": [str(plugin)]}))
        (config / "opencode.json").write_text("{}")
        env = {
            "PATH": os.environ.get("PATH", os.defpath),
            "HOME": str(home), "LANG": "C.UTF-8", "TERM": "xterm-256color",
            "XDG_CONFIG_HOME": str(home / ".config"),
            "XDG_DATA_HOME": str(home / ".local/share"),
            "XDG_CACHE_HOME": str(home / ".cache"),
            "XDG_STATE_HOME": str(home / ".local/state"),
            "TMPDIR": str(root), "TMUX_TMPDIR": str(root),
            # A benchmark must never upgrade the caller's installed CLI.
            "OPENCODE_DISABLE_AUTOUPDATE": "1",
        }
        argv = [args.opencode, "--auto"]
        if args.baseline:
            with socket.socket() as listener:
                listener.bind(("127.0.0.1", 0))
                port = listener.getsockname()[1]
            argv += ["--hostname", "127.0.0.1", "--port", str(port)]
        tmux = ["tmux", "-S", str(root / "tmux.sock"), "-f", "/dev/null"]
        started = time.monotonic()
        try:
            subprocess.run(tmux + ["new-session", "-d", "-s", "benchmark", "-c", str(home), "--"] + argv,
                           env=env, capture_output=True, check=True, timeout=10)
            while time.monotonic() - started < args.timeout:
                for path in (home / ".local/share/vmbox/opencode-tui").glob("*.sock"):
                    try:
                        identity = health(path)
                    except (OSError, ValueError, http.client.HTTPException):
                        continue
                    if identity:
                        result = {"sample": sample, "baseline": args.baseline,
                                  "bridgeSeconds": round(time.monotonic() - started, 3)}
                        try:
                            fields = pathlib.Path(f"/proc/{identity['pid']}/stat").read_text().rsplit(")", 1)[1].split()
                            result["processCPUSeconds"] = round((int(fields[11]) + int(fields[12])) / os.sysconf("SC_CLK_TCK"), 3)
                        except (OSError, ValueError, IndexError):
                            pass
                        print(json.dumps(result), flush=True)
                        return
                time.sleep(0.1)
            raise RuntimeError("Bridge did not become healthy within the benchmark timeout")
        finally:
            subprocess.run(tmux + ["kill-server"], env=env, capture_output=True, timeout=10)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--opencode", default=shutil.which("opencode"))
    parser.add_argument("--plugin", type=pathlib.Path,
                        default=pathlib.Path(__file__).resolve().parents[1] / "internal/boxruntime/opencode_tui_plugin.mjs")
    parser.add_argument("--temp-dir")
    parser.add_argument("--baseline", action="store_true", help="Use the former TCP listener (updates stay disabled in both modes)")
    parser.add_argument("--samples", type=int, default=1)
    parser.add_argument("--timeout", type=float, default=90)
    args = parser.parse_args()
    if not args.opencode or not 1 <= args.samples <= 10 or not 1 <= args.timeout <= 300:
        parser.error("Need an OpenCode binary, 1–10 samples, and a timeout of 1–300 seconds")
    args.opencode = str(pathlib.Path(args.opencode).resolve())
    for sample in range(1, args.samples + 1):
        measure(args, sample)


if __name__ == "__main__":
    main()
