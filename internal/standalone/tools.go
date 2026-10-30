package standalone

import (
	"context"
	"fmt"
	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/boxruntime"
	"io"
	"os"
	"strings"
	"time"
)

func toolSelection() ([]string, error) {
	var tools []string
	for _, s := range strings.Split(os.Getenv("VMBOX_TOOLS"), ",") {
		if s = strings.TrimSpace(s); s != "" {
			tools = append(tools, s)
		}
	}
	return tools, v1.ValidateTools(tools)
}
func PrepareTools(ctx context.Context) error {
	tools, err := toolSelection()
	if err != nil {
		return err
	}
	if len(tools) == 0 {
		return nil
	}
	if os.Geteuid() != 0 {
		return fmt.Errorf("package preparation requires container root")
	}
	return boxruntime.PrepareStandaloneToolPackages(ctx, tools, os.Stderr)
}
func installSelectedTools(ctx context.Context, home string) error {
	tools, err := toolSelection()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Minute)
	defer cancel()
	return boxruntime.InstallStandaloneTools(ctx, home, tools, io.Discard)
}
