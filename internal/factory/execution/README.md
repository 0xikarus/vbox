# Feature-stage acceptance contract

This package is a transition model, **not yet a running feature scheduler**.
The coordinator must persist graphs/attempts with leases and approval-revision
fencing and supply independently collected runtime evidence. Do not expose its
receipt-taking methods directly to browsers or accept a builder's JSON as
verification evidence.

`New` requires approved `build_queued` work whose master and feature issues exist
and whose feature specifications still match the approved plan. It derives
stable per-feature branches and queues the approved dependency graph.

The path for a feature is:

`queued → building → needs_verification → needs_review → needs_pr → pr_ready`

- `Ready` permits parallel independent features. Dependents wait for every
  parent's reviewed PR candidate; `DependencyCommits` specifies the exact commits
  their source preparation must incorporate. It does not merge them itself.
- `Built` requires actual successful process identity and a clean Git candidate;
  it does not declare the feature verified or unlock dependents.
- `Verified` requires another box, the exact candidate, unchanged source, and
  every approved check with matching argv/cwd/deadline, real exit 0 and complete
  log digests. Missing, cancelled, timed-out or truncated evidence is rejected.
- `Reviewed` requires an independent successful review of that candidate, a
  substantive summary, explicit approval and no blocking findings.
- `Published` accepts only a trusted publication result for the same candidate
  and repository PR URL. The caller must verify GitHub's actual head SHA; the
  URL alone cannot prove what code it contains.

`IntegrationReady` means every feature has reached this point, **not that the
product is complete**. The combined candidate must still undergo independent
in-box verification/review and be delivered with reproducible start instructions.
Do not convert agent exit 0, individual PRs or this flag into final acceptance.

Tests here use synthetic receipts to check acceptance rules. The separate
verification runner must execute real commands; live build/PR integration remains
required before the factory is complete.
