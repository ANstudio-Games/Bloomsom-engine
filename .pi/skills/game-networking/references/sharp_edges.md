# Game Networking - Sharp Edges

## Never Trust Client-Reported Position

### **Id**
trusting_client_position
### **Severity**
critical
### **Category**
security
### **Symptoms**
  - Speed hacking
  - Teleportation exploits
  - Wall clipping
### **Problem**
  If server accepts position from client, hackers modify their client to
  report any position. Speed hacks, teleports, noclip all become trivial.
  
### **Root Cause**
  Misunderstanding of client-server trust. Client is enemy territory -
  assume all client data is falsified.
  
### **Solution**
  Server simulates authoritative state. Client sends only inputs
  (direction, actions). Server applies inputs and broadcasts results.
  
### **Code Example**
  ```typescript
  // BAD: Client tells server where they are
  socket.on('position', (pos) => {
    player.position = pos;  // Hacker's paradise
  });
  
  // GOOD: Client sends inputs, server simulates
  socket.on('input', (input) => {
    if (!validateInput(input)) return;
    pendingInputs.queue(playerId, input);
  });
  
  function serverTick() {
    for (const [playerId, input] of pendingInputs) {
      const player = players.get(playerId);
      const newPos = simulateMovement(player, input);
  
      // Server validates movement is legal
      if (isValidPosition(newPos)) {
        player.position = newPos;
      }
    }
  }
  ```
  
### **Detection Regex**
player\.position\s*=\s*(data|packet|msg)\.
### **Related**
  - input_validation
  - server_authority

## Client-Side Hit Detection Enables Aimbots

### **Id**
client_side_hit_detection
### **Severity**
critical
### **Category**
security
### **Symptoms**
  - Aimbots working perfectly
  - Impossible hit rates
  - Hits through walls
### **Problem**
  If client reports "I hit player X", aimbot just always reports hits.
  Client can claim hits on targets behind walls or across map.
  
### **Solution**
  Server performs all hit detection. Client sends shot data (origin,
  direction, timestamp). Server validates with lag compensation.
  
### **Code Example**
  ```typescript
  // BAD: Client says what they hit
  socket.on('hit', ({ targetId, damage }) => {
    applyDamage(targetId, damage);  // Aimbot sends 100% headshots
  });
  
  // GOOD: Server validates hit
  socket.on('shot', (shotData) => {
    const shooter = getPlayer(socket.id);
  
    // Rewind world to when shooter fired
    const rewindTime = Date.now() - shooter.latency / 2;
    const rewoundState = rewindWorld(rewindTime);
  
    // Server performs raycast
    const hit = raycast(
      shotData.origin,
      shotData.direction,
      rewoundState
    );
  
    if (hit && validateHit(shooter, hit, shotData)) {
      applyDamage(hit.entityId, calculateDamage(shotData, hit));
    }
  });
  ```
  

## Tick Rate Tradeoffs

### **Id**
tick_rate_decisions
### **Severity**
high
### **Category**
performance
### **Symptoms**
  - Choppy movement at low tick
  - Server CPU maxed at high tick
  - Inconsistent hit registration
### **Problem**
  Higher tick rate = more responsive but more CPU and bandwidth.
  Lower tick rate = cheaper but less precise.
  
  Common rates:
  - 128 tick: Counter-Strike competitive
  - 60 tick: Overwatch, Valorant
  - 30 tick: Many console games
  - 20 tick: Fortnite (with client prediction)
  - 10 tick: Turn-based/slow-paced
  
### **Root Cause**
  No universal correct tick rate. Depends on game type, server budget,
  and how good your interpolation/prediction is.
  
### **Solution**
  Start with 60 tick for action games. Profile server CPU usage.
  Good interpolation can make 20-30 tick feel smooth.
  
