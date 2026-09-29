// Package main demonstrates how to import Bloomsom as a library and register a custom GameMode.
package main

import (
	"encoding/json"
	"time"

	"github.com/adnannpm/Bloomsom/cmd"
	"github.com/adnannpm/Bloomsom/engine"
	"github.com/adnannpm/Bloomsom/internal/network"
)

// SimpleGameMode is a minimal custom game mode example.
type SimpleGameMode struct{}

func (m *SimpleGameMode) Name() string {
	return "custom-game"
}

func (m *SimpleGameMode) OnRoomCreate(r *engine.Room) error {
	return nil
}

func (m *SimpleGameMode) OnJoin(r *engine.Room, p *engine.Player) error {
	msg, _ := json.Marshal(map[string]string{"message": "Welcome to Custom Game!"})
	_ = p.Send(network.Packet{
		Type:    "welcome",
		Payload: msg,
	})
	return nil
}

func (m *SimpleGameMode) OnLeave(r *engine.Room, p *engine.Player) {}

func (m *SimpleGameMode) OnInput(r *engine.Room, p *engine.Player, in engine.Input) {
	// Echo back action
	r.Broadcast(network.Packet{
		Type:    "action_echo",
		Payload: in.Payload,
	})
}

func (m *SimpleGameMode) OnTick(r *engine.Room, dt time.Duration) {}

func main() {
	// Register custom game mode
	engine.RegisterMode(&SimpleGameMode{})

	// Execute Bloomsom CLI
	cmd.Execute()
}
