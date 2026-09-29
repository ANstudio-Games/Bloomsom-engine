# Game Networking & Multiplayer

## Patterns


---
  #### **Name**
Authoritative Server Architecture
  #### **Description**
    Server owns all game state. Clients send inputs, server simulates, sends authoritative state back. Prevents most cheating.
    
  #### **When**
Building any competitive multiplayer game
  #### **Implementation**
    ```typescript
    // Server-side game loop
    class AuthoritativeServer {
      private gameState: GameState;
      private inputBuffer: Map<PlayerId, InputQueue>;
      private tickRate = 60; // 60 ticks per second
    
      tick() {
        const tickStart = performance.now();
    
        // 1. Collect all player inputs for this tick
        const inputs = this.collectInputsForTick();
    
        // 2. Simulate game with collected inputs
        this.gameState = this.simulate(this.gameState, inputs);
    
        // 3. Send authoritative state to all clients
        this.broadcastState(this.gameState);
    
        // 4. Schedule next tick
        const elapsed = performance.now() - tickStart;
        const tickInterval = 1000 / this.tickRate;
        setTimeout(() => this.tick(), Math.max(0, tickInterval - elapsed));
      }
    
      handleClientInput(playerId: PlayerId, input: PlayerInput) {
        // Validate input (anti-cheat)
        if (!this.validateInput(playerId, input)) {
          this.flagSuspiciousClient(playerId);
          return;
        }
    
        // Buffer input for next tick
        this.inputBuffer.get(playerId)?.push(input);
      }
    }
    ```
    

---
  #### **Name**
Client-Side Prediction with Server Reconciliation
  #### **Description**
    Client predicts movement locally for responsiveness, server corrects with authoritative state. Best UX for action games.
    
  #### **When**
Player movement needs to feel instant despite latency
  #### **Implementation**
    ```typescript
    class PredictiveClient {
      private pendingInputs: TimestampedInput[] = [];
      private localState: PlayerState;
    
      processLocalInput(input: PlayerInput) {
        const timestamp = Date.now();
    
        // 1. Apply input locally immediately (prediction)
        this.localState = this.applyInput(this.localState, input);
    
        // 2. Store input for reconciliation
        this.pendingInputs.push({ input, timestamp });
    
        // 3. Send to server
        this.sendToServer({ input, timestamp });
    
        // 4. Render immediately (no wait for server)
        this.render(this.localState);
      }
    
      onServerState(serverState: AuthoritativeState) {
        // 1. Discard inputs server has processed
        this.pendingInputs = this.pendingInputs.filter(
          i => i.timestamp > serverState.lastProcessedTimestamp
        );
    
        // 2. Start from server's authoritative state
        let reconciledState = serverState.playerState;
    
        // 3. Re-apply unprocessed inputs
        for (const pending of this.pendingInputs) {
          reconciledState = this.applyInput(reconciledState, pending.input);
        }
    
        // 4. Smooth correction if needed (avoid snapping)
        this.localState = this.smoothCorrection(
          this.localState,
          reconciledState
        );
      }
    }
    ```
    

---
  #### **Name**
Entity Interpolation for Remote Players
  #### **Description**
    Buffer server states and interpolate between them for smooth remote player movement. Adds latency but eliminates jitter.
    
  #### **When**
Rendering other players' movements
  #### **Implementation**
    ```typescript
    class EntityInterpolation {
      private stateBuffer: TimestampedState[] = [];
      private interpolationDelay = 100; // ms behind real-time
    
      addServerState(state: EntityState, serverTime: number) {
        this.stateBuffer.push({ state, serverTime });
    
        // Keep buffer size reasonable
        while (this.stateBuffer.length > 20) {
          this.stateBuffer.shift();
        }
      }
    
      getInterpolatedState(currentTime: number): EntityState {
        // Render in the past for smooth interpolation
        const renderTime = currentTime - this.interpolationDelay;
    
        // Find surrounding states
        let before: TimestampedState | null = null;
        let after: TimestampedState | null = null;
    
        for (let i = 0; i < this.stateBuffer.length - 1; i++) {
          if (this.stateBuffer[i].serverTime <= renderTime &&
              this.stateBuffer[i + 1].serverTime >= renderTime) {
            before = this.stateBuffer[i];
            after = this.stateBuffer[i + 1];
            break;
          }
        }
    
        if (!before || !after) {
          // Extrapolate if no data (risky)
          return this.extrapolate(renderTime);
        }
    
        // Linear interpolation
        const t = (renderTime - before.serverTime) /
                  (after.serverTime - before.serverTime);
    
        return this.lerp(before.state, after.state, t);
      }
    }
    ```
    

