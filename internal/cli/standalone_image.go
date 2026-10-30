package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// The default image is local, not a published registry image. Prepare it before
// writing an instance record, so a build failure leaves creation retryable.
func (a *App) prepareStandaloneImage(ctx context.Context, host, image string) error {
	if image != "vbox-standalone:dev" {
		return nil
	}
	docker := []string{"docker"}
	if host != "" {
		docker = append(docker, "--host", "ssh://"+host)
	}
	result, err := a.Runner.Run(ctx, append(append([]string{}, docker...), "image", "inspect", image), nil, nil, nil)
	if err == nil && result.ExitCode == 0 {
		return nil
	}
	root := a.WorkingDir
	if root == "" {
		root, _ = os.Getwd()
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return err
	}
	for _, name := range []string{"Dockerfile.standalone", "go.mod", ".dockerignore"} {
		if info, e := os.Stat(filepath.Join(root, name)); e != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("default standalone image is missing; run ./install.sh --standalone on the target, or create from the vmbox-service repository")
		}
	}
	fmt.Fprintln(a.Err, "Preparing the default standalone image on the selected Docker host…")
	argv := append(append([]string{}, docker...), "build", "--file", filepath.Join(root, "Dockerfile.standalone"), "--tag", image, root)
	result, err = a.Runner.Run(ctx, argv, nil, a.Err, a.Err)
	if err != nil || result.ExitCode != 0 {
		return fmt.Errorf("standalone image build failed; no instance was created")
	}
	return nil
}