### **Code Example**
  ```typescript
  class TickRateManager {
    private targetTickRate = 60;
    private actualTickRate = 60;
    private tickTimeHistory: number[] = [];
  
    tick() {
      const tickStart = performance.now();
  
      // Do game simulation
      this.simulate();
  
      const tickDuration = performance.now() - tickStart;
      this.tickTimeHistory.push(tickDuration);
  
      // Monitor if we're hitting target
      const avgTickTime = this.getAverageTickTime();
      const tickBudget = 1000 / this.targetTickRate;
  
      if (avgTickTime > tickBudget * 0.8) {
        console.warn(`Tick time ${avgTickTime.toFixed(1)}ms exceeds 80% of ${tickBudget}ms budget`);
        // Consider: reducing tick rate, optimizing simulation, scaling servers
      }
    }
  
    // Adaptive tick rate based on load
    adjustTickRate() {
      const load = this.getAverageTickTime() / (1000 / this.targetTickRate);
  
      if (load > 0.9) {
        this.actualTickRate = Math.max(20, this.actualTickRate - 10);
      } else if (load < 0.5 && this.actualTickRate < this.targetTickRate) {
        this.actualTickRate = Math.min(this.targetTickRate, this.actualTickRate + 10);
      }
    }
  }
  ```
  

## Missing Entity Interpolation Causes Jitter

### **Id**
missing_interpolation
### **Severity**
high
### **Category**
visual
### **Symptoms**
  - Other players stutter/teleport
  - Movement looks choppy
  - Visible jumps between positions
### **Problem**
  Server sends updates at 20-60 Hz. Rendering at 60-144 Hz. Without
  interpolation, entities jump between discrete positions.
  
### **Solution**
  Buffer incoming states. Interpolate between them for rendering.
  Render 100-150ms behind real-time for smooth playback.
  
### **Code Example**
  ```typescript
  class InterpolationBuffer {
    private buffer: StateSnapshot[] = [];
    private renderDelay = 100; // ms behind real-time
  
    addSnapshot(state: EntityState, serverTime: number) {
      this.buffer.push({ state, serverTime });
  
      // Keep last 1 second of history
      const cutoff = serverTime - 1000;
      this.buffer = this.buffer.filter(s => s.serverTime > cutoff);
    }
  
    getInterpolatedState(now: number): EntityState | null {
      const renderTime = now - this.renderDelay;
  
      // Find surrounding snapshots
      let before: StateSnapshot | null = null;
      let after: StateSnapshot | null = null;
  
      for (let i = 0; i < this.buffer.length - 1; i++) {
        if (this.buffer[i].serverTime <= renderTime &&
            this.buffer[i + 1].serverTime > renderTime) {
          before = this.buffer[i];
          after = this.buffer[i + 1];
          break;
        }
      }
  
      if (!before || !after) {
        // Extrapolate or use last known
        return this.buffer[this.buffer.length - 1]?.state ?? null;
      }
  
      // Interpolate
      const t = (renderTime - before.serverTime) /
                (after.serverTime - before.serverTime);
  
      return {
        position: this.lerpVec3(before.state.position, after.state.position, t),
        rotation: this.slerpQuat(before.state.rotation, after.state.rotation, t),
        animation: t > 0.5 ? after.state.animation : before.state.animation
      };
    }
  
    private lerpVec3(a: Vec3, b: Vec3, t: number): Vec3 {
      return {
        x: a.x + (b.x - a.x) * t,
        y: a.y + (b.y - a.y) * t,
        z: a.z + (b.z - a.z) * t
      };
    }
  }
  ```
  

## Aggressive Reconciliation Causes Snapping

### **Id**
aggressive_reconciliation
### **Severity**
medium
### **Category**
visual
### **Symptoms**
  - Player position snaps suddenly
  - Visual discontinuity on corrections
  - Jarring camera movements
### **Problem**
  When server corrects client prediction, instantly snapping to server
  position creates jarring visual. Players notice even small corrections.
  
### **Solution**
  Smooth corrections over multiple frames. Blend toward correct position
  rather than instant teleport.
  
