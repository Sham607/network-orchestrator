# ADR-003 — System Architecture & UniFi Integration Boundary

- **Status:** Accepted (dipindah ke `docs/adr/003-architecture.md` saat bootstrap ZENI-7; menunggu konfirmasi user atas keputusan arsitektur)
- **Date:** 2026-10-06
- **Deciders:** Tech Lead
- **Konteks epic:** [ZENI-1] Network Orchestrator — Foundation: Stack, Architecture & Data Model
- **Constraint dari ADR-002 (ZENI-3):** Go 1.23 + `chi` + `sqlc`/`pgx`, PostgreSQL 16 (`goose`), React SPA + Vite, polling (tanpa WebSocket/SSE), Docker compose + Caddy, session cookie + RBAC (viewer/operator/admin).

## Konteks

Kita membangun control plane internal single-tenant untuk membaca state jaringan UniFi, menyimpannya sebagai cache + audit, dan menyajikannya di dashboard operator. Beban kerjanya sempit: satu controller (lokal atau cloud — masih open question di ZENI-2), beberapa site, puluhan device/client, dan **satu-satunya sumber data eksternal adalah UniFi**.

Dua risiko arsitektural yang harus ditutup ADR ini:

1. **Kopling ke UniFi.** Bentuk API UniFi bisa bergeser antar model/versi controller. Kalau panggilan UniFi tersebar di banyak modul, setiap perubahan API menjadi perubahan lintas kode.
2. **Kredensial jaringan.** API key UniFi memberi akses ke jaringan produksi kantor. Permukaan tempat kredensial bisa bocor harus sesempit mungkin.

Prinsip yang mengikat: modular monolith dulu; contract-first; zero-trust credential (API key tidak pernah di git/log/DB plaintext); UniFi tetap source of truth, DB lokal hanya cache + audit trail.

## Keputusan (ringkasan)

| Kategori | Keputusan |
|---|---|
| Bentuk sistem | **Modular monolith** — satu binary Go, modul internal dengan interface eksplisit, komunikasi in-process |
| Modul | `identity`, `unifi` (adapter), `inventory`, `audit`, `api`; plus cross-cutting `config` & `observability`. Dashboard = SPA terpisah |
| Boundary UniFi | **Satu modul `unifi`**, satu-satunya yang tahu API/kredensial UniFi; modul lain hanya lihat domain type + typed error |
| Sinkronisasi state | **Pull (polling terjadwal 60s)**, single-flight, idempotent upsert. Push/webhook hanya optimisasi opsional nanti |
| Controller offline | Sajikan cache terakhir + tandai stale; tidak pernah hapus data karena sync gagal |
| Credential | Runtime env/secret host, dibaca sekali oleh modul `unifi`; rotasi = update secret + restart/SIGHUP |
| API internal | REST/JSON `/api/v1` same-origin; error model `{ code, message, details }` |
| Observability | `log/slog` JSON terstruktur + metrik Prometheus + `/health`, `/ready`, `/sync/status` |
| Fase 1 | **Read-only** terhadap UniFi (write ditunda sampai scope API key disetujui) |

---

## 1. Bentuk sistem — Modular monolith

### Keputusan

Satu binary Go, satu proses, satu deployment. Modularitas ditegakkan di level **package Go**, bukan proses jaringan. Modul berkomunikasi lewat interface in-process; **tidak ada HTTP antar modul**, dan tidak ada modul yang membaca tabel milik modul lain kecuali lewat method modul pemiliknya.

```
apps/api  (binary tunggal)
└── internal/
    ├── identity/      user, session, RBAC, middleware auth
    ├── unifi/         ADAPTER UniFi — satu-satunya yang bicara ke controller
    ├── inventory/     cache sites/devices/clients/networks + sync worker + sync_runs
    ├── audit/         append-only audit trail
    ├── api/           HTTP transport, routing, handler, error mapping
    ├── config/        pembacaan & validasi env/secret (cross-cutting)
    └── observability/ logging, metrik, health (cross-cutting)
apps/web               SPA React (static build, disajikan Caddy)
```

**Arah dependensi (wajib satu arah):**

```
api ──> inventory ──> unifi
 │         │
 │         └──> audit
 └──> identity ──> audit
config & observability dipakai semua (tanpa dependensi balik)
```

Aturan penegak:

