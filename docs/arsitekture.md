# Arsitektur Bloomsom Engine

## 1. Ringkasan

Bloomsom Engine adalah framework server game multiplayer berbasis Go yang dirancang local-first. Tujuannya supaya developer bisa belajar dan membuat prototype server multiplayer tanpa harus menyiapkan hosting, cloud, atau server database terpisah.

Karakteristik utama:
- **Local-first.** Berjalan di localhost/LAN. Target utama Linux Ubuntu.
- **Game-agnostic.** Mendukung game real-time (tick-based) maupun turn-based (event-driven).
- **Multi-transport.** Mendukung WebSocket dan UDP. Keduanya bisa dipakai bersamaan.
- **SQLite embedded.** Database berupa satu file. Tabel auth sudah tersedia bawaan.
- **CLI-driven.** Semua operasi dijalankan lewat Cobra CLI dan dikonfigurasi lewat `bloomsom.yaml`.
- **Log wajib.** Semua aktivitas penting tercatat supaya mudah di-debug.

### Non-goals
Hal-hal berikut sengaja tidak dikerjakan:
- Scaling ke cloud atau cluster multi-server
- Matchmaking global
- Anti-cheat tingkat lanjut (client-side)

---

## 2. Cara Pakai (Developer Flow)

```text
1. Install      →  download binary rilis  ATAU  go build
2. bloomsom init   →  wizard: pilih jenis game, transport, port, DB
3. bloomsom start  →  server jalan di foreground, log tampil terus, sampai Ctrl+C
```

### Daftar perintah CLI

| Perintah | Fungsi |
|---|---|
| `bloomsom init` | Wizard setup interaktif. Versi non-interaktif: `--preset <nama> --yes` |
| `bloomsom start` | Menjalankan server sampai menerima SIGINT/SIGTERM |
| `bloomsom status` | Menampilkan config aktif, lokasi DB, tabel, dan versi skema |
| `bloomsom db migrate` | Menjalankan migrasi skema yang belum diterapkan |
| `bloomsom db status` | Menampilkan versi skema dan daftar tabel |
| `bloomsom db create-table <nama>` | Membuat tabel kustom (otomatis diberi prefix `custom_`) |
| `bloomsom user create\|list\|ban\|unban\|reset-password` | Administrasi akun pemain |
| `bloomsom version` | Menampilkan versi engine dan versi protokol |

### `start` tanpa setup: mode sandbox
Jika `bloomsom.yaml` tidak ditemukan, server tetap jalan memakai preset default (`lobby-chat`, WebSocket, `127.0.0.1:7777`, DB `./bloomsom.db`). Saat itu log menampilkan peringatan:

```text
WARN bloomsom.yaml tidak ditemukan, jalan di mode SANDBOX. Jalankan `bloomsom init` untuk setup.
```

Tabel auth tetap dibuat otomatis, sehingga register/login langsung bisa dipakai di mode sandbox.

### Menjalankan di background
`start` selalu berjalan di foreground. Untuk menjalankannya sebagai service, disediakan contoh unit systemd di `deploy/bloomsom.service`.

---

## 3. Setup Game: Preset & Custom Logic

### Preset (hasil `bloomsom init`)

| Preset | Loop | Transport | Tabel tambahan |
|---|---|---|---|
| `realtime-action` | tick 60 TPS | UDP (in-game) + WS (lobby/auth) | `player_states`, `match_results` |
| `turn-based` | event | WS | `matches`, `match_moves` |
| `lobby-chat` | event | WS | `chat_messages` |
| `custom` | dipilih manual | dipilih manual | hanya tabel bawaan |

### Logika game custom: library Go + preset
Binary hasil download tidak bisa memuat kode Go milik developer. Karena itu tersedia dua jalur:
- **Pengguna binary** memakai preset. Server bertugas sebagai relay dan menyinkronkan state secara generik.
- **Developer game** meng-import Bloomsom sebagai library, mengimplementasikan `GameMode`, lalu mem-build binary sendiri. Dengan jalur ini, server authoritative penuh, termasuk validasi aturan game.

Kontrak yang harus diimplementasikan developer:

```go
type GameMode interface {
    Name() string
    OnRoomCreate(r *Room) error
    OnJoin(r *Room, p *Player) error          // return error = tolak join
    OnLeave(r *Room, p *Player)
    OnInput(r *Room, p *Player, in Input)     // validasi & terapkan aksi pemain
    OnTick(r *Room, dt time.Duration)         // hanya dipanggil di loop tick
}
```

Contoh `main.go` milik developer:

```go
func main() {
    bloomsom.RegisterMode(&chess.Mode{})
    bloomsom.Execute() // memakai CLI yang sama: init, start, db, user
}
```

Scripting (Lua/JS) bisa ditambahkan belakangan sebagai salah satu implementasi `GameMode`, tanpa perlu mengubah engine.

### Konfigurasi `bloomsom.yaml`
Urutan prioritas: flag CLI > environment variable (`BLOOMSOM_*`) > file config > default.

```yaml
game:
  name: my-chess
  preset: turn-based
  protocol_version: 1
server:
  host: 127.0.0.1          # 0.0.0.0 = buka ke LAN (lihat bagian Keamanan)
  ws_port: 7777
  udp_port: 7778
  transports: [ws]         # ws | udp
engine:
  loop: event              # event | tick
  tick_rate: 0             # wajib > 0 jika loop: tick
  max_rooms: 100
  max_players_per_room: 8
database:
  path: ./bloomsom.db
auth:
  session_ttl: 24h
  allow_register: true
  max_login_attempts: 5    # per 15 menit per username/IP
log:
  level: info              # debug | info | warn | error
  format: text             # text | json
  file: ""                 # kosong = stdout saja
netsim:                    # simulasi jaringan buruk (untuk belajar)
  latency: 0ms
  jitter: 0ms
  loss: 0
```

---

## 4. Sistem Auth (Bawaan, Wajib)

Auth merupakan bagian inti engine. Tabelnya dibuat otomatis oleh migrasi pada `init` maupun `start`, sehingga developer tidak perlu membuatnya secara manual.

### Tabel auth

**`players`**: akun pemain.

| Kolom | Tipe | Keterangan |
|---|---|---|
| `id` | INTEGER PK | |
| `username` | TEXT UNIQUE NOT NULL | 3–32 karakter, `[a-zA-Z0-9_]`, disimpan lowercase |
| `email` | TEXT UNIQUE NULL | opsional |
| `password_hash` | TEXT NOT NULL | argon2id (format PHC string) |
| `display_name` | TEXT | |
| `role` | TEXT NOT NULL DEFAULT 'player' | `player` \| `admin` |
| `status` | TEXT NOT NULL DEFAULT 'active' | `active` \| `banned` |
| `banned_until` | DATETIME NULL | NULL = ban permanen (jika status banned) |
| `ban_reason` | TEXT NULL | |
| `created_at`, `updated_at`, `last_login_at` | DATETIME | UTC |

**`sessions`**: sesi login aktif.

| Kolom | Tipe | Keterangan |
|---|---|---|
| `id` | INTEGER PK | |
| `player_id` | INTEGER FK → players | ON DELETE CASCADE |
| `token_hash` | TEXT UNIQUE NOT NULL | SHA-256 dari token; token asli tidak disimpan |
| `client_version` | TEXT | |
| `remote_addr` | TEXT | |
| `created_at`, `expires_at`, `last_seen_at` | DATETIME | |
| `revoked_at` | DATETIME NULL | diisi saat logout/kick/ban |

**`auth_events`**: jejak audit untuk debugging dan rate limiting.

| Kolom | Tipe | Keterangan |
|---|---|---|
| `id` | INTEGER PK | |
| `player_id` | INTEGER NULL | NULL jika username tidak ditemukan |
| `username` | TEXT | username yang dicoba |
| `event` | TEXT | `register` \| `login_ok` \| `login_fail` \| `logout` \| `banned` |
| `reason` | TEXT NULL | misal `bad_password`, `rate_limited` |
| `remote_addr` | TEXT | |
| `created_at` | DATETIME | |

### Alur auth

```text
Client ──WS: register{username,password}──▶ Server  → hash argon2id → INSERT players
Client ──WS: login{username,password}─────▶ Server  → cek rate limit, status ban, password
       ◀──────── {session_token, player_id, udp_port} ── (token acak 32 byte)
Client ──UDP: [token | payload]───────────▶ Server  → verifikasi token → bind ke player
Client ──WS: logout───────────────────────▶ Server  → set revoked_at
```

