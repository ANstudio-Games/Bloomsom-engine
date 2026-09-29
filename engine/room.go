package engine

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/adnannpm/Bloomsom/internal/network"
)

// LoopType represents the execution model for a room.
const (
	LoopTypeEvent = "event" // Reactive loop, sleeps until packet/event arrives
	LoopTypeTick  = "tick"  // Continuous fixed timestep simulation loop
)

// Status constants for a room lifecycle.
const (
	StatusOpen    = "open"
	StatusRunning = "running"
	StatusClosed  = "closed"
)

// RoomOptions configures a new game room instance.
type RoomOptions struct {
	ID         string
	Name       string
	Mode       GameMode
	MaxPlayers int
	LoopType   string
	TickRate   int // TPS (ticks per second), required for tick mode
	Logger     *slog.Logger
}

type joinReq struct {
	player *Player
	reply  chan error
}

type leaveReq struct {
	player *Player
	reply  chan struct{}
}

type inputReq struct {
	player *Player
	input  Input
}

// Room represents an active isolated game session running on its own actor goroutine.
type Room struct {
	id         string
	name       string
	mode       GameMode
	maxPlayers int
	loopType   string
	tickRate   int
	log        *slog.Logger

	players map[int64]*Player
	status  string

	joinCh  chan joinReq
	leaveCh chan leaveReq
	inputCh chan inputReq
	stopCh  chan struct{}
	doneCh  chan struct{}

	tickCount uint64
	mu        sync.RWMutex
}

// NewRoom creates and starts a room actor with the specified options.
func NewRoom(opts RoomOptions) (*Room, error) {
	if opts.ID == "" {
		return nil, errors.New("room: ID is required")
	}
	if opts.Mode == nil {
		return nil, errors.New("room: Mode is required")
	}
	if opts.LoopType == "" {
		opts.LoopType = LoopTypeEvent
	}
	if opts.LoopType == LoopTypeTick && opts.TickRate <= 0 {
		return nil, errors.New("room: TickRate must be > 0 for tick loop")
	}
	if opts.MaxPlayers <= 0 {
		opts.MaxPlayers = 8
	}

	r := &Room{
		id:         opts.ID,
		name:       opts.Name,
		mode:       opts.Mode,
		maxPlayers: opts.MaxPlayers,
		loopType:   opts.LoopType,
		tickRate:   opts.TickRate,
		log:        opts.Logger,
		players:    make(map[int64]*Player),
		status:     StatusOpen,
		joinCh:     make(chan joinReq),
		leaveCh:    make(chan leaveReq),
		inputCh:    make(chan inputReq, 256),
		stopCh:     make(chan struct{}),
		doneCh:     make(chan struct{}),
	}

	if err := opts.Mode.OnRoomCreate(r); err != nil {
		return nil, fmt.Errorf("room mode init: %w", err)
	}

	go r.run()
	return r, nil
}

// ID returns the room's unique identifier.
func (r *Room) ID() string { return r.id }

// Name returns the room's display name.
func (r *Room) Name() string { return r.name }

// Status returns current lifecycle status.
func (r *Room) Status() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.status
}

// TickCount returns the total number of simulation ticks executed.
func (r *Room) TickCount() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.tickCount
}

// PlayerCount returns the current count of players inside the room.
func (r *Room) PlayerCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.players)
}

// Players returns a snapshot slice of all active players in the room.
func (r *Room) Players() []*Player {
	r.mu.RLock()
	defer r.mu.RUnlock()
	list := make([]*Player, 0, len(r.players))
	for _, p := range r.players {
		list = append(list, p)
	}
	return list
}

// Join attempts to add a player to this room. It blocks until processed by the room actor.
func (r *Room) Join(p *Player) error {
	reply := make(chan error, 1)
	select {
	case r.joinCh <- joinReq{player: p, reply: reply}:
		return <-reply
	case <-r.doneCh:
		return errors.New("room is closed")
	}
}

// Leave removes a player from this room.
func (r *Room) Leave(p *Player) {
	reply := make(chan struct{}, 1)
	select {
	case r.leaveCh <- leaveReq{player: p, reply: reply}:
		<-reply
	case <-r.doneCh:
	}
}

// DispatchInput queues a player action to be processed inside the room actor.
func (r *Room) DispatchInput(p *Player, in Input) {
	select {
	case r.inputCh <- inputReq{player: p, input: in}:
	case <-r.doneCh:
	default:
		if r.log != nil {
			r.log.Warn("room input queue full, dropping action", "room", r.id, "player", p.Username, "type", in.Type)
		}
	}
}

