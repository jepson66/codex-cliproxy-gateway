package app

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"codex-cliproxy-gateway/internal/catalog"
	"codex-cliproxy-gateway/internal/config"
	"codex-cliproxy-gateway/internal/diagnostics"
	"codex-cliproxy-gateway/internal/gateway"
	"codex-cliproxy-gateway/internal/install"
	"codex-cliproxy-gateway/internal/kimioauth"
	"codex-cliproxy-gateway/internal/lifecycle"
	"codex-cliproxy-gateway/internal/lifecycle/dependency"
	"codex-cliproxy-gateway/internal/providerauth"
	"codex-cliproxy-gateway/internal/service"
)

var Version = "0.1.0-dev"

type Streams struct {
	In         io.Reader
	Out        io.Writer
	Err        io.Writer
	IsTerminal func() bool
}

func Run(ctx context.Context, args []string, streams Streams) int {
	streams = streams.withDefaults()
	if len(args) < 1 {
		usage(streams.Err)
		return 2
	}
	command := args[0]
	if !knownCommand(command) {
		usage(streams.Err)
		return 2
	}

	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(streams.Err)
	configPath := flags.String("config", "", "path to gateway JSON config")
	e2e := flags.Bool("e2e", false, "perform a minimal billable third-party model request (doctor only)")
	diagnosticModel := flags.String("model", "", "third-party model id to use with doctor --e2e")
	cliproxyMode := flags.String("cliproxy-mode", string(lifecycle.External), "CLIProxyAPI lifecycle mode: external or managed")
	cliproxyVersion := flags.String("cliproxy-version", dependency.DefaultCLIProxyAPIVersion, "pinned CLIProxyAPI version for managed mode")
	yes := flags.Bool("yes", false, "confirm the displayed bootstrap plan non-interactively")
	experimentalManaged := flags.Bool("experimental-managed", false, "enable the gated managed CLIProxyAPI installer")
	noBrowser := flags.Bool("no-browser", false, "do not open the provider setup page (login only)")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	providerCommand := command == "login" || command == "auth-status"
	if providerCommand && flags.NArg() != 1 {
		fmt.Fprintf(streams.Err, "error: %s requires exactly one provider id\n", command)
		return 2
	}
	if !providerCommand && flags.NArg() != 0 {
		fmt.Fprintf(streams.Err, "error: unexpected positional arguments: %s\n", strings.Join(flags.Args(), " "))
		return 2
	}
	if command == "version" || command == "--version" || command == "-version" {
		fmt.Fprintln(streams.Out, Version)
		return 0
	}
	if command == "status" {
		report, err := service.Inspect()
		if err != nil {
			return fail(streams.Err, err)
		}
		fmt.Fprintln(streams.Out, report.String())
		return 0
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return fail(streams.Err, err)
	}

	switch command {
	case "init":
		path := resolvedConfigPath(*configPath)
		created, err := lifecycle.EnsureGatewayConfig(path, cfg)
		if err != nil {
			return fail(streams.Err, err)
		}
		if created {
			fmt.Fprintln(streams.Out, "created", path)
		} else {
			fmt.Fprintln(streams.Out, "preserved existing", path)
		}
	case "plan":
		plan, err := lifecycle.BuildPlan(cfg, resolvedConfigPath(*configPath), lifecycle.Mode(*cliproxyMode))
		if err != nil {
			return fail(streams.Err, err)
		}
		fmt.Fprintln(streams.Out, plan.String())
	case "bootstrap":
		if err := bootstrap(ctx, streams, cfg, resolvedConfigPath(*configPath), lifecycle.Mode(*cliproxyMode), *cliproxyVersion, *experimentalManaged, *yes); err != nil {
			return fail(streams.Err, err)
		}
	case "catalog":
		if err := catalog.Write(cfg); err != nil {
			return fail(streams.Err, err)
		}
		doc, _ := catalog.Generate(cfg)
		fmt.Fprintf(streams.Out, "wrote %s with %d models\n", cfg.ModelCatalogPath, len(doc.Models))
	case "install":
		state, err := install.Apply(cfg)
		if err != nil {
			return fail(streams.Err, err)
		}
		fmt.Fprintln(streams.Out, "installed Codex config")
		fmt.Fprintln(streams.Out, install.Summary(cfg, state))
	case "repair-legacy-providers":
		backupPath, changed, err := install.RepairLegacyProviders(cfg)
		if err != nil {
			return fail(streams.Err, err)
		}
		if !changed {
			fmt.Fprintln(streams.Out, "legacy Codex providers already route through the Gateway")
			break
		}
		fmt.Fprintf(streams.Out, "repaired legacy Codex providers; backup: %s\n", backupPath)
	case "import-cliproxy-key":
		if err := cfg.ImportCLIProxyAPIKey(); err != nil {
			return fail(streams.Err, err)
		}
		fmt.Fprintf(streams.Out, "imported CLIProxyAPI access key to %s\n", cfg.CLIProxyAPIKeyFile)
	case "uninstall":
		if err := install.Restore(cfg); err != nil {
			return fail(streams.Err, err)
		}
		fmt.Fprintln(streams.Out, "restored Codex config backup")
	case "serve":
		logger := slog.New(slog.NewTextHandler(streams.Err, &slog.HandlerOptions{Level: slog.LevelInfo}))
		if authDir, resolveErr := cfg.ResolveCLIProxyAuthDir(); resolveErr == nil {
			if updated, priorityErr := kimioauth.EnsureCredentialPriority(authDir); priorityErr != nil {
				logger.Warn("unable to apply Kimi OAuth credential priority", "error", priorityErr)
			} else if updated > 0 {
				logger.Info("upgraded Kimi OAuth credential priority", "files", updated)
			}
		}
		server, err := gateway.New(cfg, logger)
		if err != nil {
			return fail(streams.Err, err)
		}
		if err := server.ListenAndServe(ctx); err != nil {
			return fail(streams.Err, err)
		}
	case "service-install":
		paths, err := service.Install(resolvedConfigPath(*configPath))
		if err != nil {
			return fail(streams.Err, err)
		}
		fmt.Fprintf(streams.Out, "installed and started %s using %s\n", service.Label, paths.Binary)
	case "service-uninstall":
		if err := service.Uninstall(); err != nil {
			return fail(streams.Err, err)
		}
		if lifecycle.Mode(*cliproxyMode) == lifecycle.Managed {
			if err := service.UninstallCLIProxy(); err != nil {
				return fail(streams.Err, err)
			}
			fmt.Fprintf(streams.Out, "stopped and removed the Gateway and managed CLIProxyAPI %s definitions; configs and binaries were preserved\n", service.ManagerName())
		} else {
			fmt.Fprintf(streams.Out, "stopped and removed the Gateway %s definition; config and binary were preserved\n", service.ManagerName())
		}
	case "doctor":
		if !doctor(ctx, streams.Out, cfg, *e2e, *diagnosticModel) {
			return 1
		}
	case "login":
		if err := runProviderCommand(ctx, streams, cfg, flags.Arg(0), true, *noBrowser, providerCommandDependencies{}); err != nil {
			return fail(streams.Err, err)
		}
	case "auth-status":
		if err := runProviderCommand(ctx, streams, cfg, flags.Arg(0), false, true, providerCommandDependencies{}); err != nil {
			return fail(streams.Err, err)
		}
	}
	return 0
}

