#!/usr/bin/env python3
"""Stdio MCP bridge for the controller's task-scoped API.

Configure VMBOX_CONTROLLER_URL and VMBOX_TASK_TOKEN in the MCP client's
environment. The token is an owner-issued task service credential, not an
owner controller token. This process never writes it to stdout or stderr.
"""

import json
import os
import random
import sys
import time
import urllib.error
import urllib.parse
import urllib.request


VERSION = "2025-11-25"
SERVER_INFO = {"name": "vbox-tasks", "version": "1.0.0"}


def tool(name, description, properties=None, required=()):
    return {
        "name": name,
        "description": description,
        "inputSchema": {
            "type": "object",
            "properties": properties or {},
            "required": list(required),
            "additionalProperties": False,
        },
    }


STRING = {"type": "string"}
INTEGER = {"type": "integer"}
TOOLS = [
    tool("list_task_profiles", "List reusable task configurations available to this credential. Select one before delegating a task."),
    tool("list_login_profiles", "List permitted, anonymized login profile IDs for a custom task configuration."),
    tool(
        "create_task",
        "Delegate one goal to a NEW temporary vbox task. Returns immediately, usually queued. Use get_task to poll for its result. Task boxes are deleted after retention; do not use this tool for persistent boxes. Reuse request_id when retrying an uncertain submission.",
        {
            "name": {"type": "string", "description": "Short task name"},
            "goal": {"type": "string", "description": "Complete instructions and expected result for the delegated agent"},
            "request_id": {"type": "string", "description": "Unique stable idempotency key for this submission; reuse on retry"},
            "task_profile": {"type": "string", "description": "Saved task profile name or ID. Cannot be combined with custom configuration"},
            "agent": {"type": "string", "enum": ["codex", "claude", "opencode"], "description": "Required only without task_profile"},
            "login_profile_id": {"type": "string", "description": "Opaque ID from list_login_profiles; required only without task_profile"},
            "model": STRING,
            "reasoning_effort": STRING,
            "disk_gib": INTEGER,
            "memory_gib": INTEGER,
            "swap_gib": INTEGER,
            "time_limit_seconds": INTEGER,
            "retention_seconds": INTEGER,
        },
        ("name", "goal", "request_id"),
    ),
    tool("get_task", "Read one task's current state, result, failure and expiry. Poll about every 30 seconds; this never wakes its box.", {"task_id": STRING}, ("task_id",)),
    tool("list_tasks", "List recent tasks visible to this credential, including IDs for recovery after an uncertain create response.", {"state": {"type": "string", "description": "Optional exact state, current or history"}, "limit": {"type": "integer", "minimum": 1, "maximum": 100}, "cursor": STRING}),
    tool("get_task_events", "Read a task's progress timeline after an optional event cursor.", {"task_id": STRING, "after": STRING}, ("task_id",)),
]
TOOL_NAMES = {item["name"] for item in TOOLS}


class TaskAPIError(Exception):
    pass


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, fp, code, msg, headers, newurl):
        # urllib otherwise forwards Authorization to a redirected host.
        return None


class TaskAPI:
    def __init__(self, base, token):
        parsed = urllib.parse.urlsplit(base)
        if parsed.scheme != "https" and not (parsed.scheme == "http" and parsed.hostname in {"localhost", "127.0.0.1", "::1"}):
            raise ValueError("VMBOX_CONTROLLER_URL must use HTTPS, except for localhost")
        if not parsed.netloc or parsed.username or parsed.password or parsed.query or parsed.fragment or parsed.path not in {"", "/"}:
            raise ValueError("VMBOX_CONTROLLER_URL must be the controller origin")
        if not token.startswith("vbtask_"):
            raise ValueError("VMBOX_TASK_TOKEN must be a task service token")
        self.base = base.rstrip("/")
        self.token = token
        self.opener = urllib.request.build_opener(NoRedirect)

    def request(self, method, path, body=None, idempotency_key=None):
        headers = {"Authorization": "Bearer " + self.token, "Accept": "application/json"}
        data = None
        if body is not None:
            headers["Content-Type"] = "application/json"
            data = json.dumps(body, ensure_ascii=False).encode("utf-8")
        if idempotency_key:
            headers["Idempotency-Key"] = idempotency_key
        for attempt in range(3):
            try:
                request = urllib.request.Request(self.base + path, data=data, headers=headers, method=method)
                with self.opener.open(request, timeout=45) as response:
                    raw = response.read(2_000_001)
                    if len(raw) > 2_000_000:
                        raise TaskAPIError("controller response is too large")
                    return json.loads(raw) if raw else None
            except urllib.error.HTTPError as error:
                if error.code < 500 and error.code != 429:
                    raise TaskAPIError(f"controller returned HTTP {error.code} for {method} {path}") from None
            except (urllib.error.URLError, TimeoutError):
                pass
            if attempt == 2:
                raise TaskAPIError(f"controller request failed for {method} {path}; retry with the same request_id")
            time.sleep(min(2 ** attempt, 3) + random.random())


def segment(value):
    if not isinstance(value, str) or not value or len(value) > 128:
        raise ValueError("task_id must be a nonempty string of at most 128 characters")
    return urllib.parse.quote(value, safe="")


def require_string(args, name):
    value = args.get(name)
    if not isinstance(value, str) or not value.strip():
        raise ValueError(f"{name} is required")
    return value.strip()


