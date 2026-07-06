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
	"github.com/zorak1103/rootcanal/internal/config"
	"github.com/zorak1103/rootcanal/internal/hostkeys"
	"github.com/zorak1103/rootcanal/internal/hostpool"
	"github.com/zorak1103/rootcanal/internal/jobs"
	"github.com/zorak1103/rootcanal/internal/logging"
	"github.com/zorak1103/rootcanal/internal/mcpserver"
	"github.com/zorak1103/rootcanal/internal/session"
	"github.com/zorak1103/rootcanal/internal/sftpops"
	"github.com/zorak1103/rootcanal/internal/sshconn"
	"github.com/zorak1103/rootcanal/internal/version"
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
		os.Exit(runProbe(*probeFlag, cfg))
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
	jobReg := jobs.NewRegistry(cfg.Limits.MaxJobs, cfg.Limits.JobTTL)
	defer jobReg.Close()
	hk := hostkeys.New(cfg, sshconn.ProdScanner{})

	srv := mcpserver.New(mgr, ops, cfg, jobReg, hk, func(ss *mcp.ServerSession) {
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
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := mgr.Shutdown(shutdownCtx); err != nil {
		log.Error("shutdown error", "err", err)
	}
	pool.Close()
}

// runProbe dials the named host and reports success/failure, returning the
// process exit code. Kept separate from main so its deferred cancel() always
// runs — main calls os.Exit(runProbe(...)) from a frame with no live defers.
func runProbe(name string, cfg *config.Config) int {
	h, ok := cfg.Hosts[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "probe: host %q not found in config\n", name)
		return 1
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	client, err := sshconn.ProdDialer{}.Dial(ctx, h, cfg.Limits)
	if err != nil {
		fmt.Fprintf(os.Stderr, "probe %q failed: %v\n", name, err)
		return 1
	}
	_ = client.Close()
	fmt.Printf("OK: connected to %s as %s\n", h.Address, h.User)
	return 0
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
