package udp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/adnannpm/Bloomsom/internal/network"
)

// Server implements network.Transport for UDP networking.
type Server struct {
	addr         string
	boundAddr    string
	rawConn      *net.UDPConn
	connMu       sync.RWMutex
	readyCh      chan struct{}
	onMsg        network.MessageHandler
	onDisconnect network.DisconnectHandler
	log          *slog.Logger

	conns   map[string]*UDPConn
	connsMu sync.RWMutex

	closeCh chan struct{}
	doneCh  chan struct{}
}

// NewServer constructs a new UDP transport server.
func NewServer(addr string, onMsg network.MessageHandler, onDisconnect network.DisconnectHandler, log *slog.Logger) *Server {
	return &Server{
		addr:         addr,
		readyCh:      make(chan struct{}),
		onMsg:        onMsg,
		onDisconnect: onDisconnect,
		log:          log,
		conns:        make(map[string]*UDPConn),
		closeCh:      make(chan struct{}),
		doneCh:       make(chan struct{}),
	}
}

// Start opens the UDP socket and begins listening for incoming datagrams.
func (s *Server) Start(ctx context.Context) error {
	laddr, err := net.ResolveUDPAddr("udp", s.addr)
	if err != nil {
		return fmt.Errorf("udp resolve addr: %w", err)
	}

	conn, err := net.ListenUDP("udp", laddr)
	if err != nil {
		return fmt.Errorf("udp listen: %w", err)
	}

	s.connMu.Lock()
	s.rawConn = conn
	s.boundAddr = conn.LocalAddr().String()
	s.connMu.Unlock()
	close(s.readyCh)

	if s.log != nil {
		s.log.Info("udp transport listening", "addr", s.boundAddr)
	}

	// Goroutine for session cleanup (timeout after 30s of inactivity)
	go s.janitor(30 * time.Second)

	// Context cancellation watcher
	go func() {
		select {
		case <-ctx.Done():
			_ = s.Stop()
		case <-s.closeCh:
		}
	}()

	defer close(s.doneCh)
	buf := make([]byte, 2048)

	for {
		n, remoteAddr, err := s.rawConn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-s.closeCh:
				return nil
			default:
				if errors.Is(err, net.ErrClosed) {
					return nil
				}
				if s.log != nil {
					s.log.Debug("udp read error", "err", err)
				}
				continue
			}
		}

		s.handlePacket(remoteAddr, buf[:n])
	}
}

func (s *Server) handlePacket(remoteAddr *net.UDPAddr, data []byte) {
	key := remoteAddr.String()

	s.connsMu.Lock()
	conn, exists := s.conns[key]
	if !exists {
		// New virtual connection session
		idBytes := make([]byte, 8)
		_, _ = rand.Read(idBytes)
		id := hex.EncodeToString(idBytes)
		conn = newUDPConn(id, remoteAddr, s)
		s.conns[key] = conn
	}
	s.connsMu.Unlock()

	conn.touch()

	var pkt network.Packet
	if err := json.Unmarshal(data, &pkt); err != nil {
		if s.log != nil {
			s.log.Debug("udp invalid json packet", "remote", key, "err", err)
		}
		return
	}

	if s.onMsg != nil {
		s.onMsg(conn, pkt)
	}
}

func (s *Server) removeConn(key string) {
	s.connsMu.Lock()
	conn, exists := s.conns[key]
	if exists {
		delete(s.conns, key)
	}
	s.connsMu.Unlock()

	if exists && s.onDisconnect != nil {
		s.onDisconnect(conn)
	}
}

func (s *Server) janitor(idleTimeout time.Duration) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.closeCh:
			return
		case now := <-ticker.C:
			var expired []*UDPConn

			s.connsMu.RLock()
			for _, conn := range s.conns {
				if now.Sub(conn.getLastSeen()) > idleTimeout {
					expired = append(expired, conn)
				}
			}
			s.connsMu.RUnlock()

			for _, c := range expired {
				_ = c.Close()
			}
		}
	}
}

// Stop terminates the UDP listener and closes all active virtual sessions.
func (s *Server) Stop() error {
	select {
	case <-s.closeCh:
		return nil
	default:
		close(s.closeCh)
	}

	s.connMu.Lock()
	if s.rawConn != nil {
		_ = s.rawConn.Close()
	}
	s.connMu.Unlock()

	s.connsMu.Lock()
	conns := make([]*UDPConn, 0, len(s.conns))
	for _, c := range s.conns {
		conns = append(conns, c)
	}
	s.conns = make(map[string]*UDPConn)
	s.connsMu.Unlock()

	for _, c := range conns {
		c.closed.Store(true)
		if s.onDisconnect != nil {
			s.onDisconnect(c)
		}
	}

	return nil
}

// Addr returns the bound UDP address, blocking until listener is initialized.
func (s *Server) Addr() string {
	<-s.readyCh
	s.connMu.RLock()
	defer s.connMu.RUnlock()
	return s.boundAddr
}

var _ network.Transport = (*Server)(nil)
