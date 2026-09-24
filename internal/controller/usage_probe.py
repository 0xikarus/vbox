"""Read account limits with the credentials already installed in a live box.

Only normalized, non-secret fields are written to stdout. The controller sends
this script as argv to an existing worker; it does not require a worker rollout.
"""

import json
import os
import select
import subprocess
import sys
import time
import urllib.request


class ProbeError(Exception):
    """A fixed, credential-free message safe to return to the controller."""


def emit(windows=None, balances=None, spend=None, rate_caps=None, note=""):
    print(json.dumps({"windows": windows or [], "balances": balances or [],
                      "spend": spend, "rateCaps": rate_caps or [], "note": note}))


def get_json(url, key):
    request = urllib.request.Request(url, headers={"Authorization": "Bearer " + key,
                                                   "Accept": "application/json"})
    with urllib.request.urlopen(request, timeout=12) as response:
        return json.load(response)


def opencode(provider, model):
    path = os.path.expanduser("~/.local/share/opencode/auth.json")
    with open(path, encoding="utf-8") as handle:
        credentials = json.load(handle)
    if not provider:
        available = [name for name in ("openrouter", "venice")
                     if credentials.get(name, {}).get("type") == "api"]
        if len(available) != 1:
            raise ProbeError("Select an OpenRouter or Venice model for this profile")
        provider = available[0]
    credential = credentials.get(provider, {})
    if credential.get("type") != "api" or not credential.get("key"):
        raise ProbeError("OpenCode provider API key unavailable")
    key = credential["key"]
    if provider == "openrouter":
        data = get_json("https://openrouter.ai/api/v1/key", key)["data"]
        limit = data.get("limit")
        remaining = data.get("limit_remaining")
        spend = {"currency": "USD", "limit": limit, "remaining": remaining,
                 "period": data.get("limit_reset"), "used": data.get("usage")}
        free = data.get("free_model_daily_requests") or {}
        windows = []
        if free.get("limit"):
            windows.append({"name": "free model daily requests", "group": "daily",
                            "usedPercent": 100 * free.get("used", 0) / free["limit"]})
        emit(windows=windows, spend=spend,
             note="OpenRouter key spending and free-model daily requests; account credits and live request capacity are separate.")
    elif provider == "venice":
        data = get_json("https://api.venice.ai/api/v1/api_keys/rate_limits", key)["data"]
        balances = [{"unit": unit, "amount": amount} for unit, amount in data.get("balances", {}).items()]
        caps = []
        for entry in data.get("rateLimits", []):
            if model and entry.get("apiModelId") != model:
                continue
            for cap in entry.get("rateLimits", []):
                caps.append({"model": entry.get("apiModelId", ""), "type": cap.get("type", ""),
                             "amount": cap.get("amount")})
        emit(balances=balances, rate_caps=caps[:32],
             note="Venice publishes balances and configured model rates, not live requests remaining.")
    else:
        raise ProbeError("unsupported OpenCode provider")


def claude():
    command = ["claude", "-p", "/usage", "--output-format", "stream-json", "--verbose",
               "--no-session-persistence", "--safe-mode", "--strict-mcp-config"]
    result = subprocess.run(command, capture_output=True, text=True, timeout=22)
    if result.returncode:
        raise ProbeError("Claude usage command unavailable")
    for line in result.stdout.splitlines():
        try:
            item = json.loads(line)
        except json.JSONDecodeError:
            continue
        if item.get("type") != "assistant" or item.get("local_command_run", {}).get("command") != "usage":
            continue
        limits = (item.get("usage_report") or {}).get("rate_limits")
        if limits is None:
            raise ProbeError("Claude subscription limits unavailable for this login")
        windows = []
        for row in limits.get("limits") or []:
            scope = row.get("scope") or {}
            model = (scope.get("model") or {}).get("display_name", "")
            surface = (scope.get("surface") or {}).get("display_name", "")
            windows.append({"name": row.get("kind", ""), "group": row.get("group", ""),
                            "usedPercent": row.get("percent"), "resetsAt": row.get("resets_at"),
                            "scope": model or surface, "severity": row.get("severity", "")})
        extra = limits.get("extra_usage") or {}
        spend = None
        if extra.get("is_enabled"):
            spend = {"currency": extra.get("currency"), "limit": extra.get("monthly_limit"),
                     "used": extra.get("used_credits"), "period": "monthly",
                     "unit": "credits"}
        emit(windows=windows, spend=spend)
        return
    raise ProbeError("Claude usage response unavailable")


def codex():
    process = subprocess.Popen(["codex", "app-server"], stdin=subprocess.PIPE,
                               stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
    try:
        messages = [
            {"id": 1, "method": "initialize", "params": {"clientInfo": {
                "name": "vmbox_usage", "title": "vmbox usage", "version": "1.0"}}},
            {"method": "initialized", "params": {}},
            {"id": 2, "method": "account/rateLimits/read"}
        ]
        for message in messages:
            process.stdin.write((json.dumps(message) + "\n").encode())
        process.stdin.flush()
        deadline = time.monotonic() + 18
        pending = b""
        while time.monotonic() < deadline:
            readable, _, _ = select.select([process.stdout], [], [], 1)
            if not readable:
                continue
            chunk = os.read(process.stdout.fileno(), 65536)
            if not chunk:
                break
            pending += chunk
            while b"\n" in pending:
                line, pending = pending.split(b"\n", 1)
                try:
                    item = json.loads(line)
                except json.JSONDecodeError:
                    continue
                if item.get("id") != 2:
                    continue
                if "error" in item:
                    raise ProbeError("Codex account limits unavailable")
                result = item.get("result") or {}
                buckets = result.get("rateLimitsByLimitId") or {}
                if not buckets and result.get("rateLimits"):
                    buckets = {"codex": result["rateLimits"]}
                windows = []
                for name, bucket in buckets.items():
                    for label in ("primary", "secondary"):
                        value = bucket.get(label)
                        if not value:
                            continue
                        reset = value.get("resetsAt")
                        windows.append({"name": bucket.get("limitName") or name,
                                        "group": label, "usedPercent": value.get("usedPercent"),
                                        "resetsAt": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(reset)) if reset else None,
                                        "durationMinutes": value.get("windowDurationMins")})
                if not windows:
                    raise ProbeError("Codex ChatGPT limits unavailable for this login")
                emit(windows=windows)
                return
            if len(pending) > 1048576:
                raise ProbeError("Codex usage response too large")
        raise ProbeError("Codex usage response timed out")
    finally:
        process.terminate()
        try:
            process.wait(timeout=2)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait()
        process.stdin.close()
        process.stdout.close()


def main():
    try:
        app = sys.argv[1]
        if app == "opencode":
            opencode(sys.argv[2], sys.argv[3] if len(sys.argv) > 3 else "")
        elif app == "claude":
            claude()
        elif app == "codex":
            codex()
        else:
            raise ProbeError("unsupported harness")
    except Exception as error:
        # No exception text: HTTP libraries and CLI failures may include credentials.
        print(json.dumps({"error": str(error) if isinstance(error, ProbeError) else "usage provider unavailable"}))


if __name__ == "__main__":
    main()