// Broadcast sends a packet to every player in the room.
func (r *Room) Broadcast(pkt network.Packet) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, p := range r.players {
		_ = p.Send(pkt)
	}
}

// BroadcastExcept sends a packet to every player except the specified one.
func (r *Room) BroadcastExcept(exceptPlayerID int64, pkt network.Packet) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for id, p := range r.players {
		if id != exceptPlayerID {
			_ = p.Send(pkt)
		}
	}
}

// Close gracefully stops the room actor.
func (r *Room) Close() error {
	select {
	case <-r.doneCh:
		return nil
	default:
		close(r.stopCh)
		<-r.doneCh
		return nil
	}
}

func (r *Room) run() {
	defer close(r.doneCh)

	if r.loopType == LoopTypeTick {
		r.runTickLoop()
	} else {
		r.runEventLoop()
	}
}

func (r *Room) runEventLoop() {
	for {
		select {
		case req := <-r.joinCh:
			r.handleJoin(req)
		case req := <-r.leaveCh:
			r.handleLeave(req)
		case req := <-r.inputCh:
			r.handleInput(req)
		case <-r.stopCh:
			r.handleStop()
			return
		}
	}
}

func (r *Room) runTickLoop() {
	dt := time.Second / time.Duration(r.tickRate)
	ticker := time.NewTicker(dt)
	defer ticker.Stop()

	lastTime := time.Now()
	var accumulator time.Duration
	const maxCatchUp = 5

	for {
		select {
		case req := <-r.joinCh:
			r.handleJoin(req)
		case req := <-r.leaveCh:
			r.handleLeave(req)
		case req := <-r.inputCh:
			r.handleInput(req)
		case now := <-ticker.C:
			elapsed := now.Sub(lastTime)
			lastTime = now
			accumulator += elapsed

			steps := 0
			for accumulator >= dt && steps < maxCatchUp {
				r.mu.Lock()
				r.tickCount++
				r.mu.Unlock()

				startTick := time.Now()
				r.mode.OnTick(r, dt)
				tickDur := time.Since(startTick)

				// Warn if tick calculation overruns its budget
				if tickDur > dt && r.log != nil {
					r.log.Warn("tick overrun", "room", r.id, "took", tickDur, "budget", dt)
				}

				accumulator -= dt
				steps++
			}
		case <-r.stopCh:
			r.handleStop()
			return
		}
	}
}

func (r *Room) handleJoin(req joinReq) {
	p := req.player
	r.mu.Lock()
	if len(r.players) >= r.maxPlayers {
		r.mu.Unlock()
		req.reply <- errors.New("room is full")
		return
	}
	if _, exists := r.players[p.ID]; exists {
		r.mu.Unlock()
		req.reply <- errors.New("player already in room")
		return
	}
	r.mu.Unlock()

	if err := r.mode.OnJoin(r, p); err != nil {
		req.reply <- fmt.Errorf("join rejected: %w", err)
		return
	}

	r.mu.Lock()
	r.players[p.ID] = p
	p.SetRoomID(r.id)
	if len(r.players) > 0 {
		r.status = StatusRunning
	}
	r.mu.Unlock()

	req.reply <- nil
}

func (r *Room) handleLeave(req leaveReq) {
	p := req.player
	r.mu.Lock()
	if _, exists := r.players[p.ID]; exists {
		delete(r.players, p.ID)
		p.SetRoomID("")
		if len(r.players) == 0 {
			r.status = StatusOpen
		}
		r.mu.Unlock()
		r.mode.OnLeave(r, p)
	} else {
		r.mu.Unlock()
	}
	req.reply <- struct{}{}
}

func (r *Room) handleInput(req inputReq) {
	r.mu.RLock()
	_, inRoom := r.players[req.player.ID]
	r.mu.RUnlock()
	if !inRoom {
		return
	}
	r.mode.OnInput(r, req.player, req.input)
}

func (r *Room) handleStop() {
	r.mu.Lock()
	r.status = StatusClosed
	players := make([]*Player, 0, len(r.players))
	for _, p := range r.players {
		players = append(players, p)
	}
	r.players = make(map[int64]*Player)
	r.mu.Unlock()

	for _, p := range players {
		p.SetRoomID("")
		r.mode.OnLeave(r, p)
	}
}
