"""Local protocol and API contract tests for the external task MCP bridge."""

import json
import os
from pathlib import Path
import subprocess
import sys
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import unittest


ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "scripts" / "vbox_task_mcp.py"
TOKEN = "vbtask_fixture_not_a_secret"


class APIHandler(BaseHTTPRequestHandler):
    calls = []
    redirect = False

    def log_message(self, *args):
        pass

    def respond(self, status, data):
        body = json.dumps(data).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def handle_request(self):
        length = int(self.headers.get("Content-Length", "0"))
        body = json.loads(self.rfile.read(length)) if length else None
        self.calls.append((self.command, self.path, self.headers.get("Authorization"), self.headers.get("Idempotency-Key"), body))
        if self.redirect:
            self.send_response(302)
            self.send_header("Location", "http://127.0.0.1:1/steal")
            self.end_headers()
        elif self.path == "/v1/task-profiles":
            self.respond(200, [{"name": "codex-preset"}])
        elif self.path == "/v1/tasks/profiles":
            self.respond(200, [{"id": "lp_fixture", "label": "codex · FIXTURE"}])
        elif self.command == "POST" and self.path == "/v1/tasks":
            self.respond(201, {"id": "task-fixture", "state": "queued", "name": body["name"]})
        elif self.path == "/v1/tasks/task-fixture":
            self.respond(200, {"id": "task-fixture", "state": "completed", "result": {"text": "done"}})
        elif self.path == "/v1/tasks":
            self.respond(200, {"tasks": [{"id": "task-fixture", "state": "completed"}], "nextCursor": ""})
        else:
            self.respond(404, {"error": "missing"})

    do_GET = handle_request
    do_POST = handle_request


class TaskMCPTest(unittest.TestCase):
    def setUp(self):
        APIHandler.calls = []
        APIHandler.redirect = False
        self.http = ThreadingHTTPServer(("127.0.0.1", 0), APIHandler)
        self.thread = threading.Thread(target=self.http.serve_forever, daemon=True)
        self.thread.start()
        env = {**os.environ, "VMBOX_CONTROLLER_URL": f"http://127.0.0.1:{self.http.server_port}", "VMBOX_TASK_TOKEN": TOKEN}
        self.process = subprocess.Popen([sys.executable, str(SCRIPT)], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, env=env)

    def tearDown(self):
        self.process.stdin.close()
        self.process.wait(timeout=5)
        self.process.stdout.close()
        self.process.stderr.close()
        self.http.shutdown()
        self.http.server_close()
        self.thread.join(timeout=5)

    def rpc(self, method, params=None, message_id=1):
        request = {"jsonrpc": "2.0", "id": message_id, "method": method}
        if params is not None:
            request["params"] = params
        self.process.stdin.write(json.dumps(request) + "\n")
        self.process.stdin.flush()
        response = json.loads(self.process.stdout.readline())
        self.assertEqual(response["id"], message_id)
        return response

    def initialize(self):
        response = self.rpc("initialize", {"protocolVersion": "2025-11-25", "capabilities": {}, "clientInfo": {"name": "test", "version": "1"}})
        self.assertEqual(response["result"]["protocolVersion"], "2025-11-25")

    def call(self, name, args=None):
        return self.rpc("tools/call", {"name": name, "arguments": args or {}})["result"]

    def test_delegation_returns_immediately_and_reuses_idempotency_key(self):
        self.initialize()
        names = {tool["name"] for tool in self.rpc("tools/list")["result"]["tools"]}
        self.assertIn("create_task", names)
        self.assertEqual(json.loads(self.call("list_task_profiles")["content"][0]["text"])[0]["name"], "codex-preset")
        args = {"name": "fixture-job", "goal": "Return a short result", "request_id": "fixture-request-1", "task_profile": "codex-preset"}
        self.assertEqual(json.loads(self.call("create_task", args)["content"][0]["text"])["state"], "queued")
        self.assertEqual(json.loads(self.call("get_task", {"task_id": "task-fixture"})["content"][0]["text"])["result"]["text"], "done")
        self.assertEqual(json.loads(self.call("list_tasks")["content"][0]["text"])["tasks"][0]["id"], "task-fixture")
        self.assertEqual(APIHandler.calls[1][3], "fixture-request-1")
        self.assertEqual(APIHandler.calls[1][4], {"name": "fixture-job", "goal": "Return a short result", "taskProfile": "codex-preset"})
        self.assertTrue(all(call[2] == "Bearer " + TOKEN for call in APIHandler.calls))

    def test_rejects_profile_overrides_before_api_call(self):
        self.initialize()
        result = self.call("create_task", {"name": "bad", "goal": "test", "request_id": "id-1", "task_profile": "preset", "disk_gib": 10})
        self.assertTrue(result["isError"])
        self.assertEqual(APIHandler.calls, [])

    def test_custom_task_uses_opaque_login_profile_and_resource_limits(self):
        self.initialize()
        args = {"name": "custom-job", "goal": "Do the work", "request_id": "custom-request", "agent": "codex",
                "login_profile_id": "lp_fixture", "model": "gpt-6-sol", "reasoning_effort": "high",
                "memory_gib": 4, "swap_gib": 1, "disk_gib": 10}
        result = self.call("create_task", args)
        self.assertFalse(result["isError"])
        self.assertEqual(APIHandler.calls[0][4], {
            "name": "custom-job", "goal": "Do the work", "agent": "codex",
            "loginProfiles": [{"profileId": "lp_fixture", "model": "gpt-6-sol", "reasoningEffort": "high"}],
            "memoryGiB": 4, "swapGiB": 1, "diskGiB": 10,
        })

    def test_redirect_does_not_forward_credential(self):
        self.initialize()
        APIHandler.redirect = True
        result = self.call("list_task_profiles")
        self.assertTrue(result["isError"])
        self.assertIn("HTTP 302", result["content"][0]["text"])
        self.assertNotIn(TOKEN, result["content"][0]["text"])


if __name__ == "__main__":
    unittest.main()