---
  #### **Name**
Rollback Netcode (GGPO-Style)
  #### **Description**
    For fighting games and precise timing. Run game deterministically, roll back and resimulate when late inputs arrive.
    
  #### **When**
Frame-perfect timing matters (fighting games, rhythm games)
  #### **Implementation**
    ```typescript
    class RollbackNetcode {
      private confirmedFrame = 0;
      private currentFrame = 0;
      private stateHistory: GameState[] = [];
      private inputHistory: Map<number, PlayerInputs> = new Map();
      private maxRollback = 7; // Max frames to roll back
    
      advanceFrame(localInput: Input, predictedRemoteInput: Input) {
        // Store state for potential rollback
        this.stateHistory[this.currentFrame] = this.cloneState(this.gameState);
    
        // Store inputs
        this.inputHistory.set(this.currentFrame, {
          local: localInput,
          remote: predictedRemoteInput,
          confirmed: false
        });
    
        // Simulate frame
        this.gameState = this.simulate(
          this.gameState,
          localInput,
          predictedRemoteInput
        );
    
        this.currentFrame++;
      }
    
      onRemoteInput(frame: number, actualInput: Input) {
        const stored = this.inputHistory.get(frame);
        if (!stored) return;
    
        // Check if prediction was wrong
        if (!this.inputsEqual(stored.remote, actualInput)) {
          // ROLLBACK!
          this.rollbackToFrame(frame, actualInput);
        }
    
        stored.confirmed = true;
        this.advanceConfirmedFrame();
      }
    
      private rollbackToFrame(frame: number, correctInput: Input) {
        // 1. Restore old state
        this.gameState = this.cloneState(this.stateHistory[frame]);
    
        // 2. Fix the input
        this.inputHistory.get(frame)!.remote = correctInput;
    
        // 3. Resimulate all frames up to current
        for (let f = frame; f < this.currentFrame; f++) {
          const inputs = this.inputHistory.get(f)!;
          this.gameState = this.simulate(
            this.gameState,
            inputs.local,
            inputs.remote
          );
        }
      }
    }
    ```
    

---
  #### **Name**
Deterministic Lockstep
  #### **Description**
    All clients simulate identically. Only inputs are sent, not state. Requires perfect determinism but minimal bandwidth.
    
  #### **When**
RTS games, many units, bandwidth-constrained
  #### **Implementation**
    ```typescript
    class LockstepSimulation {
      private currentTurn = 0;
      private inputsPerTurn: Map<number, Map<PlayerId, Input>> = new Map();
      private turnDelay = 2; // Simulate 2 turns behind input
    
      submitLocalInput(input: Input) {
        const inputTurn = this.currentTurn + this.turnDelay;
        this.broadcastInput(inputTurn, input);
        this.storeInput(inputTurn, this.localPlayerId, input);
      }
    
      onRemoteInput(turn: number, playerId: PlayerId, input: Input) {
        this.storeInput(turn, playerId, input);
        this.tryAdvance();
      }
    
      private tryAdvance() {
        while (this.hasAllInputsForTurn(this.currentTurn)) {
          const inputs = this.inputsPerTurn.get(this.currentTurn)!;
    
          // All clients must process in identical order
          const sortedInputs = this.sortDeterministically(inputs);
    
          // Deterministic simulation
          this.gameState = this.simulateTurn(this.gameState, sortedInputs);
    
          this.currentTurn++;
        }
      }
    
      // CRITICAL: Must be identical across all clients
      private simulateTurn(state: GameState, inputs: Input[]): GameState {
        // Use fixed-point math, not floats
        // Sort all iterations identically
        // No random() - use seeded PRNG
        // No Date.now() or external state
      }
    }
    ```
    

