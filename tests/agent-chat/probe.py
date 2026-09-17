import http.server
import base64
import json
import os
import pathlib
import subprocess
import sys
import threading
import uuid


def record(kind, value):
    path = pathlib.Path(os.environ["VMBOX_PROBE_LOG"])
    with path.open("a") as output:
        output.write(json.dumps({"kind": kind, "value": value}) + "\n")


def mcp():
    tools = [
        {"name": "chat_ask", "description": "Ask the user a question in web chat.", "inputSchema": {"type": "object", "properties": {"question": {"type": "string"}, "choices": {"type": "array", "items": {"type": "string"}}}, "required": ["question"], "additionalProperties": False}},
        {"name": "chat_reply", "description": "Send a reply to web chat.", "inputSchema": {"type": "object", "properties": {"text": {"type": "string"}}, "required": ["text"], "additionalProperties": False}},
    ]
    for line in sys.stdin:
        request = json.loads(line)
        if "id" not in request:
            continue
        method = request["method"]
        if method == "initialize":
            record("initialize", request.get("params"))
            result = {"protocolVersion": request["params"]["protocolVersion"], "capabilities": {"tools": {}}, "serverInfo": {"name": "vmbox-adapter-probe", "version": "1"}}
        elif method == "tools/list":
            result = {"tools": tools}
        elif method == "tools/call":
            record("call", request["params"])
            result = {"content": [{"type": "text", "text": "disposable-web-answer"}]}
        else:
            result = {}
        print(json.dumps({"jsonrpc": "2.0", "id": request["id"], "result": result}), flush=True)


def hook():
    payload = json.load(sys.stdin)
    record("hook", payload)
    arguments = dict(payload["tool_input"])
    arguments["__vmbox_probe"] = {"session": payload["session_id"], "call": payload["tool_use_id"]}
    print(json.dumps({"hookSpecificOutput": {"hookEventName": "PreToolUse", "updatedInput": arguments}}))


class Model(http.server.BaseHTTPRequestHandler):
    def log_message(self, *_args):
        pass

    def do_GET(self):
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(json.dumps({"data": [{"id": "gpt-4o", "object": "model", "owned_by": "fixture"}]}).encode())

    def do_POST(self):
        payload = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        record("model", {"path": self.path, "payload": payload})
        if "count_tokens" in self.path:
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(b'{"input_tokens":10}')
            return
        available = []
        for tool in payload.get("tools", []):
            if tool.get("type") == "namespace":
                for function in tool.get("tools", []):
                    available.append(tool["name"] + "." + function["name"])
            else:
                available.append(tool.get("function", tool).get("name", ""))
        serialized = json.dumps(payload.get("input", payload.get("messages", [])))
        completed = serialized.count("disposable-web-answer")
        suffix = "chat_ask" if completed == 0 else "chat_reply"
        tool_name = next((name for name in available if name.endswith(suffix)), None)
        if not tool_name:
            record("missing_tool", {"expected": suffix, "available": available})
        arguments = {"question": "Which disposable model?", "choices": ["A", "B"]} if completed == 0 else {"text": "Received disposable-web-answer"}
        if completed > 0 and os.environ.get("VMBOX_PROBE_FULL_RUNTIME") == "1":
            arguments["files"] = [str(pathlib.Path.home() / "result.png")]
        call = tool_name is not None and completed < 2
        identifier = uuid.uuid4().hex
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-cache")
        self.end_headers()
        if "/messages" in self.path:
            self.anthropic(identifier, tool_name, arguments, call)
        elif "/chat/completions" in self.path:
            self.completions(identifier, tool_name, arguments, call)
        else:
            self.openai_responses(identifier, tool_name, arguments, call)

    def event(self, name, value):
        if name and name.startswith("response."):
            self.sequence = getattr(self, "sequence", 0) + 1
            value["sequence_number"] = self.sequence
        if name:
            self.wfile.write(("event: " + name + "\n").encode())
        self.wfile.write(("data: " + json.dumps(value) + "\n\n").encode())
        self.wfile.flush()

    def anthropic(self, identifier, name, arguments, call):
        self.event("message_start", {"type": "message_start", "message": {"id": "msg_" + identifier, "type": "message", "role": "assistant", "content": [], "model": "claude-sonnet-4-6", "stop_reason": None, "stop_sequence": None, "usage": {"input_tokens": 10, "output_tokens": 0}}})
        block = {"type": "tool_use", "id": "toolu_" + identifier, "name": name, "input": {}} if call else {"type": "text", "text": ""}
        self.event("content_block_start", {"type": "content_block_start", "index": 0, "content_block": block})
        delta = {"type": "input_json_delta", "partial_json": json.dumps(arguments)} if call else {"type": "text_delta", "text": "Probe complete."}
        self.event("content_block_delta", {"type": "content_block_delta", "index": 0, "delta": delta})
        self.event("content_block_stop", {"type": "content_block_stop", "index": 0})
        self.event("message_delta", {"type": "message_delta", "delta": {"stop_reason": "tool_use" if call else "end_turn", "stop_sequence": None}, "usage": {"output_tokens": 10}})
        self.event("message_stop", {"type": "message_stop"})

    def completions(self, identifier, name, arguments, call):
        base = {"id": identifier, "object": "chat.completion.chunk", "created": 1, "model": "gpt-4o"}
        delta = {"role": "assistant", "tool_calls": [{"index": 0, "id": "call_" + identifier, "type": "function", "function": {"name": name, "arguments": json.dumps(arguments)}}]} if call else {"role": "assistant", "content": "Probe complete."}
        self.event(None, {**base, "choices": [{"index": 0, "delta": delta, "finish_reason": None}]})
        self.event(None, {**base, "choices": [{"index": 0, "delta": {}, "finish_reason": "tool_calls" if call else "stop"}], "usage": {"prompt_tokens": 10, "completion_tokens": 10, "total_tokens": 20}})
        self.wfile.write(b"data: [DONE]\n\n")

    def openai_responses(self, identifier, name, arguments, call):
        response = {"id": "resp_" + identifier, "object": "response", "created_at": 1, "status": "in_progress", "model": "gpt-4o", "output": [], "parallel_tool_calls": False}
        self.event("response.created", {"type": "response.created", "response": response})
        if call:
            item = {"type": "function_call", "id": "fc_" + identifier, "call_id": "call_" + identifier, "name": name, "arguments": "", "status": "in_progress"}
            if "." in name:
                item["namespace"], item["name"] = name.split(".", 1)
            self.event("response.output_item.added", {"type": "response.output_item.added", "output_index": 0, "item": item})
            self.event("response.function_call_arguments.delta", {"type": "response.function_call_arguments.delta", "output_index": 0, "item_id": item["id"], "delta": json.dumps(arguments)})
            item["arguments"] = json.dumps(arguments)
            item["status"] = "completed"
            self.event("response.function_call_arguments.done", {"type": "response.function_call_arguments.done", "output_index": 0, "item_id": item["id"], "arguments": item["arguments"]})
        else:
            item = {"type": "message", "id": "msg_" + identifier, "role": "assistant", "status": "completed", "content": [{"type": "output_text", "text": "Probe complete.", "annotations": []}]}
            self.event("response.output_item.added", {"type": "response.output_item.added", "output_index": 0, "item": {**item, "content": []}})
            self.event("response.output_text.delta", {"type": "response.output_text.delta", "output_index": 0, "content_index": 0, "item_id": item["id"], "delta": "Probe complete."})
        self.event("response.output_item.done", {"type": "response.output_item.done", "output_index": 0, "item": item})
        response.update(status="completed", output=[item], usage={"input_tokens": 10, "output_tokens": 10, "total_tokens": 20})
        self.event("response.completed", {"type": "response.completed", "response": response})


