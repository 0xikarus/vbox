package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/0xikarus/vmbox-service/internal/boxruntime"
)

var errAgentCLIRegistryUnavailable = errors.New("npm registry lookup unavailable; try again")

const npmRegistryURL = "https://registry.npmjs.org"

func checkAgentCLIPackageVersion(ctx context.Context, agent, version string) error {
	return checkAgentCLIPackageVersionAt(ctx, &http.Client{Timeout: 6 * time.Second}, npmRegistryURL, agent, version)
}

// The exact-version endpoint returns one published release without downloading
// the package. The package name is selected from a fixed allowlist.
func checkAgentCLIPackageVersionAt(ctx context.Context, client *http.Client, registryURL, agent, version string) error {
	if !boxruntime.ValidAgentCLIVersion(version) {
		return fmt.Errorf("%s version must be an exact release version", agent)
	}
	packageName, err := boxruntime.AgentCLIPackage(agent)
	if err != nil {
		return err
	}
	address := strings.TrimRight(registryURL, "/") + "/" + url.PathEscape(packageName) + "/" + url.PathEscape(version)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return fmt.Errorf("%w", errAgentCLIRegistryUnavailable)
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("%w", errAgentCLIRegistryUnavailable)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%s %s is not published to npm", agent, version)
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%w (HTTP %d)", errAgentCLIRegistryUnavailable, response.StatusCode)
	}
	var published struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&published); err != nil || published.Name != packageName || published.Version != version {
		return fmt.Errorf("%w (invalid package metadata)", errAgentCLIRegistryUnavailable)
	}
	return nil
}
