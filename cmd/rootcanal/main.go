package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"gitlab.com/zorak1103/rootcanal/internal/config"
	"gitlab.com/zorak1103/rootcanal/internal/sshconn"
	"gitlab.com/zorak1103/rootcanal/internal/version"
)

func main() {
	versionFlag := flag.Bool("version", false, "print version and exit")
	validateFlag := flag.Bool("validate-config", false, "validate config file and exit")
	probeFlag := flag.String("probe", "", "dial the named host and print OK or error, then exit")
	configPath := flag.String("config", defaultConfigPath(), "path to config file")
	flag.Parse()

	if *versionFlag {
		fmt.Printf("rootcanal %s (%s, built %s)\n", version.Version, version.Commit, version.Date)
		return
	}

	if *validateFlag {
		cfg, err := config.Load(*configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "config error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("OK: %d host(s) defined\n", len(cfg.Hosts))
		return
	}

	if *probeFlag != "" {
		cfg, err := config.Load(*configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "config error: %v\n", err)
			os.Exit(1)
		}
		h, ok := cfg.Hosts[*probeFlag]
		if !ok {
			fmt.Fprintf(os.Stderr, "probe: host %q not found in config\n", *probeFlag)
			os.Exit(1)
		}
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		client, err := sshconn.ProdDialer{}.Dial(ctx, h, cfg.Limits)
		if err != nil {
			fmt.Fprintf(os.Stderr, "probe %q failed: %v\n", *probeFlag, err)
			os.Exit(1)
		}
		_ = client.Close()
		fmt.Printf("OK: connected to %s as %s\n", h.Address, h.User)
		return
	}

	fmt.Fprintln(os.Stderr, "rootcanal: MCP server not yet implemented — see upcoming milestones")
	os.Exit(1)
}

func defaultConfigPath() string {
	if _, err := os.Stat("rootcanal.yaml"); err == nil {
		return "rootcanal.yaml"
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "rootcanal", "config.yaml")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".config", "rootcanal", "config.yaml")
	}
	return "rootcanal.yaml"
}
