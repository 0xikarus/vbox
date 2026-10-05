# Shared-worker hibernation and disk pressure

## Finding

The strongest code-based explanation for the two fleet-wide hibernations is
the agent run budget. Its default is eight hours
(`internal/controller/agent_runtime_budget.go`), and every reconciliation pass
selects **all** running boxes with an expired deadline. It calls
`startLogicalBoxHibernate` for each without checking active tasks or terminal
sessions. The reported events at about 10:45 and 18:56 are eight hours and
eleven minutes apart, consistent with boxes being woken after the first event
and reaching a new eight-hour deadline together. This is an inference: no
production logs or database records were accessed, and the current hibernate
event does not persist its initiating policy.

That path invokes the ordinary `prepare-hibernate` command. It saves tmux
scrollback, kills the tmux server, and stops workspace processes
(`internal/boxruntime/tmux.go`). It can therefore interrupt an interactive
agent mid-task. The separate desktop-idle path checks task tables and a
box-side desktop/terminal idle observation first; process-task auto-hibernation
also checks for live tasks and sessions. Fleet scale-down does not evict an
occupied slot. Shared-worker restart preserves retained attachments and
prepares active workspaces. Worker heartbeat loss does not itself request
logical-box hibernation.

There is no disk-free check that calls hibernate. Disk exhaustion can make the
simultaneous event much worse: snapshots and hibernate markers need writes,
while `sharedworker.Store.save` needs a temporary state file and `fsync`. On
any save error it latches a worker-wide failure until restart. Consequently a
full shared filesystem can make all boxes unavailable and can strand a
hibernate in progress, but the source does not show ENOSPC as the trigger for
the hibernation transitions. Whether the observed zero free bytes preceded or
followed the snapshots requires incident-time disk and controller logs.

## Existing telemetry and gaps

- Shared-worker container boxes have a per-box `du` scan of the bound workspace
  directory, cached for five minutes, plus host filesystem usage from `statfs`
  (`internal/sharedworker/resource_usage.go`). The per-box metric is requested
  only for a running container and omits its Docker writable layer and other
  host overhead. It is unavailable for UID and namespace isolation.
- The nominal `SizeGiB` is reported as a box disk total, but no disk quota is
  enforced. The UI labels this as an unenforced limit and displays a host disk
  percentage without a low-free-space warning.
- `CreateStorage` checks identity and capacity but never compares host free
  bytes with a reserve. Managed tool installation and restoration likewise
  have no host free-space admission check. Container writable layers share the
  host overlay; each box's `/data/tmp` is bound to the same filesystem.

## Proposed guardrails

1. Record every hibernate request with its cause (`run-budget`, `desktop-idle`,
   process completion, or user), box, deadline, and active-task count. For a
   run budget, warn before expiry and defer an automatic stop while a tracked
   task is active, or make a strict hard-stop policy explicit to the owner.
2. Sample free bytes on the worker filesystem and Docker data filesystem.
   Surface a persistent warning in Providers and box resources below the
   greater of 5 GiB or 10% free. Report both absolute free bytes and trend;
   an 85% per-box bar alone does not represent the shared host's risk.
3. Refuse **new** shared-worker workspaces and managed CLI/tool installs or
   restores below the same reserve, with a clear insufficient-storage error.
   Check on the worker immediately before the write-heavy operation, not only
   in the controller UI. Arbitrary shell installs need real per-box quotas or
   filesystem isolation to enforce a comparable limit.
4. Extend per-box usage to retained and non-container workspaces. Attribute
   bound workspace, private temp, and container writable-layer bytes
   separately, and expose observation age. Keep host-level unattributed
   overhead visible so the sum is not mistaken for complete accounting.
5. Run a bounded per-workspace cleanup of stale Chromium/Puppeteer temporary
   directories and core dumps. Verify ownership and path boundaries, never
   follow symlinks, skip live/open files, and log bytes reclaimed. Disable
   avoidable core dumps for managed workloads. Clean inactive workspaces first.

The proposed thresholds are starting values; they should be configurable for
the actual host size and workload write rate. No production cleanup or state
change was performed during this investigation.
