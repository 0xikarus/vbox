# Send a prompt from an app running in a box

An app or script running inside a box can send text to that box's **already
running managed agent** through the local `POST /prompt` endpoint. This is part
of the box's MCP HTTP service; no event bus setup is required. It works with
the managed Codex, Claude, and OpenCode conversations.

The HTTP service listens on `127.0.0.1` at a port chosen for this box. Read
`~/.local/share/vmbox/mcp-http.json` inside the box instead of hardcoding the
port. The private file contains `url`, `promptUrl`, and `token`. It appears when
the box's MCP HTTP service starts. Start the managed agent from Agent chat if
the service or conversation is not running. `/prompt` does not start or wake one.

## Send a request

Make a `POST` request to `promptUrl` with an `Authorization: Bearer <token>`
header and one JSON object containing non-empty `text`. This Python example
reads the token from the file and keeps it off the shell command line:

```python
import json
from pathlib import Path
from urllib.error import HTTPError
from urllib.request import Request, urlopen

config = json.loads((Path.home() / ".local/share/vmbox/mcp-http.json").read_text())
event = {
    "id": "build-42",
    "source": "ci-helper",
    "type": "build.finished",
    "data": {"status": "failed", "logPath": "/data/workspace/build.log"},
}
body = {"text": "Local app event:\n" + json.dumps(event)}
request = Request(
    config["promptUrl"],
    data=json.dumps(body).encode(),
    method="POST",
    headers={
        "Authorization": "Bearer " + config["token"],
        "Content-Type": "application/json",
    },
)
try:
    with urlopen(request, timeout=130) as response:
        print(response.status, response.read().decode())
except HTTPError as error:
    print(error.code, error.read().decode())
```

The event object in this example is **text sent to the agent**. `/prompt` has
no `source`, `type`, `data`, or caller-provided message ID fields of its own.
For a simple prompt, use `{"text":"Check the latest build result"}` instead.

If more than one managed conversation is running in the box, add its exact
tmux session name as `"session":"NAME"` in the JSON body, or send it as an
`X-Vmbox-Session: NAME` header. The `409` response lists the available
conversation names when a choice is required. If both forms are provided,
they must match.

## Responses and limits

| Status | Meaning |
| --- | --- |
| `202` | The prompt was handed to the selected managed conversation. The JSON response contains `accepted`, `session`, and a generated `messageId`. |
| `400` | The JSON is invalid, `text` is empty or too long, the session name is invalid, or the body and header sessions disagree. |
| `401` | The Bearer token is missing or incorrect. |
| `405` | The endpoint was called with a method other than `POST`. |
| `409` | No managed conversation is running, several need a session choice, or native delivery failed or could not be confirmed. |

`text` is limited to 140,000 bytes. Send exactly one JSON object with only
`text` and optional `session`; unknown fields are rejected. The request can
wait up to two minutes for the native handoff. A `202` means the handoff
succeeded; it does not mean the agent finished the work. Any agent reply
goes through its normal conversation, not back to this HTTP request. This
local prompt is not recorded as a controller owner chat message.

The server creates a fresh `messageId` for every request. A `409` or lost
HTTP response can be ambiguous: the prompt may already be in the agent's
inbox. `/prompt` has no caller-controlled idempotency key, so retrying can
send the same prompt twice. Have the app check its own result or the agent's
conversation before retrying uncertain requests.

The token in `mcp-http.json` also authorizes the box's HTTP MCP tools. Keep
the file private and give the token only to apps you trust with those tools.
The service binds to worker loopback and is not published externally. Boxes
sharing a worker can reach each other's loopback ports, so the token also
protects one box from another.
