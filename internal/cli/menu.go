package cli

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"sort"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/config"
)

func (a *App) controllerMenu(ctx context.Context, file config.File, c config.Context, token string) error {
	if a.IsTerminal == nil || !a.IsTerminal() {
		return fmt.Errorf("box picker requires a terminal; use vmbox ls or vmbox BOX")
	}
	var boxes []v1.LogicalBox
	if _, err := a.request(ctx, c, token, http.MethodGet, "/v1/logical-boxes"+fleetQuery(c), nil, &boxes, nil); err != nil {
		return err
	}
	sort.Slice(boxes, func(i, j int) bool { return boxes[i].Name < boxes[j].Name })
	var available []v1.LogicalBox
	var labels []string
	for _, box := range boxes {
		if box.State == v1.LogicalBoxDeleting || string(box.State) == "deleted" {
			continue
		}
		available = append(available, box)
		state := string(box.State)
		if box.RestorationState != "" && box.State != v1.LogicalBoxRunning && box.State != v1.LogicalBoxHibernated {
			state += " · " + box.RestorationState
		}
		labels = append(labels, box.Name+" · "+state)
	}
	labels = append(labels, "+ Create a box")
	choice, err := a.selectTUI(ctx, "Your boxes — Enter to open, Esc to cancel", labels, 0)
	if err != nil {
		return err
	}
	if choice < len(available) {
		return a.controllerBoxes(ctx, c, token, []string{"open", available[choice].Name})
	}
	name, err := a.readControllerPrompt(bufio.NewReader(a.In), "New box name", "")
	if err != nil {
		return err
	}
	// Reuse the normal creation flow and the already authenticated token.
	next := *a
	next.Environ = make(map[string]string, len(a.Environ)+1)
	for key, value := range a.Environ {
		next.Environ[key] = value
	}
	next.Environ[c.TokenEnv] = token
	return next.controller(ctx, file, c, []string{"new", name})
}
