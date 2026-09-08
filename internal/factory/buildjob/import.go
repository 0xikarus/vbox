package buildjob

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"
	"time"
)

// ImportBundle imports into a trusted, independently staged repository whose
// HEAD must equal trustedBaseSHA. Both SHAs must come from controller evidence,
// never from the bundle. The caller owns exclusive access to the repository.
func ImportBundle(ctx context.Context, workspace string, input io.Reader, artifact Artifact, trustedBaseSHA, candidateSHA string) error {
	if !commitID.MatchString(trustedBaseSHA) || !commitID.MatchString(candidateSHA) || candidateSHA == trustedBaseSHA || artifact.BaseSHA != trustedBaseSHA || artifact.Filename != ArtifactFilename || artifact.Size <= 0 || artifact.Size > MaxArtifactBytes {
		return errors.New("invalid artifact or trusted baseline")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var head bytes.Buffer
	if err := gitRun(ctx, &head, "-C", workspace, "rev-parse", "--verify", "HEAD"); err != nil || strings.TrimSpace(head.String()) != trustedBaseSHA {
		return errors.New("repository is not at exact trusted baseline")
	}
	// Snapshot bounded bytes before checking or invoking Git, so the validated
	// digest and header describe exactly the file Git consumes.
	b, err := io.ReadAll(io.LimitReader(input, artifact.Size+1))
	if err != nil || int64(len(b)) != artifact.Size {
		return errors.New("artifact size mismatch")
	}
	digest := sha256.Sum256(b)
	if hex.EncodeToString(digest[:]) != artifact.SHA256 {
		return errors.New("artifact digest mismatch")
	}
	end := bytes.Index(b, []byte("\n\n"))
	if end < 0 {
		return errors.New("invalid bundle header")
	}
	lines := strings.Split(string(b[:end]), "\n")
	if len(lines) != 3 || lines[0] != "# v2 git bundle" || !strings.HasPrefix(lines[1], "-"+trustedBaseSHA+" ") || lines[2] != candidateSHA+" refs/heads/candidate" {
		return errors.New("bundle must declare exactly the trusted prerequisite and candidate ref")
	}
	f, err := os.CreateTemp("", "buildjob-import-*.bundle")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err = f.Write(b); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = gitRun(ctx, nil, "-C", workspace, "bundle", "verify", f.Name()); err != nil {
		return errors.New("bundle prerequisite verification failed")
	}
	return gitRun(ctx, nil, "-C", workspace, "fetch", "--no-tags", "--no-write-fetch-head", f.Name(), "refs/heads/candidate:refs/heads/candidate")
}
