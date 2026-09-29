# Game Networking - Validations

## Client Position Trust

### **Id**
trusting_client_position
### **Description**
Accepting position data directly from client without validation
### **Severity**
critical
### **Category**
security
### **Pattern**
  player\.(position|pos|location|transform)\s*=\s*(data|packet|message|msg|payload|event\.data)\.
  
### **File Patterns**
  - **/*.ts
  - **/*.js
  - **/*.cs
  - **/*.cpp
### **Message**
  CRITICAL: Never trust client-reported position!
  Server must simulate movement from inputs.
  
### **Fix Suggestion**
  Instead of:
    player.position = data.position;
  
  Do:
    // Client sends inputs, not position
    const input = validateInput(data.input);
    player.position = simulateMovement(player, input);
  
### **Learn More**
https://gafferongames.com/post/client_server_connection/

## Client Health Trust

### **Id**
trusting_client_health
### **Description**
Accepting health/damage values directly from client
### **Severity**
critical
### **Category**
security
### **Pattern**
  (player|entity|character)\.(health|hp|damage|armor)\s*=\s*(data|packet|message|msg|payload)\.
  
### **File Patterns**
  - **/*.ts
  - **/*.js
  - **/*.cs
### **Message**
  CRITICAL: Never trust client-reported health!
  Godmode hacks exploit this vulnerability.
  
### **Fix Suggestion**
  Server calculates all damage. Client sends attack actions,
  server validates hit and calculates damage.
  

## Client Hit Detection

### **Id**
client_side_hit_detection
### **Description**
Processing hit/damage events reported by client
### **Severity**
critical
### **Category**
security
### **Pattern**
  on\(['"]?(hit|damage|kill|headshot)['"]?,\s*(?:function\s*)?\([^)]*\)\s*(?:=>)?\s*\{[^}]*apply(?:Damage|Hit)
  
### **File Patterns**
  - **/*.ts
  - **/*.js
### **Message**
  CRITICAL: Client-side hit detection enables aimbots!
  Server must perform all hit detection.
  
### **Fix Suggestion**
  Client sends: { type: 'shoot', origin, direction, timestamp }
  Server performs: raycast with lag compensation, validates hit
  

## Missing Input Rate Limiting

### **Id**
no_input_rate_limit
### **Description**
Processing client inputs without rate limiting
### **Severity**
high
### **Category**
security
### **Pattern**
  on\(['"]?(input|move|action)['"]?,.*\{(?!.*(?:rateLimit|throttle|lastInput|inputCount))
  
### **File Patterns**
  - **/*.ts
  - **/*.js
### **Message**
  HIGH: Missing input rate limiting allows speed hacks!
  Limit inputs per second (e.g., max 64/second).
  
### **Fix Suggestion**
  const inputCounts = new Map();
  const MAX_INPUTS_PER_SEC = 64;
  
  socket.on('input', (data) => {
    const count = inputCounts.get(socket.id) ?? 0;
    if (count >= MAX_INPUTS_PER_SEC) {
      flagPlayer(socket.id, 'input_flood');
      return;
    }
    inputCounts.set(socket.id, count + 1);
    // process input
  });
  

## Non-Deterministic Random

### **Id**
math_random_in_simulation
### **Description**
Using Math.random() in game simulation code
### **Severity**
critical
### **Category**
determinism
### **Pattern**
  Math\.random\(\)
  
### **File Patterns**
  - **/game*.ts
  - **/simulation*.ts
  - **/world*.ts
  - **/entity*.ts
  - **/physics*.ts
### **Exclude Patterns**
  - **/*.test.*
  - **/*.spec.*
  - **/ui/**
### **Message**
  CRITICAL: Math.random() breaks determinism!
  Use seeded PRNG for lockstep/rollback games.
  
### **Fix Suggestion**
  class SeededRandom {
    constructor(private seed: number) {}
  
    next(): number {
      let t = this.seed += 0x6D2B79F5;
      t = Math.imul(t ^ t >>> 15, t | 1);
      t ^= t + Math.imul(t ^ t >>> 7, t | 61);
      return ((t ^ t >>> 14) >>> 0) / 4294967296;
    }
  }
  
  const rng = new SeededRandom(sharedSeed);
  const value = rng.next(); // Deterministic!
  

## System Time in Simulation

### **Id**
date_now_in_simulation
### **Description**
Using Date.now() or new Date() in deterministic simulation
### **Severity**
high
### **Category**
determinism
### **Pattern**
  (Date\.now\(\)|new Date\(\))
  
### **File Patterns**
  - **/simulation*.ts
  - **/game-state*.ts
  - **/world*.ts
  - **/tick*.ts
### **Message**
  HIGH: System time varies between machines!
  Use synchronized game tick for deterministic simulation.
  
### **Fix Suggestion**
  // Use game tick instead of real time
  class GameClock {
    private tick = 0;
    private tickRate = 60;
  
    getCurrentTick(): number {
      return this.tick;
    }
  
    getGameTime(): number {
      return this.tick / this.tickRate;
    }
  }
  