### **Code Example**
  ```typescript
  class SmoothReconciliation {
    private visualPosition: Vec3;
    private logicalPosition: Vec3; // Server-authoritative
    private correctionSmoothing = 0.1; // Blend speed
  
    reconcile(serverPosition: Vec3) {
      // Set logical position immediately (gameplay logic)
      this.logicalPosition = serverPosition;
  
      // Visual position blends toward logical
      // This is purely cosmetic - collision uses logicalPosition
    }
  
    update(deltaTime: number) {
      const distance = this.distance(this.visualPosition, this.logicalPosition);
  
      if (distance < 0.01) {
        // Close enough, snap
        this.visualPosition = { ...this.logicalPosition };
      } else if (distance > 5) {
        // Too far, must snap (teleport or major desync)
        this.visualPosition = { ...this.logicalPosition };
      } else {
        // Smooth interpolation
        this.visualPosition = this.lerpVec3(
          this.visualPosition,
          this.logicalPosition,
          this.correctionSmoothing
        );
      }
    }
  
    // Renderer uses visual position
    getVisualPosition(): Vec3 {
      return this.visualPosition;
    }
  
    // Game logic uses logical position
    getPosition(): Vec3 {
      return this.logicalPosition;
    }
  }
  ```
  

## Floating Point Non-Determinism Breaks Lockstep

### **Id**
floating_point_determinism
### **Severity**
critical
### **Category**
determinism
### **Symptoms**
  - Simulation diverges between clients
  - Rollback produces different results
  - Checksum mismatches
### **Problem**
  Floating point operations can produce different results on different
  CPUs, compilers, or even optimization levels. sin(x) on Intel vs AMD
  may differ by small amounts that accumulate.
  
### **Solution**
  Use fixed-point arithmetic for deterministic games. Or use soft-float
  library with identical implementation everywhere.
  
### **Code Example**
  ```typescript
  // Fixed point math (24.8 format)
  class FixedPoint {
    private static SCALE = 256; // 8 fractional bits
  
    readonly value: number; // Integer internally
  
    private constructor(value: number) {
      this.value = value | 0; // Force integer
    }
  
    static fromFloat(f: number): FixedPoint {
      return new FixedPoint(Math.round(f * FixedPoint.SCALE));
    }
  
    static fromInt(i: number): FixedPoint {
      return new FixedPoint(i * FixedPoint.SCALE);
    }
  
    toFloat(): number {
      return this.value / FixedPoint.SCALE;
    }
  
    add(other: FixedPoint): FixedPoint {
      return new FixedPoint(this.value + other.value);
    }
  
    sub(other: FixedPoint): FixedPoint {
      return new FixedPoint(this.value - other.value);
    }
  
    mul(other: FixedPoint): FixedPoint {
      // Careful with overflow - use BigInt for large values
      return new FixedPoint((this.value * other.value) / FixedPoint.SCALE);
    }
  
    div(other: FixedPoint): FixedPoint {
      return new FixedPoint((this.value * FixedPoint.SCALE) / other.value);
    }
  
    // Lookup table for sin to ensure determinism
    static sin(angle: FixedPoint): FixedPoint {
      // 256-entry lookup table for 0-2π
      const index = (angle.value / FixedPoint.SCALE) & 0xFF;
      return new FixedPoint(SIN_TABLE[index]);
    }
  }
  ```
  

## Unsynchronized Random Numbers

### **Id**
unsynced_random
### **Severity**
critical
### **Category**
determinism
### **Symptoms**
  - Different outcomes on different clients
  - Items spawn in different places
  - AI behaves differently
### **Problem**
  Math.random() produces different sequences on each client.
  Any simulation using random without sync will diverge.
  
### **Solution**
  Use seeded PRNG. Share seed at match start. All clients generate
  identical "random" sequences when called in same order.
  
### **Code Example**
  ```typescript
  // Mulberry32 - Simple, fast, good quality
  class SyncedRandom {
    private seed: number;
  
    constructor(seed: number) {
      this.seed = seed >>> 0; // Ensure unsigned 32-bit
    }
  
    // Get next random 0-1
    next(): number {
      let t = this.seed += 0x6D2B79F5;
      t = Math.imul(t ^ t >>> 15, t | 1);
      t ^= t + Math.imul(t ^ t >>> 7, t | 61);
      return ((t ^ t >>> 14) >>> 0) / 4294967296;
    }
  
    // Random integer in range [min, max]
    nextInt(min: number, max: number): number {
      return min + Math.floor(this.next() * (max - min + 1));
    }
  
    // Shuffle array deterministically
    shuffle<T>(array: T[]): T[] {
      const result = [...array];
      for (let i = result.length - 1; i > 0; i--) {
        const j = this.nextInt(0, i);
        [result[i], result[j]] = [result[j], result[i]];
      }
      return result;
    }
  
    // Save/restore state for rollback
    getState(): number {
      return this.seed;
    }
  
    setState(state: number) {
      this.seed = state;
    }
  }
  
  // At match start
  const sharedSeed = Date.now(); // Exchange via server
  const rng = new SyncedRandom(sharedSeed);
  ```
  

