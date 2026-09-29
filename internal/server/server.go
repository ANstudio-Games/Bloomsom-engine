// Package server runs the Bloomsom game server lifecycle: boot, run until the
// context is cancelled, then shut down gracefully.
//
// Stage 1 of the roadmap covers config, logging and storage only. Network
// transports and rooms are added in later stages; until then Run keeps the
// process alive, emits periodic metrics and shuts down cleanly.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"time"

	"github.com/adnannpm/Bloomsom/internal/config"
	"github.com/adnannpm/Bloomsom/internal/storage"
	"github.com/adnannpm/Bloomsom/internal/version"
)

const (
	defaultMetricsInterval = 30 * time.Second
	defaultShutdownTimeout = 10 * time.Second
)

// Options configures a server run.
type Options struct {
	Config     config.Config
	ConfigPath string // shown in logs; informational only
	Sandbox    bool   // true when no config file was found
	Logger     *slog.Logger

	// MetricsInterval and ShutdownTimeout default to 30s and 10s when zero.
	MetricsInterval time.Duration
	ShutdownTimeout time.Duration
}

// Run boots the server and blocks until ctx is cancelled (normally by
// SIGINT/SIGTERM), then shuts down. It returns nil on a clean shutdown.
func Run(ctx context.Context, opts Options) error {
	if opts.Logger == nil {
		return errors.New("server: logger is required")
	}
	metricsEvery := opts.MetricsInterval
	if metricsEvery <= 0 {
		metricsEvery = defaultMetricsInterval
	}
	shutdownTimeout := opts.ShutdownTimeout
	if shutdownTimeout <= 0 {
		shutdownTimeout = defaultShutdownTimeout
	}

	log := opts.Logger
	cfg := opts.Config
	started := time.Now()

	log.Info("bloomsom starting",
		"version", version.Engine,
		"protocol", version.Protocol,
		"game", cfg.Game.Name,
		"preset", cfg.Game.Preset,
		"loop", cfg.Engine.Loop,
		"tick_rate", cfg.Engine.TickRate,
	)
	if opts.Sandbox {
		log.Warn("config file not found, running in SANDBOX mode; run `bloomsom init` to set up",
			"path", opts.ConfigPath)
	}
	if cfg.IsLAN() {
		log.Warn("server.host exposes the server beyond localhost and traffic is not encrypted",
			"host", cfg.Server.Host)
	}

	db, err := storage.Open(ctx, cfg.Database.Path)
	if err != nil {
		log.Error("database open failed", "path", cfg.Database.Path, "err", err)
		return fmt.Errorf("open database: %w", err)
	}

	applied, err := db.Migrate(ctx, cfg.Game.Preset)
	if err != nil {
		log.Error("database migration failed", "err", err)
		_ = db.Close()
		return fmt.Errorf("migrate database: %w", err)
	}
	for _, id := range applied {
		log.Info("migration applied", "id", id)
	}
	migrations, err := db.AppliedMigrations(ctx)
	if err != nil {
		log.Error("read schema version failed", "err", err)
		_ = db.Close()
		return fmt.Errorf("read migrations: %w", err)
	}
	log.Info("database ready", "path", db.Path(), "migrations", len(migrations))

	for _, proto := range cfg.Server.Transports {
		port := cfg.Server.WSPort
		if proto == "udp" {
			port = cfg.Server.UDPPort
		}
		log.Warn("transport not implemented yet, skipping (roadmap stage 3 and 5)",
			"proto", proto, "addr", net.JoinHostPort(cfg.Server.Host, strconv.Itoa(port)))
	}

	log.Info("server running, press Ctrl+C to stop")

	ticker := time.NewTicker(metricsEvery)
	defer ticker.Stop()
	for running := true; running; {
		select {
		case <-ctx.Done():
			running = false
		case <-ticker.C:
			log.Info("metrics", "uptime", uptime(started), "rooms", 0, "players", 0)
		}
	}

	return shutdown(log, db, started, context.Cause(ctx), shutdownTimeout)
}

// shutdown closes resources in order, bounded by timeout.
func shutdown(log *slog.Logger, db *storage.DB, started time.Time, cause error, timeout time.Duration) error {
	log.Info("shutdown signal received", "reason", cause)

	done := make(chan error, 1)
	go func() {
		// Later stages: stop accepting connections, notify clients,
		// stop room loops and flush the DB writer queue before this.
		log.Info("closing database")
		done <- db.Close()
	}()

	select {
	case err := <-done:
		if err != nil {
			log.Error("database close failed", "err", err)
			return fmt.Errorf("close database: %w", err)
		}
	case <-time.After(timeout):
		log.Error("shutdown timed out, forcing exit", "timeout", timeout)
		return fmt.Errorf("shutdown timed out after %s", timeout)
	}

	log.Info("bloomsom stopped", "uptime", uptime(started))
	return nil
}

func uptime(since time.Time) time.Duration {
	return time.Since(since).Round(time.Second)
}
