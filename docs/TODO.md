# TODO

## Telegram controller commands

- Add discoverable slash commands and help for controller-managed boxes.
- List boxes and show a selected box's precise state/status.
- Send a durable message to a specific box by name, with clear routing and delivery feedback.
- Reuse controller authorization and Telegram user/chat allowlists; keep commands scoped to the linked account.
- Test command routing, unknown boxes, unauthorized senders, and offline/hibernated recipients. Do not silently wake boxes just to deliver a message.

Command names and any additional lifecycle actions remain to be designed. This is follow-up work, not part of the current Telegram reply verification.