## Non-Deterministic Iteration Order

### **Id**
iteration_order
### **Severity**
high
### **Category**
determinism
### **Symptoms**
  - Simulation diverges randomly
  - Works sometimes, fails sometimes
  - Depends on browser/runtime
### **Problem**
  JavaScript object key order, Map iteration order, Set iteration order
  are not guaranteed identical across environments.
  
### **Solution**
  Always sort before iterating. Use arrays for deterministic access.
  Or use Map/Set with explicit ordering.
  
### **Code Example**
  ```typescript
  // BAD: Object key order not guaranteed
  const players = { p1: {...}, p2: {...}, p3: {...} };
  for (const id in players) {
    simulate(players[id]); // Order may vary!
  }
  
  // BAD: Map iteration order is insertion order (fragile)
  const entities = new Map();
  for (const [id, entity] of entities) {
    update(entity); // Order depends on insertion
  }
  
  // GOOD: Explicit sorting
  const playerIds = Object.keys(players).sort();
  for (const id of playerIds) {
    simulate(players[id]); // Deterministic order
  }
  
  // GOOD: Sorted entity array
  const sortedEntities = [...entities.entries()]
    .sort(([a], [b]) => a.localeCompare(b));
  for (const [id, entity] of sortedEntities) {
    update(entity); // Deterministic
  }
  
  // GOOD: Use numeric IDs and sort
  const entityArray = Array.from(entities.values())
    .sort((a, b) => a.id - b.id);
  ```
  

## NAT Traversal Failures

### **Id**
nat_traversal_failure
### **Severity**
high
### **Category**
connectivity
### **Symptoms**
  - Some players can't connect
  - P2P works locally but not over internet
  - Connection timeouts
### **Problem**
  Most home routers use NAT. Two players behind NAT can't directly
  connect without hole punching. Symmetric NAT defeats STUN.
  
### **Solution**
  Use STUN for hole punching. Fall back to TURN relay for symmetric NAT.
  Consider server-relay as universal fallback.
  
### **Code Example**
  ```typescript
  class NATTraversal {
    private stunServers = [
      'stun:stun.l.google.com:19302',
      'stun:stun1.l.google.com:19302',
      'stun:stun2.l.google.com:19302'
    ];
  
    private turnServers = [{
      urls: 'turn:your-turn-server.com:3478',
      username: 'user',
      credential: 'pass'
    }];
  
    async createConnection(remotePeerId: string): Promise<RTCPeerConnection> {
      const config: RTCConfiguration = {
        iceServers: [
          { urls: this.stunServers },
          ...this.turnServers
        ],
        iceCandidatePoolSize: 10,
        iceTransportPolicy: 'all' // Try direct first, fall back to relay
      };
  
      const pc = new RTCPeerConnection(config);
  
      // Track connection type for metrics
      pc.oniceconnectionstatechange = () => {
        if (pc.iceConnectionState === 'connected') {
          this.reportConnectionType(pc);
        }
      };
  
      // Log ICE candidates for debugging
      pc.onicecandidate = (event) => {
        if (event.candidate) {
          console.log('ICE candidate:', event.candidate.type);
          // 'host' = local, 'srflx' = STUN, 'relay' = TURN
        }
      };
  
      return pc;
    }
  
    private async reportConnectionType(pc: RTCPeerConnection) {
      const stats = await pc.getStats();
      stats.forEach(report => {
        if (report.type === 'candidate-pair' && report.state === 'succeeded') {
          const localCandidate = stats.get(report.localCandidateId);
          const remoteCandidate = stats.get(report.remoteCandidateId);
  
          console.log('Connected via:', {
            local: localCandidate?.candidateType,
            remote: remoteCandidate?.candidateType,
            usingRelay: localCandidate?.candidateType === 'relay'
          });
  
          // Track metrics
          analytics.track('p2p_connection', {
            type: localCandidate?.candidateType,
            usedTurn: localCandidate?.candidateType === 'relay'
          });
        }
      });
    }
  }
  ```
  