func knownCommand(command string) bool {
	switch command {
	case "init", "plan", "bootstrap", "catalog", "install", "repair-legacy-providers", "import-cliproxy-key", "uninstall", "serve", "service-install", "service-uninstall", "status", "doctor", "login", "auth-status", "version", "--version", "-version":
		return true
	default:
		return false
	}
}

type providerCommandDependencies struct {
	checker        providerauth.Checker
	openURL        func(string) error
	kimiOAuth      kimiDeviceFlow
	saveCredential func(string, kimioauth.Token, string, time.Time) (string, error)
	now            func() time.Time
}

type kimiDeviceFlow interface {
	RequestDeviceCode(context.Context) (kimioauth.DeviceCode, error)
	PollForToken(context.Context, kimioauth.DeviceCode) (kimioauth.Token, error)
	DeviceIdentifier() string
}

func runProviderCommand(ctx context.Context, streams Streams, cfg config.Config, providerID string, login, noBrowser bool, deps providerCommandDependencies) error {
	status, err := deps.checker.Check(ctx, cfg, providerID, "")
	if err != nil {
		return err
	}
	if status.Configured {
		fmt.Fprintf(streams.Out, "%s is configured in CLIProxyAPI. Credential validity is confirmed when the first model request reaches the provider.\n", status.Provider.DisplayName)
		return nil
	}
	if !login {
		return fmt.Errorf("%s", providerauth.LoginMessage(status.Provider, cfg.CLIProxyConfigPath, false))
	}
	if status.Provider.ID == "kimi-code" {
		return runKimiCodeLogin(ctx, streams, cfg, status.Provider, noBrowser, deps)
	}
	fmt.Fprintf(streams.Out, "%s is not configured.\n", status.Provider.DisplayName)
	fmt.Fprintf(streams.Out, "Setup page: %s\n", status.Provider.SetupURL)
	if hint := strings.TrimSpace(status.Provider.SetupHint); hint != "" {
		fmt.Fprintln(streams.Out, hint)
	}
	fmt.Fprintf(streams.Out, "CLIProxyAPI config: %s\n", cfg.CLIProxyConfigPath)
	fmt.Fprintf(streams.Out, "Create the provider credential, add the %s provider block documented in the README, then run:\n", status.Provider.ID)
	fmt.Fprintf(streams.Out, "  codex-cliproxy-gateway auth-status %s\n", status.Provider.ID)
	if noBrowser {
		return nil
	}
	openURL := deps.openURL
	if openURL == nil {
		openURL = providerauth.OpenSetupURL
	}
	if err := openURL(status.Provider.SetupURL); err != nil {
		fmt.Fprintf(streams.Err, "warning: %v; open %s manually\n", err, status.Provider.SetupURL)
	}
	return nil
}

