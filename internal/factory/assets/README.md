# Private image assets

Implements the contract's `New`, `Put`, and `Open` seam. `Store.Close` releases
its pinned root descriptor. Only this package and its tests are changed.

## Supported formats and dependency proposal

**PNG and JPEG are supported. WebP is explicitly rejected with ErrUnsupported.**
The starting module has no WebP decoder. For integration, propose adding
`golang.org/x/image/webp` (module `golang.org/x/image`, with a version selected
and reviewed by the integrator). Once approved, wire its `DecodeConfig` and
`Decode` into the explicit format allowlist, re-encode decoded WebP as PNG to
strip metadata, and add real lossless/lossy WebP roundtrip, corruption and
pixel-limit fixtures. Neither go.mod nor go.sum is modified here. MIME headers,
filenames, and third-party global image decoder registrations cannot enable a
format. No WebP support is claimed until decoding is actually implemented.

## Storage and limits

- Requires Linux and a local filesystem supporting fsync and
  `renameat2(RENAME_NOREPLACE)`. Uses the existing `golang.org/x/sys/unix`
  dependency. Unsupported filesystem operations fail closed.
- Root and account/asset directories are 0700; files are 0600. New creates
  missing root components and rejects symlinks throughout the root path.
  Existing storage directories must already be private. Ancestors may have
  ordinary system directory permissions. Keep the root outside public assets.
- Account IDs are 1–128 ASCII letters, digits, underscores or hyphens. Asset
  IDs are 32 lowercase hexadecimal characters from 128 cryptographic random
  bits. Names are nonempty UTF-8 display filenames of at most 255 bytes, with
  no separators, control characters, or dot/dot-dot names.
- Put creates an account namespace on first upload; Open never creates one.
  A filesystem store cannot know registered accounts: the caller supplies an
  authenticated, authorized account ID as required by the contract.
- Each asset directory contains `image` and `metadata.json`. Descriptor-relative
  operations use O_NOFOLLOW throughout. Reads reject nonregular files, hardlinks,
  and permissive modes; directory descriptors pin paths during concurrent swaps.
- Input and re-encoded output are each capped at 10 MiB. Header dimensions are
  checked against 25,000,000 pixels before full decoding. Full decoding must
  succeed. PNG is re-encoded losslessly; JPEG is re-encoded at quality 95 and may
  change pixels. EXIF, comments, profiles and trailing data are discarded;
  EXIF orientation is not applied. Size, mediaType and SHA256 describe stored
  bytes, not original upload bytes.
- Both files and the staging directory are synced before an exclusive atomic
  directory rename; the account directory is synced before success is returned.
  New/account creation also syncs parents. A failure of the final directory sync
  is reported as ErrIO even though the asset may already be visible. Cancellation
  before publication removes staging; once publication starts it completes the
  durability step. Crashes may leave inaccessible `.tmp-*` directories; retention
  cleanup is left to the integrator and must not remove active staging writes.
- Open checks metadata, byte count, SHA256 and full image decoding, then returns
  a read-only descriptor at offset zero. Caller closes the descriptor.

The storage root must be controlled by the service OS user. Hashes detect
corruption; they are not authentication against an attacker able to rewrite
both metadata and images as that user. Mount manipulation and same-user writes
to an already validated file are outside the filesystem isolation boundary.
Per-image limits do not impose aggregate concurrency or submission limits;
the caller enforces admission, max8/40MiB submissions and retention.

## Errors

All package errors are stable exported sentinels, usable with errors.Is; errors
never contain user input, filesystem paths or underlying reader messages.

| Error | Meaning |
| --- | --- |
| ErrInvalidID | Invalid account or asset identifier |
| ErrInvalidName | Unsafe or invalid display filename |
| ErrNotFound | Missing account or published asset |
| ErrUnsupported | Input is not a supported decoded format (including WebP) |
| ErrInvalidImage | Recognized format fails decoding |
| ErrTooLarge | Upload bytes, encoded bytes or decoded pixels exceed limits |
| ErrCorrupt | Missing/invalid stored metadata or bytes, hash/size/format mismatch |
| ErrUnsafePath | Symlink, non-directory component, special file, hardlink or unsafe permissions |
| ErrIO | Storage or source-reader I/O failure |

Context cancellation/deadline errors are preserved. Cancellation is checked
between reader operations and around decode/encode/publication; a blocked
arbitrary io.Reader cannot be forcibly interrupted by this API.

## Verification

Run locally with Go 1.26 or newer:

```
go build ./internal/factory/assets
go test -race -count=1 ./internal/factory/assets
go vet ./internal/factory/assets
```

Tests cover real PNG/JPEG pixels and restart, stripping embedded metadata and
trailing data, hashes, read-only handles and permissions, missing/cross-account
assets, traversal, symlinks and concurrent swaps, hardlinks/FIFOs, corruption,
forged hashes over invalid images, exact byte/pixel boundaries, concurrent
publication across Store instances, cancellation cleanup and abandoned staging.
Tests verify normal restart and publication behavior, not hardware power-loss
semantics or injected fsync failures.
