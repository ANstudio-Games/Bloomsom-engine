// Package lobbychat implements the built-in lobby-chat game mode.
package lobbychat

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/adnannpm/Bloomsom/engine"
	"github.com/adnannpm/Bloomsom/internal/network"
)

// ModeName is the identifier for the lobby-chat game mode.
const ModeName = "lobby-chat"

func init() {
	engine.RegisterMode(&Mode{})
}

// Mode implements engine.GameMode for a lobby chat room.
type Mode struct{}

// Name returns "lobby-chat".
func (m *Mode) Name() string { return ModeName }

// OnRoomCreate initializes room settings.
func (m *Mode) OnRoomCreate(r *engine.Room) error {
	return nil
}

// OnJoin accepts the player and notifies the room.
func (m *Mode) OnJoin(r *engine.Room, p *engine.Player) error {
	systemMsg, _ := json.Marshal(map[string]any{
		"system":    true,
		"player_id": p.ID,
		"username":  p.Username,
		"message":   fmt.Sprintf("%s joined the chat", p.Username),
		"time":      time.Now().UTC().Format(time.RFC3339),
	})
	r.Broadcast(network.Packet{
		Type:    network.TypeChat,
		Payload: systemMsg,
	})
	return nil
}

// OnLeave notifies the room when a player departs.
func (m *Mode) OnLeave(r *engine.Room, p *engine.Player) {
	systemMsg, _ := json.Marshal(map[string]any{
		"system":    true,
		"player_id": p.ID,
		"username":  p.Username,
		"message":   fmt.Sprintf("%s left the chat", p.Username),
		"time":      time.Now().UTC().Format(time.RFC3339),
	})
	r.Broadcast(network.Packet{
		Type:    network.TypeChat,
		Payload: systemMsg,
	})
}

// ChatPayload represents the incoming or outgoing chat message structure.
type ChatPayload struct {
	Message string `json:"message"`
}

// OnInput processes chat messages sent by players.
func (m *Mode) OnInput(r *engine.Room, p *engine.Player, in engine.Input) {
	if in.Type != network.TypeChat {
		return
	}

	var cp ChatPayload
	if err := json.Unmarshal(in.Payload, &cp); err != nil {
		_ = p.Send(network.Packet{
			Type:    network.TypeError,
			Payload: []byte(`{"error":"invalid chat payload"}`),
		})
		return
	}

	if len(cp.Message) == 0 || len(cp.Message) > 2000 {
		_ = p.Send(network.Packet{
			Type:    network.TypeError,
			Payload: []byte(`{"error":"message must be between 1 and 2000 characters"}`),
		})
		return
	}

	outPayload, _ := json.Marshal(map[string]any{
		"player_id": p.ID,
		"username":  p.Username,
		"message":   cp.Message,
		"time":      time.Now().UTC().Format(time.RFC3339),
	})

	r.Broadcast(network.Packet{
		Type:    network.TypeChat,
		Payload: outPayload,
	})
}

// OnTick is a no-op for event-driven chat rooms.
func (m *Mode) OnTick(r *engine.Room, dt time.Duration) {}

var _ engine.GameMode = (*Mode)(nil)

// Verify interface compliance
var _ = errors.New