func runKimiCodeLogin(ctx context.Context, streams Streams, cfg config.Config, provider config.ProviderSpec, noBrowser bool, deps providerCommandDependencies) error {
	flow := deps.kimiOAuth
	if flow == nil {
		flow = kimioauth.NewClient(Version)
	}
	device, err := flow.RequestDeviceCode(ctx)
	if err != nil {
		return err
	}
	verificationURL := device.VerificationURL()
	fmt.Fprintln(streams.Out, "Open this one-time Kimi authorization URL:")
	fmt.Fprintln(streams.Out, verificationURL)
	if strings.TrimSpace(device.UserCode) != "" {
		fmt.Fprintln(streams.Out, "User code:", device.UserCode)
	}
	if !noBrowser {
		openURL := deps.openURL
		if openURL == nil {
			openURL = providerauth.OpenSetupURL
		}
		if err := openURL(verificationURL); err != nil {
			fmt.Fprintf(streams.Err, "warning: %v; open the authorization URL manually\n", err)
		}
	}
	fmt.Fprintln(streams.Out, "Waiting for Kimi authorization...")
	token, err := flow.PollForToken(ctx, device)
	if err != nil {
		return err
	}
	saveCredential := deps.saveCredential
	if saveCredential == nil {
		saveCredential = kimioauth.SaveCredential
	}
	now := time.Now()
	if deps.now != nil {
		now = deps.now()
	}
	authDir, err := cfg.ResolveCLIProxyAuthDir()
	if err != nil {
		return err
	}
	path, err := saveCredential(authDir, token, flow.DeviceIdentifier(), now)
	if err != nil {
		return err
	}
	fmt.Fprintf(streams.Out, "Kimi authorization saved securely to %s.\n", path)
	if err := waitForProvider(ctx, deps.checker, cfg, provider.ID, 5*time.Second); err != nil {
		return fmt.Errorf("Kimi authorization was saved, but CLIProxyAPI has not loaded it: %w; verify cliproxy_auth_dir and run 'codex-cliproxy-gateway auth-status kimi-code'", err)
	}
	fmt.Fprintln(streams.Out, "Kimi Code is configured and available through CLIProxyAPI.")
	return nil
}

func waitForProvider(ctx context.Context, checker providerauth.Checker, cfg config.Config, providerID string, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		status, err := checker.Check(ctx, cfg, providerID, "")
		if err == nil && status.Configured {
			return nil
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("required model is not listed yet")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return lastErr
		case <-ticker.C:
		}
	}
}

func doctor(parent context.Context, output io.Writer, cfg config.Config, e2e bool, model string) bool {
	ctx, cancel := context.WithTimeout(parent, 45*time.Second)
	defer cancel()
	checks := (diagnostics.Runner{}).Run(ctx, cfg, e2e, model)
	passed := true
	for _, check := range checks {
		if check.Err != nil {
			passed = false
			fmt.Fprintf(output, "FAIL %-24s %v\n", check.Name, check.Err)
		} else {
			fmt.Fprintf(output, "OK   %s\n", check.Name)
		}
	}
	return passed
}

