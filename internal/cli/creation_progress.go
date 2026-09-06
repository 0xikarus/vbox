package cli

import (
	"fmt"
	"strings"
)

// Stage completion, not elapsed-time prediction. Unknown phases keep the last
// percentage and remain visible. Only successful creation explicitly reaches 100.
type creationMeter struct{ percent int }

func (m *creationMeter) render(message string) string {
	stages := map[string]int{
		"Uploading selected": 5, "Initializing persistent workspace": 10,
		"creation-reserved": 10, "creation-volume-requested": 20,
		"creation-volume-attached": 30, "creation-initializing": 40,
		"creation-detaching": 60, "creation-sanitizing": 70,
		"saved": 75, "requesting-allocation": 80, "verifying-slot": 82,
		"attaching-volume": 85, "waiting-for-runtime": 90,
		"restoring-workspace-metadata": 95, "restored": 95,
	}
	for phase, percent := range stages {
		if strings.Contains(message, phase) {
			m.percent = max(m.percent, percent)
		}
	}
	filled := m.percent / 5
	return fmt.Sprintf("[%s%s] %3d%% · stage progress\n%s", strings.Repeat("=", filled), strings.Repeat("-", 20-filled), m.percent, message)
}

func (m *creationMeter) complete() string {
	m.percent = 100
	return m.render("Workspace ready")
}
