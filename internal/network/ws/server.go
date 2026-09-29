package ws

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adnannpm/Bloomsom/internal/network"
	"github.com/gorilla/websocket"
)

var _ network.Transport = (*Server)(nil)

// Server implements network.Transport over WebSocket.
type Server struct {
	addr         string
	actualAddr   string
	httpServer   *http.Server
	listener     net.Listener
	upgrader     websocket.Upgrader
	connsMu      sync.RWMutex
	conns        map[string]*WSConn
	onMsg        network.MessageHandler
	onDisconnect network.DisconnectHandler
	log          *slog.Logger

	mu          sync.RWMutex
	started     atomic.Bool
	stopped     atomic.Bool
	ready       chan struct{}
	stoppedChan chan struct{}
	stopOnce    sync.Once

	pingPeriod time.Duration
	pongWait   time.Duration
	writeWait  time.Duration
}

// NewServer creates a new WebSocket network.Transport server.
func NewServer(
	addr string,
	onMsg network.MessageHandler,
	onDisconnect network.DisconnectHandler,
	log *slog.Logger,
) *Server {
	if log == nil {
		log = slog.Default()
	}

	return &Server{
		addr:         addr,
		onMsg:        onMsg,
		onDisconnect: onDisconnect,
		log:          log,
		conns:        make(map[string]*WSConn),
		ready:        make(chan struct{}),
		stoppedChan:  make(chan struct{}),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
		},
		pingPeriod: defaultPingPeriod,
		pongWait:   defaultPongWait,
		writeWait:  defaultWriteWait,
	}
}

// SetHeartbeat configures the heartbeat intervals used for new connections.
func (s *Server) SetHeartbeat(pingPeriod, pongWait, writeWait time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pingPeriod = pingPeriod
	s.pongWait = pongWait
	s.writeWait = writeWait
}

// Ready returns a receive-only channel that is closed when the server listener is bound.
func (s *Server) Ready() <-chan struct{} {
	return s.ready
}

// Addr returns the configured address, or the actual listener address if port was 0.
func (s *Server) Addr() string {
	s.mu.RLock()
	actual := s.actualAddr
	s.mu.RUnlock()
	if actual != "" {
		return actual
	}

	// Wait briefly if Start is currently running
	select {
	case <-s.ready:
		s.mu.RLock()
		defer s.mu.RUnlock()
		if s.actualAddr != "" {
			return s.actualAddr
		}
		return s.addr
	case <-time.After(1 * time.Second):
		s.mu.RLock()
		defer s.mu.RUnlock()
		if s.actualAddr != "" {
			return s.actualAddr
		}
		return s.addr
	}
}

// Start binds the listener, initializes routes, and begins serving WebSocket connections.
// It blocks until the server is stopped or ctx is cancelled.
func (s *Server) Start(ctx context.Context) error {
	if !s.started.CompareAndSwap(false, true) {
		return errors.New("ws: server already started")
	}

	if err := ctx.Err(); err != nil {
		close(s.ready)
		return err
	}

	mux := http.NewServeMux()
	handler := http.HandlerFunc(s.handleWebSocket)
	mux.Handle("/ws", handler)
	mux.Handle("/", handler)

	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		close(s.ready)
		return fmt.Errorf("ws: failed to listen on %s: %w", s.addr, err)
	}

	s.mu.Lock()
	s.listener = ln
	s.actualAddr = ln.Addr().String()
	s.httpServer = &http.Server{
		Addr:    s.actualAddr,
		Handler: mux,
	}
	s.mu.Unlock()

	close(s.ready)

	// Context cancellation watcher
	shutdownDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = s.Stop()
		case <-s.stoppedChan:
		}
		close(shutdownDone)
	}()

	s.log.Info("ws transport listening", "addr", s.actualAddr)
	serveErr := s.httpServer.Serve(ln)

	// Clean up if not already stopped
	_ = s.Stop()
	<-shutdownDone

	if errors.Is(serveErr, http.ErrServerClosed) {
		return nil
	}
	return serveErr
}

// Stop gracefully shuts down the server and closes all active client connections.
func (s *Server) Stop() error {
	var stopErr error
	s.stopOnce.Do(func() {
		s.stopped.Store(true)

		select {
		case <-s.stoppedChan:
		default:
			close(s.stoppedChan)
		}

		s.log.Info("ws transport stopping", "addr", s.Addr())

		s.mu.RLock()
		srv := s.httpServer
		s.mu.RUnlock()

		if srv != nil {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			stopErr = srv.Shutdown(shutdownCtx)
		}

		// Close all active connections
		s.connsMu.Lock()
		conns := make([]*WSConn, 0, len(s.conns))
		for _, c := range s.conns {
			conns = append(conns, c)
		}
		s.connsMu.Unlock()

		for _, c := range conns {
			_ = c.Close()
		}
	})
	return stopErr
}

// Broadcast sends a packet to all currently active connections.
func (s *Server) Broadcast(pkt network.Packet) {
	s.connsMu.RLock()
	conns := make([]*WSConn, 0, len(s.conns))
	for _, c := range s.conns {
		conns = append(conns, c)
	}
	s.connsMu.RUnlock()

	for _, c := range conns {
		if err := c.Send(pkt); err != nil {
			s.log.Debug("ws: broadcast packet dropped", "conn_id", c.ID(), "error", err)
		}
	}
}

// ActiveCount returns the number of active client connections.
func (s *Server) ActiveCount() int {
	s.connsMu.RLock()
	defer s.connsMu.RUnlock()
	return len(s.conns)
}

// handleWebSocket handles HTTP upgrade requests to WebSocket.
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	if s.stopped.Load() {
		http.Error(w, "server shutting down", http.StatusServiceUnavailable)
		return
	}

	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.log.Error("ws: upgrade failed", "remote_addr", r.RemoteAddr, "error", err)
		return
	}

	wsConn := NewWSConn(conn)

	s.mu.RLock()
	wsConn.pingPeriod = s.pingPeriod
	wsConn.pongWait = s.pongWait
	wsConn.writeWait = s.writeWait
	s.mu.RUnlock()

	s.connsMu.Lock()
	if s.stopped.Load() {
		s.connsMu.Unlock()
		_ = wsConn.Close()
		return
	}
	s.conns[wsConn.ID()] = wsConn
	s.connsMu.Unlock()

	var disconnectOnce sync.Once
	onDisconnectWrapper := func(c network.Connection) {
		disconnectOnce.Do(func() {
			s.connsMu.Lock()
			delete(s.conns, c.ID())
			s.connsMu.Unlock()

			if s.onDisconnect != nil {
				s.onDisconnect(c)
			}
		})
	}

	go wsConn.writePump()
	go wsConn.readPump(s.onMsg, onDisconnectWrapper)
}
