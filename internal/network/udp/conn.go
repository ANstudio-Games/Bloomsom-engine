package udp

import (
	"encoding/json"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adnannpm/Bloomsom/internal/network"
)

var (
	ErrConnClosed     = errors.New("udp: connection is closed")
	ErrPacketTooLarge = errors.New("udp: packet exceeds MTU limit")
)

const maxUDPPacketSize = 1400

// UDPConn represents a virtual connection session over UDP.
type UDPConn struct {
	id         string
	addr       *net.UDPAddr
	server     *Server
	playerID   atomic.Int64
	username   string
	userMu     sync.RWMutex
	lastSeen   time.Time
	lastSeenMu sync.RWMutex
	closed     atomic.Bool
}

func newUDPConn(id string, addr *net.UDPAddr, s *Server) *UDPConn {
	return &UDPConn{
		id:       id,
		addr:     addr,
		server:   s,
		lastSeen: time.Now(),
	}
}

// ID returns the virtual session identifier.
func (c *UDPConn) ID() string { return c.id }

// RemoteAddr returns the client's UDP address string.
func (c *UDPConn) RemoteAddr() string { return c.addr.String() }

// Send serializes and transmits a packet to the remote UDP client.
func (c *UDPConn) Send(pkt network.Packet) error {
	if c.closed.Load() {
		return ErrConnClosed
	}

	data, err := json.Marshal(pkt)
	if err != nil {
		return err
	}
	if len(data) > maxUDPPacketSize {
		return ErrPacketTooLarge
	}

	c.server.connMu.RLock()
	raw := c.server.rawConn
	c.server.connMu.RUnlock()
	if raw == nil {
		return ErrConnClosed
	}

	_, err = raw.WriteToUDP(data, c.addr)
	return err
}

// Close terminates this virtual session.
func (c *UDPConn) Close() error {
	if c.closed.CompareAndSwap(false, true) {
		c.server.removeConn(c.addr.String())
	}
	return nil
}

// PlayerID returns the authenticated player ID, or 0.
func (c *UDPConn) PlayerID() int64 {
	return c.playerID.Load()
}

// SetPlayerID binds this UDP session to an authenticated player ID.
func (c *UDPConn) SetPlayerID(id int64, username string) {
	c.playerID.Store(id)
	c.userMu.Lock()
	defer c.userMu.Unlock()
	c.username = username
}

// Username returns the authenticated username.
func (c *UDPConn) Username() string {
	c.userMu.RLock()
	defer c.userMu.RUnlock()
	return c.username
}

func (c *UDPConn) touch() {
	c.lastSeenMu.Lock()
	defer c.lastSeenMu.Unlock()
	c.lastSeen = time.Now()
}

func (c *UDPConn) getLastSeen() time.Time {
	c.lastSeenMu.RLock()
	defer c.lastSeenMu.RUnlock()
	return c.lastSeen
}

var _ network.Connection = (*UDPConn)(nil)
