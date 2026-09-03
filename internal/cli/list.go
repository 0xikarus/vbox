package cli

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"text/tabwriter"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

func parseListOutput(args []string) (bool, error) {
	if len(args) == 1 {
		return false, nil
	}
	if len(args) == 2 && args[1] == "--json" {
		return true, nil
	}
	return false, fmt.Errorf("%s accepts only --json", args[0])
}

func writeBoxList(output io.Writer, boxes []provider.Box) error {
	if len(boxes) == 0 {
		_, err := fmt.Fprintln(output, "No boxes found.")
		return err
	}
	sort.Slice(boxes, func(i, j int) bool { return boxes[i].Name < boxes[j].Name })
	table := tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(table, "NAME\tSTATE\tREGION\tCPU\tRAM\tDISK\tMANAGEMENT\tOPEN / RESUME"); err != nil {
		return err
	}
	for _, box := range boxes {
		if _, err := fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\t%s\tstandalone\tvmbox %s\n",
			box.Name, box.State, valueOrDash(box.Region), cpuLabel(box.Resources.CPU), memoryLabel(box.Resources.MemoryMiB), diskLabel(box.Resources.DiskGiB), box.Name); err != nil {
			return err
		}
	}
	if err := table.Flush(); err != nil {
		return err
	}
	_, err := fmt.Fprint(output, "\nPower down: vmbox stop NAME    Delete: vmbox clean NAME --yes\nDetach tmux: Ctrl-a, release both keys, then d    JSON: vmbox ls --json\n")
	return err
}

func writeRunList(output io.Writer, runs []v1.Run) error {
	if len(runs) == 0 {
		_, err := fmt.Fprintln(output, "No controller runs found.")
		return err
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].CreatedAt.After(runs[j].CreatedAt) })
	table := tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(table, "BOX\tSTATE\tPROVIDER\tREGION\tCPU\tRAM\tDISK\tRUN ID\tOPEN / RESUME"); err != nil {
		return err
	}
	for _, run := range runs {
		name := valueOrDash(run.Request.Box)
		providerName := run.Provider
		if providerName == "" {
			providerName = run.Request.Provider
		}
		action := "vmbox status " + run.ID
		if run.Request.Box != "" {
			action = "vmbox " + run.Request.Box
		}
		if _, err := fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			name, run.State, valueOrDash(providerName), valueOrDash(run.Request.Region), cpuLabel(run.Request.Resources.CPU), memoryLabel(run.Request.Resources.MemoryMiB), diskLabel(run.Request.Resources.DiskGiB), run.ID, action); err != nil {
			return err
		}
	}
	if err := table.Flush(); err != nil {
		return err
	}
	_, err := fmt.Fprint(output, "\nResume selector: vmbox resume    JSON: vmbox ls --json\n")
	return err
}

func writeInventoryList(output io.Writer, inventory v1.BoxInventory) error {
	if len(inventory.LogicalBoxes) == 0 && len(inventory.ConnectedBoxes) == 0 {
		_, err := fmt.Fprintln(output, "No controller-visible boxes found.")
		return err
	}
	sort.Slice(inventory.LogicalBoxes, func(i, j int) bool { return inventory.LogicalBoxes[i].Name < inventory.LogicalBoxes[j].Name })
	sort.Slice(inventory.ConnectedBoxes, func(i, j int) bool { return inventory.ConnectedBoxes[i].Name < inventory.ConnectedBoxes[j].Name })
	table := tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(table, "NAME\tSTATE\tPROVIDER\tSTORAGE\tCOMPUTE\tMANAGEMENT\tOPEN / RESUME"); err != nil {
		return err
	}
	for _, box := range inventory.LogicalBoxes {
		compute := "detached"
		if box.SlotID != "" {
			compute = "assigned"
		}
		if _, err := fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\tcontroller\tvmbox boxes open %s\n", box.Name, box.State, valueOrDash(box.Provider), valueOrDash(box.VolumeName), compute, box.Name); err != nil {
			return err
		}
	}
	for _, box := range inventory.ConnectedBoxes {
		storage := "-"
		if box.Storage != nil {
			storage = valueOrDash(box.Storage.Name)
		}
		if _, err := fmt.Fprintf(table, "%s\t%s\t%s\t%s\tprovider service\texternal\t--standalone\n", box.Name, box.State, valueOrDash(box.Provider), storage); err != nil {
			return err
		}
	}
	if err := table.Flush(); err != nil {
		return err
	}
	_, err := fmt.Fprint(output, "\nController boxes persist independently from fleet slots. External services are listed for visibility only.\nFree fleet slots: vmbox fleet status    JSON: vmbox ls --json\n")
	return err
}

func valueOrDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func cpuLabel(cpu float64) string {
	if cpu <= 0 {
		return "-"
	}
	return strconv.FormatFloat(cpu, 'f', -1, 64)
}

func memoryLabel(memoryMiB int64) string {
	if memoryMiB <= 0 {
		return "-"
	}
	if memoryMiB >= 1024 {
		return fmt.Sprintf("%.3g GiB", float64(memoryMiB)/1024)
	}
	return fmt.Sprintf("%d MiB", memoryMiB)
}

func diskLabel(diskGiB int64) string {
	if diskGiB <= 0 {
		return "-"
	}
	return fmt.Sprintf("%d GiB", diskGiB)
}