- `unifi` **tidak mengimpor** `inventory`, `api`, atau `identity`. Dependensi ke UniFi hanya satu arah.
- Hanya package `internal/unifi` yang boleh mengimpor HTTP client adapter/spesifik UniFi. Ditegakkan di CI dengan `depguard` (Go linter) — pelanggaran = build gagal.
- Modul mengekspos **interface + domain type**, bukan struct DB atau struct mentah UniFi.
- `api` tidak pernah memanggil `unifi` langsung; ia selalu lewat `inventory`.

### Alasan

- **Cocok dengan skala.** Satu host kantor, satu service, satu `docker compose up`. Microservice menambah jaringan, deployment, dan observability antar-service yang tidak dibayar manfaatnya (batasan epic).
- **Boundary tetap nyata di dalam monolith.** Yang membuat monolith berantakan bukan "satu proses", tapi akses lintas modul yang tidak terkontrol. Di sini boundary ditegakkan oleh aturan import yang di-lint, bukan sekadar konvensi.
- **Jalur keluar tersedia.** Kalau `unifi` kelak perlu proses terpisah (mis. gateway lintas jaringan), ia sudah berupa interface dengan type netral — tinggal ganti implementasi in-process dengan client HTTP, tanpa mengubah pemanggil.
- **Konsisten dengan ADR-002.** Satu binary Go + goroutine/context sudah jadi keputusan stack; ADR-003 hanya menata isinya.

### Alternatif ditolak

- **Microservices sejak awal (service `unifi`, `api`, `worker` terpisah).** Ditolak: menambah network boundary, service discovery, distributed tracing, dan deployment terkoordinasi untuk beban yang bisa ditangani satu proses. "Microservice hanya jika terbukti perlu" adalah constraint eksplisit epic.
- **Layered monolith tanpa batas modul (`handlers`/`services`/`repos` global).** Ditolak: memudahkan akses langsung ke klien UniFi dari mana saja — persis risiko #1 yang ingin ditutup. Boundary per-domain lebih kuat daripada boundary per-layer.
- **Library terpisah (`pkg/unifi`) yang diimpor bebas.** Ditolak untuk sekarang: tanpa repo terpisah/versi, ini hanya menambah tata kelola. Rule import `depguard` memberi isolasi yang sama dengan biaya nol.

---

## 2. UniFi adapter — interface & kontrak

### Keputusan

Modul `unifi` adalah satu-satunya tempat panggilan UniFi hidup. Ia:

1. memegang kredensial (dibaca dari `config`, tidak pernah keluar dari modul ini),
2. memetakan response UniFi mentah ke **domain type netral**,
3. memetakan error transport/HTTP ke **typed error** yang stabil,
4. menangani detail transport (base URL, TLS, timeout, pagination, retry kelas transient).

Pemanggil (`inventory`) hanya melihat interface berikut.

### Interface (Go, tipe kasar)

```go
// Package unifi — satu-satunya boundary ke UniFi controller.

type Client interface {
    // Health memastikan controller reachable & kredensial valid.
    // Dipakai untuk readiness check dan circuit breaker.
    Health(ctx context.Context) error

    // ListSites mengembalikan semua site yang dikelola.
    ListSites(ctx context.Context) ([]Site, error)

    // ListDevices mengembalikan AP/switch/gateway untuk satu site.
    // Pagination ditangani internal; hasil selalu satu snapshot penuh.
    ListDevices(ctx context.Context, siteID string) ([]Device, error)

    // ListClients mengembalikan klien terhubung (active) untuk satu site.
    ListClients(ctx context.Context, siteID string) ([]Client, error)

    // ListNetworks mengembalikan WLAN/network yang dikonfigurasi pada satu site.
    ListNetworks(ctx context.Context, siteID string) ([]Network, error)
}

// ---- Domain type (netral; BUKAN bentuk mentah response UniFi) ----

type Site struct {
    ID        string
    Name      string
    Desc      string
}

type Device struct {
    ID         string   // id UniFi (stabil)
    SiteID     string
    MAC        string
    Name       string
    Model      string
    Type       string   // ap | switch | gateway | other
    Status     string   // connected | disconnected | adopting | ...
    Firmware   string
    IP         string   // opsional
    UptimeSec  int64    // opsional
    AdoptedAt  time.Time // opsional
    Raw        json.RawMessage // payload asli, untuk audit & deteksi schema drift
}

type Client struct {
    ID         string
    SiteID     string
    MAC        string
    Hostname   string
    IP         string
    APID       string    // device/AP tempat klien terhubung (opsional)
    SSID       string    // opsional
    SignalDBm  int       // opsional
    ConnectedAt time.Time // opsional
    Raw        json.RawMessage
}

type Network struct {
    ID          string
    SiteID      string
    Name        string    // SSID untuk WLAN
    VLAN        int       // opsional
    SecurityMode string   // open | wpa2 | wpa3 | ...
    Enabled     bool
    Raw         json.RawMessage
}
```

