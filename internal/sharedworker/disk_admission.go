package sharedworker

import "fmt"

// Keep new workspace writes from consuming the last usable blocks on the
// shared filesystem. Existing workspaces remain attachable for recovery.
func requireWorkerDiskSpace(total, free int64, operation string) error {
	if total <= 0 || free < 0 || free > total {
		return fmt.Errorf("cannot %s: worker disk free space unavailable", operation)
	}
	minimum := total / 20
	if total%20 != 0 {
		minimum++
	}
	if free < minimum {
		return fmt.Errorf("cannot %s: insufficient worker disk space (%d bytes free of %d; at least 5%% free required)", operation, free, total)
	}
	return nil
}
