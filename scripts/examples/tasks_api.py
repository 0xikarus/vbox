#!/usr/bin/env python3
"""Create disposable Tasks through the API, poll, and save their results.

export VMBOX_CONTROLLER_URL=https://your-controller.example
export VMBOX_TASK_TOKEN=...  # owner-created task service token
python3 scripts/examples/tasks_api.py --list-profiles
python3 scripts/examples/tasks_api.py --profile-id lp_... --count 6 --delete

No third-party packages. --count 6 exercises queuing beyond the four-task cap.
Each invocation has a unique idempotency key. Use --task-id to resume polling.
"""
import argparse
import json
import os
from pathlib import Path
import random
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid

TERMINAL = {"completed", "failed", "timed_out", "abandoned", "cancelled"}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--list-profiles", action="store_true")
    parser.add_argument("--profile-id")
    parser.add_argument("--task-profile", help="saved task configuration name or ID")
    parser.add_argument("--list-task-profiles", action="store_true")
    parser.add_argument("--agent", choices=["codex", "claude", "opencode"], default="codex")
    parser.add_argument("--count", type=int, default=1)
    parser.add_argument("--disk-gib", type=int, default=10)
    parser.add_argument("--poll-seconds", type=float, default=30)
    parser.add_argument("--task-id", action="append", default=[])
    parser.add_argument("--output", type=Path, default=Path("task-results"))
    parser.add_argument("--delete", action="store_true", help="delete completed test tasks and boxes after downloading results")
    args = parser.parse_args()
    base = os.environ.get("VMBOX_CONTROLLER_URL", "").rstrip("/")
    token = os.environ.get("VMBOX_TASK_TOKEN", "")
    parsed = urllib.parse.urlparse(base)
    if not token or parsed.scheme not in {"http", "https"} or not parsed.netloc:
        parser.error("set VMBOX_CONTROLLER_URL and VMBOX_TASK_TOKEN")
    if not 1 <= args.count <= 20 or args.poll_seconds < 1:
        parser.error("count must be 1–20 and poll-seconds at least 1")

    def request(path, method="GET", body=None, key=None, binary=False):
        headers = {"Authorization": "Bearer " + token, "Accept": "application/json"}
        if key:
            headers["Idempotency-Key"] = key
        data = None
        if body is not None:
            headers["Content-Type"] = "application/json"
            data = json.dumps(body).encode()
        # The same idempotency key is retained across network/server retries.
        for attempt in range(5):
            try:
                req = urllib.request.Request(base + path, data=data, method=method, headers=headers)
                with urllib.request.urlopen(req, timeout=45) as response:
                    raw = response.read()
                    return raw if binary else json.loads(raw) if raw else None
            except urllib.error.HTTPError as exc:
                if exc.code < 500 and exc.code != 429:
                    raise RuntimeError(f"{method} {path}: HTTP {exc.code}") from None
            except (urllib.error.URLError, TimeoutError):
                pass
            if attempt == 4:
                raise RuntimeError(f"{method} {path}: request failed after retries")
            time.sleep(min(30, 2 ** attempt) + random.random())

    if args.list_task_profiles:
        print(json.dumps(request("/v1/task-profiles"), indent=2))
        return 0
    if args.task_profile and args.profile_id:
        parser.error("use either --task-profile or --profile-id")
    profiles = request("/v1/tasks/profiles")
    if args.list_profiles:
        print(json.dumps(profiles, indent=2))
        return 0
    if not args.task_id and not args.task_profile and not any(p["id"] == args.profile_id and p["application"] == args.agent for p in profiles):
        parser.error("choose an allowed matching --profile-id from --list-profiles")
    ids = list(args.task_id)
    for index in range(0 if ids else args.count):
        key = str(uuid.uuid4())
        name = "api-test-" + key[:8]
        goal = ("Disposable API test. Create api-test-result.txt in your workspace directory containing exactly "
                + name + ". Then call completed_task with text 'API test passed: " + name
                + "' and files ['api-test-result.txt']. Do not create other resources.")
        body = {"name": name, "goal": goal}
        if args.task_profile:
            body["taskProfile"] = args.task_profile
        else:
            body.update({"agent": args.agent, "loginProfiles": [{"profileId": args.profile_id}],
                         "diskGiB": args.disk_gib, "timeLimitSeconds": 900, "retentionSeconds": 3600})
        task = request("/v1/tasks", "POST", body, key=key)
        ids.append(task["id"])
        print(f"Created {task['id']}: {task['state']}", flush=True)
    print("Resume with " + " ".join("--task-id " + i for i in ids), flush=True)
    pending = set(ids)
    failed = False
    while pending:
        for task_id in sorted(pending):
            path = "/v1/tasks/" + urllib.parse.quote(task_id, safe="")
            task = request(path)
            print(f"{task_id}: {task['state']}" + (f" (queue position {task['queuePosition']})" if task.get("queuePosition") else ""), flush=True)
            if task["state"] not in TERMINAL:
                continue
            folder = args.output / task_id
            folder.mkdir(parents=True, exist_ok=True)
            (folder / "task.json").write_text(json.dumps(task, indent=2))
            for file in (task.get("result") or {}).get("files", []):
                # Use opaque file IDs as local names. Remote names cannot escape output.
                file_id = str(uuid.UUID(file["id"]))
                data = request(path + "/files/" + file_id, binary=True)
                (folder / file_id).write_bytes(data)
            pending.remove(task_id)
            failed |= task["state"] != "completed"
            if args.delete:
                request(path, "DELETE")
        if pending:
            time.sleep(args.poll_seconds + random.uniform(0, min(3, args.poll_seconds / 10)))
    print("Results saved to " + str(args.output))
    return 1 if failed else 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (RuntimeError, KeyboardInterrupt) as error:
        print(str(error) or "Stopped polling; tasks keep running. Resume with their IDs.", file=sys.stderr)
        sys.exit(2)