### Error taxonomy (typed, stabil)

```go
type ErrKind string

const (
    ErrUnreachable  ErrKind = "unifi_unreachable"   // timeout, DNS, conn refused, TLS
    ErrUnauthorized ErrKind = "unifi_unauthorized"  // 401/403 — kredensial invalid/expired
    ErrForbidden    ErrKind = "unifi_forbidden"     // operasi tidak diizinkan scope key
    ErrRateLimited  ErrKind = "unifi_rate_limited"  // 429, bawa RetryAfter
    ErrNotFound     ErrKind = "unifi_not_found"     // 404 resource
    ErrSchemaDrift  ErrKind = "unifi_schema_drift"  // response tidak sesuai ekspektasi
    ErrServer       ErrKind = "unifi_server_error"  // 5xx
)

type Error struct {
    Kind       ErrKind
    HTTPStatus int           // 0 jika bukan error HTTP
    RetryAfter time.Duration // >0 hanya untuk ErrRateLimited
    Message    string        // SANITIZED — tidak pernah memuat API key/header
    Cause      error         // untuk errors.Is/As, tidak diekspos ke klien
}
```

### Aturan boundary (ditegakkan review + `depguard`)

- **Tidak ada modul lain yang mengimpor HTTP client UniFi atau tahu path/header UniFi.** Semua path (`/proxy/network/...` atau integration API), header `X-API-KEY`, cookie login, dan penanganan TLS hidup di dalam `internal/unifi`.
- **Tidak ada tipe mentah UniFi yang bocor keluar.** `Raw` disimpan untuk audit, tapi pemanggil tidak boleh parse field di dalamnya; kalau butuh field baru, tambahkan ke domain type secara eksplisit.
- **Adapter tidak menyentuh DB.** Ia menerima `context`, mengembalikan data/error; persistensi adalah tanggung jawab `inventory`.
- **Read-only di fase 1.** Interface di atas sengaja hanya read. Method write (mis. reboot device, ubah WLAN) akan ditambahkan sebagai interface terpisah (`Writer`) hanya setelah scope API key disetujui, dengan RBAC `admin` + audit wajib.
- Adapter memakai timeout per panggilan (default 10s) dari `context` pemanggil, bukan timeout global.

### Alasan

- **Satu titik perubahan.** Pergeseran API UniFi (path, auth, bentuk field) hanya menyentuh `internal/unifi`.
- **Testable tanpa controller.** Pemanggil bergantung pada interface `unifi.Client`; test `inventory` memakai fake, CI tidak butuh controller nyata.
- **Type netral mencegah "UniFi bocor ke dashboard".** Kontrak HTTP internal (ZENI-6) tidak akan pernah mengekspos bentuk mentah UniFi.
- **Enum error stabil** memungkinkan `api` memetakan ke HTTP status dan `observability` memberi label metrik tanpa parsing string error.

### Alternatif ditolak

- **Klien UniFi pihak ketiga langsung di handler.** Ditolak: menyebar pengetahuan API ke seluruh kode dan mengunci kita ke bentuk library saat controller berganti versi.
- **Adapter mengembalikan `map[string]any` / JSON mentah, mapping di pemanggil.** Ditolak: memindahkan masalah schema drift ke banyak tempat dan kehilangan type-safety, justru di titik paling rawan.
- **Write di interface yang sama sejak awal.** Ditolak: memperluas permukaan berisiko ke jaringan produksi sebelum scope API key dibuktikan (open question ZENI-2). Read-only dulu.

---

## 3. Sinkronisasi state — Pull (polling), bukan push

### Keputusan

**Pull** sebagai mekanisme utama: worker di `inventory` memanggil adapter secara terjadwal, lalu melakukan **upsert idempotent** ke Postgres berdasarkan id UniFi. Push (webhook) **tidak** diandalkan di fase 1; kalau kelak tersedia ia hanya optimisasi opsional di atas polling.

**Parameter:**