---
  #### **Name**
Delta Compression
  #### **Description**
    Only send what changed since last acknowledged state. Dramatically reduces bandwidth for large game states.
    
  #### **When**
Game state is large, bandwidth is limited
  #### **Implementation**
    ```typescript
    class DeltaCompression {
      private clientAcks: Map<ClientId, number> = new Map();
      private stateSnapshots: Map<number, GameState> = new Map();
    
      broadcastState(fullState: GameState, tick: number) {
        this.stateSnapshots.set(tick, fullState);
    
        for (const client of this.clients) {
          const lastAck = this.clientAcks.get(client.id) ?? -1;
          const baseState = this.stateSnapshots.get(lastAck);
    
          if (baseState) {
            // Send delta
            const delta = this.computeDelta(baseState, fullState);
            client.send({
              type: 'delta',
              baseTick: lastAck,
              currentTick: tick,
              delta: this.compressDelta(delta)
            });
          } else {
            // Send full state (new client or too far behind)
            client.send({
              type: 'full',
              tick: tick,
              state: this.compressState(fullState)
            });
          }
        }
    
        // Prune old snapshots
        this.pruneSnapshots();
      }
    
      private computeDelta(base: GameState, current: GameState): Delta {
        return {
          added: this.findAdded(base.entities, current.entities),
          removed: this.findRemoved(base.entities, current.entities),
          changed: this.findChanged(base.entities, current.entities)
        };
      }
    }
    ```
    

---
  #### **Name**
Interest Management / Area of Interest
  #### **Description**
    Only send entities relevant to each player. Essential for MMOs and large-scale games.
    
  #### **When**
Too many entities to send to everyone
  #### **Implementation**
    ```typescript
    class InterestManagement {
      private spatialGrid: SpatialHashGrid;
      private playerAoI: Map<PlayerId, Set<EntityId>> = new Map();
    
      updatePlayerVisibility(player: Player) {
        const nearbyEntities = this.spatialGrid.query(
          player.position,
          player.viewRadius
        );
    
        const currentAoI = this.playerAoI.get(player.id) ?? new Set();
        const newAoI = new Set(nearbyEntities.map(e => e.id));
    
        // Entities entering view
        const entering = [...newAoI].filter(id => !currentAoI.has(id));
        for (const entityId of entering) {
          player.send({
            type: 'entity_spawn',
            entity: this.getFullEntityState(entityId)
          });
        }
    
        // Entities leaving view
        const leaving = [...currentAoI].filter(id => !newAoI.has(id));
        for (const entityId of leaving) {
          player.send({
            type: 'entity_despawn',
            entityId
          });
        }
    
        this.playerAoI.set(player.id, newAoI);
      }
    
      broadcastToInterested(entity: Entity, update: EntityUpdate) {
        for (const [playerId, aoi] of this.playerAoI) {
          if (aoi.has(entity.id)) {
            this.getPlayer(playerId).send(update);
          }
        }
      }
    }
    ```
    

---
  #### **Name**
NAT Traversal with STUN/TURN
  #### **Description**
    Enable P2P connections through firewalls using STUN for hole punching, TURN as relay fallback.
    
  #### **When**
