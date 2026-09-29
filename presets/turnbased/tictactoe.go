// Package turnbased implements a classic 2-player turn-based Tic-Tac-Toe mode.
package turnbased

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/adnannpm/Bloomsom/engine"
	"github.com/adnannpm/Bloomsom/internal/network"
)

// ModeName is the identifier for the turn-based Tic-Tac-Toe preset.
const ModeName = "turn-based"

func init() {
	engine.RegisterMode(&Mode{})
}

// Mode implements engine.GameMode for a 2-player turn-based Tic-Tac-Toe game.
type Mode struct {
	board       [3][3]string
	playerX     *engine.Player
	playerO     *engine.Player
	currentTurn string // "X" or "O"
	winner      string // "X", "O", "draw", or ""
	gameOver    bool
}

// Name returns "turn-based".
func (m *Mode) Name() string { return ModeName }

// OnRoomCreate initializes the board.
func (m *Mode) OnRoomCreate(r *engine.Room) error {
	m.reset()
	return nil
}

func (m *Mode) reset() {
	m.board = [3][3]string{}
	m.playerX = nil
	m.playerO = nil
	m.currentTurn = "X"
	m.winner = ""
	m.gameOver = false
}

// OnJoin assigns player X or O.
func (m *Mode) OnJoin(r *engine.Room, p *engine.Player) error {
	if m.playerX == nil {
		m.playerX = p
		p.SetData("symbol", "X")
	} else if m.playerO == nil {
		m.playerO = p
		p.SetData("symbol", "O")
	} else {
		return errors.New("match is full (2 players maximum)")
	}

	m.broadcastState(r)
	return nil
}

// OnLeave handles player forfeiture.
func (m *Mode) OnLeave(r *engine.Room, p *engine.Player) {
	if !m.gameOver && (p == m.playerX || p == m.playerO) {
		m.gameOver = true
		if p == m.playerX && m.playerO != nil {
			m.winner = "O (opponent left)"
		} else if p == m.playerO && m.playerX != nil {
			m.winner = "X (opponent left)"
		}
		m.broadcastState(r)
	}

	if p == m.playerX {
		m.playerX = nil
	}
	if p == m.playerO {
		m.playerO = nil
	}
}

// MovePayload is the JSON payload sent by a client to place their mark.
type MovePayload struct {
	Row int `json:"row"`
	Col int `json:"col"`
}

// OnInput processes a move action.
func (m *Mode) OnInput(r *engine.Room, p *engine.Player, in engine.Input) {
	if in.Type != "move" {
		return
	}

	if m.gameOver {
		_ = p.Send(network.Packet{
			Type:    network.TypeError,
			Payload: []byte(`{"error":"game is already finished"}`),
		})
		return
	}

	if m.playerX == nil || m.playerO == nil {
		_ = p.Send(network.Packet{
			Type:    network.TypeError,
			Payload: []byte(`{"error":"waiting for second player to join"}`),
		})
		return
	}

	sym, _ := p.GetData("symbol")
	symbolStr, _ := sym.(string)
	if symbolStr != m.currentTurn {
		_ = p.Send(network.Packet{
			Type:    network.TypeError,
			Payload: []byte(`{"error":"not your turn"}`),
		})
		return
	}

	var move MovePayload
	if err := json.Unmarshal(in.Payload, &move); err != nil {
		_ = p.Send(network.Packet{
			Type:    network.TypeError,
			Payload: []byte(`{"error":"invalid move payload"}`),
		})
		return
	}

	if move.Row < 0 || move.Row > 2 || move.Col < 0 || move.Col > 2 {
		_ = p.Send(network.Packet{
			Type:    network.TypeError,
			Payload: []byte(`{"error":"row and col must be between 0 and 2"}`),
		})
		return
	}

	if m.board[move.Row][move.Col] != "" {
		_ = p.Send(network.Packet{
			Type:    network.TypeError,
			Payload: []byte(`{"error":"cell is already occupied"}`),
		})
		return
	}

	m.board[move.Row][move.Col] = symbolStr

	// Check win condition
	if m.checkWin(symbolStr) {
		m.winner = symbolStr
		m.gameOver = true
	} else if m.checkDraw() {
		m.winner = "draw"
		m.gameOver = true
	} else {
		// Switch turn
		if m.currentTurn == "X" {
			m.currentTurn = "O"
		} else {
			m.currentTurn = "X"
		}
	}

	m.broadcastState(r)
}

func (m *Mode) checkWin(s string) bool {
	// Rows & Columns
	for i := 0; i < 3; i++ {
		if m.board[i][0] == s && m.board[i][1] == s && m.board[i][2] == s {
			return true
		}
		if m.board[0][i] == s && m.board[1][i] == s && m.board[2][i] == s {
			return true
		}
	}
	// Diagonals
	if m.board[0][0] == s && m.board[1][1] == s && m.board[2][2] == s {
		return true
	}
	if m.board[0][2] == s && m.board[1][1] == s && m.board[2][0] == s {
		return true
	}
	return false
}

func (m *Mode) checkDraw() bool {
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			if m.board[i][j] == "" {
				return false
			}
		}
	}
	return true
}

func (m *Mode) broadcastState(r *engine.Room) {
	statePayload, _ := json.Marshal(map[string]any{
		"board":        m.board,
		"current_turn": m.currentTurn,
		"winner":       m.winner,
		"game_over":    m.gameOver,
	})

	r.Broadcast(network.Packet{
		Type:    network.TypeState,
		Payload: statePayload,
	})
}

// OnTick is a no-op for event-driven turn-based games.
func (m *Mode) OnTick(r *engine.Room, dt time.Duration) {}

var _ engine.GameMode = (*Mode)(nil)