## Floating Point Equality

### **Id**
float_equality
### **Description**
Comparing floats with == or === in deterministic code
### **Severity**
medium
### **Category**
determinism
### **Pattern**
  ===?\s*[0-9]+\.[0-9]+|[0-9]+\.[0-9]+\s*===?
  
### **File Patterns**
  - **/simulation*.ts
  - **/physics*.ts
  - **/collision*.ts
### **Message**
  MEDIUM: Float comparison varies across platforms!
  Use epsilon comparison or fixed-point math.
  
### **Fix Suggestion**
  const EPSILON = 0.0001;
  
  function floatEquals(a: number, b: number): boolean {
    return Math.abs(a - b) < EPSILON;
  }
  

## Non-Deterministic Iteration

### **Id**
unordered_iteration
### **Description**
Iterating objects/maps without sorting in deterministic code
### **Severity**
high
### **Category**
determinism
### **Pattern**
  for\s*\(\s*(?:const|let|var)\s+\w+\s+in\s+\w+|Object\.keys\([^)]+\)\.(?:forEach|map)|\.forEach\(
  
### **File Patterns**
  - **/simulation*.ts
  - **/tick*.ts
  - **/lockstep*.ts
### **Message**
  HIGH: Iteration order may vary!
  Sort before iterating for determinism.
  
### **Fix Suggestion**
  // BAD
  for (const id in players) { ... }
  
  // GOOD
  const sortedIds = Object.keys(players).sort();
  for (const id of sortedIds) { ... }
  

## JSON for Network Packets

### **Id**
json_stringify_for_packets
### **Description**
Using JSON.stringify for game state packets
### **Severity**
medium
### **Category**
performance
### **Pattern**
  JSON\.stringify\([^)]+\).*(?:send|emit|broadcast)
  
### **File Patterns**
  - **/network*.ts
  - **/server*.ts
  - **/socket*.ts
### **Message**
  MEDIUM: JSON is verbose for game state.
  Use binary protocol (msgpack, protobuf) at scale.
  
### **Fix Suggestion**
  // Use msgpack for 30-50% smaller payloads
  import { encode, decode } from '@msgpack/msgpack';
  
  socket.send(encode(gameState));
  

## Broadcasting Full State

### **Id**
full_state_broadcast
### **Description**
Sending complete game state to all clients every tick
### **Severity**
high
### **Category**
performance
### **Pattern**
  broadcast\(.*gameState|clients\.forEach.*send.*(?:state|entities|world)
  
### **File Patterns**
  - **/server*.ts
  - **/network*.ts
### **Message**
  HIGH: Full state broadcast doesn't scale!
  Use delta compression and interest management.
  
### **Fix Suggestion**
  // Delta compression
  const delta = computeDelta(lastAckedState, currentState);
  client.send(compressDelta(delta));
  
  // Interest management
  const nearbyEntities = spatialGrid.query(player.position, viewRadius);
  client.send(filterEntities(state, nearbyEntities));
  

## Blocking I/O in Game Loop

### **Id**
blocking_io_in_tick
### **Description**
Synchronous I/O in tick/update function
### **Severity**
critical
### **Category**
performance
### **Pattern**
  (readFileSync|writeFileSync|execSync|query\([^)]*\)(?!\.then))
  
### **File Patterns**
  - **/tick*.ts
  - **/update*.ts
  - **/game-loop*.ts
### **Message**
  CRITICAL: Blocking I/O stalls entire game!
  Use async patterns or background workers.
  
### **Fix Suggestion**
  // Move I/O outside tick loop
  class AsyncSaveManager {
    private pendingSaves: GameState[] = [];
  
    queueSave(state: GameState) {
      this.pendingSaves.push(structuredClone(state));
    }
  
    async processSaves() {
      while (this.pendingSaves.length > 0) {
        const state = this.pendingSaves.shift();
        await saveToDatabase(state);
      }
    }
  }
  

## Unbounded State History

### **Id**
unbounded_history
### **Description**
Pushing to history array without cleanup
### **Severity**
high
### **Category**
performance
### **Pattern**
  history\.push\([^)]+\)(?!.*(?:shift|splice|slice|\.length\s*>))
  
### **File Patterns**
  - **/*.ts
  - **/*.js
### **Message**
  HIGH: Unbounded history causes memory leak!
  Cap history buffer size.
  
### **Fix Suggestion**
  const MAX_HISTORY = 300; // ~5 seconds at 60 tick
  
  function addToHistory(state: GameState) {
    history.push(state);
    while (history.length > MAX_HISTORY) {
      history.shift();
    }
  }
  

## Missing Sequence Numbers