| Parameter | Nilai | Catatan |
|---|---|---|
| Interval poll | **60 detik** | Konsisten dengan ADR-002 |
| Jitter | 0–5 detik per site | Menghindari thundering herd & pola yang mudah di-rate-limit |
| Timeout per panggilan | 10 detik | Dari `context`; satu site gagal tidak menggantung seluruh siklus |
| Concurrency | Terbatas (mis. 2–4 site paralel) | Controller kantor tidak perlu fan-out besar |
| Single-flight | Ya | Siklus tidak boleh overlap; jika siklus sebelumnya belum selesai, skip + catat |
| Strategi | Full snapshot list + upsert | Dataset kecil; jauh lebih sederhana daripada delta |
| Idempotensi | Upsert by `(site_id, unifi_id)` | Aman di-retry; tidak ada duplikasi |

**Alur satu siklus sync:**

```
tick (60s, jitter)
  └─> single-flight lock
        ├─> ListSites
        └─> untuk setiap site:
              ├─> ListDevices / ListClients / ListNetworks
              ├─> validasi ringan + tandai schema drift (bukan fatal)
              └─> transaksi: upsert cache + tulis raw ke jsonb
        ├─> catat sync_run { started_at, duration, counts, status, error }
        └─> update metrik + timestamp sync terakhir
```

**Retry/backoff:**

- Hanya untuk error **transient** (`ErrUnreachable`, `ErrServer`, `ErrRateLimited`).
- Backoff eksponensial + jitter di dalam siklus: ~1s → 2s → 4s, maksimum 3 percobaan atau ~30s total; `ErrRateLimited` menghormati header `Retry-After`.
- Error **non-transient** (`ErrUnauthorized`, `ErrForbidden`) **tidak di-retry** — retry tidak akan menyembuhkan kredensial invalid dan hanya membebani controller. Ditandai `auth_invalid`.
- **Circuit breaker** per controller: setelah N kegagalan berturut-turut (mis. 5), breaker terbuka dan polling diperlambat (mis. 5 menit) sampai percobaan probe `Health` berhasil. Mencegah hot-loop saat controller mati.

**Controller offline:**

- **Cache terakhir tetap disajikan.** Tidak ada penghapusan data karena sync gagal — data lama berlabel `synced_at` jauh lebih berguna daripada dashboard kosong.
- Setiap kegagalan tetap tercatat sebagai baris `sync_runs` dengan error tersanitasi.
- `/sync/status` dan dashboard menampilkan `last_successful_sync_at` + status staleness, sehingga operator tahu data mungkin basi alih-alih mempercayainya buta.

### Alasan

- **Push UniFi tidak dapat diandalkan lintas model/versi.** Ketersediaan webhook bergantung model dan firmware controller — masih open question di ZENI-2. Membangun arsitektur di atas mekanisme yang belum terverifikasi itu berisiko.
- **Beban trivial dan prediktabel.** Satu controller, puluhan device: biaya polling 60s tidak signifikan, dan bebannya jauh lebih mudah diprediksi/di-debug daripada event stream yang bisa hilang atau duplikat.
- **SLA data kita longgar.** Status device berubah dalam hitungan detik, bukan milidetik; operator mengawasi, bukan mengendalikan real-time.
- **Pull lebih tahan terhadap kehilangan event.** Webhook yang hilang butuh rekonsiliasi penuh; polling penuh secara desain selalu konvergen ke state sebenarnya.
- **Konsisten dengan ADR-002** (server poller 60s, client refetch 15–30s), sehingga tidak ada dua mekanisme realtime yang bersaing.

### Konsekuensi

- Ada jendela staleness ≤ ~60s + durasi siklus. Mitigasi: tampilkan `last synced at` dari `sync_runs`.
- Endpoint list harus efisien (pagination/filter server-side) supaya refetch dashboard murah — detail di ZENI-6.
- Bila nanti butuh reaksi instan, jalur upgrade adalah **SSE** (satu arah, cocok untuk update status), bukan WebSocket penuh. Keputusan push ditunda secara sadar, bukan diabaikan.

### Alternatif ditolak

- **Push/webhook sebagai mekanisme utama.** Ditolak untuk fase 1: ketersediaan belum terbukti, butuh endpoint publik/ingress dan penanganan replay/duplikat, serta tetap butuh polling rekonsiliasi sebagai jaring pengaman — jadi menambah kompleksitas tanpa menghapus polling.
- **WebSocket/SSE ke dashboard untuk update device.** Ditolak, sejalan ADR-002: koneksi long-lived + state server tanpa use case yang menuntut latensi sub-detik.
- **Polling per-device (bukan per-site).** Ditolak: melipatgandakan panggilan tanpa manfaat karena endpoint list mengembalikan snapshot per site.
- **Listener/delta stream dari controller.** Ditolak: bergantung API yang belum terverifikasi dan menambah state parsing delta yang rawan saat controller restart.

