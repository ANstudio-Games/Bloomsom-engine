# Game Server Architecture Design: Bloomsom Engine

- **Date:** 2026-09-29
- **Status:** Approved
- **Repository:** `bloomsom-engine`

---

## 1. Problem & Intended Outcome
- **Problem:** Developer membutuhkan game server lokal yang fleksibel untuk belajar dan membangun prototype multiplayer game tanpa kerumitan hosting, cloud compute, atau setup database eksternal.
- **Intended Outcome:** Framework game server berbasis Go yang *local-first*, mendukung berbagai tipe game (real-time tick rate & event-driven turn-based), multi-transport (WebSocket & UDP/KCP), dengan embedded SQLite dan manajemen tabel bawaan serta kustom via CLI Cobra.

---

## 2. Chosen Approach
**Hybrid Layered Clean Architecture with Multi-Transport & Multi-Loop:**
- **Core Domain (`internal/core`):** Entitas murni (`Player`, `Room`, `Packet`) tanpa ketergantungan transport/database.
- **Transport Abstraction (`internal/network`):** Interface `Transport` yang mengabstraksi komunikasi (WebSocket dan UDP/KCP).
- **Engine Loops (`internal/engine`):** Mendukung dua mode:
  1. *Tick-based* (fixed 20-60 TPS) untuk game aksi real-time.
  2. *Event-driven* untuk game santai/turn-based hemat sumber daya CPU.
- **Storage Layer (`internal/storage`):** Embedded SQLite (`bloomsom.db`) dengan command CLI untuk DDL tabel default dan dinamis.
- **CLI (`cmd/`):** Command Cobra (`start`, `db init`, `db create-table`, `db status`).

---

## 3. Rejected Alternatives
1. **Monolithic WebSocket-Only Engine:**
   - *Alasan ditolak:* Menutup kemungkinan game aksi berkecepatan tinggi yang memerlukan packet sequencing dan latensi rendah khas UDP.
2. **Hardcore Dedicated UDP Engine Only:**
   - *Alasan ditolak:* Terlalu tinggi kurva belajarnya untuk game turn-based / kartu / chat, dan menyulitkan pengujian cepat via web browser atau HTTP/WS debugging tools.

---

## 4. Architecture Diagram

```text
+-------------------------------------------------------------+
|                     CLI Interface (cmd/)                    |
|      bloomsom start  |  bloomsom db (init, create-table)    |
+------------------------------+------------------------------+
                               |
                               v
+-------------------------------------------------------------+
|                  Runtime Engine (internal/engine)           |
|  +------------------------+      +------------------------+ |
|  |     Room Manager       |      | Player Session Registry| |
|  +------------------------+      +------------------------+ |
|  | Tick Loop (Action/FPS) |  OR  | Event Loop (Turn-Based)| |
|  +------------------------+      +------------------------+ |
+------------------------------+------------------------------+
         |                                           |
         v                                           v
+-------------------------+             +-------------------------+
| Network Transport Layer |             | Storage Layer (SQLite)  |
| - WebSocket (TCP)       |             | - Schema Migrations     |
| - UDP / KCP             |             | - Built-in & Custom DDL |
+-------------------------+             +-------------------------+
```

---

## 5. Constraints & Assumptions
- Target OS utama: Linux Ubuntu / POSIX (mendukung cross-platform lokal).
- SQLite menggunakan file lokal `bloomsom.db` (zero external configuration).
- Go standard concurrency model: goroutine per connection / room, channel-based message passing.

---

## 6. Verification Strategy
1. **Storage Verification:** Jalankan `bloomsom db init`, verifikasi file SQLite terbuat dan skema tabel valid.
2. **Loop Verification:** Benchmark CPU dan stabilitas interval tick pada 30 TPS dan 60 TPS.
3. **Transport Verification:** Jalankan koneksi uji coba WebSocket dan UDP loopback lokal (127.0.0.1) untuk memastikan round-trip paket data.
