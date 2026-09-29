# How vbox compares

| Feature | vbox | Grok Bot | Muse |
| --- | --- | --- | --- |
| Bring your own agent harness | Codex, Claude Code, or OpenCode | Cursor-managed bot; other harnesses not documented | Muse's built-in agent; other harnesses not documented |
| Bring your own model key | OpenCode profiles support OpenRouter or Venice API keys | Cursor manages model selection; own key not documented | Own model key not documented |
| Bring your own connected apps | Browser logins and apps in the box | Connectors, plugins, and browser sites | Connected apps and browser sites |
| Install your own software | Linux packages and trusted setup scripts | Yes; manual installs are lost on computer recreation | Installation in its secure VM not documented |
| Choose where it runs | Self-host the controller and Linux workers | Cursor-hosted only | Meta-hosted |
| Persistent workspace | Separate volume per box | One computer shared by a user's bots | One secure VM per person |
| Browser and shell | Both | Both | Browser; shell not documented |
| Agent-to-agent coordination | Direct contacts with role-based permissions | Bots message each other and share group chats | Not documented |
| Scheduled routines | Not built in | Yes | Proactive goal work; a schedule feature is not documented |
| Hibernate while retaining files | Yes | Yes, with a durable disk | Not documented |

Comparison checked 29 September 2026 against the [Grok Bot overview](https://docs.x.ai/grok-bot/overview), [computer management](https://docs.x.ai/grok-bot/computers), [teams and hosting](https://docs.x.ai/grok-bot/teams-and-enterprises), [security FAQ](https://docs.x.ai/grok-bot/security-faq), and [Meta's Muse announcement](https://about.fb.com/news/2026/09/introducing-muse-personal-ai-agent/). "Not documented" means the linked material does not confirm the feature; it does not establish that the feature is impossible.