---

## 4. Credential handling — zero-trust API key

### Keputusan

| Aspek | Keputusan |
|---|---|
| Penyimpanan | **Runtime secret di host** — file `.env`/Docker secret dengan permission `0600`, di-inject sebagai environment variable ke container `api` |
| Pembacaan | Sekali saat startup oleh `internal/config`, disimpan **hanya di memori** di dalam `internal/unifi` |
| Tidak disimpan di | Git, image Docker, DB (plaintext **maupun** kolom biasa), log, issue/chat, browser, contoh OpenAPI |
| Paparan ke klien | **Nol.** API key tidak pernah dikirim ke browser; semua panggilan UniFi terjadi server-side |
| Startup | **Fail fast** jika key atau base URL hilang → container exit dengan error jelas (tanpa mencetak nilainya) |
| Rotasi | Update secret di host → `SIGHUP` untuk reload (atau restart container). Key lama dimatikan di sisi UniFi setelah verifikasi |
| Multi-key | Didukung (primary/next) untuk rotasi tanpa downtime bila controller mengizinkan lebih dari satu key |

**Larangan eksplisit (ditegakkan review + secret scanning + redaction):**

- ❌ API key tidak pernah masuk **git** (`.gitignore`, secret scanning dasar di CI — lihat ZENI-7).
- ❌ Tidak pernah ditulis ke **log**, termasuk header request/response mentah. Logger di modul `unifi` me-redact header `X-API-KEY`/cookie **secara default**; tidak boleh ada `log.Printf("%+v", req)`.
- ❌ Tidak pernah disimpan di **DB dalam bentuk plaintext** (maupun terenkripsi dengan key di sebelahnya — tidak ada gunanya; simpan di secret host).
- ❌ Tidak muncul di **pesan error** yang dikembalikan ke klien atau `sync_runs`; `Error.Message` dipaksa tersanitasi.
- ❌ Tidak muncul di **contoh/response OpenAPI**.

### Alasan

- **Permukaan minimum.** Kredensial jaringan hidup di satu modul dengan satu titik baca; tidak ada jalur lain yang perlu diaudit.
- **Runtime injection, bukan build-time.** Secret tidak pernah menjadi layer image atau artefak yang bisa tersebar.
- **Tanpa secret manager eksternal.** Untuk single-tenant di host kantor, file `0600` + env runtime adalah trade-off yang tepat; Vault/cloud KMS menambah dependensi operasional tanpa manfaat sepadan. Bila kelak ada, `config` adalah satu-satunya yang berubah.
- **Rotasi murah.** Reload via SIGHUP cukup untuk key yang jarang berubah; restart selalu menjadi fallback.

### Konsekuensi

- Rotasi adalah **prosedur manual terdokumentasi**, bukan otomatis — konsisten dengan "tidak ada platform ops tambahan".
- Perlu disiplin redaction di logger; ditutup dengan helper logger di `observability` yang tidak menerima header sensitif secara default.
- Backup tidak boleh memuat secret; `.env` di host **di luar** cakupan backup kode.

### Alternatif ditolak

- **Secret manager (Vault/AWS Secrets Manager) sekarang.** Ditolak: dependensi & operasional baru untuk satu secret di satu host; jalur upgrade tetap terbuka.
- **API key di kolom DB.** Ditolak eksplisit oleh constraint epic dan menciptakan kebocoran lewat dump backup.
- **Menyimpan key di image Docker.** Ditolak: secret menyebar ke registry/layer dan tidak bisa dirotasi tanpa rebuild.
- **API key per-operator dari browser.** Ditolak: bertentangan dengan zero-trust dan RBAC; identitas operator adalah session cookie, bukan key UniFi.

---

## 5. API surface internal (backend ↔ dashboard)

### Keputusan

REST/JSON di bawah `/api/v1`, same-origin di belakang Caddy (SPA + `/api` pada host yang sama), konsisten dengan ADR-002 (session cookie + RBAC). Dashboard **tidak pernah** berbicara langsung ke UniFi.

**Endpoint fase 1** (bentuk final & contoh ada di ZENI-6):

