package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/0xikarus/vmbox-service/internal/boxruntime"
)

var errAgentCLIRegistryUnavailable = errors.New("npm registry lookup unavailable; try again")

const npmRegistryURL = "https://registry.npmjs.org"

func checkAgentCLIPackageVersion(ctx context.Context, agent, version string) error {
	return checkAgentCLIPackageVersionAt(ctx, &http.Client{Timeout: 6 * time.Second}, npmRegistryURL, agent, version)
}

func resolveLatestAgentCLIPackageVersion(ctx context.Context, agent string) (string, error) {
	return resolveAgentCLIPackageVersionAt(ctx, &http.Client{Timeout: 6 * time.Second}, npmRegistryURL, agent, "latest")
}

type AgentCLIVersionCatalog struct {
	Agent    string   `json:"agent"`
	Latest   string   `json:"latest"`
	Versions []string `json:"versions"`
}

func listAgentCLIPackageVersions(ctx context.Context, agent string) (AgentCLIVersionCatalog, error) {
	return listAgentCLIPackageVersionsAt(ctx, &http.Client{Timeout: 20 * time.Second}, npmRegistryURL, agent)
}

// The exact-version endpoint returns one published release without downloading
// the package. The package name is selected from a fixed allowlist.
func checkAgentCLIPackageVersionAt(ctx context.Context, client *http.Client, registryURL, agent, version string) error {
	if !boxruntime.ValidAgentCLIVersion(version) {
		return fmt.Errorf("%s version must be an exact release version", agent)
	}
	_, err := resolveAgentCLIPackageVersionAt(ctx, client, registryURL, agent, version)
	return err
}

func resolveAgentCLIPackageVersionAt(ctx context.Context, client *http.Client, registryURL, agent, selector string) (string, error) {
	if selector != "latest" && !boxruntime.ValidAgentCLIVersion(selector) {
		return "", fmt.Errorf("%s version must be an exact release version", agent)
	}
	packageName, err := boxruntime.AgentCLIPackage(agent)
	if err != nil {
		return "", err
	}
	address := strings.TrimRight(registryURL, "/") + "/" + url.PathEscape(packageName) + "/" + url.PathEscape(selector)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return "", fmt.Errorf("%w", errAgentCLIRegistryUnavailable)
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("%w", errAgentCLIRegistryUnavailable)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		if selector == "latest" {
			return "", fmt.Errorf("%w (latest tag missing)", errAgentCLIRegistryUnavailable)
		}
		return "", fmt.Errorf("%s %s is not published to npm", agent, selector)
	}
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%w (HTTP %d)", errAgentCLIRegistryUnavailable, response.StatusCode)
	}
	var published struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&published); err != nil || published.Name != packageName || !boxruntime.ValidAgentCLIVersion(published.Version) || selector != "latest" && published.Version != selector {
		return "", fmt.Errorf("%w (invalid package metadata)", errAgentCLIRegistryUnavailable)
	}
	return published.Version, nil
}

// The abbreviated packument carries every published version and the current
// latest dist-tag without the README and other large fields of the full form.
func listAgentCLIPackageVersionsAt(ctx context.Context, client *http.Client, registryURL, agent string) (AgentCLIVersionCatalog, error) {
	var catalog AgentCLIVersionCatalog
	packageName, err := boxruntime.AgentCLIPackage(agent)
	if err != nil {
		return catalog, err
	}
	address := strings.TrimRight(registryURL, "/") + "/" + url.PathEscape(packageName)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return catalog, fmt.Errorf("%w", errAgentCLIRegistryUnavailable)
	}
	request.Header.Set("Accept", "application/vnd.npm.install-v1+json")
	response, err := client.Do(request)
	if err != nil {
		return catalog, fmt.Errorf("%w", errAgentCLIRegistryUnavailable)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return catalog, fmt.Errorf("%w (HTTP %d)", errAgentCLIRegistryUnavailable, response.StatusCode)
	}
	var metadata struct {
		Name     string            `json:"name"`
		DistTags map[string]string `json:"dist-tags"`
		Versions map[string]struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"versions"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<20)).Decode(&metadata); err != nil || metadata.Name != packageName || !boxruntime.ValidAgentCLIVersion(metadata.DistTags["latest"]) {
		return catalog, fmt.Errorf("%w (invalid package metadata)", errAgentCLIRegistryUnavailable)
	}
	catalog.Agent, catalog.Latest = agent, metadata.DistTags["latest"]
	for version, item := range metadata.Versions {
		if boxruntime.ValidAgentCLIVersion(version) && item.Version == version && item.Name == packageName {
			catalog.Versions = append(catalog.Versions, version)
		}
	}
	sort.Strings(catalog.Versions)
	index := sort.SearchStrings(catalog.Versions, catalog.Latest)
	if index == len(catalog.Versions) || catalog.Versions[index] != catalog.Latest {
		return AgentCLIVersionCatalog{}, fmt.Errorf("%w (latest tag is not a published release)", errAgentCLIRegistryUnavailable)
	}
	return catalog, nil
}
