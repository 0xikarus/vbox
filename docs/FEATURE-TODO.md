# Feature follow-ups

- Add a Commands page in Chat for owner-defined text prompts. Typing `/` in the composer should offer those saved commands and insert the chosen prompt for editing before send.
- Add an `@` box picker in the Chat composer. Sending a message with a selected box mention should add reciprocal direct-contact permissions for the two boxes, so each can message the other; show the chosen box in a dropdown and avoid creating connections from unsent drafts.
- Disable sending in Chat while the selected box is not running. Re-enable the composer when its state becomes running, including after a live state refresh.
- Show remaining usage for every configured agent account/profile in the controller and chat app. Check each harness/provider's supported account-usage API (or MCP endpoint), distinguish unavailable data from zero remaining, and refresh without exposing credentials or waking boxes.
- Fix model selection: selecting “astra 6 luna low” currently starts “astra 6 low.” Trace the UI model/effort value through box creation, saved profile overrides, runtime arguments, and the active harness model; add a regression check for this exact selection.
- Add runnable MCP call examples to the generated AGENTS.md / CLAUDE.md content. Prefer a simple validated CLI form such as `vmbox-runtime <mcp-name> <JSON-args>` if the runtime can safely expose one; examples should show owner replies, contact replies, images, and `get_contacts` without requiring agents to guess transport details.
