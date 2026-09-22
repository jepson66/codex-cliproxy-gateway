package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
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

const version = "0.1.0-poc"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	command := os.Args[1]
	flags := flag.NewFlagSet(command, flag.ExitOnError)
	configPath := flags.String("config", "", "path to gateway JSON config")
	e2e := flags.Bool("e2e", false, "perform a minimal billable third-party model request (doctor only)")
	diagnosticModel := flags.String("model", "", "third-party model id to use with doctor --e2e")
	cliproxyMode := flags.String("cliproxy-mode", string(lifecycle.External), "CLIProxyAPI lifecycle mode: external or managed")
	cliproxyVersion := flags.String("cliproxy-version", dependency.DefaultCLIProxyAPIVersion, "pinned CLIProxyAPI version for managed mode")
	yes := flags.Bool("yes", false, "confirm the displayed bootstrap plan non-interactively")
	experimentalManaged := flags.Bool("experimental-managed", false, "enable the gated managed CLIProxyAPI installer")
	_ = flags.Parse(os.Args[2:])

	cfg, err := config.Load(*configPath)
	if err != nil {
		fatal(err)
	}

	switch command {
	case "init":
		path := *configPath
		if path == "" {
			path = config.DefaultPath()
		}
		created, err := lifecycle.EnsureGatewayConfig(path, cfg)
		if err != nil {
			fatal(err)
		}
		if created {
			fmt.Println("created", path)
		} else {
			fmt.Println("preserved existing", path)
		}
	case "plan":
		path := resolvedConfigPath(*configPath)
		plan, err := lifecycle.BuildPlan(cfg, path, lifecycle.Mode(*cliproxyMode))
		if err != nil {
			fatal(err)
		}
		fmt.Println(plan.String())
	case "bootstrap":
		if err := bootstrap(cfg, resolvedConfigPath(*configPath), lifecycle.Mode(*cliproxyMode), *cliproxyVersion, *experimentalManaged, *yes); err != nil {
			fatal(err)
		}
	case "catalog":
		if err := catalog.Write(cfg); err != nil {
			fatal(err)
		}
		doc, _ := catalog.Generate(cfg)
		fmt.Printf("wrote %s with %d models\n", cfg.ModelCatalogPath, len(doc.Models))
	case "install":
		state, err := install.Apply(cfg)
		if err != nil {
			fatal(err)
		}
		fmt.Println("installed Codex config")
		fmt.Println(install.Summary(cfg, state))
	case "repair-legacy-providers":
		backupPath, changed, err := install.RepairLegacyProviders(cfg)
		if err != nil {
			fatal(err)
		}
		if !changed {
			fmt.Println("legacy Codex providers already route through the Gateway")
			break
		}
		fmt.Printf("repaired legacy Codex providers; backup: %s\n", backupPath)
	case "import-cliproxy-key":
		if err := cfg.ImportCLIProxyAPIKey(); err != nil {
			fatal(err)
		}
		fmt.Printf("imported CLIProxyAPI access key to %s\n", cfg.CLIProxyAPIKeyFile)
	case "uninstall":
		if err := install.Restore(cfg); err != nil {
			fatal(err)
		}
		fmt.Println("restored Codex config backup")
	case "serve":
		logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
		server, err := gateway.New(cfg, logger)
		if err != nil {
			fatal(err)
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := server.ListenAndServe(ctx); err != nil {
			fatal(err)
		}
	case "service-install":
		path := *configPath
		if path == "" {
			path = config.DefaultPath()
		}
		paths, err := service.Install(path)
		if err != nil {
			fatal(err)
		}
		fmt.Printf("installed and started %s using %s\n", service.Label, paths.Binary)
	case "service-uninstall":
		if err := service.Uninstall(); err != nil {
			fatal(err)
		}
		if lifecycle.Mode(*cliproxyMode) == lifecycle.Managed {
			if err := service.UninstallCLIProxy(); err != nil {
				fatal(err)
			}
		}
		if lifecycle.Mode(*cliproxyMode) == lifecycle.Managed {
			fmt.Println("stopped and removed the Gateway and managed CLIProxyAPI LaunchAgents; configs and binaries were preserved")
		} else {
			fmt.Println("stopped and removed the Gateway LaunchAgent; config and binary were preserved")
		}
	case "doctor":
		doctor(cfg, *e2e, *diagnosticModel)
	case "version", "--version", "-version":
		fmt.Println(version)
	default:
		usage()
		os.Exit(2)
	}
}

func doctor(cfg config.Config, e2e bool, model string) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	checks := (diagnostics.Runner{}).Run(ctx, cfg, e2e, model)
	failed := false
	for _, check := range checks {
		if check.Err != nil {
			failed = true
			fmt.Printf("FAIL %-24s %v\n", check.Name, check.Err)
		} else {
			fmt.Printf("OK   %s\n", check.Name)
		}
	}
	if failed {
		os.Exit(1)
	}
}

func bootstrap(cfg config.Config, configPath string, mode lifecycle.Mode, cliproxyVersion string, experimentalManaged, yes bool) error {
	plan, err := lifecycle.BuildPlan(cfg, configPath, mode)
	if err != nil {
		return err
	}
	fmt.Println(plan.String())
	if mode == lifecycle.Managed && !experimentalManaged {
		return fmt.Errorf("managed mode is experiment-only until the clean-machine admission test passes; rerun with --experimental-managed after reviewing the plan")
	}
	if err := confirm("Apply this installation plan?", yes); err != nil {
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
		if _, err := (dependency.Installer{}).Install(context.Background(), asset, binaryPath, statePath); err != nil {
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
	fmt.Println(install.Summary(cfg, state))
	if mode == lifecycle.Managed {
		fmt.Println("Managed CLIProxyAPI was installed with an empty provider mapping; add provider credentials to", cfg.CLIProxyConfigPath, "without committing that file to Git.")
	}
	return nil
}

func confirm(prompt string, yes bool) error {
	if yes {
		return nil
	}
	info, err := os.Stdin.Stat()
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeCharDevice == 0 {
		return fmt.Errorf("confirmation required; rerun with --yes after reviewing the plan")
	}
	fmt.Printf("%s [y/N] ", prompt)
	answer, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
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

func usage() {
	fmt.Fprintln(os.Stderr, "usage: codex-cliproxy-gateway <init|plan|bootstrap|import-cliproxy-key|catalog|install|repair-legacy-providers|uninstall|serve|service-install|service-uninstall|doctor|version> [options]")
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
