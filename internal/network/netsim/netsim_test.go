package netsim_test

import (
	"sync"
	"testing"
	"time"

	"github.com/adnannpm/Bloomsom/internal/config"
	"github.com/adnannpm/Bloomsom/internal/network"
	"github.com/adnannpm/Bloomsom/internal/network/netsim"
)

type dummyConn struct {
	mu   sync.Mutex
	sent []network.Packet
}

func (d *dummyConn) ID() string         { return "dummy" }
func (d *dummyConn) RemoteAddr() string { return "127.0.0.1:1234" }
func (d *dummyConn) Send(pkt network.Packet) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.sent = append(d.sent, pkt)
	return nil
}
func (d *dummyConn) Close() error                          { return nil }
func (d *dummyConn) PlayerID() int64                       { return 1 }
func (d *dummyConn) SetPlayerID(id int64, username string) {}
func (d *dummyConn) Username() string                      { return "dummy" }
func (d *dummyConn) SentCount() int                        { d.mu.Lock(); defer d.mu.Unlock(); return len(d.sent) }

func TestNetSimZero(t *testing.T) {
	sim := netsim.New(config.NetsimConfig{})
	conn := &dummyConn{}

	sim.Transmit(conn, network.Packet{Type: "ping"})
	if conn.SentCount() != 1 {
		t.Fatalf("expected immediate send when netsim is disabled")
	}
}

func TestNetSimLatency(t *testing.T) {
	sim := netsim.New(config.NetsimConfig{
		Latency: 50 * time.Millisecond,
		Jitter:  0,
	})
	conn := &dummyConn{}

	start := time.Now()
	sim.Transmit(conn, network.Packet{Type: "test"})

	if conn.SentCount() != 0 {
		t.Errorf("expected packet to be delayed, but was sent immediately")
	}

	time.Sleep(80 * time.Millisecond)

	if conn.SentCount() != 1 {
		t.Errorf("expected packet to arrive after latency delay")
	}
	if time.Since(start) < 45*time.Millisecond {
		t.Errorf("expected elapsed time to be at least latency duration")
	}
}

func TestNetSimLoss(t *testing.T) {
	sim := netsim.New(config.NetsimConfig{
		Loss: 1.0, // 100% loss
	})
	conn := &dummyConn{}

	for i := 0; i < 10; i++ {
		sim.Transmit(conn, network.Packet{Type: "ping"})
	}

	if conn.SentCount() != 0 {
		t.Errorf("expected 100%% drop rate, but %d packets arrived", conn.SentCount())
	}
}
