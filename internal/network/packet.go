// Package network defines the packet formats and transport abstractions
// for the Bloomsom game server engine.
package network

import (
	"context"
	"encoding/json"
)

// PacketType constants for standard server & client messages.
const (
	TypeRegister = "register" // Client -> Server registration
	TypeLogin    = "login"    // Client -> Server login
	TypeAuth     = "auth"     // Client -> Server session token handshake
	TypeJoin     = "join"     // Client -> Server join room
	TypeLeave    = "leave"    // Client -> Server leave room
	TypeChat     = "chat"     // Chat message
	TypeInput    = "input"    // Client gameplay input
	TypeState    = "state"    // Server state snapshot
	TypeAck      = "ack"      // Server input acknowledgment
	TypeError    = "error"    // Server error message
	TypePing     = "ping"     // Keepalive ping
	TypePong     = "pong"     // Keepalive pong
)

// Packet represents the standard wire message envelope across WebSocket and UDP.
type Packet struct {
	Type    string          `json:"type"`              // Message type
	Seq     uint32          `json:"seq,omitempty"`     // Client input sequence number
	Ack     uint32          `json:"ack,omitempty"`     // Server last processed input sequence
	Tick    uint64          `json:"tick,omitempty"`    // Server simulation tick
	Payload json.RawMessage `json:"payload,omitempty"` // Message payload bytes
}

// Connection represents an active client connection (WebSocket or UDP virtual session).
type Connection interface {
	// ID returns a unique connection identifier.
	ID() string
	// RemoteAddr returns the client's network address string.
	RemoteAddr() string
	// Send queues a packet to be transmitted to the client. Returns error if queue is full or closed.
	Send(pkt Packet) error
	// Close terminates the connection.
	Close() error
	// PlayerID returns the authenticated player ID, or 0 if unauthenticated.
	PlayerID() int64
	// SetPlayerID binds this connection to an authenticated player ID.
	SetPlayerID(id int64, username string)
	// Username returns the authenticated username, or empty if unauthenticated.
	Username() string
}

// MessageHandler is the callback invoked when a packet is received from a connection.
type MessageHandler func(conn Connection, pkt Packet)

// DisconnectHandler is the callback invoked when a connection disconnects.
type DisconnectHandler func(conn Connection)

// Transport defines the lifecycle of a network server (WebSocket, UDP).
type Transport interface {
	// Start begins listening and serving connections. It blocks or runs until ctx cancelled.
	Start(ctx context.Context) error
	// Stop gracefully shuts down the transport listener and closes active connections.
	Stop() error
	// Addr returns the bound network address.
	Addr() string
}
