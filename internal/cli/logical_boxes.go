package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/config"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

func (a *App) controllerBoxes(ctx context.Context, c config.Context, token string, args []string) error {
	asJSON := len(args) > 0 && args[len(args)-1] == "--json"
	if asJSON {
		args = args[:len(args)-1]
	}
	if len(args) == 0 {
		args = []string{"list"}
	}
	switch args[0] {
	case "list", "ls":
		jsonOutput := asJSON
		if len(args) > 2 || (len(args) == 2 && !jsonOutput) {
			return fmt.Errorf("usage: vmbox boxes [list] [--json]")
		}
		var boxes []v1.LogicalBox
		if _, err := a.request(ctx, c, token, http.MethodGet, "/v1/logical-boxes"+fleetQuery(c), nil, &boxes, nil); err != nil {
			return err
		}
		if jsonOutput {
			return json.NewEncoder(a.Out).Encode(boxes)
		}
		fmt.Fprintln(a.Out, "NAME                 STATE        SLOT                 VOLUME ID            FAILURE")
		for _, box := range boxes {
			volumeID := box.VolumeID
			if len(volumeID) > 20 {
				volumeID = volumeID[:20]
			}
			fmt.Fprintf(a.Out, "%-20s %-12s %-20s %-20s %s\n", box.Name, box.State, box.SlotID, volumeID, box.FailureReason)
		}
		return nil
	case "status":
		if len(args) != 2 && !(len(args) == 3 && args[2] == "--json") {
			return fmt.Errorf("usage: vmbox boxes status NAME")
		}
		var status json.RawMessage
		if _, err := a.request(ctx, c, token, http.MethodGet, "/v1/logical-boxes/"+url.PathEscape(args[1])+"/status", nil, &status, nil); err != nil {
			return err
		}
		return a.providerOutput(status, asJSON)
	case "update":
		if len(args) != 4 || args[2] != "--default-agent" {
			return fmt.Errorf("usage: boxes update BOX --default-agent AGENT")
		}
		var box v1.LogicalBox
		_, err := a.request(ctx, c, token, http.MethodPatch, "/v1/logical-boxes/"+url.PathEscape(args[1]), v1.UpdateLogicalBoxRequest{DefaultAgent: args[3]}, &box, nil)
		if err != nil {
			return err
		}
		return a.logicalBoxOutput(box, asJSON)
	case "contacts":
		if len(args) < 2 {
			return fmt.Errorf("usage: vmbox boxes contacts NAME [--allow CONTACT | --block CONTACT | --inherit CONTACT | --protect | --unprotect] [--two-way] [--json]")
		}
		name := args[1]
		fs := flag.NewFlagSet("contacts", flag.ContinueOnError)
		fs.SetOutput(a.Err)
		allow := fs.String("allow", "", "add a manual allowance to CONTACT (box name or id)")
		block := fs.String("block", "", "block CONTACT even when an assigned role grants access")
		inherit := fs.String("inherit", "", "remove the override and inherit assigned-role grants for CONTACT")
		add := fs.String("add", "", "alias for --allow")
		remove := fs.String("remove", "", "alias for --inherit")
		protect := fs.Bool("protect", false, "mark the box as off-limits to agents")
		unprotect := fs.Bool("unprotect", false, "remove agent protection from the box")
		twoWay := fs.Bool("two-way", false, "apply the connection setting in both directions")
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return fmt.Errorf("unexpected contacts argument %q", fs.Arg(0))
		}
		selected := 0
		for _, value := range []string{*allow, *block, *inherit, *add, *remove} {
			if value != "" {
				selected++
			}
		}
		if *protect {
			selected++
		}
		if *unprotect {
			selected++
		}
		if selected > 1 {
			return fmt.Errorf("choose one of --allow, --block, --inherit, --protect or --unprotect")
		}
		switch {
		case *protect:
			var status map[string]bool
			if _, err := a.request(ctx, c, token, http.MethodPut, "/v1/logical-boxes/"+url.PathEscape(name)+"/protection", map[string]bool{"protected": true}, &status, nil); err != nil {
				return err
			}
			fmt.Fprintf(a.Out, "%s is protected\n", name)
			return nil
		case *unprotect:
			var status map[string]bool
			if _, err := a.request(ctx, c, token, http.MethodPut, "/v1/logical-boxes/"+url.PathEscape(name)+"/protection", map[string]bool{"protected": false}, &status, nil); err != nil {
				return err
			}
			fmt.Fprintf(a.Out, "%s is not protected\n", name)
			return nil
		case *allow != "" || *add != "":
			contactRef := *allow
			if contactRef == "" {
				contactRef = *add
			}
			var contact v1.BoxContact
			if _, err := a.request(ctx, c, token, http.MethodPut, "/v1/logical-boxes/"+url.PathEscape(name)+"/contacts", v1.PutBoxContactRequest{Contact: contactRef, State: "allow", TwoWay: *twoWay}, &contact, nil); err != nil {
				return err
			}
			fmt.Fprintf(a.Out, "%s -> %s: allow (%s)\n", contact.BoxName, contact.ContactName, contact.Reason)
			return nil
		case *block != "":
			var contact v1.BoxContact
			if _, err := a.request(ctx, c, token, http.MethodPut, "/v1/logical-boxes/"+url.PathEscape(name)+"/contacts", v1.PutBoxContactRequest{Contact: *block, State: "block", TwoWay: *twoWay}, &contact, nil); err != nil {
				return err
			}
			fmt.Fprintf(a.Out, "%s -> %s: block (%s)\n", contact.BoxName, contact.ContactName, contact.Reason)
			return nil
		case *inherit != "" || *remove != "":
			contactRef := *inherit
			if contactRef == "" {
				contactRef = *remove
			}
			var contact v1.BoxContact
			if _, err := a.request(ctx, c, token, http.MethodPut, "/v1/logical-boxes/"+url.PathEscape(name)+"/contacts", v1.PutBoxContactRequest{Contact: contactRef, State: "inherit", TwoWay: *twoWay}, &contact, nil); err != nil {
				return err
			}
			fmt.Fprintf(a.Out, "%s -> %s: inherit (%s)\n", contact.BoxName, contact.ContactName, contact.Reason)
			return nil
		}
		var contacts []v1.BoxContact
		if _, err := a.request(ctx, c, token, http.MethodGet, "/v1/logical-boxes/"+url.PathEscape(name)+"/contacts", nil, &contacts, nil); err != nil {
			return err
		}
		if asJSON {
			return json.NewEncoder(a.Out).Encode(contacts)
		}
		fmt.Fprintln(a.Out, "CONTACT              ROLES                STATE        OVERRIDE  EFFECTIVE  REASON")
		for _, contact := range contacts {
			roleNames := make([]string, 0, len(contact.ContactRoles))
			for _, role := range contact.ContactRoles {
				roleNames = append(roleNames, role.Name)
			}
			fmt.Fprintf(a.Out, "%-20s %-20s %-12s %-9s %-10t %s\n", contact.ContactName, strings.Join(roleNames, ","), contact.ContactState, contact.Override, contact.CanMessage, contact.Reason)
		}
		return nil
	case "create", "new":
		if len(args) < 2 {
			return fmt.Errorf("usage: vmbox new NAME [--disk GiB] [--region ID] [--role NAME]... [--detach|--hibernate] [--no-dialog] [--start-cli COMMAND]")
		}
		fs := flag.NewFlagSet("new", flag.ContinueOnError)
		fs.SetOutput(a.Err)
		disk := fs.Int64("disk", 10, "persistent workspace size in GiB")
		region := fs.String("region", "", "preferred region")
		var roleRefs []string
		fs.Func("role", "native role name or id; repeat to assign several roles", func(value string) error {
			value = strings.TrimSpace(value)
			if value == "worker" || value == "manager" {
				return fmt.Errorf("worker/manager roles are obsolete; use an owner-defined role name")
			}
			if value == "" {
				return fmt.Errorf("--role requires a native role name or id")
			}
			roleRefs = append(roleRefs, value)
			return nil
		})
		var selectedProfiles []v1.LoginProfileRef
		fs.Func("profile", "saved login profile APP=NAME (repeat for each app)", func(value string) error {
			app, name, ok := strings.Cut(value, "=")
			if !ok || name == "" || (app != "claude" && app != "codex" && app != "opencode" && app != "github") {
				return fmt.Errorf("--profile requires claude=NAME, codex=NAME, opencode=NAME or github=NAME")
			}
			for _, ref := range selectedProfiles {
				if ref.Application == app {
					return fmt.Errorf("select only one %s profile", app)
				}
			}
			selectedProfiles = append(selectedProfiles, v1.LoginProfileRef{Application: app, Name: name})
			return nil
		})
		noProfiles := fs.Bool("no-profiles", false, "skip agent credential provisioning")
		var tools []string
		setupScript := fs.String("setup-script", "", "trusted Bash install commands; rerun on resume, so keep idempotent")
		fs.Func("tool", "install an optional tool preset (foundry, blender, desktop); repeat for additional tools", func(value string) error { tools = append(tools, value); return v1.ValidateTools(tools) })
		noDialog := fs.Bool("no-dialog", false, "use explicit arguments without the creation dialog")
		hibernated := fs.Bool("hibernate", false, "create the workspace without leaving compute running")
		startCLI := fs.String("start-cli", "", "run an explicit command once in the new persistent shell")
		allocate := fs.Bool("allocate", false, "allocate a warm slot after volume creation")
		detach := fs.Bool("detach", false, "allocate a warm slot and leave it running without opening tmux")
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return fmt.Errorf("unexpected creation argument %q", fs.Arg(0))
		}
		if *noProfiles && len(selectedProfiles) > 0 {
			return fmt.Errorf("--no-profiles cannot be combined with --profile")
		}
		if *hibernated && (*allocate || *detach || *startCLI != "") {
			return fmt.Errorf("--hibernate cannot be combined with --allocate, --detach or --start-cli")
		}
		mode := creationConnect
		if *allocate || *detach {
			mode = creationDetached
		}
		if *hibernated {
			mode = creationHibernated
		}
		terminal := a.IsTerminal != nil && a.IsTerminal()
		if !terminal && mode == creationConnect {
			return fmt.Errorf("scripts must specify --detach (running) or --hibernate; automatic connection needs a terminal")
		}
		if asJSON && mode == creationConnect {
			return fmt.Errorf("--json requires --detach or --hibernate")
		}
		roleIDs := []string{}
		if len(roleRefs) > 0 {
			var roles []v1.AgentRole
			if _, err := a.request(ctx, c, token, http.MethodGet, "/v1/agent-roles", nil, &roles, nil); err != nil {
				return err
			}
			for _, ref := range roleRefs {
				found := ""
				for _, role := range roles {
					if role.ID == ref || strings.EqualFold(role.Name, ref) {
						found = role.ID
						break
					}
				}
				if found == "" {
					return fmt.Errorf("native role %q was not found in this account", ref)
				}
				roleIDs = append(roleIDs, found)
			}
		}
		request := v1.CreateLogicalBoxRequest{Name: args[1], Provider: c.Provider, ProviderCredential: c.ProviderCredential, RoleIDs: roleIDs, Region: *region, DiskGiB: *disk, DefaultAgent: "shell", AllocationRequestKey: "cli-create:" + args[1] + ":" + fmt.Sprint(time.Now().UnixNano())}
		request.LoginProfiles = selectedProfiles
		request.Tools = tools
		request.SetupScript = *setupScript
		if err := v1.ValidateSetupScript(request.SetupScript); err != nil {
			return err
		}
		return a.createWorkspace(ctx, c, token, request, mode, *startCLI, terminal && !*noDialog && !asJSON, *noProfiles, asJSON)
	case "allocate", "open":
		session := ""
		agent := ""
		startCLI := ""
		forcePicker := false
		if args[0] == "open" && len(args) == 4 && args[2] == "--start-cli" {
			startCLI = args[3]
			if strings.TrimSpace(startCLI) == "" {
				return fmt.Errorf("--start-cli requires a command")
			}
		} else if args[0] == "open" && len(args) == 4 && args[2] == "--session" {
			session = args[3]
			if session == "" {
				return fmt.Errorf("session name cannot be empty; use --session without a value for the picker")
			}
		} else if args[0] == "open" && len(args) == 3 && args[2] == "--session" {
			forcePicker = true
		} else if args[0] == "open" && len(args) == 3 && (args[2] == "codex" || args[2] == "claude" || args[2] == "shell") {
			agent = args[2]
		} else if len(args) != 2 {
			return fmt.Errorf("usage: vmbox boxes %s NAME", args[0])
		}
		if args[0] == "open" {
			if a.IsTerminal == nil || !a.IsTerminal() {
				return fmt.Errorf("opening a logical box requires an interactive terminal")
			}
			if err := a.requireCapability(ctx, c, token, "nativeAttach"); err != nil {
				return err
			}
			box, err := a.controllerLogicalBox(ctx, c, token, args[1])
			if err != nil {
				return err
			}
			if box.State == v1.LogicalBoxRunning {
				return a.openInteractiveStartup(ctx, c, token, box, session, agent, forcePicker, startCLI)
			}
		}
		key := "cli-allocate:" + args[1] + ":" + fmt.Sprint(time.Now().UnixNano())
		var allocation v1.Allocation
		_, err := a.request(ctx, c, token, http.MethodPost, "/v1/logical-boxes/"+url.PathEscape(args[1])+"/allocate", map[string]any{"leaseOwner": "cli"}, &allocation, map[string]string{"Idempotency-Key": key})
		if err != nil {
			return err
		}
		allocation, err = a.waitAllocation(ctx, c, token, allocation)
		if err != nil {
			return err
		}
		if args[0] == "open" {
			box, err := a.controllerLogicalBox(ctx, c, token, args[1])
			if err != nil {
				return err
			}
			return a.openInteractiveStartup(ctx, c, token, box, session, agent, forcePicker, startCLI)
		}
		return a.providerOutput(allocation, asJSON)
	case "hibernate":
		if len(args) != 2 {
			return fmt.Errorf("usage: vmbox hibernate NAME")
		}
		var box v1.LogicalBox
		status, err := a.request(ctx, c, token, http.MethodPost, "/v1/logical-boxes/"+url.PathEscape(args[1])+"/hibernate", map[string]any{}, &box, nil)
		if err != nil {
			return err
		}
		if status == http.StatusAccepted {
			fmt.Fprintf(a.Err, "vmbox: hibernate accepted for %q; volume %s (%s) is retained\n", box.Name, box.VolumeName, box.VolumeID)
			fmt.Fprintf(a.Err, "vmbox: progress continues after this CLI exits; check with: vmbox status %s\n", box.Name)
			return nil
		}
		fmt.Fprintf(a.Err, "vmbox: hibernated %q; volume %s (%s) was retained and its compute slot was freed\n", box.Name, box.VolumeName, box.VolumeID)
		return nil
	case "delete", "delete-volume":
		if a.IsTerminal == nil || !a.IsTerminal() {
			return fmt.Errorf("volume deletion requires an interactive confirmation; nothing deleted")
		}
		if len(args) != 2 {
			return fmt.Errorf("usage: vmbox delete BOX")
		}
		box, err := a.controllerLogicalBox(ctx, c, token, args[1])
		if err != nil {
			return err
		}
		fmt.Fprintf(a.Err, "\nPERMANENT DELETION: logical box %q\nRailway volume: %s (ID: %s)\nAll workspace data will be permanently deleted. The fleet service will remain.\nType %s to confirm: ", box.Name, box.VolumeName, box.VolumeID, box.Name)
		timeout := a.ExitPromptTimeout
		if timeout <= 0 {
			timeout = defaultExitPromptTimeout
		}
		confirmation, ok := readLineWithTimeout(ctx, bufio.NewReader(a.In), timeout)
		if !ok || strings.TrimSpace(confirmation) != box.Name {
			fmt.Fprintln(a.Err, "vmbox: deletion cancelled; logical box and volume were kept")
			return nil
		}
		return a.requestVolumeDeletion(ctx, c, token, box)
	default:
		return fmt.Errorf("unknown boxes command %q", args[0])
	}
}

