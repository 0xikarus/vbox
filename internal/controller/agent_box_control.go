package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/boxruntime"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

type remoteControlRequest struct {
	Action    string `json:"action"`
	X         *int   `json:"x,omitempty"`
	Y         *int   `json:"y,omitempty"`
	ToX       *int   `json:"toX,omitempty"`
	ToY       *int   `json:"toY,omitempty"`
	Button    string `json:"button,omitempty"`
	Double    bool   `json:"double,omitempty"`
	DX        int    `json:"dx,omitempty"`
	DY        int    `json:"dy,omitempty"`
	Text      string `json:"text,omitempty"`
	Keys      string `json:"keys,omitempty"`
	Thumbnail *bool  `json:"thumbnail,omitempty"`
}

func decodeRemoteControlRequest(w http.ResponseWriter, r *http.Request) (remoteControlRequest, error) {
	var request remoteControlRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32768))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || decoder.Decode(new(any)) != io.EOF {
		return request, fmt.Errorf("invalid remote control request")
	}
	return request, nil
}

func (r remoteControlRequest) desktopAction() (boxruntime.DesktopAction, error) {
	a := boxruntime.DesktopAction{Action: r.Action}
	point := func() error {
		if r.X == nil || r.Y == nil {
			return fmt.Errorf("x and y are required")
		}
		a.X, a.Y = *r.X, *r.Y
		return nil
	}
	button := func() error {
		switch r.Button {
		case "", "left":
			a.Button = 1
		case "middle":
			a.Button = 2
		case "right":
			a.Button = 3
		default:
			return fmt.Errorf("button must be left, middle, or right")
		}
		return nil
	}
	if r.Double && r.Action != "click" {
		return a, fmt.Errorf("double is only valid for click")
	}
	switch r.Action {
	case "screenshot":
		if r.X != nil || r.Y != nil || r.ToX != nil || r.ToY != nil || r.DX != 0 || r.DY != 0 || r.Text != "" || r.Keys != "" || r.Double || r.Button != "" {
			return a, fmt.Errorf("screenshot takes no action fields")
		}
		return a, nil
	case "move":
		if err := point(); err != nil {
			return a, err
		}
	case "click":
		if err := point(); err != nil {
			return a, err
		}
		if err := button(); err != nil {
			return a, err
		}
		a.Count = 1
		if r.Double {
			a.Count = 2
		}
	case "drag":
		if err := point(); err != nil {
			return a, err
		}
		if r.ToX == nil || r.ToY == nil {
			return a, fmt.Errorf("toX and toY are required")
		}
		a.ToX, a.ToY = *r.ToX, *r.ToY
		if err := button(); err != nil {
			return a, err
		}
	case "scroll":
		if err := point(); err != nil {
			return a, err
		}
		if (r.DX == 0) == (r.DY == 0) {
			return a, fmt.Errorf("provide dx or dy, not both")
		}
		step := r.DY
		if r.DX != 0 {
			step = r.DX
			if step < 0 {
				a.Text = "left"
			} else {
				a.Text = "right"
			}
		} else if step < 0 {
			a.Text = "up"
		} else {
			a.Text = "down"
		}
		if step < 0 {
			step = -step
		}
		if step > 20 {
			return a, fmt.Errorf("scroll must be at most 20 steps")
		}
		a.Count = step
	case "type":
		a.Text = r.Text
	case "keys":
		if r.Keys == "" {
			return a, fmt.Errorf("keys are required")
		}
		a.Action = "key"
		a.Keys = strings.Split(r.Keys, "+")
	default:
		return a, fmt.Errorf("unsupported remote control action")
	}
	data, _ := json.Marshal(a)
	if _, err := boxruntime.DecodeDesktopAction(data); err != nil {
		return a, err
	}
	return a, nil
}

func validateRemoteControlTarget(actorID string, box v1.LogicalBox, protected bool) error {
	if box.ID == actorID {
		return fmt.Errorf("use this box's own desktop tools")
	}
	if protected {
		return fmt.Errorf("protected boxes cannot be controlled by an agent")
	}
	if box.State != v1.LogicalBoxRunning {
		return fmt.Errorf("box is not running")
	}
	return nil
}

