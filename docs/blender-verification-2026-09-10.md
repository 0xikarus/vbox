# Blender production verification — 2026-09-10

Code: `bfa6745` adds the Blender/desktop preset; `218f703` adds bounded apt
dependency repair with package removals forbidden. Both were pushed to main and
deployed. Controller deployment `5a12963e-409c-4425-9b65-d454d41421f4` reported
SUCCESS with a RUNNING instance. Unrelated interaction drafts were excluded.

## Evidence

- Clean-release Go tests and vet passed. Controller browser suite: 15 tests
  passed, including preset submission and compact layout. Runtime tests exercise
  restoration without custom Bash, already-installed packages, repair and failure.
- Created a box through the production UI with only the Blender preset selected.
  Installation completed and the new volume reached `hibernated/saved`.
- Initial resume observation timed out. This was NOT proof that the box was dead.
  The allocation later exposed `restoring-tools` with an apt dependency failure.
- Direct diagnostics found 152 partly configured packages. Repair forbidding
  removals was added. A subsequent installation reported disappearing `/tmp`
  archives; the reason was not established. A later check using `/var/tmp` found
  all requested packages installed, with no further changes needed. Do not claim
  that changing TMPDIR was proven to fix the cause; controller retries were active.
- Ultimately the box reported `running/restored` and the allocation `ready`.
- Actual Blender 3.4.1 background execution loaded the factory scene (3 objects)
  and saved `/data/workspace/blender-proof.blend` successfully.
- Browser connected to desktop without calling desktop enable; Blender GUI process
  started. No uncaught browser errors. First screenshot caught a grey loading frame;
  a later reconnect screenshot was visually inspected and confirmed the loaded
  Blender editor with the default cube, camera and light.
- At the user's request, the assigned service was set to 12 GB RAM and 2 CPUs.
  Railway reported 12,000,000,000 bytes; the worker cgroup reported 11,999,997,952.
  The shell's old 4096 MiB banner is stale. This is a slot-level setting, not a
  durable logical-box resource requirement; a future different slot may differ.

## Retained box and limits

Box `e3dbeca7-1da1-42eb-8441-dacf00000000`, named
`blender-proof-1788998683741`, was kept running for the user, not cleaned up.
Earlier cleanup attempts while attaching were rejected; no volume was deleted.
Workspace: `/boxes/e3dbeca7-1da1-42eb-8441-dacf00000000#desktop`.
The user must choose **Start / reconnect desktop** to connect their own viewer.

Local evidence: `/tmp/vmbox-blender-live.json` (initial timeout),
`/tmp/vmbox-blender-verified.json`, `/tmp/vmbox-blender-desktop.png`, and
`/tmp/vmbox-blender-desktop-final.png` (loaded editor).
These temporary artifacts are not committed and may disappear.

MCP/add-on integration, GPU rendering and upstream Blender-version compatibility
remain untested. The distribution package is not a promise of the latest Blender.
