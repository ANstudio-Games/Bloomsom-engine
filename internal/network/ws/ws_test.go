package ws_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adnannpm/Bloomsom/internal/network"
	"github.com/adnannpm/Bloomsom/internal/network/ws"
	"github.com/gorilla/websocket"
)

// TestWSServer_SendReceive verifies dialing, sending a packet from client to server,
// and receiving a packet from server to client.
func TestWSServer_SendReceive(t *testing.T) {
	msgReceived := make(chan network.Packet, 1)
	onMsg := func(conn network.Connection, pkt network.Packet) {
		msgReceived <- pkt
		_ = conn.Send(network.Packet{
			Type: network.TypeAck,
			Ack:  pkt.Seq,
			Tick: 42,
		})
	}

	srv := ws.NewServer("127.0.0.1:0", onMsg, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = srv.Start(ctx)
	}()

	addr := srv.Addr()
	if addr == "" || addr == "127.0.0.1:0" {
		t.Fatalf("expected resolved address, got %q", addr)
	}

	dialer := websocket.DefaultDialer
	clientConn, resp, err := dialer.Dial("ws://"+addr+"/ws", nil)
	if err != nil {
		t.Fatalf("failed to dial websocket: %v", err)
	}
	defer clientConn.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("expected status 101 switching protocols, got %d", resp.StatusCode)
	}

	testPkt := network.Packet{
		Type:    network.TypeChat,
		Seq:     1,
		Payload: json.RawMessage(`"hello world"`),
	}
	if err := clientConn.WriteJSON(testPkt); err != nil {
		t.Fatalf("failed to send packet: %v", err)
	}

	select {
	case pkt := <-msgReceived:
		if pkt.Type != network.TypeChat || pkt.Seq != 1 {
			t.Fatalf("unexpected packet received on server: %+v", pkt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for server onMsg")
	}

	var ackPkt network.Packet
	if err := clientConn.ReadJSON(&ackPkt); err != nil {
		t.Fatalf("failed to read ack from server: %v", err)
	}
	if ackPkt.Type != network.TypeAck || ackPkt.Ack != 1 || ackPkt.Tick != 42 {
		t.Fatalf("unexpected ack packet: %+v", ackPkt)
	}

	if err := srv.Stop(); err != nil {
		t.Fatalf("failed to stop server: %v", err)
	}
}

// TestWSServer_PingPong verifies websocket ping and pong handling in both directions:
// 1. Client pings server -> server replies with pong.
// 2. Server heartbeat sends ping to client -> client receives ping.
func TestWSServer_PingPong(t *testing.T) {
	srv := ws.NewServer("127.0.0.1:0", nil, nil, nil)
	// Fast heartbeat for test execution speed
	srv.SetHeartbeat(50*time.Millisecond, 300*time.Millisecond, 50*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = srv.Start(ctx)
	}()

	addr := srv.Addr()
	clientConn, _, err := websocket.DefaultDialer.Dial("ws://"+addr+"/ws", nil)
	if err != nil {
		t.Fatalf("dial error: %v", err)
	}
	defer clientConn.Close()

	// 1. Set handlers on clientConn before starting reader goroutine
	pongReceived := make(chan string, 1)
	clientConn.SetPongHandler(func(appData string) error {
		select {
		case pongReceived <- appData:
		default:
		}
		return nil
	})

	pingReceived := make(chan struct{}, 1)
	clientConn.SetPingHandler(func(appData string) error {
		select {
		case pingReceived <- struct{}{}:
		default:
		}
		// Write pong back as per websocket RFC
		return clientConn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(time.Second))
	})

	// Start reading to allow gorilla control message handlers to run
	readErr := make(chan error, 1)
	go func() {
		for {
			_, _, err := clientConn.ReadMessage()
			if err != nil {
				readErr <- err
				return
			}
		}
	}()

	// Send Ping from client to server
	if err := clientConn.WriteMessage(websocket.PingMessage, []byte("ping-payload")); err != nil {
		t.Fatalf("failed to send ping: %v", err)
	}

	select {
	case appData := <-pongReceived:
		if appData != "ping-payload" {
			t.Fatalf("expected ping-payload in pong, got %q", appData)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for pong from server")
	}

	// 2. Test Server -> Client Ping (server heartbeat is 50ms)
	select {
	case <-pingReceived:
		// Ping from server received successfully
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for server heartbeat ping")
	}

	_ = srv.Stop()
}

// TestWSServer_ClientDisconnect verifies that client disconnection notifies DisconnectHandler.
func TestWSServer_ClientDisconnect(t *testing.T) {
	disconnectChan := make(chan string, 1)
	onDisconnect := func(conn network.Connection) {
		disconnectChan <- conn.ID()
	}

	srv := ws.NewServer("127.0.0.1:0", nil, onDisconnect, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = srv.Start(ctx)
	}()

	addr := srv.Addr()
	clientConn, _, err := websocket.DefaultDialer.Dial("ws://"+addr+"/ws", nil)
	if err != nil {
		t.Fatalf("dial error: %v", err)
	}

	// Wait for server to register connection
	time.Sleep(50 * time.Millisecond)
	if srv.ActiveCount() != 1 {
		t.Fatalf("expected 1 active connection, got %d", srv.ActiveCount())
	}

	// Client closes connection
	_ = clientConn.Close()

	select {
	case connID := <-disconnectChan:
		if connID == "" {
			t.Fatal("expected non-empty connection ID on disconnect")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for onDisconnect callback")
	}

	// Give slight moment for map deletion
	time.Sleep(20 * time.Millisecond)
	if srv.ActiveCount() != 0 {
		t.Fatalf("expected 0 active connections after disconnect, got %d", srv.ActiveCount())
	}

	_ = srv.Stop()
}

// TestWSServer_ServerStopClosesClient verifies that server Stop() closes client connection cleanly.
func TestWSServer_ServerStopClosesClient(t *testing.T) {
	var disconnected atomic.Bool
	onDisconnect := func(conn network.Connection) {
		disconnected.Store(true)
	}

	srv := ws.NewServer("127.0.0.1:0", nil, onDisconnect, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = srv.Start(ctx)
	}()

	addr := srv.Addr()
	clientConn, _, err := websocket.DefaultDialer.Dial("ws://"+addr+"/ws", nil)
	if err != nil {
		t.Fatalf("dial error: %v", err)
	}
	defer clientConn.Close()

	// Wait for connection to be active
	time.Sleep(50 * time.Millisecond)
	if srv.ActiveCount() != 1 {
		t.Fatalf("expected 1 active connection, got %d", srv.ActiveCount())
	}

	// Stop the server
	if err := srv.Stop(); err != nil {
		t.Fatalf("server Stop() failed: %v", err)
	}

	// Client reading should receive close or EOF
	_ = clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, readErr := clientConn.ReadMessage()
	if readErr == nil {
		t.Fatal("expected read error on client after server Stop(), got nil")
	}

	// Wait briefly for disconnect callback
	time.Sleep(50 * time.Millisecond)
	if !disconnected.Load() {
		t.Fatal("expected onDisconnect to be called when server stopped")
	}

	if srv.ActiveCount() != 0 {
		t.Fatalf("expected 0 active connections after Stop(), got %d", srv.ActiveCount())
	}
}

// TestWSServer_Broadcast verifies broadcasting packets to multiple active clients.
func TestWSServer_Broadcast(t *testing.T) {
	srv := ws.NewServer("127.0.0.1:0", nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = srv.Start(ctx)
	}()

	addr := srv.Addr()
	const numClients = 4
	clients := make([]*websocket.Conn, numClients)
	for i := 0; i < numClients; i++ {
		c, _, err := websocket.DefaultDialer.Dial("ws://"+addr+"/ws", nil)
		if err != nil {
			t.Fatalf("dial client %d error: %v", i, err)
		}
		defer c.Close()
		clients[i] = c
	}

	// Wait for all to register
	time.Sleep(50 * time.Millisecond)
	if srv.ActiveCount() != numClients {
		t.Fatalf("expected %d active connections, got %d", numClients, srv.ActiveCount())
	}

	bcastPkt := network.Packet{
		Type:    network.TypeState,
		Tick:    999,
		Payload: json.RawMessage(`{"status":"running"}`),
	}
	srv.Broadcast(bcastPkt)

	var wg sync.WaitGroup
	wg.Add(numClients)
	for i, c := range clients {
		go func(idx int, conn *websocket.Conn) {
			defer wg.Done()
			_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			var received network.Packet
			if err := conn.ReadJSON(&received); err != nil {
				t.Errorf("client %d failed to read broadcast: %v", idx, err)
				return
			}
			if received.Type != network.TypeState || received.Tick != 999 {
				t.Errorf("client %d received unexpected packet: %+v", idx, received)
			}
		}(i, c)
	}
	wg.Wait()

	_ = srv.Stop()
}

// TestWSConn_PlayerAuth verifies binding and retrieving authenticated player ID and username.
func TestWSConn_PlayerAuth(t *testing.T) {
	conn := ws.NewWSConn(nil)
	if conn.ID() == "" {
		t.Fatal("expected non-empty ID")
	}
	if conn.PlayerID() != 0 {
		t.Fatalf("expected initial PlayerID 0, got %d", conn.PlayerID())
	}
	if conn.Username() != "" {
		t.Fatalf("expected initial Username empty, got %q", conn.Username())
	}

	conn.SetPlayerID(42, "tester")
	if conn.PlayerID() != 42 {
		t.Fatalf("expected PlayerID 42, got %d", conn.PlayerID())
	}
	if conn.Username() != "tester" {
		t.Fatalf("expected Username 'tester', got %q", conn.Username())
	}

	_ = conn.Close()
}

// TestWSConn_QueueFull verifies Send returns ErrQueueFull when buffer is saturated.
func TestWSConn_QueueFull(t *testing.T) {
	conn := ws.NewWSConn(nil)
	defer conn.Close()

	// Fill buffer (default 256)
	for i := 0; i < 256; i++ {
		if err := conn.Send(network.Packet{Type: network.TypePing}); err != nil {
			t.Fatalf("send %d failed unexpectedly: %v", i, err)
		}
	}

	// 257th packet must return ErrQueueFull
	err := conn.Send(network.Packet{Type: network.TypePing})
	if !errors.Is(err, ws.ErrQueueFull) {
		t.Fatalf("expected ErrQueueFull, got %v", err)
	}
}

// TestWSConn_ClosedSend verifies Send returns ErrConnClosed when connection is closed.
func TestWSConn_ClosedSend(t *testing.T) {
	conn := ws.NewWSConn(nil)
	if err := conn.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}

	// Send on closed connection
	err := conn.Send(network.Packet{Type: network.TypePing})
	if !errors.Is(err, ws.ErrConnClosed) {
		t.Fatalf("expected ErrConnClosed, got %v", err)
	}

	// Duplicate close should be idempotent
	if err := conn.Close(); err != nil {
		t.Fatalf("subsequent close failed: %v", err)
	}
}

// TestWSServer_RootEndpoint verifies websocket upgrades on root `/` endpoint.
func TestWSServer_RootEndpoint(t *testing.T) {
	srv := ws.NewServer("127.0.0.1:0", nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = srv.Start(ctx)
	}()

	addr := srv.Addr()
	clientConn, resp, err := websocket.DefaultDialer.Dial("ws://"+addr+"/", nil)
	if err != nil {
		t.Fatalf("failed to dial root endpoint: %v", err)
	}
	defer clientConn.Close()

	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("expected 101, got %d", resp.StatusCode)
	}

	_ = srv.Stop()
}

// TestWSServer_ContextCancellation verifies that context cancellation stops the server cleanly.
func TestWSServer_ContextCancellation(t *testing.T) {
	srv := ws.NewServer("127.0.0.1:0", nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())

	errChan := make(chan error, 1)
	go func() {
		errChan <- srv.Start(ctx)
	}()

	_ = srv.Addr() // Wait for server to bind

	// Cancel context to trigger shutdown
	cancel()

	select {
	case err := <-errChan:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("expected nil or context.Canceled on graceful shutdown, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop in time after context cancellation")
	}
}

// TestWSServer_NonWSRequest verifies plain HTTP requests are rejected properly.
func TestWSServer_NonWSRequest(t *testing.T) {
	srv := ws.NewServer("127.0.0.1:0", nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = srv.Start(ctx)
	}()

	addr := srv.Addr()
	resp, err := http.Get("http://" + addr + "/ws")
	if err != nil {
		t.Fatalf("http GET error: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected HTTP 400 Bad Request for non-ws upgrade, got %d", resp.StatusCode)
	}

	_ = srv.Stop()
}
