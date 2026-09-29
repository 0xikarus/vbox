package cli

import (
	"bufio"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/0xikarus/vmbox-service/internal/config"
	"golang.org/x/term"
)

// promptControllerConnection makes a controller the safe first-run default.
func (a *App) promptControllerConnection(file config.File) (config.File, config.Context, error) {
	if a.IsTerminal == nil || !a.IsTerminal() {
		return file, config.Context{}, fmt.Errorf("controller is not configured; run 'vbox connect URL'")
	}
	reader := bufio.NewReader(singleByteReader{a.In})
	fmt.Fprintln(a.Err, "vbox: no controller is configured; connect this CLI to one now.")
	controller, err := a.readControllerPrompt(reader, "Controller URL", "")
	if err != nil {
		return file, config.Context{}, err
	}
	configured, err := a.setControllerConnection(&file, controller, "VMBOX_CONTROLLER_TOKEN")
	if err != nil {
		return file, config.Context{}, err
	}
	fmt.Fprintf(a.Err, "vbox: connected to %s; authentication is read from %s and is never stored in the config\n", configured.Controller, configured.TokenEnv)
	return file, configured, nil
}

func (a *App) setControllerConnection(file *config.File, controller, tokenEnv string) (config.Context, error) {
	if err := validateControllerURL(controller); err != nil {
		return config.Context{}, err
	}
	controller = strings.TrimRight(controller, "/")
	if tokenEnv == "" {
		tokenEnv = "VMBOX_CONTROLLER_TOKEN"
	}
	previous, _ := file.Connected()
	connection := &config.ControllerConnection{Controller: controller, TokenEnv: tokenEnv}
	if previous.Controller == controller {
		connection.LocationPresets = previous.LocationPresets
		if previous.Name != "" {
			legacy, err := os.ReadFile(a.legacyTokenPath(previous))
			if err != nil && !os.IsNotExist(err) {
				return config.Context{}, fmt.Errorf("cannot read saved controller token")
			}
			if len(legacy) > 0 {
				if err := a.saveControllerToken(config.Context{Controller: controller}, string(legacy)); err != nil {
					return config.Context{}, err
				}
			}
		}
	}
	file.Connection = connection
	if err := config.Save(a.ConfigPath, *file); err != nil {
		return config.Context{}, fmt.Errorf("save controller connection: %w", err)
	}
	return file.Connected()
}

// Avoid buffering keys meant for the next picker, password prompt, or SSH.
type singleByteReader struct{ io.Reader }

func (r singleByteReader) Read(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:1]
	}
	return r.Reader.Read(p)
}

func (a *App) readSecret(label string) (string, error) {
	if a.authPrompt != nil && label == "Controller token" {
		return a.authPrompt(label)
	}
	file, ok := a.In.(*os.File)
	if a.IsTerminal == nil || !a.IsTerminal() || !ok || !term.IsTerminal(int(file.Fd())) {
		return "", fmt.Errorf("%s missing; configure its environment variable securely (hidden input requires a terminal)", label)
	}
	fmt.Fprintf(a.Err, "%s (hidden): ", label)
	value, err := term.ReadPassword(int(file.Fd()))
	fmt.Fprintln(a.Err)
	defer clear(value)
	if err != nil {
		return "", fmt.Errorf("could not read hidden input")
	}
	if len(value) == 0 {
		return "", fmt.Errorf("%s is required", label)
	}
	return string(value), nil
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
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("controller URL must not contain credentials, a query, or a fragment")
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
