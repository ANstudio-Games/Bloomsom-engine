package server_test

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/adnannpm/Bloomsom/engine"
	"github.com/adnannpm/Bloomsom/internal/config"
	"github.com/adnannpm/Bloomsom/internal/logging"
	"github.com/adnannpm/Bloomsom/internal/network"
	"github.com/adnannpm/Bloomsom/internal/server"
)

func getFreePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("get free port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func dialWS(addr string, timeout time.Duration) (*websocket.Conn, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		c, _, err := websocket.DefaultDialer.Dial(addr, nil)
		if err == nil {
			return c, nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	c, _, err := websocket.DefaultDialer.Dial(addr, nil)
	return c, err
}

func TestServerEndToEndChat(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	wsPort := getFreePort(t)

	cfg := config.Default()
	cfg.Database.Path = dbPath
	cfg.Server.WSPort = wsPort
	cfg.Server.Transports = []string{"ws"}
	cfg.Game.Preset = config.PresetLobbyChat
	cfg.Engine.Loop = engine.LoopTypeEvent

	logger, closer, err := logging.New(logging.Options{Level: "error"}, nil)
	if err != nil {
		t.Fatalf("logging new: %v", err)
	}
	defer closer.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serverErrCh := make(chan error, 1)
	go func() {
		serverErrCh <- server.Run(ctx, server.Options{
			Config:  cfg,
			Sandbox: false,
			Logger:  logger,
		})
	}()

	wsAddr := "ws://127.0.0.1:" + strconv.Itoa(wsPort)

	// Dial Client 1 (Alice)
	c1, err := dialWS(wsAddr+"/ws", 3*time.Second)
	if err != nil {
		t.Fatalf("alice dial failed: %v", err)
	}
	defer c1.Close()

	// Dial Client 2 (Bob)
	c2, err := dialWS(wsAddr+"/ws", 3*time.Second)
	if err != nil {
		t.Fatalf("bob dial failed: %v", err)
	}
	defer c2.Close()

	// 1. Register Alice
	regAlice, _ := json.Marshal(map[string]string{
		"username": "alice",
		"password": "password123",
	})
	_ = c1.WriteJSON(network.Packet{
		Type:    network.TypeRegister,
		Payload: regAlice,
	})

	var regRes network.Packet
	_ = c1.ReadJSON(&regRes)
	if regRes.Type != network.TypeRegister {
		t.Fatalf("expected register response, got %s", regRes.Type)
	}

	// 2. Login Alice
	loginAlice, _ := json.Marshal(map[string]string{
		"username": "alice",
		"password": "password123",
	})
	_ = c1.WriteJSON(network.Packet{
		Type:    network.TypeLogin,
		Payload: loginAlice,
	})

	var loginRes network.Packet
	_ = c1.ReadJSON(&loginRes)
	if loginRes.Type != network.TypeLogin {
		t.Fatalf("expected login response, got %s", loginRes.Type)
	}

	// 3. Register & Login Bob
	regBob, _ := json.Marshal(map[string]string{
		"username": "bob",
		"password": "password123",
	})
	_ = c2.WriteJSON(network.Packet{
		Type:    network.TypeRegister,
		Payload: regBob,
	})
	_ = c2.ReadJSON(&regRes)

	loginBob, _ := json.Marshal(map[string]string{
		"username": "bob",
		"password": "password123",
	})
	_ = c2.WriteJSON(network.Packet{
		Type:    network.TypeLogin,
		Payload: loginBob,
	})
	_ = c2.ReadJSON(&loginRes)

	// 4. Both Join Default Room
	_ = c1.SetReadDeadline(time.Now().Add(2 * time.Second))
	_ = c2.SetReadDeadline(time.Now().Add(2 * time.Second))

	_ = c1.WriteJSON(network.Packet{
		Type:    network.TypeJoin,
		Payload: []byte(`{"room_id":"default"}`),
	})
	var joinRes network.Packet
	if err := c1.ReadJSON(&joinRes); err != nil {
		t.Fatalf("c1 join read err: %v", err)
	}

	_ = c2.WriteJSON(network.Packet{
		Type:    network.TypeJoin,
		Payload: []byte(`{"room_id":"default"}`),
	})
	if err := c2.ReadJSON(&joinRes); err != nil {
		t.Fatalf("c2 join read err: %v", err)
	}

	// 5. Alice sends chat message
	time.Sleep(50 * time.Millisecond)
	_ = c1.WriteJSON(network.Packet{
		Type:    network.TypeChat,
		Payload: []byte(`{"message":"Hello Bob!"}`),
	})

	// Bob should receive chat message
	var bobPkt network.Packet
	_ = c2.SetReadDeadline(time.Now().Add(2 * time.Second))
	if err := c2.ReadJSON(&bobPkt); err != nil {
		t.Fatalf("bob read chat error: %v", err)
	}

	if bobPkt.Type != network.TypeChat {
		t.Fatalf("expected chat packet for bob, got %s", bobPkt.Type)
	}

	var chatData map[string]any
	_ = json.Unmarshal(bobPkt.Payload, &chatData)
	if chatData["message"] != "Hello Bob!" || chatData["username"] != "alice" {
		t.Errorf("bob received unexpected chat data: %+v", chatData)
	}

	cancel()
	select {
	case err := <-serverErrCh:
		if err != nil {
			t.Errorf("server shutdown error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Errorf("timed out waiting for server to stop")
	}
}

func TestServerEndToEndRealtimeUDP(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "realtime.db")
	wsPort := getFreePort(t)
	udpPort := getFreePort(t)

	cfg, err := config.ForPreset(config.PresetRealtimeAction, "realtime-test")
	if err != nil {
		t.Fatalf("config preset: %v", err)
	}
	cfg.Database.Path = dbPath
	cfg.Server.WSPort = wsPort
	cfg.Server.UDPPort = udpPort
	cfg.Server.Transports = []string{"ws", "udp"}

	logger, closer, err := logging.New(logging.Options{Level: "error"}, nil)
	if err != nil {
		t.Fatalf("logging new: %v", err)
	}
	defer closer.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serverErrCh := make(chan error, 1)
	go func() {
		serverErrCh <- server.Run(ctx, server.Options{
			Config:  cfg,
			Sandbox: false,
			Logger:  logger,
		})
	}()

	// Step 1: Login via WebSocket to get auth token
	wsAddr := "ws://127.0.0.1:" + strconv.Itoa(wsPort)
	wsClient, err := dialWS(wsAddr+"/ws", 3*time.Second)
	if err != nil {
		t.Fatalf("ws dial failed: %v", err)
	}
	defer wsClient.Close()

	regPayload, _ := json.Marshal(map[string]string{
		"username": "gamer1",
		"password": "gamepassword",
	})
	_ = wsClient.WriteJSON(network.Packet{
		Type:    network.TypeRegister,
		Payload: regPayload,
	})

	var regRes network.Packet
	_ = wsClient.ReadJSON(&regRes)

	loginPayload, _ := json.Marshal(map[string]string{
		"username": "gamer1",
		"password": "gamepassword",
	})
	_ = wsClient.WriteJSON(network.Packet{
		Type:    network.TypeLogin,
		Payload: loginPayload,
	})

	var loginRes network.Packet
	_ = wsClient.ReadJSON(&loginRes)

	var loginData struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(loginRes.Payload, &loginData)
	if loginData.Token == "" {
		t.Fatalf("expected session token from login, got empty")
	}

	// Step 2: Connect over UDP and Authenticate using session token
	udpRemoteAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:"+strconv.Itoa(udpPort))
	if err != nil {
		t.Fatalf("resolve udp addr: %v", err)
	}
	udpClient, err := net.DialUDP("udp", nil, udpRemoteAddr)
	if err != nil {
		t.Fatalf("dial udp: %v", err)
	}
	defer udpClient.Close()

	authPayload, _ := json.Marshal(map[string]string{
		"token": loginData.Token,
	})
	authBytes, _ := json.Marshal(network.Packet{
		Type:    network.TypeAuth,
		Payload: authPayload,
	})
	_, _ = udpClient.Write(authBytes)

	// Step 3: Join default room over UDP
	time.Sleep(50 * time.Millisecond)
	joinBytes, _ := json.Marshal(network.Packet{
		Type:    network.TypeJoin,
		Payload: []byte(`{"room_id":"default"}`),
	})
	_, _ = udpClient.Write(joinBytes)

	// Step 4: Send Movement input with Sequence 500
	time.Sleep(50 * time.Millisecond)
	moveBytes, _ := json.Marshal(network.Packet{
		Type:    network.TypeInput,
		Seq:     500,
		Payload: []byte(`{"dx":1.0,"dy":0.0}`),
	})
	_, _ = udpClient.Write(moveBytes)

	// Read UDP packet replies (expecting ACK and Tick State)
	_ = udpClient.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 2048)

	var gotAck bool
	var gotState bool

	for i := 0; i < 50; i++ {
		n, err := udpClient.Read(buf)
		if err != nil {
			break
		}
		var pkt network.Packet
		if err := json.Unmarshal(buf[:n], &pkt); err == nil {
			if pkt.Type == network.TypeAck && pkt.Seq == 500 {
				gotAck = true
			}
			if pkt.Type == network.TypeState {
				gotState = true
			}
		}
		if gotAck && gotState {
			break
		}
	}

	if !gotAck {
		t.Errorf("expected UDP client to receive ACK for Seq 500")
	}
	if !gotState {
		t.Errorf("expected UDP client to receive 60 TPS world state tick snapshots")
	}

	cancel()
	_ = <-serverErrCh
}
