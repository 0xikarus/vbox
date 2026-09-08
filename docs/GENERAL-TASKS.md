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
- Current status: queued; no worker result or synthesis yet.

This first trial is dispatched by the implementation agent through the existing
controller API. It is **not** proof that the new UI or scoped coordinator
delegation tool already works. The same flow must subsequently run through the
product without manual orchestration.