func (s *Server) agentBoxControlHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	request, err := decodeRemoteControlRequest(w, r)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	action, err := request.desktopAction()
	if err != nil {
		writeError(w, 400, err)
		return
	}
	actorID := agentBoxID(p)
	capabilities, err := s.Store.EffectiveAgentCapabilities(r.Context(), p.AccountID, actorID)
	if err != nil {
		writeError(w, 500, fmt.Errorf("remote control permissions unavailable"))
		return
	}
	allowed := capabilities.ManageAgentBoxes.Control && capabilities.MCPTools.Enabled && slices.Contains(capabilities.MCPTools.AllowedTools, "remote_control_box")
	if err := requireCapability(allowed, "remote_control_box"); err != nil {
		writeError(w, 403, err)
		return
	}
	owner := ownerPrincipal(p)
	box, err := s.Store.LogicalBox(r.Context(), owner, r.PathValue("box"))
	if err != nil {
		writeError(w, 404, fmt.Errorf("box unavailable"))
		return
	}
	if box.ID == actorID {
		writeError(w, 403, fmt.Errorf("use this box's own desktop tools"))
		return
	}
	protected, err := s.Store.BoxProtection(r.Context(), owner, box.ID)
	if err != nil {
		writeError(w, 500, fmt.Errorf("box protection unavailable"))
		return
	}
	if err := validateRemoteControlTarget(actorID, box, protected); err != nil {
		writeError(w, 403, err)
		return
	}
	actor, err := s.Store.LogicalBox(r.Context(), owner, actorID)
	if err != nil {
		writeError(w, 403, fmt.Errorf("actor box unavailable"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	assignment, err := s.Store.assignment(ctx, p.AccountID, box.ID)
	if err != nil || assignment.Box.State != v1.LogicalBoxRunning {
		writeError(w, 409, fmt.Errorf("desktop is offline; remote control does not wake boxes"))
		return
	}
	prov, err := s.provider(ctx, p.AccountID, box.Provider, box.ProviderCredential)
	if err != nil {
		writeError(w, 502, fmt.Errorf("worker unavailable"))
		return
	}
	actionID, limited, err := s.Store.reserveRemoteControl(ctx, p.AccountID, actorID, box.ID, request)
	if err != nil {
		writeError(w, 500, fmt.Errorf("remote control audit unavailable"))
		return
	}
	if limited {
		writeError(w, 429, fmt.Errorf("remote control rate limit reached"))
		return
	}
	finished := false
	defer func() {
		if !finished {
			cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_, _ = s.Store.DB.ExecContext(cleanup, `UPDATE remote_control_actions SET status='failed' WHERE id=$1`, actionID)
		}
	}()
	if request.Action != "screenshot" {
		data, _ := json.Marshal(action)
		result, err := prov.Exec(ctx, assignment.Slot.ServiceID, []string{"vmbox-runtime", "desktop-input", nativeFence(assignment)}, provider.ExecOptions{Stdin: bytes.NewReader(data), Stdout: io.Discard, Stderr: io.Discard})
		if err != nil || result.ExitCode != 0 {
			writeError(w, 409, fmt.Errorf("target desktop input unavailable"))
			return
		}
	}
	current, err := s.Store.assignment(ctx, p.AccountID, box.ID)
	if err != nil || current.Box.State != v1.LogicalBoxRunning || nativeFence(current) != nativeFence(assignment) {
		writeError(w, 409, fmt.Errorf("assignment changed"))
		return
	}
	thumbnail := request.Thumbnail == nil || *request.Thumbnail
	pixels, err := readDesktopCapture(ctx, prov, assignment.Slot.ServiceID, nativeFence(assignment), thumbnail)
	if err != nil {
		writeError(w, 409, err)
		return
	}
	current, err = s.Store.assignment(ctx, p.AccountID, box.ID)
	if err != nil || current.Box.State != v1.LogicalBoxRunning || nativeFence(current) != nativeFence(assignment) {
		writeError(w, 409, fmt.Errorf("assignment changed"))
		return
	}
	if err := s.Store.finishRemoteControl(ctx, p.AccountID, actorID, actor.Name, box.ID, actionID); err != nil {
		writeError(w, 500, fmt.Errorf("remote control audit unavailable"))
		return
	}
	finished = true
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("X-Captured-At", time.Now().UTC().Format(time.RFC3339Nano))
	_, _ = w.Write(pixels)
}
