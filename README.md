<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/assets/vbox-logo-dark.png">
    <img src="docs/assets/vbox-logo.png" alt="vbox" width="420">
  </picture>
</p>

# vbox

vbox is a self-hosted home for coding agents. Each box keeps its files and agent sessions, with chat, a desktop, and a TMUX terminal in the browser. A controller manages accounts, storage, and worker capacity; you choose where to run it.

Codex, Claude Code, and OpenCode are supported, but any harness can be used inside a box. Boxes can also use owner-approved MCP tools to message each other and work with each other and third-party websites.

## What you can do

- **Work across boxes:** pin chats, read box ↔ box messages, and keep both desktops visible above their conversation. The mascot and short activity phrase show each agent's current mood and work.
- **Chat with context:** send images, annotate them with pen, rectangle, ellipse, line, or arrow, and reply in threads. Mobile swipe gestures navigate chats; a local chat cache makes revisits quicker.
- **Manage capacity:** the Providers popup summarizes workers, slots, and host resources; the Providers page lets owners edit and delete provider configurations. Details shows RAM, swap, disk, activity, hibernation, and limits. Usage is available from the top bar.
- **Keep work between sessions:** hibernation frees a worker slot while retaining box files. Wake the box to resume; closing a browser tab leaves a running box alone.

## Screenshots

These screens use fictional fixture boxes, including **builder** and **reviewer**. The desktop image is synthetic.

| Desktop chat | Mobile chat |
| --- | --- |
| <picture><source media="(prefers-color-scheme: dark)" srcset="docs/assets/ui/chat-dark.webp"><img src="docs/assets/ui/chat-light.webp" alt="Chat with pinned boxes, activity, messages, and image attachments" width="900"></picture> | <img src="docs/assets/ui/chat-mobile.webp" alt="Chat on a phone" width="260"> |

| Box ↔ box chat with pinned desktops | Details: resources, hibernation, and activity |
| --- | --- |
| <img src="docs/assets/ui/pair-desktops.webp" alt="Builder and reviewer direct chat with two pinned desktop views" width="700"> | <img src="docs/assets/ui/details-resources-activity.webp" alt="Box details with RAM, swap, disk, hibernation limits, and activity" width="700"> |

| Box ↔ box handoff on desktop | The same chat on mobile |
| --- | --- |
| <img src="docs/assets/ui/box-chat-desktop.webp" alt="Light desktop chat between builder and reviewer with a handoff, review feedback, and quoted replies" width="700"> | <img src="docs/assets/ui/box-chat-mobile-dark.webp" alt="Dark mobile view of the builder and reviewer conversation with quoted replies" width="260"> |

| Asking the owner | Chat list and activity |
| --- | --- |
| <img src="docs/assets/ui/owner-question.webp" alt="Builder asks the owner a multi-select question; the owner answers with a quoted reply and read ticks" width="700"> | <img src="docs/assets/ui/chat-list-activity.webp" alt="Pinned sample project chats with working and idle boxes, unread counts, and a stalled activity warning" width="700"> |

| Mail inbox | Outbox approval |
| --- | --- |
| <img src="docs/assets/ui/mail-inbox.webp" alt="Mail inbox across fixture boxes with a message from example.test open" width="700"> | <img src="docs/assets/ui/mail-outbox-approval.webp" alt="Owner review dialog for a fictional outgoing message awaiting approval" width="700"> |

| Details: Access & permissions | Tool categories and checkboxes |
| --- | --- |
| <img src="docs/assets/ui/details-access-overview.webp" alt="Builder details panel on Access and permissions with box management tool checkboxes" width="700"> | <img src="docs/assets/ui/details-access-permissions.webp" alt="Access and permissions showing mail and computer-use category toggles and individual tool checkboxes" width="700"> |

| Providers | Workspace |
| --- | --- |
| <img src="docs/assets/ui/providers.webp" alt="Providers popup showing workers, slots, and host resources" width="700"> | <img src="docs/assets/ui/workspace.webp" alt="Workspace desktop beside status, resources, and power controls" width="700"> |

| Image annotation |
| --- |
| <img src="docs/assets/ui/annotation.webp" alt="Annotating a chat image with a rectangle and arrow" width="700"> |

## Quick start

You need a controller with PostgreSQL and HTTPS, plus at least one worker. Running capacity can incur hosting charges.

1. Deploy the controller and bootstrap an owner account using the [controller guide](docs/CONTROLLER.md).
2. Register a worker with the [Linux VPS guide](docs/LINUX-VPS-SETUP.md) or [another provider](docs/PROVIDERS.md).
3. Open the controller, sign in, add an agent profile, and create a box. Chat, desktop, and TMUX are available in its browser workspace.

For terminal access on Linux or macOS, install Git, OpenSSH, and Go 1.26 or Docker, then:

```bash
git clone https://github.com/0xikarus/vmbox-service.git
cd vmbox-service
./install.sh
vbox connect https://YOUR-CONTROLLER
vbox new builder
vbox builder
```

The CLI is named `vbox`; `vmbox` is a compatibility symlink. Run `vbox help` for other commands.

## Documentation

Start with the [documentation index](docs/README.md). See [controller operations](docs/CONTROLLER.md), [worker isolation](docs/SHARED-WORKERS.md), the [agent desktop guide](docs/AGENT-DESKTOP-IMPLEMENTATION.md), and the [API contract](docs/openapi.yaml).
