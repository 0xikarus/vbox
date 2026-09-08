# General tasks: revised target

The controller is the starting point for **any task**, not only software work.
An idea and optional attachments go to a coordinator. The coordinator proposes
bounded assignments; the controller launches workers, records their real outputs
and exits, and returns results to the coordinator for review and synthesis.
The UI shows that conversation, worker tree, progress, evidence and final result.

Repositories, GitHub issues, branches, builds and PRs are optional **software
workflow steps**, not prerequisites for creating a task. Non-software work uses
deliverables and acceptance criteria; it must not invent Git commits or shell
checks to satisfy the old software-only schema. Process exit and acceptance
remain separate for both modes.

## Implementation order

1. Live non-software delegation trial using the existing controller and saved
   authentication: coordinator proposes two assignments, two boxes execute them,
   coordinator reviews their actual results. Record limitations honestly.
2. Generalize the UI and persisted work/plan contract: optional repository,
   assignments and deliverables rather than mandatory features/check commands.
3. Connect coordinator-requested delegation to controller-enforced limits and
   durable parent/child task records. Workers receive scoped task authority,
   never a controller owner's credential. Apply the same limits to nested
   coordinators; no unbounded recursive spawning.
4. Wire result delivery, cancellation, retries, restart recovery and final
   synthesis into one visible UI journey. Retain actual exits and artifacts.
5. Attach the existing software workflow to this general task mechanism and
   finish its source transfer, dependency preparation, review and PR integration.

No main merge or production-code deployment without approval. Use existing
isolated boxes first; fleet maximum remains six.

## Live trial: household-waste workshop

Input: a free two-hour workshop for twelve adults, an available indoor room,
chairs/tables/projector, and EUR60 materials budget. Estimates must be labelled.
No external messages, purchases, bookings, browsing or repository mutations.

- Coordinator planning task: `39ab00e4-dc56-4cd5-8395-fa4600000000`.
- Coordinator box: `factory-review-0908`.
- Expected assignments: agenda/facilitation; logistics/materials/accessibility.
- Coordinator planning completed with actual exit 0. Its actual JSON assignments
  were parsed and dispatched unchanged, with shared constraints, to two boxes.
- Agenda worker: `ca82c549-a1fc-4e0c-8f4f-822400000000`, box
  `factory-ui-0908`.
- Logistics worker: `2ebb8bcf-3167-4ba5-84b9-329300000000`, box
  `factory-integration-0908`.
- Agenda worker completed with actual exit 0 and a timed 120-minute agenda.
- Initial logistics worker exited 1: Codex received HTTP 401 authentication
  errors. Controller access worked, but that box lacked usable agent auth.
- Explicit logistics retry `fd83a13a-25ee-4884-8cb0-c15800000000` on
  `factory-assets-0908` completed with exit 0 using its existing saved login.
  No credentials or model settings were changed; the failed attempt remains.
- Both actual final worker outputs were sent to coordinator synthesis task
  `f9b50f95-ddab-43f6-8ae3-a0e500000000` on `factory-review-0908`.
  Synthesis is queued; semantic acceptance is not yet established.

Dispatch must distinguish controller authorization from per-box agent login
readiness. A failed authentication attempt must remain visible across retries.

This first trial is dispatched by the implementation agent through the existing
controller API. It is **not** proof that the new UI or scoped coordinator
delegation tool already works. The same flow must subsequently run through the
product without manual orchestration.