| Endpoint | Role minimum | Sumber |
|---|---|---|
| `POST /api/v1/auth/login`, `POST /api/v1/auth/logout`, `GET /api/v1/me` | publik / session | identity |
| `GET /api/v1/sites`, `GET /api/v1/sites/{id}` | viewer | inventory (cache) |
| `GET /api/v1/devices`, `GET /api/v1/devices/{id}` | viewer | inventory |
| `GET /api/v1/clients` | viewer | inventory |
| `GET /api/v1/networks` | viewer | inventory |
| `GET /api/v1/health` | publik | api (liveness) |
| `GET /api/v1/ready` | publik | api (DB + freshness sync) |
| `GET /api/v1/sync/status` | viewer | inventory (`sync_runs`) |

Aturan lintas endpoint:

- Filter/pagination/sorting **konsisten di semua list endpoint** (`page`, `page_size`, `sort`, filter per `site_id`/`status`/`mac`).
- Setiap resource dari cache menyertakan **`synced_at`** agar klien tahu kesegaran data.
- Tidak ada endpoint yang mem-proxy UniFi secara langsung/live; semua dari cache — sehingga dashboard tidak pernah terblokir controller yang lambat.

**Model error standar** (satu-satunya bentuk error, semua endpoint):

```json
{
  "code": "unifi_unreachable",
  "message": "UniFi controller is not reachable",
  "details": {
    "last_synced_at": "2026-10-06T12:40:00Z",
    "request_id": "01a11141-..."
  }
}
```

- `code` — **stabil & machine-readable** (daftar tetap: `unifi_unreachable`, `unifi_unauthorized`, `unifi_rate_limited`, `unifi_schema_drift`, `not_found`, `validation_failed`, `unauthorized`, `forbidden`, `rate_limited`, `internal_error`). Klien boleh bercabang pada `code`.
- `message` — human-readable, **tersanitasi** (tanpa detail internal/secret).
- `details` — opsional, objek; `request_id` selalu ada untuk korelasi log.

**Pemetaan status HTTP:**

| code | HTTP |
|---|---|
| `validation_failed` | 400 |
| `unauthorized` | 401 |
| `forbidden` | 403 |
| `not_found` | 404 |
| `rate_limited` | 429 |
| `unifi_unreachable`, `unifi_schema_drift` | 503 |
| `unifi_unauthorized`, `unifi_forbidden` | 502 (upstream credential problem — bukan kesalahan user) |
| `internal_error` | 500 |

Error UniFi internal **tidak** diteruskan mentah ke klien; `api` memetakan typed error ke `code` di atas. Endpoint write (bila nanti disetujui) butuh role `admin`, CSRF token, rate limit, dan **wajib** menulis audit log.

### Alasan

- **Cache-backed API** memutus kopling uptime dashboard dari uptime controller: controller offline → dashboard tetap menampilkan state terakhir + penanda stale, bukan halaman error.
- **Model error tunggal dengan `code` stabil** memungkinkan frontend menangani kegagalan secara terprogram dan konsisten, serta menyelaraskan dengan permintaan ZENI-6.
- **Same-origin + session cookie** menyederhanakan CSRF/CORS dan mencegah token yang bisa dicuri XSS (ADR-002).

### Konsekuensi

- Semua konsumen (termasuk dashboard) harus siap menerima data yang **mungkin basi**; `synced_at` bukan opsional.
- `api` memegang tabel pemetaan error yang harus dijaga sinkron dengan taxonomy adapter.
- Perubahan bentuk `code`/`details` adalah perubahan kontrak — harus lewat ZENI-6.

### Alternatif ditolak

- **GraphQL.** Ditolak: menambah tooling/skema/otorisasi per-field untuk kebutuhan read-only yang sebagian besar datar; REST + OpenAPI lebih langsung dan sudah jadi kontrak di epic.
- **Proxy live ke UniFi dari dashboard.** Ditolak: membocorkan boundary UniFi ke klien, memaparkan latensi/topologi controller, dan menggagalkan isolasi kredensial.
- **Error ad-hoc per handler (`{"error": "..."}`).** Ditolak: klien tidak bisa bercabang stabil; menggandakan logika penanganan.

---

## 6. Observability

### Keputusan

