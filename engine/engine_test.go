package engine_test

import (
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adnannpm/Bloomsom/engine"
	"github.com/adnannpm/Bloomsom/internal/network"
)

type mockConn struct {
	id       string
	playerID int64
	username string
	sent     []network.Packet
	mu       sync.Mutex
}

func (m *mockConn) ID() string         { return m.id }
func (m *mockConn) RemoteAddr() string { return "127.0.0.1:9999" }
func (m *mockConn) Send(pkt network.Packet) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, pkt)
	return nil
}
func (m *mockConn) Close() error                   { return nil }
func (m *mockConn) PlayerID() int64                { return m.playerID }
func (m *mockConn) SetPlayerID(id int64, u string) { m.playerID = id; m.username = u }
func (m *mockConn) Username() string               { return m.username }
func (m *mockConn) SentPackets() []network.Packet {
	m.mu.Lock()
	defer m.mu.Unlock()
	copied := make([]network.Packet, len(m.sent))
	copy(copied, m.sent)
	return copied
}

type testMode struct {
	name       string
	inputsRcvd atomic.Int32
	ticksRcvd  atomic.Int32
}

func (t *testMode) Name() string                                  { return t.name }
func (t *testMode) OnRoomCreate(r *engine.Room) error             { return nil }
func (t *testMode) OnJoin(r *engine.Room, p *engine.Player) error { return nil }
func (t *testMode) OnLeave(r *engine.Room, p *engine.Player)      {}
func (t *testMode) OnInput(r *engine.Room, p *engine.Player, in engine.Input) {
	t.inputsRcvd.Add(1)
	r.Broadcast(network.Packet{
		Type:    "echo",
		Payload: in.Payload,
	})
}
func (t *testMode) OnTick(r *engine.Room, dt time.Duration) {
	t.ticksRcvd.Add(1)
}

func TestEngineEventRoom(t *testing.T) {
	mode := &testMode{name: "test-event"}
	engine.RegisterMode(mode)

	mgr := engine.NewRoomManager(nil)
	room, err := mgr.CreateRoom("room-1", "Room 1", "test-event", 4, engine.LoopTypeEvent, 0)
	if err != nil {
		t.Fatalf("failed to create room: %v", err)
	}
	defer mgr.CloseAll()

	conn1 := &mockConn{id: "c1", playerID: 1, username: "alice"}
	conn2 := &mockConn{id: "c2", playerID: 2, username: "bob"}
	p1 := engine.NewPlayer(1, "alice", "Alice", conn1)
	p2 := engine.NewPlayer(2, "bob", "Bob", conn2)

	if err := room.Join(p1); err != nil {
		t.Fatalf("p1 join failed: %v", err)
	}
	if err := room.Join(p2); err != nil {
		t.Fatalf("p2 join failed: %v", err)
	}

	if room.PlayerCount() != 2 {
		t.Fatalf("expected 2 players, got %d", room.PlayerCount())
	}

	// Dispatch input
	payload, _ := json.Marshal(map[string]string{"msg": "hello"})
	room.DispatchInput(p1, engine.Input{
		Type:    "chat",
		Payload: payload,
	})

	time.Sleep(50 * time.Millisecond)

	if mode.inputsRcvd.Load() != 1 {
		t.Errorf("expected 1 input received, got %d", mode.inputsRcvd.Load())
	}

	// Check broadcast received by both
	if len(conn1.SentPackets()) != 1 || len(conn2.SentPackets()) != 1 {
		t.Errorf("expected both players to receive broadcast packet")
	}

	room.Leave(p1)
	if room.PlayerCount() != 1 {
		t.Errorf("expected 1 player remaining, got %d", room.PlayerCount())
	}
}

func TestEngineTickRoom(t *testing.T) {
	mode := &testMode{name: "test-tick"}
	engine.RegisterMode(mode)

	room, err := engine.NewRoom(engine.RoomOptions{
		ID:         "room-tick",
		Name:       "Tick Room",
		Mode:       mode,
		LoopType:   engine.LoopTypeTick,
		TickRate:   30,
		MaxPlayers: 4,
	})
	if err != nil {
		t.Fatalf("failed to create tick room: %v", err)
	}
	defer room.Close()

	time.Sleep(120 * time.Millisecond)

	ticks := mode.ticksRcvd.Load()
	if ticks < 2 {
		t.Errorf("expected at least 2 ticks at 30 TPS in 120ms, got %d", ticks)
	}
}
