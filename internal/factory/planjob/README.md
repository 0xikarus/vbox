# Box-local planning job

`vmbox-planner` reads one bounded JSON job from stdin. The controller must stage
the binary, source checkout, images and private job file before submitting the
fixed shell command. It must resolve the current box connection for each staging
operation. No factory gateway token or GitHub App private key belongs in the box.

The job includes version 1, a 32-character lowercase hexadecimal attempt ID,
`planner.Request`, distinct sibling result/receipt paths in a private directory,
and an HTTPS result endpoint with a per-attempt bearer capability. Literal
loopback HTTP is available for isolated in-box testing. Redirects are refused.

The command saves the agent's actual exit code/signal separately from its own
delivery exit. It does not treat an agent exit of zero as implementation or
verification success. Valid structured planner output accompanies successful
runtime results; failed or invalid runtime output is not promoted to a plan.

A durable start marker and a nonblocking attempt lock prevent silent duplicate
execution. If the process stopped before committing its receipt, the attempt
requires explicit reconciliation rather than automatic replay. If only delivery
failed, invoking the same job again resends the byte-stable receipt without
calling the agent. Changed request content under the same attempt is rejected.
The receipt contains no delivery credential. The controller should remove the
staged job credential after confirmed receipt, not before recoverable delivery.

CLI exit 0 means result delivery succeeded, including delivery of an agent's
nonzero exit. It does **not** mean the agent succeeded. The controller must read
the scoped durable inbox and reconcile the corresponding process identity.

Tests here use controlled runner functions and loopback HTTP to test persistence,
failure evidence, rejection and retry semantics. Real agent image evidence lives
in `../planner/EVIDENCE.md`; end-to-end controller staging is not yet implemented.