- **Structured logging:** `log/slog` JSON ke stdout, field tetap: `ts`, `level`, `msg`, `request_id`, `module`, `event`, plus field kontekstual (`site_id`, `device_id`, `duration_ms`, `result`). **Redaction default** untuk header sensitif; tidak ada payload mentah yang memuat kredensial.
- **Metrik dasar** (format Prometheus, diekspos `/metrics` dari `api`):
  - `unifi_requests_total{kind, result}` — sukses/gagal panggilan UniFi per operasi (`sites|devices|clients|networks|health`).
  - `unifi_request_duration_seconds{kind}` — histogram latensi panggilan UniFi.
  - `sync_runs_total{result}` dan `sync_duration_seconds` — durasi siklus & hasil (`success|partial|failed|skipped`).
  - `sync_last_success_timestamp_seconds` — dasar perhitungan **staleness** (indikator paling penting untuk "controller sehat?").
  - `unifi_circuit_breaker_state{state}` — `closed|open|half_open`.
  - `http_requests_total{route,status,method}` + `http_request_duration_seconds`.
- **Health check (tiga level berbeda, jangan dicampur):**
  - `GET /api/v1/health` — **liveness**: proses hidup. Selalu 200 selama binary berjalan; tidak menyentuh DB/UniFi.
  - `GET /api/v1/ready` — **readiness**: DB reachable **dan** sync terakhir lebih muda dari ambang (mis. 3× interval). 503 bila tidak, dengan alasan terstruktur.
  - `GET /api/v1/sync/status` — **status operasional**: `last_successful_sync_at`, `last_run`, `consecutive_failures`, `circuit_breaker`, `stale: bool`.
- **Korelasi:** `request_id` di-set/generuskan per request, disertakan di log dan di `details` error.
- **Log audit** (dari modul `audit`) adalah jalur terpisah dari log operasional: append-only, memuat aktor/aksi/waktu, terutama untuk operasi write.

### Alasan

- **Staleness adalah metrik utama.** Karena kita memilih pull + cache, pertanyaan operasionalnya bukan "apakah tiap request sukses" tapi "seberapa segar data kita". `sync_last_success_timestamp_seconds` menjawabnya langsung.
- **Pemisahan health/ready.** Compose/restart policy butuh liveness yang tidak boleh gagal hanya karena controller offline; sedangkan traffic/alerting butuh readiness yang justru peka terhadap DB dan staleness. Menggabungkan keduanya menyebabkan restart-loop yang tidak perlu.
- **Metrik UniFi berlabel `result`** membuat laju kegagalan dan jenisnya (unreachable vs auth vs rate limit) langsung terlihat, yang memetakan 1:1 ke failure mode di bagian 7.

### Konsekuensi

- Metrik adalah endpoint in-process; tidak ada push gateway/collector eksternal di fase 1 (dapat ditambah nanti).
- Threshold staleness harus dikonfigurasi (mis. `SYNC_STALE_AFTER=180s`), bukan hardcoded.
- Kartu "last synced" di dashboard membaca endpoint yang sama dengan `/sync/status`, sehingga tidak ada dua definisi kesegaran.

### Alternatif ditolak

- **Tracing terdistribusi (OpenTelemetry) penuh sekarang.** Ditolak: single process tanpa panggilan antar-service; `request_id` + log terstruktur sudah cukup. OTel bisa ditambah nanti.
- **Logging teks bebas.** Ditolak: sulit di-query dan rawan membocorkan field tak terduga; JSON terstruktur dengan field tetap lebih aman dan searchable.
- **Satu endpoint `/health` untuk semua.** Ditolak: mencampur liveness/readiness/status menyebabkan restart tak perlu saat controller offline atau readiness hijau saat data basi.

---

## 7. Failure modes & penanganannya

