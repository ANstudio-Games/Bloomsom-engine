// Package realtime implements a 2D real-time multiplayer arena game mode with 60 TPS tick loop.
package realtime

import (
	"encoding/json"
	"math"
	"math/rand"
	"time"

	"github.com/adnannpm/Bloomsom/engine"
	"github.com/adnannpm/Bloomsom/internal/network"
)

// ModeName is the identifier for the realtime-action arena preset.
const ModeName = "realtime-action"

func init() {
	engine.RegisterMode(&Mode{})
}

// PlayerEntity represents the spatial simulation state of a player.
type PlayerEntity struct {
	ID      int64   `json:"id"`
	X       float64 `json:"x"`
	Y       float64 `json:"y"`
	Vx      float64 `json:"vx"`
	Vy      float64 `json:"vy"`
	LastSeq uint32  `json:"last_seq"`
}

// Mode implements engine.GameMode for a real-time 60 TPS 2D movement arena.
type Mode struct {
	entities map[int64]*PlayerEntity
	width    float64
	height   float64
	speed    float64
}

// Name returns "realtime-action".
func (m *Mode) Name() string { return ModeName }

// OnRoomCreate initializes arena boundaries.
func (m *Mode) OnRoomCreate(r *engine.Room) error {
	m.entities = make(map[int64]*PlayerEntity)
	m.width = 800.0
	m.height = 600.0
	m.speed = 200.0 // pixels per second
	return nil
}

// OnJoin spawns the player at a random position.
func (m *Mode) OnJoin(r *engine.Room, p *engine.Player) error {
	m.entities[p.ID] = &PlayerEntity{
		ID:      p.ID,
		X:       100.0 + rand.Float64()*(m.width-200.0),
		Y:       100.0 + rand.Float64()*(m.height-200.0),
		Vx:      0,
		Vy:      0,
		LastSeq: 0,
	}
	return nil
}

// OnLeave removes the player entity.
func (m *Mode) OnLeave(r *engine.Room, p *engine.Player) {
	delete(m.entities, p.ID)
}

// MoveInput represents client movement intent.
type MoveInput struct {
	DX float64 `json:"dx"` // -1.0 to 1.0
	DY float64 `json:"dy"` // -1.0 to 1.0
}

// OnInput updates player velocity based on client input and tracks client sequence number.
func (m *Mode) OnInput(r *engine.Room, p *engine.Player, in engine.Input) {
	if in.Type != "move" && in.Type != "input" {
		return
	}

	entity, exists := m.entities[p.ID]
	if !exists {
		return
	}

	var move MoveInput
	if err := json.Unmarshal(in.Payload, &move); err != nil {
		return
	}

	// Normalize movement direction
	len := math.Hypot(move.DX, move.DY)
	if len > 0 {
		entity.Vx = (move.DX / len) * m.speed
		entity.Vy = (move.DY / len) * m.speed
	} else {
		entity.Vx = 0
		entity.Vy = 0
	}

	entity.LastSeq = in.Seq

	// Send immediate ACK packet back to the player for reconciliation
	_ = p.Send(network.Packet{
		Type: network.TypeAck,
		Seq:  in.Seq,
		Tick: r.TickCount(),
	})
}

// OnTick advances physics and broadcasts authoritative state snapshots.
func (m *Mode) OnTick(r *engine.Room, dt time.Duration) {
	sec := dt.Seconds()

	for _, e := range m.entities {
		e.X += e.Vx * sec
		e.Y += e.Vy * sec

		// Boundary clamp
		if e.X < 0 {
			e.X = 0
		} else if e.X > m.width {
			e.X = m.width
		}
		if e.Y < 0 {
			e.Y = 0
		} else if e.Y > m.height {
			e.Y = m.height
		}
	}

	// Broadcast world snapshot every tick
	snapshotList := make([]*PlayerEntity, 0, len(m.entities))
	for _, e := range m.entities {
		snapshotList = append(snapshotList, e)
	}

	snapshotPayload, err := json.Marshal(map[string]any{
		"tick":     r.TickCount(),
		"entities": snapshotList,
	})
	if err != nil {
		return
	}

	r.Broadcast(network.Packet{
		Type:    network.TypeState,
		Tick:    r.TickCount(),
		Payload: snapshotPayload,
	})
}

var _ engine.GameMode = (*Mode)(nil)
