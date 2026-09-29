package presets_test

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/adnannpm/Bloomsom/engine"
	"github.com/adnannpm/Bloomsom/internal/network"
	"github.com/adnannpm/Bloomsom/presets/lobbychat"
	"github.com/adnannpm/Bloomsom/presets/realtime"
	"github.com/adnannpm/Bloomsom/presets/turnbased"
)

type mockPlayerConn struct {
	id       string
	playerID int64
	username string
	sent     []network.Packet
	mu       sync.Mutex
}

func (m *mockPlayerConn) ID() string         { return m.id }
func (m *mockPlayerConn) RemoteAddr() string { return "127.0.0.1:5555" }
func (m *mockPlayerConn) Send(pkt network.Packet) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, pkt)
	return nil
}
func (m *mockPlayerConn) Close() error                   { return nil }
func (m *mockPlayerConn) PlayerID() int64                { return m.playerID }
func (m *mockPlayerConn) SetPlayerID(id int64, u string) { m.playerID = id; m.username = u }
func (m *mockPlayerConn) Username() string               { return m.username }
func (m *mockPlayerConn) Sent() []network.Packet {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := make([]network.Packet, len(m.sent))
	copy(c, m.sent)
	return c
}

func TestLobbyChatPreset(t *testing.T) {
	mode, err := engine.GetMode(lobbychat.ModeName)
	if err != nil {
		t.Fatalf("lobbychat mode not found: %v", err)
	}

	room, err := engine.NewRoom(engine.RoomOptions{
		ID:       "chat-1",
		Mode:     mode,
		LoopType: engine.LoopTypeEvent,
	})
	if err != nil {
		t.Fatalf("failed to create room: %v", err)
	}
	defer room.Close()

	c1 := &mockPlayerConn{id: "c1", playerID: 1, username: "alice"}
	c2 := &mockPlayerConn{id: "c2", playerID: 2, username: "bob"}
	p1 := engine.NewPlayer(1, "alice", "Alice", c1)
	p2 := engine.NewPlayer(2, "bob", "Bob", c2)

	_ = room.Join(p1)
	_ = room.Join(p2)

	// alice sends a chat message
	room.DispatchInput(p1, engine.Input{
		Type:    network.TypeChat,
		Payload: []byte(`{"message":"hello world!"}`),
	})

	time.Sleep(50 * time.Millisecond)

	var foundChat bool
	for _, pkt := range c2.Sent() {
		if pkt.Type == network.TypeChat {
			var m map[string]any
			if err := json.Unmarshal(pkt.Payload, &m); err == nil && m["message"] == "hello world!" {
				foundChat = true
				break
			}
		}
	}
	if !foundChat {
		t.Errorf("expected bob to receive alice's chat message")
	}
}

func TestTurnBasedPreset(t *testing.T) {
	mode, err := engine.GetMode(turnbased.ModeName)
	if err != nil {
		t.Fatalf("turnbased mode not found: %v", err)
	}

	room, err := engine.NewRoom(engine.RoomOptions{
		ID:       "tictactoe-1",
		Mode:     mode,
		LoopType: engine.LoopTypeEvent,
	})
	if err != nil {
		t.Fatalf("failed to create room: %v", err)
	}
	defer room.Close()

	c1 := &mockPlayerConn{id: "c1", playerID: 1, username: "p1"}
	c2 := &mockPlayerConn{id: "c2", playerID: 2, username: "p2"}
	p1 := engine.NewPlayer(1, "p1", "P1", c1)
	p2 := engine.NewPlayer(2, "p2", "P2", c2)

	_ = room.Join(p1)
	_ = room.Join(p2)

	// P1 moves at (0, 0)
	room.DispatchInput(p1, engine.Input{
		Type:    "move",
		Payload: []byte(`{"row":0,"col":0}`),
	})

	time.Sleep(50 * time.Millisecond)

	// Check state sent to P2
	var lastState map[string]any
	for _, pkt := range c2.Sent() {
		if pkt.Type == network.TypeState {
			_ = json.Unmarshal(pkt.Payload, &lastState)
		}
	}
	if lastState == nil {
		t.Fatalf("expected state broadcast to player 2")
	}
	if lastState["current_turn"] != "O" {
		t.Errorf("expected turn to switch to O, got %v", lastState["current_turn"])
	}
}

func TestRealtimePreset(t *testing.T) {
	mode, err := engine.GetMode(realtime.ModeName)
	if err != nil {
		t.Fatalf("realtime mode not found: %v", err)
	}

	room, err := engine.NewRoom(engine.RoomOptions{
		ID:       "arena-1",
		Mode:     mode,
		LoopType: engine.LoopTypeTick,
		TickRate: 60,
	})
	if err != nil {
		t.Fatalf("failed to create arena room: %v", err)
	}
	defer room.Close()

	c1 := &mockPlayerConn{id: "c1", playerID: 1, username: "hero"}
	p1 := engine.NewPlayer(1, "hero", "Hero", c1)
	_ = room.Join(p1)

	// Send movement input
	room.DispatchInput(p1, engine.Input{
		Seq:     101,
		Type:    "move",
		Payload: []byte(`{"dx":1.0,"dy":0.0}`),
	})

	time.Sleep(80 * time.Millisecond)

	sent := c1.Sent()
	var gotAck bool
	var gotState bool
	for _, pkt := range sent {
		if pkt.Type == network.TypeAck && pkt.Seq == 101 {
			gotAck = true
		}
		if pkt.Type == network.TypeState {
			gotState = true
		}
	}
	if !gotAck {
		t.Errorf("expected player to receive ACK with Seq 101")
	}
	if !gotState {
		t.Errorf("expected player to receive world state tick snapshot")
	}
}