Aturan auth:
- Semua paket game (WS maupun UDP) harus terautentikasi, kecuali `register`, `login`, dan `ping`.
- Karena UDP tidak memiliki koneksi, token didapat lewat login di WS, lalu disertakan pada handshake UDP.
- Pesan error login selalu generik ("username atau password salah") supaya tidak membocorkan username mana yang terdaftar.
- Jika percobaan login melebihi `max_login_attempts`, permintaan ditolak sementara dan dicatat dengan `reason=rate_limited`.
- Sesi kedaluwarsa dibersihkan secara berkala oleh goroutine background.
- Admin pertama dibuat lewat `bloomsom user create --admin` saat `init`.

---

## 5. Komponen Inti

### A. Networking
- Interface `Transport` (`Listen`, `Send`, `Broadcast`, `Close`) dengan implementasi `ws` dan `udp`.
- **Wire format:** JSON di WebSocket supaya mudah dibaca saat debugging, binary di UDP supaya ringkas. Payload UDP dijaga maksimal ±1200 byte untuk menghindari fragmentasi.
- **Envelope paket:** `type`, `seq`, `ack`, `tick`, `payload`.
- **Versi protokol:** handshake mengirim `protocol_version`. Jika versi tidak cocok, koneksi ditolak dengan pesan yang jelas.
- **Network simulator (`netsim`):** menambahkan latency, jitter, dan packet loss buatan. Tanpa fitur ini, efek netcode tidak akan terlihat di localhost.

### B. Engine & Game Loop
- **Room Manager:** membuat, menutup, dan mencari room, serta mendaftarkan pemain ke room.
- **Loop event:** room menunggu input dari channel, memanggil `OnInput`, lalu melakukan broadcast. CPU nyaris nol saat room idle.
- **Loop tick (fixed timestep):** simulasi selalu maju dengan `dt` tetap. `time.Ticker` saja tidak presisi karena bisa drift atau melewatkan tick, sehingga dipakai accumulator:
  ```text
  acc += now - last
  while acc >= dt && steps < maxCatchUp:  OnTick(dt); acc -= dt; steps++
  broadcast snapshot
  ```
  Jika satu tick melebihi budget waktu, dicatat log `WARN tick overrun`.

### C. Netcode (untuk mode realtime)
Tanggung jawab di sisi server:
- Setiap input membawa `seq`. Server membalas dengan `ack` berisi seq input terakhir yang diproses, supaya client bisa melakukan reconciliation.
- Setiap snapshot diberi nomor `tick` server, supaya client bisa melakukan interpolation.
- Server menyimpan history posisi (misal 1 detik terakhir) untuk lag compensation.

Client-side prediction dan interpolation adalah tanggung jawab client. Server tetap authoritative dan tidak pernah mempercayai state yang dikirim client.

### D. Model Konkurensi
- **Satu goroutine per room** menjadi pemilik tunggal state room (pola actor). Input masuk lewat channel, sehingga state game tidak memerlukan mutex.
- **Per koneksi:** satu goroutine pembaca dan satu goroutine penulis, dengan antrean kirim berukuran terbatas.
- **Client lambat:** jika antrean kirimnya penuh, paket snapshot lama di-drop. Jika tetap penuh, koneksi diputus. Tujuannya supaya satu client lambat tidak memperlambat seluruh room.
- Semua goroutine terikat pada `context.Context` dari `start` supaya bisa dihentikan dengan bersih.