def run_probe(agent):
    home = pathlib.Path("/tmp/probe-" + agent)
    home.mkdir(mode=0o700, exist_ok=True)
    os.environ.update(HOME=str(home), XDG_CONFIG_HOME=str(home / ".config"), XDG_DATA_HOME=str(home / ".local/share"), XDG_CACHE_HOME=str(home / ".cache"), VMBOX_PROBE_LOG=str(home / "events.jsonl"))
    production_binding = os.environ.get("VMBOX_PROBE_RUNTIME")
    full_runtime = os.environ.get("VMBOX_PROBE_FULL_RUNTIME") == "1"
    if full_runtime:
        (home / "result.png").write_bytes(base64.b64decode("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGP4z8AAAAMBAQDJ/pLvAAAAAElFTkSuQmCC"))
    assignment = os.environ.get("VMBOX_PROBE_ASSIGNMENT", "a" * 64)
    if production_binding:
        subprocess.run([production_binding, "native-bind", assignment], check=True)
        subprocess.run([production_binding, "desktop-register", agent], check=True)
    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Model)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    base = "http://127.0.0.1:" + str(server.server_port)
    command = ["python3", "/probe/probe.py", "mcp"]
    if full_runtime:
        command = [production_binding, "desktop-mcp"]
    prompt = "Call chat_ask, then chat_reply with the answer. This is a disposable integration probe."
    if agent == "codex":
        config = home / ".codex"
        config.mkdir(exist_ok=True)
        (config / "config.toml").write_text('model = "gpt-4o"\nmodel_provider = "fixture"\n[model_providers.fixture]\nname = "Fixture"\nbase_url = "' + base + '/v1"\nwire_api = "responses"\n[mcp_servers.vmbox]\ncommand = "python3"\nargs = ["/probe/probe.py", "mcp"]\nenv_vars = ["VMBOX_PROBE_LOG"]\n')
        if full_runtime:
            path = config / "config.toml"
            path.write_text(path.read_text().replace('[mcp_servers.vmbox]', '[mcp_servers.vmbox-desktop]').replace('command = "python3"', 'command = ' + json.dumps(production_binding)).replace('args = ["/probe/probe.py", "mcp"]', 'args = ["desktop-mcp"]'))
            subprocess.run([production_binding, "desktop-register", agent], check=True)
        argv = ["codex", "exec", "--skip-git-repo-check", "--dangerously-bypass-approvals-and-sandbox", prompt]
    elif agent == "claude":
        os.environ.update(ANTHROPIC_BASE_URL=base, ANTHROPIC_API_KEY="disposable-fixture-key", CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC="1", DISABLE_AUTOUPDATER="1")
        config = home / "mcp.json"
        server_name = "vmbox-desktop" if production_binding else "vmbox"
        config.write_text(json.dumps({"mcpServers": {server_name: {"command": command[0], "args": command[1:], "env": {"VMBOX_PROBE_LOG": os.environ["VMBOX_PROBE_LOG"]}}}}))
        settings = home / "settings.json"
        settings.write_text(json.dumps({"hooks": {"PreToolUse": [{"matcher": "mcp__vmbox__chat_.*", "hooks": [{"type": "command", "command": "python3 /probe/probe.py hook"}]}]}}))
        argv = ["claude", "-p", prompt, "--model", "claude-sonnet-4-6", "--mcp-config", str(config), "--strict-mcp-config", "--settings", str(settings), "--allowedTools", "mcp__vmbox__chat_ask,mcp__vmbox__chat_reply"]
        if production_binding:
            settings.write_text("{}")
            argv[-1] = "mcp__vmbox-desktop__chat_ask,mcp__vmbox-desktop__chat_reply"
    else:
        os.environ.update(OPENCODE_DISABLE_MODELS_FETCH="true", OPENCODE_DISABLE_AUTOUPDATE="true", OPENAI_API_KEY="disposable-fixture-key")
        config = home / ".config/opencode"
        config.mkdir(parents=True, exist_ok=True)
        (config / "opencode.json").write_text(json.dumps({"model": "openai/gpt-4o", "provider": {"openai": {"options": {"baseURL": base + "/v1", "apiKey": "disposable-fixture-key"}}}, "mcp": {"vmbox": {"type": "local", "command": command, "environment": {"VMBOX_PROBE_LOG": os.environ["VMBOX_PROBE_LOG"]}}}}))
        plugins = config / "plugins"
        plugins.mkdir(exist_ok=True)
        (plugins / "vmbox-probe.js").write_text('export const Probe = async () => ({"tool.execute.before": async (input, output) => { if (input.tool.includes("chat_")) output.args.__vmbox_probe = {session: input.sessionID, call: input.callID}; }});\n')
        if production_binding:
            (plugins / "vmbox-probe.js").unlink()
            config_path = config / "opencode.json"
            settings = json.loads(config_path.read_text())
            settings["mcp"]["vmbox-desktop"] = settings["mcp"].pop("vmbox")
            config_path.write_text(json.dumps(settings))
        argv = ["opencode", "run", "--format", "json", prompt]
    try:
        result = subprocess.run(argv, cwd=home, capture_output=True, text=True, timeout=180 if full_runtime else 100)
        (home / "stdout.log").write_text(result.stdout)
        (home / "stderr.log").write_text(result.stderr)
        events = [json.loads(line) for line in pathlib.Path(os.environ["VMBOX_PROBE_LOG"]).read_text().splitlines()]
        calls = [event["value"] for event in events if event["kind"] == "call"]
        print(json.dumps({"agent": agent, "exit": result.returncode, "calls": calls}), flush=True)
        if result.returncode != 0 or (not full_runtime and len(calls) != 2):
            print(result.stderr[-5000:], file=sys.stderr)
            print(result.stdout[-3000:], file=sys.stderr)
            raise RuntimeError("real client probe did not complete both calls")
        if full_runtime:
            model_events = [event["value"]["payload"] for event in events if event["kind"] == "model"]
            assert any("disposable-web-answer" in json.dumps(payload.get("input", payload.get("messages", []))) for payload in model_events), "web answer did not reach the native conversation"
            print(json.dumps({"agent": agent, "runtimeAnswerReceived": True}), flush=True)
        if production_binding and agent in ("claude", "opencode"):
            bindings = [json.loads(path.read_text()) for path in (home / ".local/share/vmbox/chat/bindings").glob("*.json")]
            assert len(bindings) == 2, bindings
            assert len({binding["nativeSession"] for binding in bindings}) == 1, bindings
            assert len({binding["callId"] for binding in bindings}) == 2, bindings
            for call in calls:
                call_id = call["_meta"]["claudecode/toolUseId"] if agent == "claude" else call["arguments"]["__vmbox_binding"]["callID"]
                assert any(binding["callId"] == call_id and binding["agent"] == agent and binding["assignment"] == assignment for binding in bindings)
                assert "replyTo" not in call["arguments"]
            print(json.dumps({"agent": agent, "productionBindingsVerified": True}), flush=True)
    finally:
        server.shutdown()


if __name__ == "__main__":
    if sys.argv[1] == "mcp":
        mcp()
    elif sys.argv[1] == "hook":
        hook()
    else:
        run_probe(sys.argv[1])
