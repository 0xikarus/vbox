package cli

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"text/tabwriter"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/config"
)

// The default command is observational, even in a terminal. In particular it
// must never allocate compute or attach SSH. Authentication may prompt.
func (a *App) overview(ctx context.Context, contextName string) error {
	file, err := config.Load(a.ConfigPath)
	if err != nil {
		return err
	}
	c, err := file.Active(contextName)
	if err != nil || c.Controller == "" {
		fmt.Fprintln(a.Out, "No controller connected.\nSetup: vmbox context add NAME --controller URL\nHelp:  vmbox help")
		return nil
	}
	fmt.Fprintf(a.Out, "Boxes · %s\n", tuiLabel(c.Name, 100))
	if err := validateControllerURL(c.Controller); err != nil {
		return err
	}
	if c.TokenEnv == "" {
		c.TokenEnv = "VMBOX_CONTROLLER_TOKEN"
	}
	token, err := a.controllerToken(ctx, c)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var boxes []v1.LogicalBox
	if _, err := a.request(ctx, c, token, http.MethodGet, "/v1/logical-boxes"+fleetQuery(c), nil, &boxes, nil); err != nil {
		return err
	}
	sort.Slice(boxes, func(i, j int) bool { return boxes[i].Name < boxes[j].Name })
	if len(boxes) == 0 {
		fmt.Fprintln(a.Out, "No boxes yet.")
	} else {
		w := tabwriter.NewWriter(a.Out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "\nBOX\tSTATE\tPHASE / ERROR")
		for _, box := range boxes {
			detail := box.RestorationState
			if box.FailureReason != "" {
				detail += " · " + box.FailureReason
			}
			fmt.Fprintf(w, "%s\t%s\t%s\n", tuiLabel(box.Name, 100), tuiLabel(string(box.State), 40), tuiLabel(detail, 200))
		}
		if err := w.Flush(); err != nil {
			return err
		}
	}
	fmt.Fprintln(a.Out, "\nvmbox BOX            Connect / resume\nvmbox new NAME       Create a box\nvmbox hibernate BOX  Stop compute; keep files\nvmbox delete BOX     Delete box and files\nvmbox help           More commands")
	return nil
}