func (a *App) logicalBoxOutput(box v1.LogicalBox, asJSON bool) error {
	if asJSON {
		return json.NewEncoder(a.Out).Encode(box)
	}
	_, err := fmt.Fprintf(a.Out, "%s · %s\n", tuiLabel(box.Name, 100), tuiLabel(string(box.State), 40))
	return err
}

func (a *App) controllerLogicalBox(ctx context.Context, c config.Context, token, id string) (v1.LogicalBox, error) {
	var box v1.LogicalBox
	_, err := a.request(ctx, c, token, http.MethodGet, "/v1/logical-boxes/"+url.PathEscape(id), nil, &box, nil)
	return box, err
}

func (a *App) waitLogicalBoxCreation(ctx context.Context, c config.Context, token string, box v1.LogicalBox) (v1.LogicalBox, error) {
	lastPhase := ""
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		if box.RestorationState != lastPhase {
			message := tuiLabel(fmt.Sprintf("%s · %s", box.State, box.RestorationState), 240)
			if a.Verbose {
				message = fmt.Sprintf("phase=%s state=%s", box.RestorationState, box.State)
			}
			if a.creationProgress != nil {
				a.creationProgress(message)
			} else if a.Verbose {
				fmt.Fprintln(a.Err, message)
			}
			lastPhase = box.RestorationState
		}
		switch box.State {
		case v1.LogicalBoxHibernated, v1.LogicalBoxDetached, v1.LogicalBoxRunning:
			return box, nil
		case v1.LogicalBoxFailed:
			return box, fmt.Errorf("logical box creation failed: %s", box.FailureReason)
		}
		select {
		case <-ctx.Done():
			return box, ctx.Err()
		case <-ticker.C:
			var err error
			box, err = a.controllerLogicalBox(ctx, c, token, box.ID)
			if err != nil {
				return box, err
			}
		}
	}
}