def execute(api, name, args):
    if not isinstance(args, dict):
        raise ValueError("arguments must be an object")
    allowed = next(item["inputSchema"]["properties"] for item in TOOLS if item["name"] == name)
    unexpected = set(args) - set(allowed)
    if unexpected:
        raise ValueError("unsupported argument: " + sorted(unexpected)[0])
    if name == "list_task_profiles":
        return api.request("GET", "/v1/task-profiles")
    if name == "list_login_profiles":
        return api.request("GET", "/v1/tasks/profiles")
    if name == "create_task":
        task_name = require_string(args, "name")
        goal = require_string(args, "goal")
        key = require_string(args, "request_id")
        if len(key) > 128 or "\r" in key or "\n" in key:
            raise ValueError("request_id must be at most 128 characters without line breaks")
        body = {"name": task_name, "goal": goal}
        profile = args.get("task_profile")
        custom_fields = ("agent", "login_profile_id", "model", "reasoning_effort", "disk_gib", "memory_gib", "swap_gib", "time_limit_seconds", "retention_seconds")
        if profile:
            if any(args.get(field) is not None for field in custom_fields):
                raise ValueError("task_profile cannot be combined with custom configuration")
            body["taskProfile"] = require_string(args, "task_profile")
        else:
            agent = require_string(args, "agent")
            if agent not in {"codex", "claude", "opencode"}:
                raise ValueError("agent must be codex, claude or opencode")
            login = {"profileId": require_string(args, "login_profile_id")}
            if args.get("model"):
                login["model"] = require_string(args, "model")
            if args.get("reasoning_effort"):
                login["reasoningEffort"] = require_string(args, "reasoning_effort")
            body.update({"agent": agent, "loginProfiles": [login]})
            for field, target in (("disk_gib", "diskGiB"), ("memory_gib", "memoryGiB"), ("swap_gib", "swapGiB"), ("time_limit_seconds", "timeLimitSeconds"), ("retention_seconds", "retentionSeconds")):
                if field in args:
                    value = args[field]
                    if type(value) is not int or value < 0:
                        raise ValueError(f"{field} must be a nonnegative integer")
                    body[target] = value
        return api.request("POST", "/v1/tasks", body, key)
    if name == "get_task":
        return api.request("GET", "/v1/tasks/" + segment(require_string(args, "task_id")))
    if name == "list_tasks":
        query = {}
        for field in ("state", "cursor"):
            if args.get(field):
                query[field] = require_string(args, field)
        if "limit" in args:
            value = args["limit"]
            if type(value) is not int or not 1 <= value <= 100:
                raise ValueError("limit must be an integer from 1 to 100")
            query["limit"] = value
        path = "/v1/tasks" + ("?" + urllib.parse.urlencode(query) if query else "")
        return api.request("GET", path)
    if name == "get_task_events":
        path = "/v1/tasks/" + segment(require_string(args, "task_id")) + "/events"
        if args.get("after"):
            path += "?" + urllib.parse.urlencode({"after": require_string(args, "after")})
        return api.request("GET", path)
    raise ValueError("unknown tool")


def reply(message_id, result=None, error=None):
    message = {"jsonrpc": "2.0", "id": message_id}
    message["error" if error else "result"] = error if error else result
    sys.stdout.write(json.dumps(message, ensure_ascii=False, separators=(",", ":")) + "\n")
    sys.stdout.flush()


def serve(api):
    initialized = False
    for line in sys.stdin:
        try:
            request = json.loads(line)
        except (ValueError, UnicodeError):
            reply(None, error={"code": -32700, "message": "Parse error"})
            continue
        if not isinstance(request, dict) or request.get("jsonrpc") != "2.0" or not isinstance(request.get("method"), str):
            reply(request.get("id") if isinstance(request, dict) else None, error={"code": -32600, "message": "Invalid Request"})
            continue
        if "id" not in request:
            if request["method"] == "notifications/initialized":
                initialized = True
            continue
        method = request["method"]
        if method == "initialize":
            initialized = True
            reply(request["id"], {"protocolVersion": VERSION, "capabilities": {"tools": {"listChanged": False}}, "serverInfo": SERVER_INFO,
                                  "instructions": "Use create_task only for temporary delegated goals. It returns immediately; poll get_task about every 30 seconds until completed, failed, timed_out, abandoned or cancelled. Use the same request_id on uncertain create retries."})
        elif not initialized:
            reply(request["id"], error={"code": -32000, "message": "Server not initialized"})
        elif method == "ping":
            reply(request["id"], {})
        elif method == "tools/list":
            reply(request["id"], {"tools": TOOLS})
        elif method == "tools/call":
            params = request.get("params") or {}
            name = params.get("name") if isinstance(params, dict) else None
            if name not in TOOL_NAMES:
                reply(request["id"], error={"code": -32602, "message": "Unknown tool"})
                continue
            try:
                output = execute(api, name, params.get("arguments") or {})
                result = {"content": [{"type": "text", "text": json.dumps(output, ensure_ascii=False)}], "isError": False}
            except (ValueError, TaskAPIError) as error:
                result = {"content": [{"type": "text", "text": str(error)}], "isError": True}
            reply(request["id"], result)
        else:
            reply(request["id"], error={"code": -32601, "message": "Method not found"})


def main():
    try:
        api = TaskAPI(os.environ.get("VMBOX_CONTROLLER_URL", ""), os.environ.get("VMBOX_TASK_TOKEN", ""))
    except ValueError as error:
        print(error, file=sys.stderr)
        return 2
    serve(api)
    return 0


if __name__ == "__main__":
    sys.exit(main())
