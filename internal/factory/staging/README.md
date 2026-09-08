# Private staging

`Stager{SSH: transport.SSH{...}, BinaryPath: trustedLocalPlanner}.Stage(ctx, input)`
uses the **current controller-provided** `input.Connection` on every call. There
is no provider client, connection cache, task launch, build, or agent execution.
Build the planner inside the controller's box and pass that regular local file.
Local binary path components cannot be symlinks.

All attempt values and bytes travel on uncaptured SSH stdin. The SSH command is
a static embedded Bash program, started with an empty environment. Both remote
output streams are discarded, including transport error details. Job JSON is
opaque to staging and may contain private capabilities; it is never interpreted
as shell or logged.

The remote layout is fixed:

```
/data/workspace/.vmbox-factory/
  .staging.lock
  bin/vmbox-planner
  reservations/ATTEMPT
  scratch/stage.RANDOM/
  attempts/ATTEMPT/
    job.json
    images/IMAGE_ID
    repo/
    manifest
    ready
```

Directories are 0700, staged data and markers are 0600, and the executable is
0700. Ancestors must be real directories owned by root or the SSH uid, without
group/other write access. Private directories must be owned by the SSH uid.
Managed files reject symlinks, hardlinks, and nonregular files. The SSH account,
root, and trusted ancestors are the filesystem security boundary: this shell
protocol does not isolate against a malicious process running as the same uid
(or root) that deliberately replaces paths or bypasses the advisory lock.
Repository-controlled symlinks may exist **inside** the Git checkout; staging
does not follow them to write job, image, binary, or marker files.

A global `flock` serializes staging. The manifest records attempt, repository,
SHA, binary/job hashes and lengths, and sorted image IDs/hashes/lengths. Its
SHA-256 digest deliberately excludes the rotating source credential. A durable
create-only reservation binds an attempt to this digest before payload staging.
Changed input fails even after a partial transfer or failed fetch. Each retry
uses fresh private scratch; incomplete scratch is never promoted or reused.
Normal exits remove scratch. SIGKILL or machine failure can leave private orphan
scratch; administrative cleanup requires quiescing staging and is outside this
API. The attempt directory and its ready marker are published together by a
single rename, after complete transfer, verification, checkout, and filesystem
sync. Consumers must require `ready` before starting work.

A retry verifies the published manifest, job, images, binary, and directory
safety, then returns without fetching or replacing files. Changes made by the
agent inside `repo/` and additional agent output files are preserved. Missing or
tampered staged inputs fail without repair. The shared `bin/vmbox-planner` is
pinned to its first hash: a different binary fails even for another attempt.
Upgrading this shared executable requires a separate quiesced lifecycle; staging
never replaces a binary that running attempts could use.

`SourceToken` must be issued by the controller as a GitHub token scoped to read
only `Repository`. Opaque token syntax cannot establish permissions: this API
cannot verify or narrow the token's grant. It supplies Basic authentication
through `GIT_CONFIG_VALUE_0` (`http.extraHeader`) **only for fetch**. There is no
credential in URLs, process arguments, files, or Git configuration on disk. The
shell unsets the token before fetch and clears the temporary encoded header
immediately after fetch (and in the exit trap on failure). This is lifetime
control of variables, not a guarantee of physical memory zeroization. Git uses
an empty HOME/template, disabled system/global configuration, empty credential
helper, disabled prompts/askpass, HTTPS-only protocol, and no redirects. There is
no fallback credential source. Fetch is shallow, no-tags, no-submodules, and asks
for precisely `BaseSHA` at `https://github.com/OWNER/REPO.git`; the fetched commit
must match before detached checkout. Hooks and fsmonitor are disabled, no LFS
filter is configured, and checkout does not recurse into submodules. Repository
scripts are not executed.

Limits: 10 MiB/image, 40 MiB/images total, 4,096 images (including empty ones),
200,000 bytes/job, 128 MiB/nonempty binary, and 4,096 token characters. Lock wait
is at most 120 seconds, Git init 30 seconds, fetch/checkout 120 seconds each
(with a 5-second forced-kill grace), and the whole SSH operation has a 5-minute
context deadline. Source repository byte size is not capped; Git operations are
bounded by time and shallow fetch. The remote requires Linux, Bash, Git, GNU
coreutils, and util-linux `flock` at standard system paths.

## Validation in this box

The tests use the real `transport.SSH.StreamConnection` and its attached-runner
interface. The fixture checks SSH argv and output handling, executes the exact
SSH-quoted command with a real local Bash, and redirects only the fixed root and
PATH into a private temporary directory. A Git wrapper validates fetch options
and the environment-only header, then replaces the network destination with a
local repository. Real Git performs init, shallow fetch, SHA verification, and
checkout. No GitHub write or other box is involved.

Coverage includes binary-safe transfers, private permissions, source checkout,
rotating tokens/current connections, canonical image ordering, content conflicts,
parallel calls, failed fetch and truncated/corrupt transfer recovery, symlink /
hardlink / FIFO rejection, missing published paths, poisoned inherited Git
configuration, preserved agent work, exact job/image limits, binary bounds and
pinning, cancellation, and redacted errors.

Executed successfully:

```
go test -v ./internal/factory/staging
go vet ./internal/factory/staging
go build -o /data/workspace/staging-planner-0908 ./cmd/vmbox-planner
STAGING_TEST_BINARY=/data/workspace/staging-planner-0908 go test -v ./internal/factory/staging -run TestBuiltPlanner -count=1
```

`TestBuiltPlanner` additionally transferred the actual locally built planner and
compared every byte; the test skips without that environment variable. The build
artifact was removed after validation. Race instrumentation was unavailable:
`go test -race` requires cgo and this box has no C compiler configured.

Limits of evidence: no live SSH daemon/network, GitHub authentication or token
scope enforcement, power-loss durability test, forced-SIGKILL recovery test,
agent launch, deployment, or fleet test. The real-Git fixture deliberately
substitutes the fetch protocol with local-file transport only inside the test
wrapper; production permits HTTPS alone.
