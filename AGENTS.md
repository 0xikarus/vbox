# Repository orientation

Read `README.md` for user workflows and `docs/AGENT-GUIDE.md` for current
architecture, lifecycle rules, implementation entry points, and verification.
Older proposals and dated verification reports are historical evidence, not
necessarily the current product contract. Check the code and working tree.

Preserve existing uncommitted work. Never print or commit credentials. Tests that
create resources must use isolated, explicitly identified disposable resources.
Do not restart user workers, delete their volumes, or deploy changes without
authorization covering those actions.
