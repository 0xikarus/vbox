//go:build vmbox_operator

package cli

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/0xikarus/vmbox-service/internal/config"
	"github.com/0xikarus/vmbox-service/internal/procexec"
)

const controllerServiceName = "vmbox-controller"
const controllerDatabaseName = "vmbox-postgres"

type railwayService struct {
	ID   string
	Name string
}

func (a *App) provisionController(ctx context.Context, file config.File, c config.Context, args []string) error {
	if len(args) == 0 || (args[0] != "init" && args[0] != "ensure") {
		return fmt.Errorf("usage: vmbox controller init|ensure [--endpoint HTTPS_URL] [--source PATH|--controller-image IMAGE@sha256:DIGEST] --box-image IMAGE@sha256:DIGEST [--yes]")
	}
	operation := args[0]
	defaultSource := c.ControllerSource
	if defaultSource == "" && c.ControllerImage == "" {
		defaultSource = a.WorkingDir
		if defaultSource == "" {
			defaultSource = "."
		}
	}
	fs := flag.NewFlagSet("controller "+operation, flag.ContinueOnError)
	fs.SetOutput(a.Err)
	endpoint := fs.String("endpoint", firstNonEmpty(a.Environ["VMBOX_CONTROLLER_URL"], c.Controller), "public controller HTTPS endpoint; generated when omitted")
	account := fs.String("account", firstNonEmpty(c.Account, "default"), "initial account name")
	owner := fs.String("owner", "owner", "initial owner subject")
	source := fs.String("source", defaultSource, "local controller source directory")
	controllerImage := fs.String("controller-image", c.ControllerImage, "digest-pinned controller OCI image")
	boxImage := fs.String("box-image", c.Image, "digest-pinned default box OCI image")
	yes := fs.Bool("yes", false, "confirm billable provisioning")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	explicitSource, explicitControllerImage := false, false
	fs.Visit(func(value *flag.Flag) {
		explicitSource = explicitSource || value.Name == "source"
		explicitControllerImage = explicitControllerImage || value.Name == "controller-image"
	})
	if explicitControllerImage && !explicitSource {
		*source = ""
	}
	if explicitSource && !explicitControllerImage {
		*controllerImage = ""
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected controller argument %q", fs.Arg(0))
	}
	if operation == "ensure" && c.Controller != "" && a.controllerHealthyOnce(ctx, c.Controller) {
		fmt.Fprintln(a.Out, "controller is healthy; existing Railway services were reused")
		return nil
	}
	if operation == "init" && c.Controller != "" {
		return fmt.Errorf("context %q already has controller %s; use controller ensure", c.Name, c.Controller)
	}
	if c.Provider != "railway" {
		return fmt.Errorf("controller provisioning currently requires a Railway context")
	}
	if c.Project == "" || c.Environment == "" {
		return fmt.Errorf("Railway project and environment are required")
	}
	if !digestPinned(*boxImage) {
		return fmt.Errorf("--box-image must be pinned by digest (IMAGE@sha256:DIGEST)")
	}
	if *controllerImage != "" && !digestPinned(*controllerImage) {
		return fmt.Errorf("--controller-image must be pinned by digest (IMAGE@sha256:DIGEST)")
	}
	if *controllerImage != "" && *source != "" {
		return fmt.Errorf("select only one controller deployment source: --source or --controller-image")
	}
	if *controllerImage == "" {
		absolute, err := filepath.Abs(*source)
		if err != nil {
			return fmt.Errorf("resolve controller source: %w", err)
		}
		if info, err := os.Stat(filepath.Join(absolute, "Dockerfile")); err != nil || info.IsDir() {
			return fmt.Errorf("controller source %s must contain a Dockerfile", absolute)
		}
		*source = absolute
	}
	if *endpoint != "" {
		validated, err := validateControllerEndpoint(*endpoint)
		if err != nil {
			return err
		}
		*endpoint = validated
	}
	endpointPlan := *endpoint
	if endpointPlan == "" {
		endpointPlan = "<generated Railway domain>"
	}
	controllerArtifact := *controllerImage
	if controllerArtifact == "" {
		controllerArtifact = *source
	}
	fmt.Fprintf(a.Out, "Controller plan\n  provider: Railway\n  project: %s\n  environment: %s\n  service: %s (reused by name)\n  database: %s (reused by name)\n  controller source: %s\n  box image: %s\n  endpoint: %s\n", c.Project, c.Environment, controllerServiceName, controllerDatabaseName, controllerArtifact, *boxImage, endpointPlan)
	if !*yes {
		return fmt.Errorf("provisioning may create billable infrastructure; review the plan and rerun with --yes")
	}

	runner, err := a.controllerRailwayRunner(c)
	if err != nil {
		return err
	}
	target := []string{"--project", c.Project, "--environment", c.Environment}
	runInput := func(stdin io.Reader, argv ...string) (procexec.Result, error) {
		full := append([]string{"railway"}, argv...)
		full = append(full, target...)
		result, runErr := runner.Run(ctx, full, stdin, nil, nil)
		return result, railwayProvisionError(argv, result, runErr)
	}
	run := func(argv ...string) (procexec.Result, error) { return runInput(nil, argv...) }
	runAPI := func(document string, variables any) (procexec.Result, error) {
		encoded, marshalErr := json.Marshal(variables)
		if marshalErr != nil {
			return procexec.Result{}, marshalErr
		}
		argv := []string{"railway", "api", document, "--variables", string(encoded), "--compact"}
		result, runErr := runner.Run(ctx, argv, nil, nil, nil)
		return result, railwayProvisionError([]string{"api"}, result, runErr)
	}

	services, err := listRailwayServices(run)
	if err != nil {
		return err
	}
	if railwayServiceNamed(services, controllerDatabaseName).Name == "" {
		finished := a.progress(ctx, "creating Railway controller database")
		_, err = run("add", "--database", "postgres", "--service", controllerDatabaseName)
		finished()
		if err != nil {
			return err
		}
	} else {
		fmt.Fprintf(a.Err, "vmbox: reusing Railway service %s\n", controllerDatabaseName)
	}
	if railwayServiceNamed(services, controllerServiceName).Name == "" {
		finished := a.progress(ctx, "creating Railway controller service")
		_, err = run("add", "--service", controllerServiceName, "--json")
		finished()
		if err != nil {
			return err
		}
	} else {
		fmt.Fprintf(a.Err, "vmbox: reusing Railway service %s\n", controllerServiceName)
	}
	services, err = listRailwayServices(run)
	if err != nil {
		return err
	}
	controllerService := railwayServiceNamed(services, controllerServiceName)
	if controllerService.ID == "" {
		return fmt.Errorf("Railway did not return an ID for %s", controllerServiceName)
	}

	if *endpoint == "" {
		*endpoint, err = ensureRailwayDomain(run)
		if err != nil {
			return err
		}
	}
	existingVariables, err := listRailwayVariables(run)
	if err != nil {
		return err
	}
	variables := map[string]string{
		"DATABASE_URL":         "$" + "{{" + controllerDatabaseName + ".DATABASE_URL}}",
		"VMBOX_CONTROLLER_URL": *endpoint,
		"VMBOX_IMAGE":          *boxImage,
	}
	ownerToken := ""
	if existingVariables["VMBOX_ENCRYPTION_KEY"] == "" {
		encryption, randomErr := randomBytes(32)
		if randomErr != nil {
			return randomErr
		}
		variables["VMBOX_ENCRYPTION_KEY"] = base64.RawURLEncoding.EncodeToString(encryption)
	}
	if existingVariables["VMBOX_BOOTSTRAP_TOKEN_HASH"] == "" {
		rawToken, randomErr := randomBytes(32)
		if randomErr != nil {
			return randomErr
		}
		ownerToken = base64.RawURLEncoding.EncodeToString(rawToken)
		tokenHash := sha256.Sum256([]byte(ownerToken))
		variables["VMBOX_BOOTSTRAP_TOKEN_HASH"] = base64.RawURLEncoding.EncodeToString(tokenHash[:])
		variables["VMBOX_ACCOUNT_NAME"] = *account
		variables["VMBOX_OWNER_SUBJECT"] = *owner
		fmt.Fprintf(a.Out, "Save this controller owner token now (shown once):\n%s=%s\n", firstNonEmpty(c.TokenEnv, "VMBOX_CONTROLLER_TOKEN"), ownerToken)
	}
	keys := make([]string, 0, len(variables))
	for key := range variables {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	finishedVariables := a.progress(ctx, "configuring Railway controller variables")
	for _, key := range keys {
		if _, err := runInput(strings.NewReader(variables[key]), "variable", "set", key, "--stdin", "--service", controllerServiceName, "--skip-deploys"); err != nil {
			finishedVariables()
			return err
		}
	}
	finishedVariables()

	const updateService = "mutation($serviceId: String!, $environmentId: String!, $input: ServiceInstanceUpdateInput!) { serviceInstanceUpdate(serviceId: $serviceId, environmentId: $environmentId, input: $input) }"
	serviceConfig := map[string]any{
		"serviceId":     controllerService.ID,
		"environmentId": c.Environment,
		"input": map[string]any{
			"startCommand":    "/usr/local/bin/vmbox-controller",
			"healthcheckPath": "/healthz",
		},
	}
	if _, err := runAPI(updateService, serviceConfig); err != nil {
		return fmt.Errorf("configure controller start and health check: %w", err)
	}

	deploymentSource := *source
	cleanupSource := func() {}
	if *controllerImage == "" {
		deploymentSource, cleanupSource, err = stageControllerSource(*source)
		if err != nil {
			return err
		}
	}
	defer cleanupSource()
	finishedDeploy := a.progress(ctx, "deploying Railway controller")
	if *controllerImage != "" {
		if _, err = run("service", "source", "connect", "--service", controllerServiceName, "--image", *controllerImage, "--json"); err == nil {
			_, err = run("redeploy", "--service", controllerServiceName, "--yes", "--json", "--from-source")
		}
	} else {
		_, err = run("up", deploymentSource, "--path-as-root", "--ci", "--json", "--service", controllerServiceName)
	}
	finishedDeploy()
	if err != nil {
		return err
	}
	if err := a.waitControllerHealth(ctx, *endpoint, 8*time.Minute); err != nil {
		return err
	}

	c.Controller = *endpoint
	c.ControllerImage = *controllerImage
	c.ControllerSource = ""
	if *controllerImage == "" {
		c.ControllerSource = *source
	}
	c.Account = *account
	c.Image = *boxImage
	if c.TokenEnv == "" {
		c.TokenEnv = "VMBOX_CONTROLLER_TOKEN"
	}
	file.Contexts[c.Name] = c
	if err := config.Save(a.ConfigPath, file); err != nil {
		return err
	}
	if ownerToken == "" {
		fmt.Fprintln(a.Out, "controller configuration ensured; existing account, token, database, and service were preserved")
	}
	fmt.Fprintf(a.Out, "controller is healthy at %s\n", *endpoint)
	return nil
}

func (a *App) controllerRailwayRunner(c config.Context) (procexec.Runner, error) {
	runner := a.Runner
	if _, ok := runner.(procexec.OSRunner); !ok {
		return runner, nil
	}
	token, tokenEnvironment, err := railwayToken(a.Environ)
	if err != nil {
		if !c.RailwayCLIAuth || a.Environ["RAILWAY_TOKEN"] != "" || a.Environ["RAILWAY_API_TOKEN"] != "" {
			return nil, err
		}
		localRunner, _, runnerErr := railwayRunner("", "", a.Environ)
		return localRunner, runnerErr
	}
	tokenRunner, _, runnerErr := railwayRunner(token, tokenEnvironment, a.Environ)
	return tokenRunner, runnerErr
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func digestPinned(image string) bool {
	name, digest, ok := strings.Cut(image, "@sha256:")
	return ok && name != "" && len(digest) >= 32
}

func validateControllerEndpoint(value string) (string, error) {
	value = strings.TrimRight(strings.TrimSpace(value), "/")
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return "", fmt.Errorf("--endpoint must be a valid HTTPS URL")
	}
	if parsed.Scheme != "https" {
		host := parsed.Hostname()
		if parsed.Scheme != "http" || (host != "localhost" && net.ParseIP(host) == nil) {
			return "", fmt.Errorf("--endpoint must use HTTPS (HTTP is allowed only for a local IP or localhost)")
		}
	}
	return value, nil
}

func listRailwayServices(run func(...string) (procexec.Result, error)) ([]railwayService, error) {
	result, err := run("service", "list", "--json")
	if err != nil {
		return nil, err
	}
	var services []railwayService
	if err := json.Unmarshal(result.Stdout, &services); err != nil {
		return nil, fmt.Errorf("decode Railway services: %w", err)
	}
	return services, nil
}

func railwayServiceNamed(services []railwayService, name string) railwayService {
	for _, service := range services {
		if service.Name == name {
			return service
		}
	}
	return railwayService{}
}

func listRailwayVariables(run func(...string) (procexec.Result, error)) (map[string]string, error) {
	result, err := run("variable", "list", "--service", controllerServiceName, "--json")
	if err != nil {
		return nil, err
	}
	variables := make(map[string]string)
	if len(strings.TrimSpace(string(result.Stdout))) == 0 {
		return variables, nil
	}
	if err := json.Unmarshal(result.Stdout, &variables); err != nil {
		return nil, fmt.Errorf("decode Railway controller variables: %w", err)
	}
	return variables, nil
}

func ensureRailwayDomain(run func(...string) (procexec.Result, error)) (string, error) {
	result, err := run("domain", "list", "--service", controllerServiceName, "--json")
	if err != nil {
		return "", err
	}
	if endpoint := railwayEndpoint(result.Stdout); endpoint != "" {
		return endpoint, nil
	}
	result, err = run("domain", "--service", controllerServiceName, "--port", "8080", "--json")
	if err != nil {
		return "", err
	}
	if endpoint := railwayEndpoint(result.Stdout); endpoint != "" {
		return endpoint, nil
	}
	return "", fmt.Errorf("Railway created no public domain for %s", controllerServiceName)
}

func railwayEndpoint(data []byte) string {
	var root any
	if json.Unmarshal(data, &root) != nil {
		return ""
	}
	var found string
	var walk func(any)
	walk = func(value any) {
		if found != "" {
			return
		}
		switch typed := value.(type) {
		case string:
			candidate := strings.TrimSpace(typed)
			if strings.HasPrefix(candidate, "https://") || strings.HasPrefix(candidate, "http://") {
				found = strings.TrimRight(candidate, "/")
			} else if strings.Contains(candidate, ".up.railway.app") {
				found = "https://" + strings.Trim(candidate, "/")
			}
		case []any:
			for _, child := range typed {
				walk(child)
			}
		case map[string]any:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(root)
	return found
}

func railwayProvisionError(argv []string, result procexec.Result, err error) error {
	if err != nil {
		return err
	}
	if result.ExitCode == 0 {
		return nil
	}
	action := "Railway command"
	if len(argv) > 0 {
		action += " " + argv[0]
	}
	detail := strings.TrimSpace(string(result.Stderr))
	if detail == "" {
		return fmt.Errorf("%s failed with exit %d", action, result.ExitCode)
	}
	return fmt.Errorf("%s failed with exit %d: %s", action, result.ExitCode, detail)
}

func (a *App) controllerHealthyOnce(ctx context.Context, endpoint string) bool {
	requestCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, strings.TrimRight(endpoint, "/")+"/healthz", nil)
	if err != nil {
		return false
	}
	resp, err := a.HTTP.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (a *App) waitControllerHealth(ctx context.Context, endpoint string, timeout time.Duration) error {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	finished := a.progress(waitCtx, "waiting for Railway controller health")
	defer finished()
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		if a.controllerHealthyOnce(waitCtx, endpoint) {
			return nil
		}
		select {
		case <-waitCtx.Done():
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("controller did not become healthy at %s within %s", endpoint, timeout)
		case <-ticker.C:
		}
	}
}

func stageControllerSource(source string) (string, func(), error) {
	staged, err := os.MkdirTemp("", "vmbox-controller-source-*")
	if err != nil {
		return "", func() {}, fmt.Errorf("create controller source staging directory: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(staged) }
	var total int64
	for _, name := range []string{"Dockerfile", ".dockerignore", "entrypoint.sh", "go.mod", "go.sum", "cmd", "internal"} {
		if err := copyControllerSource(filepath.Join(source, name), filepath.Join(staged, name), &total); err != nil {
			cleanup()
			return "", func() {}, err
		}
	}
	return staged, cleanup, nil
}

func copyControllerSource(source, destination string, total *int64) error {
	info, err := os.Lstat(source)
	if err != nil {
		return fmt.Errorf("read controller source %s: %w", source, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("controller source contains unsupported symlink: %s", source)
	}
	if info.IsDir() {
		if err := os.Mkdir(destination, info.Mode().Perm()); err != nil {
			return fmt.Errorf("stage controller directory %s: %w", source, err)
		}
		entries, err := os.ReadDir(source)
		if err != nil {
			return fmt.Errorf("read controller directory %s: %w", source, err)
		}
		for _, entry := range entries {
			if err := copyControllerSource(filepath.Join(source, entry.Name()), filepath.Join(destination, entry.Name()), total); err != nil {
				return err
			}
		}
		return nil
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("controller source contains unsupported file: %s", source)
	}
	if info.Size() > 16<<20 || *total+info.Size() > 64<<20 {
		return fmt.Errorf("controller source exceeds the 16 MiB per-file or 64 MiB total staging limit")
	}
	*total += info.Size()
	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open controller source %s: %w", source, err)
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return fmt.Errorf("stage controller source %s: %w", source, err)
	}
	if _, err := io.Copy(output, input); err != nil {
		output.Close()
		return fmt.Errorf("copy controller source %s: %w", source, err)
	}
	if err := output.Close(); err != nil {
		return fmt.Errorf("close staged controller source %s: %w", source, err)
	}
	return nil
}
