// Package server coordinates the Bloomsom game server lifecycle: network
// transports, authentication, room actors, metrics, and graceful shutdown.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/adnannpm/Bloomsom/engine"
	"github.com/adnannpm/Bloomsom/internal/auth"
	"github.com/adnannpm/Bloomsom/internal/config"
	"github.com/adnannpm/Bloomsom/internal/network"
	"github.com/adnannpm/Bloomsom/internal/network/netsim"
	"github.com/adnannpm/Bloomsom/internal/network/udp"
	"github.com/adnannpm/Bloomsom/internal/network/ws"
	"github.com/adnannpm/Bloomsom/internal/storage"
	"github.com/adnannpm/Bloomsom/internal/version"

	// Auto-register built-in presets
	_ "github.com/adnannpm/Bloomsom/presets/lobbychat"
	_ "github.com/adnannpm/Bloomsom/presets/realtime"
	_ "github.com/adnannpm/Bloomsom/presets/turnbased"
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

type activeSession struct {
	conn   network.Connection
	player *engine.Player
	room   *engine.Room
}

// Run boots the server, initializes network transports, routes packets to rooms,
// and blocks until ctx is cancelled (normally by SIGINT/SIGTERM), then shuts down.
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

	// 1. Open Database & Run Migrations
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

	authSvc := auth.NewService(db, cfg.Auth, log)
	roomMgr := engine.NewRoomManager(log)
	sim := netsim.New(cfg.Netsim)

	// Ensure default lobby room exists
	_, err = roomMgr.CreateRoom("default", "Main Lobby", cfg.Game.Preset, cfg.Engine.MaxPlayersPerRoom, cfg.Engine.Loop, cfg.Engine.TickRate)
	if err != nil {
		log.Warn("could not create default room", "err", err)
	}

	// Active connection sessions map
	var (
		sessionsMu sync.RWMutex
		sessions   = make(map[string]*activeSession)
	)

	// Packet dispatcher
	onMessage := func(conn network.Connection, pkt network.Packet) {
		handlePacket(ctx, conn, pkt, authSvc, roomMgr, sim, log, &sessionsMu, sessions, cfg)
	}

	// Disconnect handler
	onDisconnect := func(conn network.Connection) {
		sessionsMu.Lock()
		sess, ok := sessions[conn.ID()]
		if ok {
			delete(sessions, conn.ID())
		}
		sessionsMu.Unlock()

		if ok && sess.room != nil && sess.player != nil {
			sess.room.Leave(sess.player)
			log.Info("player left room on disconnect", "player", sess.player.Username, "room", sess.room.ID())
		}
	}

	// 2. Start Transports
	var transports []network.Transport
	for _, proto := range cfg.Server.Transports {
		switch proto {
		case "ws":
			wsAddr := net.JoinHostPort(cfg.Server.Host, strconv.Itoa(cfg.Server.WSPort))
			wsServer := ws.NewServer(wsAddr, onMessage, onDisconnect, log)
			go func() {
				if err := wsServer.Start(ctx); err != nil && !errors.Is(err, context.Canceled) {
					log.Error("websocket server error", "err", err)
				}
			}()
			transports = append(transports, wsServer)
			log.Info("websocket transport ready", "addr", wsAddr)

		case "udp":
			udpAddr := net.JoinHostPort(cfg.Server.Host, strconv.Itoa(cfg.Server.UDPPort))
			udpServer := udp.NewServer(udpAddr, onMessage, onDisconnect, log)
			go func() {
				if err := udpServer.Start(ctx); err != nil && !errors.Is(err, context.Canceled) {
					log.Error("udp server error", "err", err)
				}
			}()
			transports = append(transports, udpServer)
			log.Info("udp transport ready", "addr", udpAddr)
		}
	}

	log.Info("server running, press Ctrl+C to stop")

	ticker := time.NewTicker(metricsEvery)
	defer ticker.Stop()
	for running := true; running; {
		select {
		case <-ctx.Done():
			running = false
		case <-ticker.C:
			if purged, err := authSvc.PurgeExpired(ctx); err == nil && purged > 0 {
				log.Debug("purged expired sessions", "count", purged)
			}
			log.Info("metrics", "uptime", uptime(started), "rooms", roomMgr.RoomCount(), "players", roomMgr.TotalPlayers())
		}
	}

	return shutdown(log, db, transports, roomMgr, started, context.Cause(ctx), shutdownTimeout)
}