### **Id**
no_sequence_number
### **Description**
Network packets without sequence numbers
### **Severity**
medium
### **Category**
protocol
### **Pattern**
  send\(\s*\{(?!.*(?:seq|sequence|tick|frame))
  
### **File Patterns**
  - **/network*.ts
  - **/socket*.ts
### **Message**
  MEDIUM: Packets need sequence numbers!
  Required for ordering, deduplication, and ack.
  
### **Fix Suggestion**
  let sequenceNumber = 0;
  
  function sendPacket(data: any) {
    socket.send({
      seq: sequenceNumber++,
      timestamp: Date.now(),
      ...data
    });
  }
  

## Missing Acknowledgment System

### **Id**
no_packet_ack
### **Description**
Sending reliable data without acknowledgment
### **Severity**
medium
### **Category**
protocol
### **Pattern**
  send\(.*type:\s*['"](?:spawn|despawn|item|inventory|chat)['"](?!.*ack)
  
### **File Patterns**
  - **/network*.ts
### **Message**
  MEDIUM: Important events need acknowledgment!
  Implement ack/retry for reliable delivery.
  
### **Fix Suggestion**
  class ReliableChannel {
    private pending = new Map<number, { data: any, retries: number }>();
    private seq = 0;
  
    send(data: any) {
      const id = this.seq++;
      this.pending.set(id, { data, retries: 0 });
      this.socket.send({ ...data, reliableId: id });
      this.scheduleRetry(id);
    }
  
    onAck(id: number) {
      this.pending.delete(id);
    }
  
    private scheduleRetry(id: number) {
      setTimeout(() => {
        const pending = this.pending.get(id);
        if (pending && pending.retries < 5) {
          pending.retries++;
          this.socket.send({ ...pending.data, reliableId: id });
          this.scheduleRetry(id);
        }
      }, 100);
    }
  }
  

## Missing Client Timestamp

### **Id**
missing_timestamp
### **Description**
Shot/action packets without client timestamp
### **Severity**
high
### **Category**
lag_compensation
### **Pattern**
  send\(\s*\{[^}]*type:\s*['"](?:shoot|fire|attack|ability)['"][^}]*\}(?!.*timestamp)
  
### **File Patterns**
  - **/client*.ts
  - **/input*.ts
### **Message**
  HIGH: Actions need timestamps for lag compensation!
  Server uses timestamp to rewind world state.
  
### **Fix Suggestion**
  function sendShot(origin: Vec3, direction: Vec3) {
    socket.send({
      type: 'shoot',
      origin,
      direction,
      clientTimestamp: Date.now(),  // When player clicked
      clientTick: currentTick        // Game tick when fired
    });
  }
  

## Rendering Server State Directly

### **Id**
direct_state_render
### **Description**
Rendering remote entities from server state without interpolation
### **Severity**
medium
### **Category**
visual
### **Pattern**
  entity\.(position|transform)\s*=\s*serverState\.|render\(serverState\)
  
### **File Patterns**
  - **/render*.ts
  - **/client*.ts
  - **/game*.ts
### **Message**
  MEDIUM: Direct rendering causes jitter!
  Buffer and interpolate for smooth visuals.
  
### **Fix Suggestion**
  class RemoteEntityRenderer {
    private stateBuffer: StateSnapshot[] = [];
    private renderDelay = 100; // ms behind real-time
  
    onServerState(state: EntityState, serverTime: number) {
      this.stateBuffer.push({ state, serverTime });
    }
  
    getInterpolatedState(): EntityState {
      const renderTime = Date.now() - this.renderDelay;
      // Interpolate between buffered states
      return this.interpolate(renderTime);
    }
  }
  

## Hardcoded Matchmaking Parameters

### **Id**
hardcoded_matchmaking
### **Description**
Magic numbers in matchmaking without configuration
### **Severity**
low
### **Category**
matchmaking
### **Pattern**
  (?:ratingRange|skillDiff|queueTime|expandRate)\s*[=:]\s*\d+(?!\s*[,}])
  
### **File Patterns**
  - **/matchmaking*.ts
  - **/matchmaker*.ts
### **Message**
  LOW: Hardcoded matchmaking values hurt tuning.
  Use configuration for easy adjustment.
  
### **Fix Suggestion**
  const MATCHMAKING_CONFIG = {
    initialRatingRange: 100,
    expandRatePerSecond: 10,
    maxRatingRange: 500,
    maxQueueTimeSeconds: 120,
    teamSizeBalance: true
  };
  

## Missing TURN Server Fallback

### **Id**
missing_turn_fallback
### **Description**
WebRTC without TURN server configuration
### **Severity**
high
### **Category**
connectivity
### **Pattern**
  RTCPeerConnection\(\s*\{[^}]*iceServers:\s*\[[^\]]*stun[^\]]*\](?![^\]]*turn)
  
### **File Patterns**
  - **/webrtc*.ts
  - **/p2p*.ts
  - **/connection*.ts
### **Message**
  HIGH: STUN alone fails for symmetric NAT!
  ~15% of players need TURN relay fallback.
  
### **Fix Suggestion**
  const config = {
    iceServers: [
      { urls: 'stun:stun.l.google.com:19302' },
      {
        urls: 'turn:your-server.com:3478',
        username: 'user',
        credential: 'pass'
      }
    ]
  };
  