P2P architecture, players behind NAT
  #### **Implementation**
    ```typescript
    class NATTraversal {
      private stunServers = ['stun:stun.l.google.com:19302'];
      private turnServer = {
        urls: 'turn:your-turn-server.com:3478',
        username: 'user',
        credential: 'pass'
      };
    
      async establishP2PConnection(remotePeerId: string): Promise<RTCPeerConnection> {
        const pc = new RTCPeerConnection({
          iceServers: [
            { urls: this.stunServers },
            this.turnServer
          ]
        });
    
        // Create data channel for game data
        const gameChannel = pc.createDataChannel('game', {
          ordered: false,  // UDP-like for game state
          maxRetransmits: 0
        });
    
        const reliableChannel = pc.createDataChannel('reliable', {
          ordered: true  // TCP-like for important events
        });
    
        // ICE candidate handling
        pc.onicecandidate = (event) => {
          if (event.candidate) {
            this.signalingServer.send({
              type: 'ice-candidate',
              to: remotePeerId,
              candidate: event.candidate
            });
          }
        };
    
        // Connection quality monitoring
        pc.oniceconnectionstatechange = () => {
          if (pc.iceConnectionState === 'disconnected') {
            this.handleDisconnection(remotePeerId);
          }
        };
    
        // Create and send offer
        const offer = await pc.createOffer();
        await pc.setLocalDescription(offer);
    
        this.signalingServer.send({
          type: 'offer',
          to: remotePeerId,
          offer
        });
    
        return pc;
      }
    }
    ```
    

---
  #### **Name**
Lag Compensation (Shooter Games)
  #### **Description**
    Rewind server state to what shooter saw when they fired. Essential for fair hit detection in FPS games.
    
  #### **When**
Projectile/hitscan hit detection in shooters
  #### **Implementation**
    ```typescript
    class LagCompensation {
      private positionHistory: Map<EntityId, PositionSnapshot[]> = new Map();
      private maxHistoryMs = 1000;
    
      recordPositions(tick: number, timestamp: number) {
        for (const entity of this.entities) {
          const history = this.positionHistory.get(entity.id) ?? [];
          history.push({
            tick,
            timestamp,
            position: entity.position.clone(),
            hitbox: entity.hitbox.clone()
          });
    
          // Prune old history
          while (history.length > 0 &&
                 timestamp - history[0].timestamp > this.maxHistoryMs) {
            history.shift();
          }
    
          this.positionHistory.set(entity.id, history);
        }
      }
    
      processShot(shooter: Player, shot: ShotData) {
        // Calculate when shooter saw the world
        const clientTime = shot.clientTimestamp;
        const rtt = shooter.latency;
        const serverTimeWhenFired = Date.now() - rtt / 2;
    
        // Clamp to prevent abuse
        const maxRewind = Math.min(rtt, this.maxHistoryMs);
        const rewindTime = Math.max(
          serverTimeWhenFired,
          Date.now() - maxRewind
        );
    
        // Rewind all potential targets
        const rewoundPositions = this.rewindEntities(rewindTime);
    
        // Perform hit detection against rewound state
        const hit = this.raycast(
          shot.origin,
          shot.direction,
          rewoundPositions
        );
    
        if (hit && this.validateHit(shooter, hit)) {
          this.applyDamage(hit.entity, shot.damage);
        }
      }
    
      private rewindEntities(targetTime: number): Map<EntityId, Hitbox> {
        const result = new Map();
    
        for (const [entityId, history] of this.positionHistory) {
          const interpolated = this.interpolatePosition(history, targetTime);
          if (interpolated) {
            result.set(entityId, interpolated);
          }
        }
    
        return result;
      }
    }
    ```
    

---
  #### **Name**
Matchmaking with Skill-Based Rating
  #### **Description**
    Match players by skill using Elo/Glicko/TrueSkill variants. Balance queue times vs match quality.
    
  #### **When**
