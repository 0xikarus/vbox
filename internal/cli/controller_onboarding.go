package cli

import (
	"bufio"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/0xikarus/vmbox-service/internal/config"
)

// promptControllerContext makes controller management the safe first-run
// default. Standalone operation remains available only when the caller asks
// for it explicitly with --standalone.
func (a *App) promptControllerContext(file config.File, requestedName string, existing config.Context) (config.File, config.Context, error) {
	if a.IsTerminal == nil || !a.IsTerminal() {
		return file, existing, fmt.Errorf("controller is not configured; run 'vmbox context add NAME --controller URL'")
	}
	reader := bufio.NewReader(a.In)
	fmt.Fprintln(a.Err, "vmbox: no controller is configured; connect this CLI to one now.")

	controller, err := a.readControllerPrompt(reader, "Controller URL", existing.Controller)
	if err != nil {
		return file, existing, err
	}
	if err := validateControllerURL(controller); err != nil {
		return file, existing, err
	}

	nameDefault := requestedName
	if nameDefault == "" {
		nameDefault = existing.Name
	}
	if nameDefault == "" {
		nameDefault = "production"
	}
	name, err := a.readControllerPrompt(reader, "Context name", nameDefault)
	if err != nil {
		return file, existing, err
	}
	configured := config.Context{Name: name, Controller: strings.TrimRight(controller, "/"), TokenEnv: existing.TokenEnv}
	if configured.TokenEnv == "" {
		configured.TokenEnv = "VMBOX_CONTROLLER_TOKEN"
	}
	if file.Contexts == nil {
		file.Contexts = make(map[string]config.Context)
	}
	file.Contexts[name] = configured
	file.Current = name
	if err := config.Save(a.ConfigPath, file); err != nil {
		return file, existing, fmt.Errorf("save controller context: %w", err)
	}
	fmt.Fprintf(a.Err, "vmbox: saved controller context %q; authentication is read from %s and is never stored in the config\n", name, configured.TokenEnv)
	return file, configured, nil
}

func (a *App) readControllerPrompt(reader *bufio.Reader, label, defaultValue string) (string, error) {
	if defaultValue == "" {
		fmt.Fprintf(a.Err, "%s: ", label)
	} else {
		fmt.Fprintf(a.Err, "%s [%s]: ", label, defaultValue)
	}
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", fmt.Errorf("read %s: %w", strings.ToLower(label), err)
	}
	value := strings.TrimSpace(line)
	if value == "" {
		value = defaultValue
	}
	if value == "" {
		return "", fmt.Errorf("%s is required", strings.ToLower(label))
	}
	return value, nil
}

func validateControllerURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("controller URL must be an absolute HTTPS URL")
	}
	if parsed.Scheme == "https" {
		return nil
	}
	host := parsed.Hostname()
	if parsed.Scheme == "http" && (host == "localhost" || host == "127.0.0.1" || host == "::1") {
		return nil
	}
	return fmt.Errorf("controller URL must use HTTPS (HTTP is allowed only for localhost)")
}
