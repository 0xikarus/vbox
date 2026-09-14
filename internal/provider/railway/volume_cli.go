package railway

import (
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/0xikarus/vmbox-service/internal/procexec"
)

const volumeCreateMutation = `mutation($input: VolumeCreateInput!) { volumeCreate(input: $input) { id name } }`
const volumeUpdateMutation = `mutation($volumeId: String!, $environmentId: String!, $input: VolumeInstanceUpdateInput!) { volumeInstanceUpdate(volumeId: $volumeId, environmentId: $environmentId, input: $input) }`
const volumeDeleteMutation = `mutation($volumeId: String!) { volumeDelete(volumeId: $volumeId) }`

// Volume mutations use structured HTTP inputs. Inventory uses fetchVolumes;
// there is no fallback to Railway CLI volume commands.
func (p *Provider) runVolume(ctx context.Context, serviceID string, stdin io.Reader, args ...string) (procexec.Result, error) {
	if len(args) == 0 {
		return procexec.Result{}, errors.New("volume operation required")
	}
	if args[0] != "list" {
		if stdin != nil {
			return procexec.Result{}, errors.New("unexpected volume operation input")
		}
		volumeID := ""
		for i := 1; i+1 < len(args); i++ {
			if args[i] == "--volume" {
				volumeID = args[i+1]
			}
		}
		var document, field string
		var variables map[string]any
		switch args[0] {
		case "add":
			if serviceID == "" {
				return procexec.Result{}, errors.New("volume service ID required")
			}
			document = volumeCreateMutation
			field = "volumeCreate"
			variables = map[string]any{"input": map[string]any{"projectId": p.cfg.ProjectID, "environmentId": p.cfg.EnvironmentID, "serviceId": serviceID, "mountPath": "/data"}}
		case "attach", "detach":
			if volumeID == "" || serviceID == "" {
				return procexec.Result{}, errors.New("volume and service IDs required")
			}
			document = volumeUpdateMutation
			field = "volumeInstanceUpdate"
			input := map[string]any{"serviceId": nil}
			if args[0] == "attach" {
				input["serviceId"] = serviceID
				input["mountPath"] = "/data"
			}
			variables = map[string]any{"volumeId": volumeID, "environmentId": p.cfg.EnvironmentID, "input": input}
		case "delete":
			if volumeID == "" {
				return procexec.Result{}, errors.New("volume ID required")
			}
			document = volumeDeleteMutation
			field = "volumeDelete"
			variables = map[string]any{"volumeId": volumeID}
		default:
			return procexec.Result{}, errors.New("unsupported volume operation")
		}
		result, err := p.api(ctx, document, variables)
		if err != nil {
			return result, err
		}
		var response struct {
			Data map[string]json.RawMessage `json:"data"`
		}
		if json.Unmarshal(result.Stdout, &response) != nil {
			return result, &APIError{Status: 200, Ambiguous: true}
		}
		if args[0] == "add" {
			var volume struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(response.Data[field], &volume) != nil || volume.ID == "" {
				return result, &APIError{Status: 200, Ambiguous: true}
			}
		} else {
			var applied bool
			if json.Unmarshal(response.Data[field], &applied) != nil || !applied {
				return result, &APIError{Status: 200, Ambiguous: true}
			}
		}
		return result, nil
	}
	return procexec.Result{}, errors.New("volume inventory requires the scoped HTTP query")
}