| # | Failure mode | Deteksi | Penanganan | Dampak ke operator |
|---|---|---|---|---|
| 1 | **Controller unreachable** (timeout, conn refused, DNS, TLS) | `ErrUnreachable`, probe `Health` gagal | Retry backoff (≤3, ~30s) → circuit breaker terbuka + poll diperlambat (5 menit); **cache terakhir tetap disajikan**; `sync_runs` mencatat error; metrik `unifi_requests_total{result=error}` naik | Dashboard tetap tampil + banner **stale** + `last_synced_at`; `/ready` 503 |
| 2 | **API key expired/revoked (401/403)** | `ErrUnauthorized`/`ErrForbidden` | **Tanpa retry** (retry tidak menyembuhkan); tandai `auth_invalid`, hentikan siklus; alert; dokumentasikan prosedur rotasi (§4); `/sync/status` eksplisit menyebut masalah kredensial | Pesan jelas "credential invalid — perbarui API key", bukan error generik; dashboard tetap dari cache |
| 3 | **Rate limited (429)** | `ErrRateLimited` + `Retry-After` | Hormati `Retry-After`; backoff eksponensial; **skip siklus** yang tidak muat; turunkan interval poll sementara; metrik khusus | Data lebih basi sementara; tidak ada kegagalan user-visible |
| 4 | **Schema drift dari UniFi** (field hilang/berubah/tipe berbeda) | Validasi ringan saat mapping; field wajib kosong / tipe tak terduga | **Tolerant decoding**: field opsional hilang → zero-value + `warn` terstruktur; payload asli tetap disimpan di `Raw`/`jsonb`; siklus **tidak** gagal total; alert bila laju drift melewati ambang | Data yang tak terbaca ditandai; tidak crash, tidak data loss (raw tersimpan untuk forensik) |
| 5 | **DB down / tidak reachable** | Ping DB / error query | `/ready` 503; handler list mengembalikan 503 `internal_error`; poller menjeda penulisan (data UniFi tidak hilang, akan ter-upsert siklus berikutnya) | Dashboard error terkontrol (bukan 500 mentah) |
| 6 | **TLS self-signed / rotasi sertifikat controller** | handshake error | Konfigurasi CA eksplisit / fingerprint yang dipin (mis. `UNIFI_CA_FILE`); **tidak** mematikan verifikasi TLS secara global; bila CA berubah → error jelas menunjuk sebabnya | Tidak ada; error terklasifikasi sebagai `ErrUnreachable` dengan pesan spesifik |
| 7 | **Siklus sync overlap / durasi > interval** | Lock single-flight | Skip siklus baru + `sync_runs{result=skipped}`; tidak ada dua penulis paralel; tidak ada penumpukan panggilan | Tidak ada; metrik `skipped` terlihat |
| 8 | **Write operation salah (fase lanjut)** | — | RBAC `admin` + CSRF + audit wajib + rate limit endpoint write; dry-run/konfirmasi untuk aksi destruktif | Aksi tercatat & dapat dilacak |

**Prinsip umum penanganan kegagalan:**

- **Cache adalah buffer, bukan sumber.** Kegagalan sync tidak pernah menghapus/mengosongkan cache; operator melihat data terakhir + tingkat kesegaran.
- **Error transient di-retry terbatas; error permanen tidak.** Retry tanpa batas pada kredensial invalid atau schema drift hanya menyembunyikan masalah dan membebani controller.
- **Semua kegagalan terlihat.** Setiap siklus menulis `sync_runs`; tidak ada kegagalan yang hanya muncul di log dan hilang.

## Ringkasan konsekuensi menyeluruh

- **Kopling UniFi terkonsentrasi** di `internal/unifi`; satu perubahan API = satu modul (ditegakkan `depguard`).
- **Dashboard ter-decouple dari uptime controller** lewat API berbasis cache + penanda `synced_at`; tidak ada proxy live.
- **Kredensial punya satu titik baca & satu jalur rotasi**, dengan larangan eksplisit terhadap git/log/DB/browser.
- **Polling + circuit breaker** membuat controller offline menjadi kondisi *terdegradasi yang terlihat*, bukan kegagalan sistem.
- **Tidak ada keputusan breaking** (greenfield). Pergantian mekanisme sinkronisasi (push), penambahan write, atau pemisahan service UniFi kelak **harus lewat ADR baru**.
- **Open question epic tidak memblokir ADR ini:** model gateway (lokal vs cloud) hanya mengubah konfigurasi base URL/TLS di adapter; scope API key read/write hanya menentukan kapan interface `Writer` ditambahkan; URL repo tidak memengaruhi desain.

## Tindak lanjut

- Setelah repo ada, pindahkan ke `docs/adr/003-architecture.md` (status Accepted).
- **ZENI-5 (DATA)** mengacu ke domain type di §2 dan jenis tabel di §3 (`synced_at`, `sync_runs`).
- **ZENI-6 (API)** mengacu ke §5 untuk model error `{ code, message, details }` dan daftar endpoint.
- **ZENI-7 (BOOT)** mengacu ke §4 untuk wiring secret runtime + `.env.example` tanpa nilai sensitif, dan aturan `depguard`.
- Interface `Writer` UniFi (write ops) ditunda sampai ZENI-2 menjawab scope API key.