func bootstrap(ctx context.Context, streams Streams, cfg config.Config, configPath string, mode lifecycle.Mode, cliproxyVersion string, experimentalManaged, yes bool) error {
	plan, err := lifecycle.BuildPlan(cfg, configPath, mode)
	if err != nil {
		return err
	}
	fmt.Fprintln(streams.Out, plan.String())
	if mode == lifecycle.Managed && !experimentalManaged {
		return fmt.Errorf("managed mode is experiment-only until the clean-machine admission test passes; rerun with --experimental-managed after reviewing the plan")
	}
	if err := confirm(streams, "Apply this installation plan?", yes); err != nil {
		return err
	}
	if _, err := lifecycle.EnsureGatewayConfig(configPath, cfg); err != nil {
		return err
	}

	if mode == lifecycle.Managed {
		asset, err := dependency.CurrentAsset(cliproxyVersion)
		if err != nil {
			return err
		}
		binaryPath, err := dependency.DefaultBinaryPath()
		if err != nil {
			return err
		}
		statePath := filepath.Join(cfg.CodexHome, "codex-cliproxy-gateway", "dependencies", "cliproxyapi.json")
		if _, err := (dependency.Installer{BeforeWrite: service.StopCLIProxyForUpdate}).Install(ctx, asset, binaryPath, statePath); err != nil {
			return err
		}
		if _, err := lifecycle.EnsureManagedCLIProxyConfig(cfg); err != nil {
			return err
		}
		if _, err := cfg.ResolveCLIProxyAPIKey(); err != nil {
			if importErr := cfg.ImportCLIProxyAPIKey(); importErr != nil {
				return fmt.Errorf("import managed CLIProxyAPI access key: %w", importErr)
			}
		}
		if _, err := service.InstallCLIProxy(binaryPath, cfg.CLIProxyConfigPath); err != nil {
			return err
		}
	} else if _, err := cfg.ResolveCLIProxyAPIKey(); err != nil {
		if importErr := cfg.ImportCLIProxyAPIKey(); importErr != nil {
			return fmt.Errorf("external CLIProxyAPI is not ready: %w", importErr)
		}
	}

	state, err := install.Apply(cfg)
	if err != nil {
		return err
	}
	if _, err := service.Install(configPath); err != nil {
		if restoreErr := install.Restore(cfg); restoreErr != nil {
			return fmt.Errorf("install Gateway service: %v; automatic Codex config rollback also failed: %v", err, restoreErr)
		}
		return fmt.Errorf("install Gateway service: %w; Codex config was rolled back", err)
	}
	fmt.Fprintln(streams.Out, install.Summary(cfg, state))
	if mode == lifecycle.Managed {
		fmt.Fprintln(streams.Out, "Managed CLIProxyAPI was installed with an empty provider mapping; add provider credentials to", cfg.CLIProxyConfigPath, "without committing that file to Git.")
	}
	return nil
}

func confirm(streams Streams, prompt string, yes bool) error {
	if yes {
		return nil
	}
	if streams.IsTerminal == nil || !streams.IsTerminal() {
		return fmt.Errorf("confirmation required; rerun with --yes after reviewing the plan")
	}
	fmt.Fprintf(streams.Out, "%s [y/N] ", prompt)
	answer, err := bufio.NewReader(streams.In).ReadString('\n')
	if err != nil && err != io.EOF {
		return err
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	if answer != "y" && answer != "yes" {
		return fmt.Errorf("installation cancelled")
	}
	return nil
}

func resolvedConfigPath(path string) string {
	if path == "" {
		return config.DefaultPath()
	}
	return config.ExpandPath(path)
}

func usage(output io.Writer) {
	fmt.Fprintln(output, "usage: codex-cliproxy-gateway <init|plan|bootstrap|import-cliproxy-key|catalog|install|repair-legacy-providers|uninstall|serve|service-install|service-uninstall|status|doctor|login|auth-status|version> [options]")
}

func fail(output io.Writer, err error) int {
	fmt.Fprintln(output, "error:", err)
	return 1
}

func (s Streams) withDefaults() Streams {
	if s.In == nil {
		s.In = strings.NewReader("")
	}
	if s.Out == nil {
		s.Out = io.Discard
	}
	if s.Err == nil {
		s.Err = io.Discard
	}
	return s
}