## Bandwidth Explosion with Player Count

### **Id**
bandwidth_explosion
### **Severity**
high
### **Category**
performance
### **Symptoms**
  - Works with 4 players, fails with 20
  - High ping when more players join
  - Packet loss increases
### **Problem**
  Naive broadcast: N players * M entities * B bytes = O(N*M*B) bandwidth.
  100 players * 100 entities * 100 bytes * 60Hz = 60MB/s total.
  
### **Solution**
  Interest management (only send nearby entities).
  Delta compression (only send changes).
  Priority system (important entities update more).
  
### **Code Example**
  ```typescript
  class BandwidthManager {
    private bytesPerSecondLimit = 64000; // 64 KB/s per client
    private updateBudget: Map<ClientId, number> = new Map();
  
    allocateUpdates(client: Client, entities: Entity[]): EntityUpdate[] {
      const budget = this.bytesPerSecondLimit / 60; // per tick
      let spent = 0;
      const updates: EntityUpdate[] = [];
  
      // Sort by priority (distance, importance, last update time)
      const prioritized = this.prioritizeEntities(client, entities);
  
      for (const entity of prioritized) {
        const update = this.createUpdate(entity, client);
        const size = this.estimateSize(update);
  
        if (spent + size > budget) {
          break; // Budget exhausted
        }
  
        updates.push(update);
        spent += size;
      }
  
      return updates;
    }
  
    private prioritizeEntities(client: Client, entities: Entity[]): Entity[] {
      return entities
        .map(entity => ({
          entity,
          priority: this.calculatePriority(client, entity)
        }))
        .sort((a, b) => b.priority - a.priority)
        .map(({ entity }) => entity);
    }
  
    private calculatePriority(client: Client, entity: Entity): number {
      const distance = this.distance(client.position, entity.position);
      const timeSinceUpdate = Date.now() - entity.lastSentTo.get(client.id);
      const importance = entity.importanceScore; // Enemies > terrain
  
      // Higher priority = closer + older update + more important
      return (importance * 1000) / (distance + 1) + timeSinceUpdate / 100;
    }
  }
  ```
  

## Rollback Causes Visual Artifacts

### **Id**
rollback_visual_artifacts
### **Severity**
medium
### **Category**
visual
### **Symptoms**
  - Characters flicker or jump
  - Attacks appear, disappear, reappear
  - Audio plays twice
### **Problem**
  Rollback resimulates frames. If visual/audio effects trigger during
  simulation, they may fire multiple times or at wrong times.
  
### **Solution**
  Separate simulation state from presentation. Only trigger effects on
  confirmed frames. Track which effects already played.
  
### **Code Example**
  ```typescript
  class RollbackSafeEffects {
    private confirmedFrame = 0;
    private pendingEffects: Map<number, Effect[]> = new Map();
    private playedEffects: Set<string> = new Set();
  
    // Called during simulation (may be rolled back)
    queueEffect(frame: number, effect: Effect) {
      const effects = this.pendingEffects.get(frame) ?? [];
      effects.push(effect);
      this.pendingEffects.set(frame, effects);
    }
  
    // Called when frame is confirmed (never rolled back)
    confirmFrame(frame: number) {
      const effects = this.pendingEffects.get(frame) ?? [];
  
      for (const effect of effects) {
        const effectId = `${frame}-${effect.type}-${effect.entityId}`;
  
        // Only play once
        if (!this.playedEffects.has(effectId)) {
          this.playedEffects.add(effectId);
          this.playEffect(effect);
        }
      }
  
      // Clean up old effects
      this.pendingEffects.delete(frame);
      this.confirmedFrame = frame;
    }
  
    // Called on rollback
    onRollback(toFrame: number) {
      // Clear effects from rolled-back frames
      for (const [frame] of this.pendingEffects) {
        if (frame > toFrame) {
          this.pendingEffects.delete(frame);
        }
      }
    }
  
    private playEffect(effect: Effect) {
      switch (effect.type) {
        case 'sound':
          this.audio.play(effect.soundId);
          break;
        case 'particle':
          this.particles.spawn(effect.particleType, effect.position);
          break;
        case 'animation':
          this.animations.trigger(effect.entityId, effect.animationName);
          break;
      }
    }
  }
  ```
  

