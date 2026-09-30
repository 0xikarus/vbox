package cli

import "fmt"

func uniqueVerifiableApplications(applications []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, application := range applications {
		if seen[application] || (application != "codex" && application != "claude") {
			continue
		}
		seen[application] = true
		result = append(result, application)
	}
	return result
}

func (a *App) reportApplicationAuthentication(applications []string, authentication map[string]bool) {
	for _, application := range uniqueVerifiableApplications(applications) {
		if authentication[application] {
			fmt.Fprintf(a.Err, "vbox: %s authentication is ready\n", application)
		} else {
			fmt.Fprintf(a.Err, "vbox: warning: %s did not recognize the uploaded login; authenticate inside the box\n", application)
		}
	}
}
