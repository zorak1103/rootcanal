package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gitlab.com/zorak1103/rootcanal/internal/config"
	"gitlab.com/zorak1103/rootcanal/internal/hostpool"
	"gitlab.com/zorak1103/rootcanal/internal/logging"
	"gitlab.com/zorak1103/rootcanal/internal/mcpserver"
	"gitlab.com/zorak1103/rootcanal/internal/session"
	"gitlab.com/zorak1103/rootcanal/internal/sftpops"
	"gitlab.com/zorak1103/rootcanal/internal/sshconn"
	"gitlab.com/zorak1103/rootcanal/internal/version"
)

func main() {
	versionFlag := flag.Bool("version", false, "print version and exit")
	validateFlag := flag.Bool("validate-config", false, "validate config file and exit")
	probeFlag := flag.String("probe", "", "dial the named host and exit")
	configPath := flag.String("config", defaultConfigPath(), "path to config file")
	flag.Parse()

	if *versionFlag {
		fmt.Printf("rootcanal %s (%s, built %s)\n", version.Version, version.Commit, version.Date)
		return
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	if *validateFlag {
		fmt.Printf("OK: %d host(s) defined\n", len(cfg.Hosts))
		return
	}

	if *probeFlag != "" {
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

	// MCP server mode.
	//
	// Before the MCP session is established, log to stderr (safe — the stdio
	// transport only reads stdout). Once the session handshake completes, swap
	// to mcp.NewLoggingHandler so subsequent logs reach the client via the
	// notifications/message channel.
	swap := logging.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	log := slog.New(swap)

	pool := hostpool.New(cfg, sshconn.ProdDialer{})
	mgr := session.NewManager(cfg, pool, log)
	ops := sftpops.New(cfg, pool)

	srv := mcpserver.New(mgr, ops, func(ss *mcp.ServerSession) {
		mcpH := mcp.NewLoggingHandler(ss, &mcp.LoggingHandlerOptions{
			LoggerName:  "rootcanal",
			MinInterval: 100 * time.Millisecond,
		})
		swap.Swap(mcpH)
		log.Info("MCP logging active", "version", version.Version)
	})

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	log.Info("rootcanal starting", "version", version.Version, "hosts", len(cfg.Hosts))

	if err := srv.Run(ctx, &mcp.StdioTransport{}); err != nil {
		log.Error("server exited with error", "err", err)
	}

	log.Info("shutting down")
	if err := mgr.Shutdown(context.Background()); err != nil {
		log.Error("shutdown error", "err", err)
	}
	pool.Close()
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
