package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"gitlab.com/zorak1103/rootcanal/internal/config"
	"gitlab.com/zorak1103/rootcanal/internal/version"
)

func main() {
	versionFlag := flag.Bool("version", false, "print version and exit")
	validateFlag := flag.Bool("validate-config", false, "validate config file and exit")
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

	fmt.Fprintln(os.Stderr, "rootcanal: MCP server not yet implemented — see upcoming milestones")
	os.Exit(1)
}

// defaultConfigPath returns the config file path to use when -config is not given.
// Checks ./rootcanal.yaml first, then $XDG_CONFIG_HOME/rootcanal/config.yaml,
// then ~/.config/rootcanal/config.yaml.
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
