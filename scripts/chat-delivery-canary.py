#!/usr/bin/env python3
"""Verify real Chat delivery and a linked agent reply in a disposable box."""

import argparse
import json
import os
import sys
import time
import urllib.error
import urllib.request
import uuid


def request(base, token, path, body=None, key=None):
    headers = {"Authorization": "Bearer " + token}
    data = None
    if body is not None:
        headers["Content-Type"] = "application/json"
        headers["Idempotency-Key"] = key
        data = json.dumps(body).encode()
    call = urllib.request.Request(base + path, data=data, headers=headers)
    with urllib.request.urlopen(call, timeout=20) as response:
        return json.load(response)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--url", required=True, help="Controller base URL")
    parser.add_argument("--box", required=True, help="Running disposable box name or ID")
    parser.add_argument("--timeout", type=int, default=180, help="Seconds to await a linked reply")
    args = parser.parse_args()
    token = os.environ.get("VMBOX_TOKEN", "")
    if not token:
        parser.error("VMBOX_TOKEN is required")
    if args.timeout < 10:
        parser.error("--timeout must be at least 10 seconds")
    base = args.url.rstrip("/")
    boxes = request(base, token, "/v1/logical-boxes")
    box = next((item for item in boxes if args.box in (item["id"], item["name"])), None)
    if box is None or box.get("state") != "running":
        parser.error("--box must identify a running disposable box")
    nonce = uuid.uuid4().hex[:12]
    prompt = f"Chat delivery canary {nonce}. Reply exactly: ACK {nonce}"
    result = request(
        base,
        token,
        f"/v1/logical-boxes/{box['id']}/messages",
        {"text": prompt},
        "chat-canary-" + nonce,
    )
    message_id = result["message"]["id"]
    deadline = time.monotonic() + args.timeout
    state = "unknown"
    while time.monotonic() < deadline:
        messages = request(base, token, f"/v1/logical-boxes/{box['id']}/messages?limit=500")
        submitted = next((item for item in messages if item["id"] == message_id), None)
        if submitted is not None:
            state = submitted.get("state", "unknown")
        replied = any(
            item.get("direction") == "agent"
            and item.get("parentMessageId") == message_id
            and "ACK " + nonce in item.get("text", "")
            for item in messages
        )
        if state == "delivered" and replied:
            print(f"PASS {box['name']} ({box.get('defaultAgent', 'agent')}): native delivery and linked reply for {message_id}")
            return 0
        time.sleep(3)
    print(f"FAIL {box['name']}: {message_id} state={state}; no matching linked reply", file=sys.stderr)
    return 1


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (urllib.error.URLError, ValueError, KeyError) as error:
        print(f"canary request failed: {error}", file=sys.stderr)
        sys.exit(1)