## Skill Rating Compression Over Time

### **Id**
matchmaking_skill_compression
### **Severity**
medium
### **Category**
matchmaking
### **Symptoms**
  - Veteran players seem equal skill
  - New players ranked too high
  - Smurfs dominate low ranks
### **Problem**
  Elo/Glicko ratings compress over time. Uncertainty decreases, making
  it hard to correct initial miscalibrations. Smurfs exploit this.
  
### **Solution**
  Periodic uncertainty injection. Placement matches with high K-factor.
  Confidence decay for inactive players.
  
### **Code Example**
  ```typescript
  class RatingSystem {
    updateRating(player: Player, opponent: Player, won: boolean) {
      // Decay uncertainty for inactive players
      const daysSincePlay = (Date.now() - player.lastGameTime) / 86400000;
      if (daysSincePlay > 30) {
        player.sigma = Math.min(350, player.sigma + daysSincePlay * 2);
      }
  
      // Higher K-factor for uncertain ratings
      const kFactor = this.getKFactor(player);
  
      // Standard Glicko update...
      const expected = this.expectedScore(player.mu, opponent.mu);
      const actual = won ? 1 : 0;
  
      player.mu += kFactor * (actual - expected);
      player.sigma *= 0.9; // Reduce uncertainty after game
  
      player.lastGameTime = Date.now();
    }
  
    private getKFactor(player: Player): number {
      // High uncertainty = bigger swings
      // Placement matches (first 10) = even bigger
      const baseFactor = 32;
      const uncertaintyMultiplier = player.sigma / 100;
      const placementMultiplier = player.gamesPlayed < 10 ? 2 : 1;
  
      return baseFactor * uncertaintyMultiplier * placementMultiplier;
    }
  
    // Detect potential smurfs
    detectSmurf(player: Player): boolean {
      if (player.gamesPlayed < 20) {
        const expectedWinRate = 0.5;
        const actualWinRate = player.wins / player.gamesPlayed;
  
        // Way too good for their rating
        if (actualWinRate > 0.9) {
          return true;
        }
      }
      return false;
    }
  }
  ```
  

## TCP for Real-Time Game State

### **Id**
tcp_for_game_state
### **Severity**
high
### **Category**
protocol
### **Symptoms**
  - Periodic freezes then catch-up
  - Rubber-banding despite good ping
  - Delays compound under packet loss
### **Problem**
  TCP guarantees order and delivery. If packet 5 is lost, packets 6-20
  are buffered until 5 arrives. For game state, old positions are
  worthless - you want latest state, not ordered history.
  
### **Solution**
  Use UDP or WebRTC DataChannel (unreliable mode) for game state.
  Use TCP/WebSocket for reliable events (chat, purchases).
  
### **Code Example**
  ```typescript
  class DualChannelNetwork {
    private reliableChannel: WebSocket; // TCP for events
    private unreliableChannel: RTCDataChannel; // UDP-like for state
  
    constructor() {
      // Reliable channel for important events
      this.reliableChannel = new WebSocket('wss://game.server/events');
  
      // Unreliable channel for game state
      this.setupWebRTCDataChannel();
    }
  
    private async setupWebRTCDataChannel() {
      const pc = new RTCPeerConnection({ /* ICE servers */ });
  
      this.unreliableChannel = pc.createDataChannel('gamestate', {
        ordered: false,    // Don't wait for missing packets
        maxRetransmits: 0  // Never retransmit (true UDP behavior)
      });
  
      this.unreliableChannel.onmessage = (event) => {
        const state = this.decode(event.data);
        // State updates - okay to miss some
        this.handleStateUpdate(state);
      };
    }
  
    // Use appropriate channel for each message type
    sendStateUpdate(state: GameState) {
      if (this.unreliableChannel.readyState === 'open') {
        // Latest state over unreliable - okay if lost
        this.unreliableChannel.send(this.encode(state));
      }
    }
  
    sendEvent(event: GameEvent) {
      // Important events over reliable
      this.reliableChannel.send(JSON.stringify(event));
    }
  }
  ```
  