// Package ws implements a WebSocket transport for the Bloomsom game server engine.
package ws

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adnannpm/Bloomsom/internal/network"
	"github.com/gorilla/websocket"
)

const (
	defaultWriteWait      = 10 * time.Second
	defaultPongWait       = 60 * time.Second
	defaultPingPeriod     = 54 * time.Second
	defaultMaxMessageSize = 64 * 1024 // 64KB
	defaultSendBufferSize = 256
)

var (
	// ErrQueueFull is returned when a packet cannot be queued for sending because the buffer is full.
	ErrQueueFull = errors.New("ws: send queue full")

	// ErrConnClosed is returned when an operation is attempted on a closed connection.
	ErrConnClosed = errors.New("ws: connection closed")
)

var _ network.Connection = (*WSConn)(nil)

// WSConn represents an active client WebSocket connection implementing network.Connection.
type WSConn struct {
	id         string
	remoteAddr string
	ws         *websocket.Conn
	send       chan network.Packet
	closeChan  chan struct{}

	playerMu sync.RWMutex
	playerID int64
	username string

	sendMu    sync.RWMutex
	closed    atomic.Bool
	closeOnce sync.Once
	closeErr  error

	writeWait      time.Duration
	pongWait       time.Duration
	pingPeriod     time.Duration
	maxMessageSize int64
}

// newConnectionID generates a unique random 128-bit hex identifier.
func newConnectionID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// NewWSConn wraps an existing gorilla websocket connection into a WSConn.
func NewWSConn(ws *websocket.Conn) *WSConn {
	var remoteAddr string
	if ws != nil && ws.RemoteAddr() != nil {
		remoteAddr = ws.RemoteAddr().String()
	}

	return &WSConn{
		id:             newConnectionID(),
		remoteAddr:     remoteAddr,
		ws:             ws,
		send:           make(chan network.Packet, defaultSendBufferSize),
		closeChan:      make(chan struct{}),
		writeWait:      defaultWriteWait,
		pongWait:       defaultPongWait,
		pingPeriod:     defaultPingPeriod,
		maxMessageSize: defaultMaxMessageSize,
	}
}

// ID returns the unique connection identifier.
func (c *WSConn) ID() string {
	return c.id
}

// RemoteAddr returns the client's network address string.
func (c *WSConn) RemoteAddr() string {
	return c.remoteAddr
}

// PlayerID returns the authenticated player ID, or 0 if unauthenticated.
func (c *WSConn) PlayerID() int64 {
	c.playerMu.RLock()
	defer c.playerMu.RUnlock()
	return c.playerID
}

// Username returns the authenticated username, or empty if unauthenticated.
func (c *WSConn) Username() string {
	c.playerMu.RLock()
	defer c.playerMu.RUnlock()
	return c.username
}

// SetPlayerID binds this connection to an authenticated player ID and username.
func (c *WSConn) SetPlayerID(id int64, username string) {
	c.playerMu.Lock()
	defer c.playerMu.Unlock()
	c.playerID = id
	c.username = username
}

// Send queues a packet to be transmitted to the client via non-blocking send.
// Returns ErrQueueFull if the queue is full, or ErrConnClosed if closed.
func (c *WSConn) Send(pkt network.Packet) error {
	c.sendMu.RLock()
	defer c.sendMu.RUnlock()

	if c.closed.Load() {
		return ErrConnClosed
	}

	select {
	case c.send <- pkt:
		return nil
	default:
		return ErrQueueFull
	}
}

// Close terminates the connection, closes the send channel safely once, and closes the websocket.
func (c *WSConn) Close() error {
	c.closeOnce.Do(func() {
		c.sendMu.Lock()
		c.closed.Store(true)
		close(c.send)
		close(c.closeChan)
		c.sendMu.Unlock()

		if c.ws != nil {
			c.closeErr = c.ws.Close()
		}
	})
	return c.closeErr
}

// CloseNotify returns a receive-only channel that is closed when the connection closes.
func (c *WSConn) CloseNotify() <-chan struct{} {
	return c.closeChan
}

// readPump pumps messages from the websocket connection to the MessageHandler.
// Ensures read limit, read deadlines with pong handler, and triggers disconnect on error or close.
func (c *WSConn) readPump(onMsg network.MessageHandler, onDisconnect network.DisconnectHandler) {
	defer func() {
		_ = c.Close()
		if onDisconnect != nil {
			onDisconnect(c)
		}
	}()

	maxMsg := c.maxMessageSize
	if maxMsg <= 0 {
		maxMsg = defaultMaxMessageSize
	}
	pWait := c.pongWait
	if pWait <= 0 {
		pWait = defaultPongWait
	}

	c.ws.SetReadLimit(maxMsg)
	_ = c.ws.SetReadDeadline(time.Now().Add(pWait))
	c.ws.SetPongHandler(func(string) error {
		_ = c.ws.SetReadDeadline(time.Now().Add(pWait))
		return nil
	})

	for {
		var pkt network.Packet
		err := c.ws.ReadJSON(&pkt)
		if err != nil {
			break
		}
		if onMsg != nil {
			onMsg(c, pkt)
		}
	}
}

// writePump pumps messages from the send channel to the websocket connection.
// Sends ping frames periodically and writes CloseMessage upon closing.
func (c *WSConn) writePump() {
	pPeriod := c.pingPeriod
	if pPeriod <= 0 {
		pPeriod = defaultPingPeriod
	}
	wWait := c.writeWait
	if wWait <= 0 {
		wWait = defaultWriteWait
	}

	ticker := time.NewTicker(pPeriod)
	defer func() {
		ticker.Stop()
		_ = c.Close()
	}()

	for {
		select {
		case pkt, ok := <-c.send:
			_ = c.ws.SetWriteDeadline(time.Now().Add(wWait))
			if !ok {
				// The send channel was closed.
				_ = c.ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
				return
			}

			if err := c.ws.WriteJSON(pkt); err != nil {
				return
			}

		case <-ticker.C:
			_ = c.ws.SetWriteDeadline(time.Now().Add(wWait))
			if err := c.ws.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}

		case <-c.closeChan:
			_ = c.ws.SetWriteDeadline(time.Now().Add(wWait))
			_ = c.ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			return
		}
	}
}

// ReadPump is an exported wrapper for readPump.
func (c *WSConn) ReadPump(onMsg network.MessageHandler, onDisconnect network.DisconnectHandler) {
	c.readPump(onMsg, onDisconnect)
}

// WritePump is an exported wrapper for writePump.
func (c *WSConn) WritePump() {
	c.writePump()
}
