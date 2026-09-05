package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/config"
)

func (a *App) controllerUpdates(ctx context.Context, c config.Context, token string, args []string) error {
	if len(args) > 0 && args[0] == "ack" {
		if len(args) < 2 {
			return fmt.Errorf("usage: updates ack BOX --session NAME --revision REV")
		}
		fs := flag.NewFlagSet("updates ack", flag.ContinueOnError)
		fs.SetOutput(a.Err)
		session := fs.String("session", "", "exact session name")
		revision := fs.String("revision", "", "displayed revision")
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		if *session == "" || *revision == "" || fs.NArg() != 0 {
			return fmt.Errorf("session and revision required")
		}
		_, err := a.request(ctx, c, token, http.MethodPost, "/v1/logical-boxes/"+url.PathEscape(args[1])+"/updates/ack", map[string]string{"session": *session, "revision": *revision}, nil, nil)
		return err
	}
	jsonOutput := false
	box := ""
	for _, arg := range args {
		if arg == "--json" {
			jsonOutput = true
		} else if box == "" {
			box = arg
		} else {
			return fmt.Errorf("usage: updates [BOX] [--json]")
		}
	}
	boxes := []v1.LogicalBox{{ID: box}}
	if box == "" {
		if _, err := a.request(ctx, c, token, http.MethodGet, "/v1/logical-boxes", nil, &boxes, nil); err != nil {
			return err
		}
	}
	if len(boxes) > 32 {
		return fmt.Errorf("more than 32 boxes: select BOX to keep probes bounded")
	}
	values := []v1.SessionUpdates{}
	for _, box := range boxes {
		var value v1.SessionUpdates
		if _, err := a.request(ctx, c, token, http.MethodGet, "/v1/logical-boxes/"+url.PathEscape(box.ID)+"/updates", nil, &value, nil); err != nil {
			return err
		}
		values = append(values, value)
	}
	if jsonOutput {
		return json.NewEncoder(a.Out).Encode(values)
	}
	for _, v := range values {
		fmt.Fprintf(a.Out, "%s: %s\n", v.Inventory.LogicalBoxID, v.Inventory.State)
		for _, u := range v.Updates {
			fmt.Fprintf(a.Out, "%q\t%s\t%s\tpartial=%t\n", u.Session, u.State, u.Revision, u.Partial)
		}
	}
	fmt.Fprintln(a.Err, "Snapshot changes only; not agent completion. Reading does not acknowledge.")
	return nil
}