Competitive multiplayer with ranked play
  #### **Implementation**
    ```typescript
    class SkillBasedMatchmaking {
      private queue: QueuedPlayer[] = [];
      private matchmakingInterval = 1000; // Check every second
    
      // Glicko-2 inspired rating
      interface PlayerRating {
        mu: number;      // Skill estimate (default 1500)
        sigma: number;   // Uncertainty (default 350)
        lastPlayed: Date;
      }
    
      addToQueue(player: Player) {
        this.queue.push({
          player,
          rating: player.rating,
          joinedAt: Date.now(),
          expandingRange: false
        });
      }
    
      findMatches() {
        // Sort by wait time (longer waiting = higher priority)
        this.queue.sort((a, b) => a.joinedAt - b.joinedAt);
    
        const matches: Match[] = [];
        const matched = new Set<string>();
    
        for (const seeker of this.queue) {
          if (matched.has(seeker.player.id)) continue;
    
          // Expand search range based on wait time
          const waitTime = Date.now() - seeker.joinedAt;
          const baseRange = 100;
          const expandedRange = baseRange + Math.floor(waitTime / 10000) * 50;
          const maxRange = 500;
          const searchRange = Math.min(expandedRange, maxRange);
    
          // Find suitable opponent
          const opponent = this.findOpponent(seeker, searchRange, matched);
    
          if (opponent) {
            matches.push(this.createMatch(seeker, opponent));
            matched.add(seeker.player.id);
            matched.add(opponent.player.id);
          }
        }
    
        // Remove matched players from queue
        this.queue = this.queue.filter(p => !matched.has(p.player.id));
    
        return matches;
      }
    
      updateRatings(match: Match, result: MatchResult) {
        // Glicko-2 update
        const winner = result.winner;
        const loser = result.loser;
    
        const expectedScore = 1 / (1 + Math.pow(10,
          (loser.rating.mu - winner.rating.mu) / 400));
    
        const kFactor = this.getKFactor(winner);
    
        winner.rating.mu += kFactor * (1 - expectedScore);
        loser.rating.mu += kFactor * (expectedScore - 1);
    
        // Reduce uncertainty after each game
        winner.rating.sigma *= 0.95;
        loser.rating.sigma *= 0.95;
      }
    }
    ```
    

---
  #### **Name**
Lobby System with Host Migration
  #### **Description**
    Player-hosted lobbies with seamless host transfer if host disconnects. Essential for P2P games.
    
  #### **When**
Player-hosted game sessions
  #### **Implementation**
    ```typescript
    class LobbySystem {
      private lobbies: Map<string, Lobby> = new Map();
    
      createLobby(host: Player, settings: LobbySettings): Lobby {
        const lobby: Lobby = {
          id: crypto.randomUUID(),
          host: host.id,
          players: [host],
          settings,
          hostCandidates: [host.id], // Ordered by priority
          state: 'waiting'
        };
    
        this.lobbies.set(lobby.id, lobby);
        return lobby;
      }
    
      handleDisconnect(playerId: string) {
        const lobby = this.findPlayerLobby(playerId);
        if (!lobby) return;
    
        // Remove player
        lobby.players = lobby.players.filter(p => p.id !== playerId);
        lobby.hostCandidates = lobby.hostCandidates.filter(id => id !== playerId);
    
        // Host migration needed?
        if (lobby.host === playerId && lobby.players.length > 0) {
          this.migrateHost(lobby);
        }
    
        // Lobby empty?
        if (lobby.players.length === 0) {
          this.lobbies.delete(lobby.id);
        }
      }
    
      private migrateHost(lobby: Lobby) {
        // Select new host (best connection, longest in lobby)
        const newHost = this.selectBestHost(lobby);
        lobby.host = newHost.id;
    
        // Notify all players
        this.broadcast(lobby, {
          type: 'host_migrated',
          newHost: newHost.id,
          // Include full state for new host to take over
          gameState: lobby.state === 'playing' ? this.getGameState(lobby) : null
        });
    
        // New host acknowledges
        this.waitForHostAck(lobby, newHost);
      }
    
      private selectBestHost(lobby: Lobby): Player {
        return lobby.players.reduce((best, current) => {
          // Prefer lower latency, then longer in lobby
          const currentScore = current.avgLatency + current.joinedAt / 1000;
          const bestScore = best.avgLatency + best.joinedAt / 1000;
          return currentScore < bestScore ? current : best;
        });
      }
    }
    ```
    

## Anti-Patterns


---
  #### **Name**
Trusting Client Data
  #### **Description**
Accepting client-reported positions, health, or game state
  #### **Why Bad**
    Clients can be modified. Any data from client can be falsified. Position hacks, speed hacks, god mode all exploit trusted clients.
    
  #### **Instead**
    Server validates all inputs, simulates authoritatively, clients only send inputs (movement direction, actions), never state.
    
  #### **Example Bad**
    // NEVER DO THIS
    socket.on('player_update', (data) => {
      player.position = data.position;  // Client says where they are
      player.health = data.health;      // Client says their health
    });
    
  #### **Example Good**
    // Server-authoritative
    socket.on('player_input', (input) => {
      if (this.validateInput(input)) {
        this.inputBuffer.add(playerId, input);
        // Server will simulate next tick
      }
    });
    

