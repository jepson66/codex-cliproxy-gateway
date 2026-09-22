package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"codex-cliproxy-gateway/internal/catalog"
	"codex-cliproxy-gateway/internal/config"
	"codex-cliproxy-gateway/internal/diagnostics"
	"codex-cliproxy-gateway/internal/gateway"
	"codex-cliproxy-gateway/internal/install"
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
		if err := cfg.Save(path); err != nil {
			fatal(err)
		}
		fmt.Println(path)
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
		fmt.Println("stopped and removed the Gateway LaunchAgent; config and binary were preserved")
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

func usage() {
	fmt.Fprintln(os.Stderr, "usage: codex-cliproxy-gateway <init|import-cliproxy-key|catalog|install|repair-legacy-providers|uninstall|serve|service-install|service-uninstall|doctor|version> [--config PATH] [--e2e] [--model ID]")
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
