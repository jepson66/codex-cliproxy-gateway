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
	"codex-cliproxy-gateway/internal/lifecycle"
	"codex-cliproxy-gateway/internal/lifecycle/dependency"
	"codex-cliproxy-gateway/internal/service"
)

const Version = "0.1.0-poc"

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
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(streams.Err, "error: unexpected positional arguments: %s\n", strings.Join(flags.Args(), " "))
		return 2
	}
	if command == "version" || command == "--version" || command == "-version" {
		fmt.Fprintln(streams.Out, Version)
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
	}
	return 0
}

func knownCommand(command string) bool {
	switch command {
	case "init", "plan", "bootstrap", "catalog", "install", "repair-legacy-providers", "import-cliproxy-key", "uninstall", "serve", "service-install", "service-uninstall", "doctor", "version", "--version", "-version":
		return true
	default:
		return false
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
	fmt.Fprintln(output, "usage: codex-cliproxy-gateway <init|plan|bootstrap|import-cliproxy-key|catalog|install|repair-legacy-providers|uninstall|serve|service-install|service-uninstall|doctor|version> [options]")
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