### E. Storage (SQLite)
- **Driver:** `modernc.org/sqlite` (pure Go, tanpa CGO), sehingga build di Ubuntu cukup dengan `go build`.
- **Mode:** `journal_mode=WAL`, `busy_timeout=5000`, `foreign_keys=ON`.
- **Satu goroutine writer** dengan batching. Tick loop tidak pernah menulis langsung ke DB. Perubahan state dikirim ke antrean writer.
- **Migrasi berversi:** tabel `schema_migrations(version, applied_at)`. Migrasi disimpan sebagai file SQL yang di-embed (`embed.FS`) dan dijalankan berurutan di dalam transaksi.
- **Tabel bawaan (selalu ada):** `players`, `sessions`, `auth_events`, `game_rooms`, `schema_migrations`.
- **Tabel preset:** dibuat sesuai preset yang dipilih (lihat bagian 3).
- **Tabel kustom:** `db create-table` memvalidasi nama tabel/kolom dengan regex `^[a-z_][a-z0-9_]{0,62}$`, membatasi tipe ke `INTEGER|REAL|TEXT|BLOB`, dan otomatis memberi prefix `custom_`. Identifier tidak bisa di-parameter-kan dalam SQL, sehingga validasi ini wajib untuk mencegah SQL injection dan mencegah penimpaan tabel bawaan.

---

## 6. Logging (Wajib)

- Memakai `log/slog` dari standard library.
- Output selalu ke stdout, dan opsional juga ke file (`log.file`).
- Format `text` untuk terminal dan `json` untuk diolah dengan tools (misal `jq`).
- Setiap log membawa atribut konteks yang relevan: `room`, `player`, `proto`, `remote`.

Pembagian level log:

| Level | Isi |
|---|---|
| ERROR | Kegagalan yang memengaruhi server: DB error, listen gagal, panic room yang di-recover |
| WARN | Tick overrun, client lambat di-drop, login rate-limited, mode sandbox, versi protokol tidak cocok |
| INFO | Start/stop, transport listening, connect/disconnect, login/logout, join/leave room, room dibuat/ditutup |
| DEBUG | Detail per paket dan per tick. Tidak dicatat di INFO karena akan membanjiri log di 60 TPS |

Aturan:
- Password, token, dan hash tidak pernah ditulis ke log.
- Panic di dalam room di-recover, dicatat sebagai ERROR beserta stack trace, lalu room tersebut ditutup tanpa menjatuhkan seluruh server.
- Metrik ringan dicatat setiap 30 detik di level INFO: jumlah room, jumlah pemain, paket/detik, dan durasi tick p99.

Contoh output:

```text
INFO bloomsom starting        version=0.1.0 game=my-chess preset=turn-based loop=event
INFO database ready           path=./bloomsom.db schema_version=3
INFO transport listening      proto=ws addr=127.0.0.1:7777
INFO player logged in         player=budi remote=127.0.0.1:51234
INFO player joined room       player=budi room=r-01
WARN login rate limited       username=admin remote=127.0.0.1:51300
WARN tick overrun             room=r-02 took=21ms budget=16ms
INFO shutdown signal received signal=SIGINT
INFO saving state             rooms=2
INFO bloomsom stopped         uptime=1h02m
```

### Graceful shutdown
Saat menerima SIGINT/SIGTERM, server menjalankan langkah berikut secara berurutan:
1. Berhenti menerima koneksi baru.
2. Mengirim pesan `server_closing` ke semua client.
3. Menghentikan loop room dan menyimpan state.
4. Mem-flush antrean writer DB.
5. Menutup koneksi dan database, lalu exit.

Jika proses shutdown melebihi 10 detik, server dihentikan paksa dengan log ERROR.

---

## 7. Keamanan

- **Default bind `127.0.0.1`.** Akses LAN harus dibuka secara eksplisit dengan `server.host: 0.0.0.0`, dan saat itu muncul log WARN.
- **Enkripsi:** WebSocket berjalan tanpa TLS secara default, sehingga password terkirim tanpa enkripsi. Kondisi ini aman di localhost, tetapi berisiko di LAN bersama. Tersedia opsi `server.tls_cert` dan `server.tls_key` untuk mengaktifkan WSS.
- **Password** di-hash dengan argon2id. **Token** berupa 32 byte acak dari `crypto/rand`, dan yang disimpan di DB hanya hash SHA-256-nya.
- **Query SQL** selalu memakai parameter binding. Identifier tabel kustom divalidasi dengan whitelist (lihat bagian 5E).
- **Batas ukuran paket** diterapkan di WS maupun UDP untuk mencegah paket raksasa menghabiskan memori.
- **Semua input client** divalidasi server melalui `GameMode.OnInput`.

---

## 8. Struktur Direktori

