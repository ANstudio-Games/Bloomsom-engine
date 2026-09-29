package engine

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"
)

// RoomInfo provides a read-only metadata snapshot of an active room.
type RoomInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Mode        string `json:"mode"`
	LoopType    string `json:"loop_type"`
	PlayerCount int    `json:"player_count"`
	MaxPlayers  int    `json:"max_players"`
	Status      string `json:"status"`
}

// RoomManager coordinates the creation, lookup, and lifecycle of rooms on the server.
type RoomManager struct {
	mu     sync.RWMutex
	rooms  map[string]*Room
	logger *slog.Logger
}

// NewRoomManager initializes a room manager.
func NewRoomManager(log *slog.Logger) *RoomManager {
	return &RoomManager{
		rooms:  make(map[string]*Room),
		logger: log,
	}
}

// CreateRoom instantiates a new room using the named game mode.
func (m *RoomManager) CreateRoom(id, name, modeName string, maxPlayers int, loopType string, tickRate int) (*Room, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.rooms[id]; exists {
		return nil, fmt.Errorf("room %q already exists", id)
	}

	mode, err := GetMode(modeName)
	if err != nil {
		return nil, err
	}

	room, err := NewRoom(RoomOptions{
		ID:         id,
		Name:       name,
		Mode:       mode,
		MaxPlayers: maxPlayers,
		LoopType:   loopType,
		TickRate:   tickRate,
		Logger:     m.logger,
	})
	if err != nil {
		return nil, err
	}

	m.rooms[id] = room
	if m.logger != nil {
		m.logger.Info("room created", "id", id, "mode", modeName, "loop", loopType)
	}
	return room, nil
}

// GetRoom retrieves an active room by ID.
func (m *RoomManager) GetRoom(id string) (*Room, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := m.rooms[id]
	return r, ok
}

// ListRooms returns a snapshot list of all active rooms.
func (m *RoomManager) ListRooms() []RoomInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	list := make([]RoomInfo, 0, len(m.rooms))
	for _, r := range m.rooms {
		list = append(list, RoomInfo{
			ID:          r.ID(),
			Name:        r.Name(),
			Mode:        r.mode.Name(),
			LoopType:    r.loopType,
			PlayerCount: r.PlayerCount(),
			MaxPlayers:  r.maxPlayers,
			Status:      r.Status(),
		})
	}
	return list
}

// CloseRoom closes and removes a room by ID.
func (m *RoomManager) CloseRoom(id string) error {
	m.mu.Lock()
	r, exists := m.rooms[id]
	if !exists {
		m.mu.Unlock()
		return errors.New("room not found: " + id)
	}
	delete(m.rooms, id)
	m.mu.Unlock()

	if m.logger != nil {
		m.logger.Info("room closed", "id", id)
	}
	return r.Close()
}

// CloseAll terminates all managed rooms.
func (m *RoomManager) CloseAll() {
	m.mu.Lock()
	rooms := make([]*Room, 0, len(m.rooms))
	for _, r := range m.rooms {
		rooms = append(rooms, r)
	}
	m.rooms = make(map[string]*Room)
	m.mu.Unlock()

	for _, r := range rooms {
		_ = r.Close()
	}
}

// TotalPlayers returns the sum of all players across all active rooms.
func (m *RoomManager) TotalPlayers() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	total := 0
	for _, r := range m.rooms {
		total += r.PlayerCount()
	}
	return total
}

// RoomCount returns the current count of active rooms.
func (m *RoomManager) RoomCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.rooms)
}
