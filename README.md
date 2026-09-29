# Bloomsom Engine

> A local-first multiplayer game server framework written in Go, designed for learning, prototyping, and building real-time netcode without cloud or hosting overhead.

[![Go Version](https://img.shields.io/badge/Go-1.22%2B-00ADD8?style=flat&logo=go)](https://golang.org)
[![Database](https://img.shields.io/badge/Database-SQLite-003B57?style=flat&logo=sqlite)](https://sqlite.org)
[![Platform](https://img.shields.io/badge/Platform-Linux%20Ubuntu%20%7C%20POSIX-E95420?style=flat&logo=ubuntu)](https://ubuntu.com)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Documentation](https://img.shields.io/badge/Docs-Architecture-blue.svg)](docs/arsitekture.md)

---

## Overview

Building multiplayer game servers often introduces friction early in development: setting up Docker containers, managing external database instances, and orchestrating cloud environments before a single packet is synchronized.

Bloomsom Engine provides a local-first alternative:
- Compiles into a single standalone Go binary.
- Uses an embedded SQLite database (zero external installation or database daemon required).
- Runs directly on localhost or local area networks (LAN), optimized for Linux (Ubuntu) and POSIX environments.
- Features continuous, structured console logging out of the box for clear real-time debugging.

---

## Key Features

- **Game-Agnostic with Dual Loop Modes:**
  - **Tick-Based Loop (20-60 TPS):** Fixed timestep accumulator loop for real-time action titles (arena shooters, movement synchronization, physics-driven gameplay).
  - **Event-Driven Loop:** Resource-efficient reactive loop triggered only upon incoming player actions (turn-based strategy, card games, chess, lobby chat).
- **Multi-Transport Networking:**
  - **WebSocket (TCP):** Human-readable debugging, compatible with web browsers, Postman, and CLI test tools.
  - **UDP / KCP:** Low-overhead packet transmission for high-frequency state updates.
  - **Hybrid Dual-Channel Support:** Run WebSocket for authentication/lobby and UDP for fast in-game replication simultaneously.
- **Built-In Mandatory Authentication System:**
  - Automatic schema initialization for `players`, `sessions`, and `auth_events`.
  - Secure password hashing using Argon2id.
  - Session tokens generated via cryptographically secure random bytes (`crypto/rand`) with SHA-256 hash storage.
  - Rate limiting and brute-force mitigations.
  - CLI user administration (`bloomsom user create`, `list`, `ban`, `unban`, `reset-password`).
- **Embedded SQLite with Dynamic Table Management:**
  - Write-Ahead Logging (WAL) enabled by default for concurrent read/write throughput.
  - Dedicated background writer routine preventing database I/O from blocking tick loops.
  - DDL generator for creating custom tables via CLI with strict identifier whitelisting to eliminate SQL injection risks.
- **Continuous Structured Logging (`log/slog`):**
  - Always-on console logging during runtime.
  - Colorized terminal output for interactive use, with optional JSON output for machine parsing.
  - Tracks server lifecycle events, network handshakes, room occupancy, and tick overruns.
- **Built-In Network Simulator (`netsim`):**
  - Simulates artificial latency, jitter, and packet loss on localhost to test client-side prediction, server reconciliation, and lag compensation algorithms.

---

## Quickstart

### Prerequisites
- Go 1.22 or higher
- Linux (Ubuntu recommended), macOS, or Windows

### 1. Build the Binary
Clone the repository and compile the executable:
```bash
git clone https://github.com/adnannpm/bloomsom-engine.git
cd bloomsom-engine
go build -o bloomsom main.go
```

### 2. Initialize the Project (Interactive Setup Wizard)
Run the initialization command to select your game type, transport protocol, and network ports:
```bash
./bloomsom init
```

For non-interactive or automated environments:
```bash
./bloomsom init --preset turn-based --yes
```

This generates the configuration file `bloomsom.yaml` and initializes `bloomsom.db` with all required authentication and system tables.

### 3. Start the Server
```bash
./bloomsom start
```

The server runs in the foreground, streaming live logs to standard output until terminated via `Ctrl+C` (triggering graceful shutdown).

**Sandbox Mode:**  
Running `./bloomsom start` without prior initialization automatically launches the engine in Sandbox Mode using safe fallback defaults (WebSocket on `127.0.0.1:7777` with auto-initialized SQLite).

---

## CLI Reference

```text
Core Commands:
  bloomsom init            Run the interactive configuration wizard
  bloomsom start           Start the game server (foreground execution with live logs)
  bloomsom status          Display current configuration, database state, and runtime metrics
  bloomsom version         Display engine and network protocol versions

Database Management:
  bloomsom db status       Check SQLite database connectivity and active tables
  bloomsom db migrate      Execute pending database schema migrations
  bloomsom db create-table Scaffold a new custom table (automatically prefixed with custom_)

User & Authentication Management:
  bloomsom user create     Register a new player account (use --admin for administrative role)
  bloomsom user list       List all registered accounts
  bloomsom user ban        Ban a player account (supports duration and reason flags)
  bloomsom user unban      Revoke an active ban on an account
  bloomsom user reset-pass Reset the password of an existing player account
```

---

## Configuration (`bloomsom.yaml`)

```yaml
game:
  name: "my-game"
  preset: "realtime-action" # Options: realtime-action | turn-based | lobby-chat | custom
  protocol_version: 1

server:
  host: "127.0.0.1"         # Set to 0.0.0.0 to allow LAN access
  ws_port: 7777
  udp_port: 7778
  transports:
    - ws
    - udp

engine:
  loop: "tick"              # Options: tick | event
  tick_rate: 60             # Ticks per second (required when loop is tick)
  max_rooms: 100
  max_players_per_room: 16

database:
  path: "./bloomsom.db"

auth:
  session_ttl: 24h
  allow_register: true
  max_login_attempts: 5

log:
  level: "info"             # debug | info | warn | error
  format: "text"            # text | json
  file: ""                  # Leave empty to log solely to stdout

netsim:                     # Local network simulation (for netcode testing)
  latency: 0ms
  jitter: 0ms
  loss: 0
```

---

## Custom Game Logic (`GameMode`)

To implement authoritative server rules (combat math, board rules, movement validation), import Bloomsom as a Go library and implement the `engine.GameMode` interface:

```go
package main

import (
	"time"

	"bloomsom"
	"bloomsom/engine"
)

type CustomGameMode struct{}

func (m *CustomGameMode) Name() string {
	return "deathmatch"
}

func (m *CustomGameMode) OnRoomCreate(r *engine.Room) error {
	return nil
}

func (m *CustomGameMode) OnJoin(r *engine.Room, p *engine.Player) error {
	return nil
}

func (m *CustomGameMode) OnLeave(r *engine.Room, p *engine.Player) {
}

func (m *CustomGameMode) OnInput(r *engine.Room, p *engine.Player, in engine.Input) {
	// Validate client inputs and execute state transitions (Server-Authoritative)
}

func (m *CustomGameMode) OnTick(r *engine.Room, dt time.Duration) {
	// Advance physics, timers, and state simulations every tick
}

func main() {
	bloomsom.RegisterMode(&CustomGameMode{})
	bloomsom.Execute()
}
```

---

## Directory Layout

```text
bloomsom-engine/
├── cmd/                      # Cobra CLI command definitions (root, start, init, db, user)
├── engine/                   # Public API contracts (GameMode, Room, Player, Input, Server)
├── presets/                  # Preconfigured game modes (realtime-action, turn-based, lobby-chat)
├── internal/                 # Private server subsystems
│   ├── auth/                 # Authentication, Argon2id hashing, session token verification
│   ├── config/               # Viper configuration parsing and validation
│   ├── logging/              # Slog configuration and writer pipelines
│   ├── network/              # WebSocket, UDP transports, and network simulator
│   ├── room/                 # Room management, tick loops, and event dispatchers
│   └── storage/              # SQLite connection pool, embedded SQL migrations, repositories
├── docs/
│   └── arsitekture.md        # Detailed technical architecture specification
├── examples/                 # Minimal client implementations for testing
├── .gitignore
├── LICENSE                   # MIT License
├── go.mod
├── go.sum
└── main.go                   # Default binary entry point
```

---

## Technical Documentation

For in-depth explanations covering the concurrency architecture (actor-per-room pattern), SQLite schema details, packet envelopes, lag compensation mechanics, and the phased development roadmap, refer to:
- [Architecture Specification (`docs/arsitekture.md`)](docs/arsitekture.md)

---

## License

This project is licensed under the MIT License. See the [LICENSE](LICENSE) file for details.