API publik (`GameMode`, `Room`, `Player`) harus bisa di-import dari luar module. Karena Go melarang import package `internal/` dari module lain, API publik ditaruh di `engine/`, sedangkan detail implementasi tetap di `internal/`.

```text
bloomsom-engine/
├── main.go                   # entry point binary bawaan
├── bloomsom.go               # API root: Execute(), RegisterMode()
├── cmd/                      # Cobra: init, start, status, db, user, version
├── engine/                   # PUBLIK: GameMode, Room, Player, Input, Server
├── presets/                  # PUBLIK: realtime-action, turn-based, lobby-chat
├── internal/
│   ├── config/               # Viper + validasi bloomsom.yaml
│   ├── logging/              # setup slog (text/json, file)
│   ├── auth/                 # register, login, session, rate limit, argon2id
│   ├── network/
│   │   ├── transport.go      # interface Transport
│   │   ├── ws/
│   │   ├── udp/
│   │   └── netsim/           # latency / jitter / loss buatan
│   ├── room/                 # room manager, loop event & loop tick
│   └── storage/
│       ├── sqlite.go         # koneksi, pragma, writer goroutine
│       ├── migrations/       # *.sql yang di-embed
│       └── repo/             # players, sessions, rooms, custom tables
├── examples/
│   ├── web-client/           # client JS untuk browser (WS)
│   ├── go-client/            # client CLI
│   └── bot/                  # bot untuk load test
├── deploy/bloomsom.service   # contoh unit systemd
└── docs/arsitekture.md
```

> **Catatan:** module path saat ini adalah `bloomsom`. Supaya bisa di-import sebagai library, module path perlu diganti menjadi path lengkap, misal `github.com/<user>/bloomsom-engine`.

---

## 9. Siklus Hidup Pemain

```text
[ Client ]
    │ 1. connect WS
    ▼
[ Transport ] ── 2. register/login ──▶ [ auth ] ──▶ SQLite (players, sessions, auth_events)
    │ ◀── session_token
    │ 3. (realtime) handshake UDP dengan token
    ▼
[ Room Manager ] ── 4. join room ──▶ GameMode.OnJoin
    │
    ▼ 5. main loop
 ┌──────────────┴──────────────┐
 ▼                             ▼
[ Loop tick ]              [ Loop event ]
 input → OnTick → snapshot   input → OnInput → broadcast
 └──────────────┬──────────────┘
                ▼ 6. leave / disconnect / shutdown
      GameMode.OnLeave → writer queue → SQLite
```

---

## 10. Roadmap

| Tahap | Isi | Selesai jika |
|---|---|---|
| 1 | Config, logging, SQLite, migrasi, tabel auth, `init`/`start`/`status`/`db` | `bloomsom init` lalu `bloomsom start` jalan dan menampilkan log |
| 2 | Auth (register/login/session), `user` CLI | Login lewat WS berhasil dan tercatat di `auth_events` |
| 3 | WS transport, room manager, loop event, preset `lobby-chat` | 2 client browser bisa saling chat di satu room |
| 4 | Preset `turn-based` + API `GameMode` publik + contoh game | Contoh tic-tac-toe berjalan sebagai library |
| 5 | Loop tick, UDP transport, preset `realtime-action` | Posisi 2 client tersinkron di 60 TPS |
| 6 | Netcode (seq/ack, snapshot, lag compensation) + `netsim` | Gerakan tetap mulus dengan `netsim.latency=100ms` |

---

## 11. Verifikasi & Testing

- **Unit test:** packet encoding, validasi config, validasi identifier tabel kustom, hashing password, dan rate limit.
- **Storage test:** migrasi dijalankan pada SQLite file sementara (`t.TempDir()`). Migrasi harus idempotent, sehingga `start` yang dijalankan dua kali tidak menimbulkan error.
- **Loop test:** loop tick diuji dengan clock yang bisa diinjeksi (atau `testing/synctest`) supaya deterministik.
- **Concurrency:** seluruh test dijalankan dengan `go test -race`.
- **Integration test:** 2 client (WS dan UDP) menjalankan alur login → join → kirim input → terima broadcast.
- **Load test:** bot di `examples/bot` mensimulasikan N pemain. Durasi tick p99 dipantau lewat log metrik.