func (a *App) waitAllocation(ctx context.Context, c config.Context, token string, allocation v1.Allocation) (v1.Allocation, error) {
	last := ""
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		message := allocation.Phase
		if allocation.State == "queued" && allocation.Phase == "waiting-for-hibernate" {
			box, err := a.controllerLogicalBox(ctx, c, token, allocation.LogicalBoxID)
			if err != nil {
				return allocation, err
			}
			message = fmt.Sprintf("%s · %s (resume queued)", box.State, box.RestorationState)
			if box.FailureReason != "" {
				message += " · " + box.FailureReason
			}
		} else if allocation.State == "queued" {
			message = fmt.Sprintf("waiting-for-capacity queue=%d", allocation.QueuePosition)
		}
		if message != last {
			progress := tuiLabel(message, 240)
			if progress == "" {
				progress = allocation.State
			}
			if a.Verbose {
				progress = fmt.Sprintf("allocation=%s phase=%s retry=%d", allocation.RequestID, message, allocation.RetryCount)
			}
			if a.creationProgress != nil {
				a.creationProgress(progress)
			} else {
				fmt.Fprintln(a.Err, progress)
			}
			last = message
		}
		switch allocation.State {
		case "ready":
			return allocation, nil
		case "failed", "cancelled":
			return allocation, fmt.Errorf("allocation %s: %s", allocation.State, allocation.FailureReason)
		}
		select {
		case <-ctx.Done():
			return allocation, ctx.Err()
		case <-ticker.C:
			if _, err := a.request(ctx, c, token, http.MethodGet, "/v1/allocations/"+url.PathEscape(allocation.RequestID), nil, &allocation, nil); err != nil {
				return allocation, err
			}
		}
	}
}

