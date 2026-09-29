package engine

import (
	"sync"

	"github.com/adnannpm/Bloomsom/internal/network"
)

// Player represents an active, authenticated player in the game server.
type Player struct {
	ID          int64
	Username    string
	DisplayName string

	conn network.Connection

	mu         sync.RWMutex
	roomID     string
	customData map[string]any
}

// NewPlayer constructs a Player bound to an authenticated network connection.
func NewPlayer(id int64, username, displayName string, conn network.Connection) *Player {
	return &Player{
		ID:          id,
		Username:    username,
		DisplayName: displayName,
		conn:        conn,
		customData:  make(map[string]any),
	}
}

// Send transmits a network packet to this player.
func (p *Player) Send(pkt network.Packet) error {
	if p.conn == nil {
		return nil
	}
	return p.conn.Send(pkt)
}

// RoomID returns the ID of the room the player is currently inside, or empty.
func (p *Player) RoomID() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.roomID
}

// SetRoomID updates the current room ID for this player.
func (p *Player) SetRoomID(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.roomID = id
}

// SetData stores custom runtime state on the player (e.g. game piece, score).
func (p *Player) SetData(key string, val any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.customData[key] = val
}

// GetData retrieves custom runtime state on the player.
func (p *Player) GetData(key string) (any, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	val, ok := p.customData[key]
	return val, ok
}
