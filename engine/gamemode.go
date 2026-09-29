// Package engine provides the public game engine APIs for Bloomsom Engine.
// Developers implement GameMode to define authoritative game rules.
package engine

import (
	"errors"
	"sync"
	"time"
)

// GameMode is the core contract for game server logic.
// All methods except OnRoomCreate are executed on the Room's single actor goroutine,
// ensuring thread-safety without external locks.
type GameMode interface {
	// Name returns the identifier for this game mode.
	Name() string
	// OnRoomCreate is called when a room is created with this mode.
	OnRoomCreate(r *Room) error
	// OnJoin is called when a player attempts to enter the room. Returning an error rejects the join.
	OnJoin(r *Room, p *Player) error
	// OnLeave is called after a player leaves the room.
	OnLeave(r *Room, p *Player)
	// OnInput processes a gameplay action from a player.
	OnInput(r *Room, p *Player, in Input)
	// OnTick is invoked on each simulation frame in tick-based loop mode.
	OnTick(r *Room, dt time.Duration)
}

var (
	modesMu sync.RWMutex
	modes   = make(map[string]GameMode)
)

// RegisterMode registers a game mode globally so it can be instantiated by name in rooms.
func RegisterMode(mode GameMode) {
	if mode == nil {
		panic("engine: cannot register nil GameMode")
	}
	modesMu.Lock()
	defer modesMu.Unlock()
	modes[mode.Name()] = mode
}

// GetMode retrieves a registered game mode by name.
func GetMode(name string) (GameMode, error) {
	modesMu.RLock()
	defer modesMu.RUnlock()
	m, ok := modes[name]
	if !ok {
		return nil, errors.New("game mode not found: " + name)
	}
	return m, nil
}

// ListModes returns a slice of all registered game mode names.
func ListModes() []string {
	modesMu.RLock()
	defer modesMu.RUnlock()
	names := make([]string, 0, len(modes))
	for name := range modes {
		names = append(names, name)
	}
	return names
}