func (a *App) attachControllerLogicalBox(ctx context.Context, c config.Context, token string, box v1.LogicalBox) error {
	return a.attachNative(ctx, c, token, box, "")
}

func (a *App) controllerLogicalBoxAuth(ctx context.Context, c config.Context, token string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("auth requires a logical box")
	}
	box, err := a.controllerLogicalBox(ctx, c, token, args[0])
	if err != nil {
		return err
	}
	if box.State != v1.LogicalBoxRunning {
		return fmt.Errorf("logical box %q is %s; open it before syncing credentials", box.Name, box.State)
	}
	var resolved v1.LogicalBoxConnection
	path := "/v1/logical-boxes/" + url.PathEscape(box.ID) + "/connection?session=vmbox"
	if _, err := a.request(ctx, c, token, http.MethodGet, path, nil, &resolved, nil); err != nil {
		return err
	}
	setup, err := a.selectAuthentication(ctx, args[1:])
	if err != nil {
		return err
	}
	return a.uploadSelectedAuthentication(ctx, box.Name, setup, func(ctx context.Context, argv []string, opts provider.ExecOptions) (provider.ExecResult, error) {
		return a.resolvedExec(ctx, c, token, resolved.Connection, argv, opts, false)
	})
}

func (a *App) refreshControllerWelcome(ctx context.Context, resolved v1.LogicalBoxConnection, execute setupExec) error {
	metadata := resolved.Connection.Metadata
	value := func(key, fallback string) string {
		if metadata[key] != "" {
			return metadata[key]
		}
		return fallback
	}
	cpu, memory, disk := "fleet default", "fleet default", "persistent volume"
	if metadata["vmboxCPU"] != "" {
		cpu = metadata["vmboxCPU"] + " CPU"
	}
	if metadata["vmboxMemoryMiB"] != "" {
		memory = metadata["vmboxMemoryMiB"] + " MiB RAM"
	}
	if metadata["vmboxDiskGiB"] != "" {
		disk = metadata["vmboxDiskGiB"] + " GiB disk"
	}
	welcome := fmt.Sprintf("vmbox %s is ready\nProvider: %s (controller)  Region: %s\nSpecs: %s / %s / %s\nWorkspace: %s  Compute slot: %s\nState: %s  Network: %s\nConnection: direct OpenSSH, resolved for this deployment\nCost: %s\nDetach safely: press Ctrl-a, release both keys, then press d\nUseful: vmbox %s | vmbox hibernate %s\n\n",
		value("vmboxBoxName", resolved.BoxName), value("vmboxProvider", "managed"), value("vmboxRegion", "provider default"),
		cpu, memory, disk,
		value("vmboxWorkspace", "/data/workspace"), value("vmboxComputeSlot", "managed"),
		value("vmboxAssignmentState", "running"), value("vmboxConnectionHealth", "connected"), value("vmboxCost", "managed fleet slot; see provider billing"),
		resolved.BoxName, resolved.BoxName)
	result, err := execute(ctx, []string{"vmbox-runtime", "put-file", "/data/home/.vmbox-welcome", "0600"}, provider.ExecOptions{Stdin: bytes.NewBufferString(welcome), Stdout: io.Discard, Stderr: a.Err})
	if err != nil {
		return fmt.Errorf("refresh logical-box details: %w", err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("refresh logical-box details exited with status %d", result.ExitCode)
	}
	return nil
}

func (a *App) postControllerInteractiveExit(ctx context.Context, c config.Context, token string, box v1.LogicalBox) error {
	reader := bufio.NewReader(a.In)
	timeout := a.ExitPromptTimeout
	if timeout <= 0 {
		timeout = defaultExitPromptTimeout
	}
	fmt.Fprintln(a.Err, "\nWhat should happen to this box?")
	fmt.Fprintln(a.Err, "  1. Leave unchanged    (default; keep running if already running)")
	fmt.Fprintln(a.Err, "  2. Shut down compute  Hibernate: retain the volume and free the compute slot")
	fmt.Fprintln(a.Err, "  3. Delete box         Permanently delete this logical box and its workspace data")
	fmt.Fprint(a.Err, "Choice [1]: ")
	line, ok := readLineWithTimeout(ctx, reader, timeout)
	if !ok {
		return a.keepControllerRunning(box.Name)
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "", "1", "keep", "keep running":
		return a.keepControllerRunning(box.Name)
	case "2", "hibernate":
		var updated v1.LogicalBox
		if _, err := a.request(ctx, c, token, http.MethodPost, "/v1/logical-boxes/"+url.PathEscape(box.ID)+"/hibernate", map[string]any{}, &updated, nil); err != nil {
			return err
		}
		fmt.Fprintf(a.Err, "%s · %s · %s; volume retained\n", updated.Name, updated.State, updated.RestorationState)
		return nil
	case "3", "delete", "delete volume":
		fresh, err := a.controllerLogicalBox(ctx, c, token, box.ID)
		if err != nil {
			return err
		}
		fmt.Fprintf(a.Err, "\nPERMANENT DELETION: logical box %q\nRailway volume: %s (ID: %s)\nAll workspace data will be permanently deleted. The compute service remains.\nType %s to confirm: ", fresh.Name, fresh.VolumeName, fresh.VolumeID, fresh.Name)
		confirmation, confirmed := readLineWithTimeout(ctx, reader, timeout)
		if !confirmed || strings.TrimSpace(confirmation) != fresh.Name {
			fmt.Fprintln(a.Err, "vmbox: deletion cancelled; logical box and volume were kept")
			return nil
		}
		return a.requestVolumeDeletion(ctx, c, token, fresh)
	default:
		fmt.Fprintln(a.Err, "vmbox: unrecognized choice; keeping the box running")
		return a.keepControllerRunning(box.Name)
	}
}

func (a *App) keepControllerRunning(name string) error {
	fmt.Fprintf(a.Err, "vmbox: leaving %q unchanged; connect with: vmbox %s\n", name, name)
	return nil
}
