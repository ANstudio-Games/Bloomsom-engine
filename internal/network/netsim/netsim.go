// Package netsim provides simulated network conditions (latency, jitter, packet loss)
// to test client-side prediction, reconciliation, and lag compensation on localhost.
package netsim

import (
	"math/rand"
	"sync"
	"time"

	"github.com/adnannpm/Bloomsom/internal/config"
	"github.com/adnannpm/Bloomsom/internal/network"
)

// Simulator simulates network impairment conditions based on configuration.
type Simulator struct {
	mu      sync.RWMutex
	latency time.Duration
	jitter  time.Duration
	loss    float64 // 0.0 to 1.0
	rng     *rand.Rand
}

// New creates a new network simulator instance.
func New(cfg config.NetsimConfig) *Simulator {
	return &Simulator{
		latency: cfg.Latency,
		jitter:  cfg.Jitter,
		loss:    cfg.Loss,
		rng:     rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// ShouldDrop returns true if the packet should be dropped due to simulated packet loss.
func (s *Simulator) ShouldDrop() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loss <= 0 {
		return false
	}
	return s.rng.Float64() < s.loss
}

// Delay calculates the simulated delivery delay with jitter applied.
func (s *Simulator) Delay() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.latency <= 0 {
		return 0
	}

	delay := s.latency
	if s.jitter > 0 {
		// jitter offset between -jitter and +jitter
		offset := time.Duration(s.rng.Int63n(int64(2*s.jitter))) - s.jitter
		delay += offset
		if delay < 0 {
			delay = 0
		}
	}
	return delay
}

// Transmit sends a packet through the simulated link to the destination connection.
// If dropped by simulated loss, the packet is silently discarded.
// If latency is configured, delivery is asynchronously delayed.
func (s *Simulator) Transmit(dst network.Connection, pkt network.Packet) {
	if s.ShouldDrop() {
		return // Dropped by simulation
	}

	delay := s.Delay()
	if delay <= 0 {
		_ = dst.Send(pkt)
		return
	}

	time.AfterFunc(delay, func() {
		_ = dst.Send(pkt)
	})
}
