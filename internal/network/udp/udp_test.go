package udp_test

import (
	"context"
	"encoding/json"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/adnannpm/Bloomsom/internal/network"
	"github.com/adnannpm/Bloomsom/internal/network/udp"
)

func TestUDPServerRoundTrip(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var receivedPkt network.Packet
	var receivedConn network.Connection
	var mu sync.Mutex
	done := make(chan struct{}, 1)

	onMsg := func(conn network.Connection, pkt network.Packet) {
		mu.Lock()
		receivedPkt = pkt
		receivedConn = conn
		mu.Unlock()

		// Echo packet back
		_ = conn.Send(network.Packet{
			Type:    "ack",
			Seq:     pkt.Seq,
			Payload: pkt.Payload,
		})
		select {
		case done <- struct{}{}:
		default:
		}
	}

	server := udp.NewServer("127.0.0.1:0", onMsg, nil, nil)
	serverErrCh := make(chan error, 1)
	go func() {
		serverErrCh <- server.Start(ctx)
	}()

	time.Sleep(50 * time.Millisecond)
	serverAddr := server.Addr()

	// Client sends a packet via UDP
	raddr, err := net.ResolveUDPAddr("udp", serverAddr)
	if err != nil {
		t.Fatalf("resolve udp: %v", err)
	}

	clientConn, err := net.DialUDP("udp", nil, raddr)
	if err != nil {
		t.Fatalf("dial udp: %v", err)
	}
	defer clientConn.Close()

	sendPkt := network.Packet{
		Type:    "input",
		Seq:     42,
		Payload: []byte(`{"action":"jump"}`),
	}
	data, _ := json.Marshal(sendPkt)

	if _, err := clientConn.Write(data); err != nil {
		t.Fatalf("write udp: %v", err)
	}

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatalf("timed out waiting for server to receive packet")
	}

	mu.Lock()
	if receivedPkt.Seq != 42 || receivedPkt.Type != "input" {
		t.Errorf("server received unexpected packet: %+v", receivedPkt)
	}
	if receivedConn == nil {
		t.Errorf("expected connection to be passed to onMsg")
	}
	mu.Unlock()

	// Client receives echo ack
	readBuf := make([]byte, 2048)
	_ = clientConn.SetReadDeadline(time.Now().Add(1 * time.Second))
	n, err := clientConn.Read(readBuf)
	if err != nil {
		t.Fatalf("client read echo failed: %v", err)
	}

	var ackPkt network.Packet
	if err := json.Unmarshal(readBuf[:n], &ackPkt); err != nil {
		t.Fatalf("unmarshal echo failed: %v", err)
	}
	if ackPkt.Type != "ack" || ackPkt.Seq != 42 {
		t.Errorf("unexpected ack packet: %+v", ackPkt)
	}

	_ = server.Stop()
}