func handlePacket(
	ctx context.Context,
	conn network.Connection,
	pkt network.Packet,
	authSvc *auth.Service,
	roomMgr *engine.RoomManager,
	sim *netsim.Simulator,
	log *slog.Logger,
	sessionsMu *sync.RWMutex,
	sessions map[string]*activeSession,
	cfg config.Config,
) {
	switch pkt.Type {
	case network.TypePing:
		sim.Transmit(conn, network.Packet{Type: network.TypePong})

	case network.TypeRegister:
		var req struct {
			Username    string `json:"username"`
			Password    string `json:"password"`
			Email       string `json:"email"`
			DisplayName string `json:"display_name"`
		}
		if err := json.Unmarshal(pkt.Payload, &req); err != nil {
			sendError(conn, "invalid register payload")
			return
		}
		p, err := authSvc.Register(ctx, req.Username, req.Password, req.Email, req.DisplayName, auth.RolePlayer)
		if err != nil {
			sendError(conn, err.Error())
			return
		}
		res, _ := json.Marshal(map[string]any{
			"status":    "registered",
			"player_id": p.ID,
			"username":  p.Username,
		})
		sim.Transmit(conn, network.Packet{Type: network.TypeRegister, Payload: res})

	case network.TypeLogin:
		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := json.Unmarshal(pkt.Payload, &req); err != nil {
			sendError(conn, "invalid login payload")
			return
		}
		sess, p, err := authSvc.Login(ctx, req.Username, req.Password, conn.RemoteAddr(), "bloomsom-client")
		if err != nil {
			sendError(conn, err.Error())
			return
		}
		conn.SetPlayerID(p.ID, p.Username)

		sessionsMu.Lock()
		sessions[conn.ID()] = &activeSession{
			conn:   conn,
			player: engine.NewPlayer(p.ID, p.Username, p.DisplayName, conn),
		}
		sessionsMu.Unlock()

		res, _ := json.Marshal(map[string]any{
			"status":    "logged_in",
			"token":     sess.Token,
			"player_id": p.ID,
			"username":  p.Username,
		})
		sim.Transmit(conn, network.Packet{Type: network.TypeLogin, Payload: res})

	case network.TypeAuth:
		var req struct {
			Token string `json:"token"`
		}
		if err := json.Unmarshal(pkt.Payload, &req); err != nil {
			sendError(conn, "invalid auth payload")
			return
		}
		p, err := authSvc.Authenticate(ctx, req.Token)
		if err != nil {
			sendError(conn, err.Error())
			return
		}
		conn.SetPlayerID(p.ID, p.Username)

		sessionsMu.Lock()
		sessions[conn.ID()] = &activeSession{
			conn:   conn,
			player: engine.NewPlayer(p.ID, p.Username, p.DisplayName, conn),
		}
		sessionsMu.Unlock()

		res, _ := json.Marshal(map[string]any{
			"status":    "authenticated",
			"player_id": p.ID,
			"username":  p.Username,
		})
		sim.Transmit(conn, network.Packet{Type: network.TypeAuth, Payload: res})

	case network.TypeJoin:
		if conn.PlayerID() == 0 {
			sendError(conn, "authentication required before joining a room")
			return
		}
		var req struct {
			RoomID string `json:"room_id"`
		}
		_ = json.Unmarshal(pkt.Payload, &req)
		roomID := req.RoomID
		if roomID == "" {
			roomID = "default"
		}

		room, ok := roomMgr.GetRoom(roomID)
		if !ok {
			// Auto create room if it matches preset
			var err error
			room, err = roomMgr.CreateRoom(roomID, roomID, cfg.Game.Preset, cfg.Engine.MaxPlayersPerRoom, cfg.Engine.Loop, cfg.Engine.TickRate)
			if err != nil {
				sendError(conn, "room not found: "+roomID)
				return
			}
		}

		sessionsMu.Lock()
		sess := sessions[conn.ID()]
		if sess == nil {
			sess = &activeSession{
				conn:   conn,
				player: engine.NewPlayer(conn.PlayerID(), conn.Username(), conn.Username(), conn),
			}
			sessions[conn.ID()] = sess
		}
		sessionsMu.Unlock()

		if sess.room != nil {
			sess.room.Leave(sess.player)
		}

		if err := room.Join(sess.player); err != nil {
			sendError(conn, "cannot join room: "+err.Error())
			return
		}
		sess.room = room

		res, _ := json.Marshal(map[string]any{
			"status":  "joined",
			"room_id": room.ID(),
		})
		sim.Transmit(conn, network.Packet{Type: network.TypeJoin, Payload: res})

	case network.TypeLeave:
		sessionsMu.Lock()
		sess := sessions[conn.ID()]
		sessionsMu.Unlock()

		if sess != nil && sess.room != nil {
			sess.room.Leave(sess.player)
			sess.room = nil
			res, _ := json.Marshal(map[string]any{"status": "left"})
			sim.Transmit(conn, network.Packet{Type: network.TypeLeave, Payload: res})
		}

	default:
		// Dispatch gameplay/chat input to the current room
		sessionsMu.RLock()
		sess := sessions[conn.ID()]
		sessionsMu.RUnlock()

		if sess == nil || sess.room == nil || sess.player == nil {
			sendError(conn, "action rejected: not inside a room")
			return
		}

		sess.room.DispatchInput(sess.player, engine.Input{
			Seq:        pkt.Seq,
			Type:       pkt.Type,
			Payload:    pkt.Payload,
			ReceivedAt: time.Now(),
		})
	}
}

func sendError(conn network.Connection, msg string) {
	b, _ := json.Marshal(map[string]string{"error": msg})
	_ = conn.Send(network.Packet{
		Type:    network.TypeError,
		Payload: b,
	})
}

// shutdown closes network listeners, active rooms, and database.
func shutdown(
	log *slog.Logger,
	db *storage.DB,
	transports []network.Transport,
	roomMgr *engine.RoomManager,
	started time.Time,
	cause error,
	timeout time.Duration,
) error {
	log.Info("shutdown signal received", "reason", cause)

	done := make(chan error, 1)
	go func() {
		// Stop transports
		for _, tr := range transports {
			_ = tr.Stop()
		}
		// Close all active rooms
		roomMgr.CloseAll()
		// Close database
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