---
  #### **Name**
Fixed Tick Rate Without Interpolation
  #### **Description**
Low tick rate server without client-side interpolation
  #### **Why Bad**
    20 tick server = 50ms between updates. Without interpolation, players see stuttery movement. With interpolation, silky smooth.
    
  #### **Instead**
    Always interpolate between received states. Buffer slightly to ensure smooth playback even with network jitter.
    

---
  #### **Name**
Synchronizing Random Numbers
  #### **Description**
Using Math.random() in deterministic simulations
  #### **Why Bad**
    Different clients get different random values. Simulation diverges. Lockstep breaks. Rollback produces different results.
    
  #### **Instead**
    Use seeded PRNG. Share seed at game start. All clients generate identical "random" sequences.
    
  #### **Example Good**
    class SeededRandom {
      private seed: number;
    
      constructor(seed: number) {
        this.seed = seed;
      }
    
      next(): number {
        // Mulberry32 - fast, good distribution
        let t = this.seed += 0x6D2B79F5;
        t = Math.imul(t ^ t >>> 15, t | 1);
        t ^= t + Math.imul(t ^ t >>> 7, t | 61);
        return ((t ^ t >>> 14) >>> 0) / 4294967296;
      }
    }
    

---
  #### **Name**
Sending Full State Every Frame
  #### **Description**
Broadcasting complete game state to all clients every tick
  #### **Why Bad**
    Wastes bandwidth exponentially. 100 entities * 100 bytes * 60 ticks * 100 players = 60 MB/second. Unscalable.
    
  #### **Instead**
    Delta compression (only changes), interest management (only nearby), variable update rates (far entities update less).
    

---
  #### **Name**
TCP for Real-Time Game State
  #### **Description**
Using TCP/WebSocket for position updates
  #### **Why Bad**
    TCP's reliable ordering causes head-of-line blocking. One lost packet delays ALL subsequent packets. Causes rubber-banding.
    
  #### **Instead**
    UDP for state (okay to lose old positions), TCP/WebSocket for important events (chat, inventory). WebRTC DataChannel unreliable mode.
    
  #### **Exception**
Turn-based games where latency doesn't matter

---
  #### **Name**
Client-Side Hit Detection
  #### **Description**
Client determines if their shot hit
  #### **Why Bad**
    Aimbot sends "I hit headshot" regardless of aim. Impossible to prevent client-side. Must validate server-side.
    
  #### **Instead**
    Client sends shot data (origin, direction, timestamp). Server performs hit detection with lag compensation.
    

---
  #### **Name**
No Rate Limiting on Inputs
  #### **Description**
Processing unlimited inputs from clients
  #### **Why Bad**
    Malicious client sends 1000 inputs per second. Server overwhelmed. Speed hacks work by sending rapid inputs.
    
  #### **Instead**
    Rate limit inputs (e.g., max 64/second). Queue excess. Detect and flag anomalous rates.
    
  #### **Example Good**
    const MAX_INPUTS_PER_SECOND = 64;
    const inputCounts = new Map<string, number>();
    
    function handleInput(playerId: string, input: Input) {
      const count = inputCounts.get(playerId) ?? 0;
      if (count >= MAX_INPUTS_PER_SECOND) {
        flagPlayer(playerId, 'input_flood');
        return;
      }
      inputCounts.set(playerId, count + 1);
      processInput(playerId, input);
    }
    
    // Reset counts every second
    setInterval(() => inputCounts.clear(), 1000);
    

---
  #### **Name**
Hardcoded Server IP
  #### **Description**
Hardcoding server addresses in client
  #### **Why Bad**
    Can't migrate servers, can't do regional routing, can't handle server failures. Also security risk if exposed.
    
  #### **Instead**
    Service discovery, DNS, or matchmaking service provides server addresses dynamically.
    