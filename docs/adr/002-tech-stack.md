# ADR-002 — Tech Stack Decision (BE / FE / DB / Realtime / Deploy / Auth)

- **Status:** Accepted (dipindah ke `docs/adr/002-tech-stack.md` saat bootstrap ZENI-7; menunggu konfirmasi user atas pilihan stack)
- **Date:** 2026-10-06
- **Deciders:** Tech Lead
- **Konteks epic:** [ZENI-1] Network Orchestrator — Foundation: Stack, Architecture & Data Model

## Konteks

Kita membangun control plane internal (single-tenant, kantor) untuk ekosistem UniFi: membaca state jaringan via UniFi API, menyimpannya sebagai cache + audit trail, dan menampilkannya di dashboard operator. Tidak ada kode, repo, atau kontrak apa pun saat ini.

Beban kerja sistem ini spesifik dan sempit:

1. **Integrasi HTTP keluar** ke UniFi controller (read-heavy, sedikit/ tanpa write di fase 1).
2. **Scheduler polling** periodik terhadap sejumlah kecil site/device (puluhan hingga ratusan, bukan jutaan).
3. **REST API** ringan untuk melayani dashboard internal.
4. **Dashboard operator** dengan tabel, grafik, dan topologi.
5. **Runtime di lingkungan kantor** — operatornya engineer internal, bukan tim SRE penuh, dan tidak ada keinginan menambah platform ops.

Batasan dari epic: modular monolith lebih dulu, microservice hanya jika terbukti perlu; contract-first; zero-trust credential (API key tidak pernah masuk git/log/DB plaintext); UniFi tetap source of truth.

Skala ini kecil. Trade-off yang benar adalah meminimalkan jumlah moving part dan biaya operasional, bukan mengejar throughput atau elastisitas.

## Keputusan (ringkasan)

| Kategori | Keputusan |
|---|---|
| Backend | **Go 1.23+**, router `chi`, `net/http` stdlib, `sqlc` + `pgx` |
| Frontend | **React + TypeScript + Vite** (SPA), TanStack Query/Table, shadcn/ui + Tailwind |
| Database | **PostgreSQL 16**, migrasi forward-only dengan `goose` |
| Realtime | **Polling** (server poller + client refetch interval). Tanpa WebSocket/SSE di fase 1 |
| Deployment | **Docker + docker compose** di host Linux kantor, Caddy sebagai reverse proxy/TLS |
| Auth admin | **Server-side session cookie** + RBAC minimal (viewer/operator/admin), hash `argon2id` |

---

## 1. Backend — Go + chi + sqlc

### Keputusan

Go sebagai bahasa backend. Routing dengan `chi` (atau `net/http` mux standar bila cukup), akses DB dengan `pgx` dan query ter-generate via `sqlc`. Binary tunggal, dikonfigurasi lewat environment variable.

### Alasan

- **Deploy satu binary statis.** Di lingkungan kantor tidak ada runtime/versi manager yang perlu dirawat; cukup copy binary ke container atau host. Ini menghapus satu kelas penuh masalah operasional.
- **Concurrency native untuk polling.** Scheduler + fan-out ke banyak device adalah pola goroutine + `context` dengan cancellation/timeout bawaan — pas langsung dengan bentuk kerja kita tanpa library tambahan.
- **HTTP client & server di stdlib** sudah matang; dependency pihak ketiga minimal, permukaan supply-chain kecil untuk sistem yang menyentuh kredensial jaringan.
- **Jejak memori & latensi rendah** untuk worker polling yang hidup terus-menerus di box kecil.
- **Tipe statis** menangkap kesalahan pada boundary parsing response UniFi (data eksternal yang bentuknya bisa bergeser), yang justru sumber bug utama di integrasi semacam ini.

### Konsekuensi

- Backend dan frontend memakai bahasa berbeda. **Mitigasi:** kontrak OpenAPI (ZENI-6) adalah sumber tunggal, dan client TS di-generate dari spesifikasi itu — jadi perbedaan bahasa tidak menciptakan duplikasi tipe manual.
- Harus ada toolchain Go di CI dan image build multi-stage.
- Untuk tim yang belum familiar, kurva belajar awal ada; namun idiom yang dipakai di sini (HTTP handler, worker, SQL) termasuk yang paling standar.

### Alternatif ditolak

- **Node.js + TypeScript (NestJS/Fastify).** Ditolak *bukan* karena buruk — satu bahasa untuk seluruh stack itu nilai nyata. Ditolak karena keunggulan utamanya (berbagi tipe) sudah sebagian besar kita peroleh lewat OpenAPI + generated client, sementara biayanya nyata: runtime + dependency tree besar untuk dirawat, memory footprint worker polling lebih tinggi, dan penanganan concurrency untuk fan-out lebih berhati-hati. Untuk sistem kecil yang harus sesederhana mungkin secara operasional, Go menang.
- **Python (FastAPI).** Ekosistem UniFi/otomasi bagus dan pengembangan cepat, tetapi distribusi runtime di box kantor lebih berat dibanding binary Go, dan tidak ada kebutuhan analitik/data-science yang menjustifikasi Python.
- **Java/Spring.** Terlalu berat untuk skala ini; startup dan memori besar, biaya operasional tinggi tanpa manfaat sepadan.

---

## 2. Frontend — React + TypeScript + Vite (SPA)

### Keputusan

SPA React + TypeScript, di-build dengan Vite, disajikan sebagai static asset oleh Caddy (bukan dirender server-side).

- Data fetching/caching: **TanStack Query**
- Tabel: **TanStack Table**
- Komponen: **shadcn/ui** + Tailwind CSS
- Grafik: **Recharts**
- Topologi jaringan: **React Flow**

### Alasan

- **Tidak butuh SSR.** Dashboard ini di belakang login, internal, dan data-nya live dari API. Next.js/SSR menambah server Node yang harus dioperasikan tanpa memberi manfaat (SEO, first paint publik) apa pun. Vite SPA = satu set file statis yang bisa disajikan proxy.
- **TanStack Query** menutup kebutuhan realtime kita di fase 1 secara langsung: `refetchInterval` per query memberi refresh berkala terpusat dengan caching, dedup, dan state loading/error — tanpa infrastruktur socket apa pun.
- **shadcn/ui** memberi komponen aksesibel yang kita miliki sendiri (copy-in, bukan paket terkunci), menghindari coupling ke library besar dan memudahkan penyesuaian.
- **TanStack Table** untuk tabel device/client dengan sorting/filter/pagination di sisi klien; **Recharts** untuk metrik historis; **React Flow** adalah pilihan de-facto untuk topologi node-edge (AP ↔ client/switch) dan menerima struktur graph apa adanya.

### Konsekuensi

- Menyajikan file statis mengharuskan konfigurasi proxy yang benar (routing SPA fallback + `/api` ke backend).
- Data yang sangat besar harus dipaginasi di server; TanStack Table di sisi klien cukup untuk skala kita, tetapi tidak boleh diasumsikan untuk volume besar.
- React Flow dan Recharts adalah dependensi yang perlu dipantau, keduanya populer dan aktif.

### Alternatif ditolak

- **Next.js.** Ditolak: SSR/RSC memberi kompleksitas (server Node kedua, model rendering, boundary server/client) untuk produk yang tidak butuh SEO atau halaman publik. Manfaat nyatanya kecil untuk dashboard internal.
- **Vue/Svelte.** Sama-sama capable, tetapi React memiliki ekosistem paling luas untuk komponen tabel/grafik/topologi yang kita butuhkan; memilih React mengurangi risiko kekurangan library.
- **MUI/Ant Design sebagai basis.** Cepat di awal, tetapi paket besar dan opinionated, serta styling override untuk tampilan network-centric lebih repot dibanding pendekatan utility-first + komponen milik sendiri.

---

## 3. Database — PostgreSQL 16

### Keputusan

PostgreSQL 16 sebagai satu-satunya datastore. Migrasi forward-only dengan `goose`; query ter-tipe dengan `sqlc`.

### Alasan

- **Default yang tepat.** Postgres menangani seluruh kebutuhan kita tanpa satu pun alasan untuk menyimpang: relasional dengan foreign key, tipe `jsonb` untuk menyimpan payload mentah UniFi, `timestamptz` untuk audit/sinkronisasi, index ekspresi untuk MAC/IP, dan constraint yang bisa menegakkan integritas cache.
- **`jsonb` sangat berguna** untuk menyimpan response UniFi mentah (audit/schema drift) di samping kolom ter-normalisasi — persis pola "cache + histori" yang diinginkan epic.
- **Operasional mudah**: satu container di compose, backup `pg_dump` via cron, tooling universal yang sudah dikenal.
- **`sqlc`** menghasilkan kode Go bertipe dari SQL mentah — menghindari ORM magic dan query builder runtime, sekaligus menjaga SQL tetap eksplisit dan bisa di-`EXPLAIN`.

### Konsekuensi

- UniFi adalah source of truth; tabel lokal adalah cache + audit. Setiap tabel cache punya `synced_at` dan strategi upsert berdasarkan id UniFi (lihat ZENI-5).
- Migrasi harus backward-compatible dan forward-only; tidak ada perubahan destruktif tanpa persetujuan.
- Backup dan retensi audit adalah tanggung jawab yang harus dijadwalkan, bukan otomatis dari DB.

### Alternatif ditolak

- **SQLite.** Menggoda untuk kesederhanaan, tetapi menghilangkan concurrency tulis yang lebih baik, `jsonb`, dan jalur pertumbuhan tanpa migrasi memori; untuk service dengan worker polling + API bersamaan, Postgres lebih aman.
- **MongoDB.** Tidak ada kebutuhan skema bebas yang tidak sudah ditangani `jsonb`; query relasional dan constraint justru lebih penting di sini.
- **MySQL/MariaDB.** Setara untuk kasus dasar, tetapi `jsonb`, tipe waktu, dan fitur SQL lanjutan Postgres lebih nyaman; tidak ada alasan berpindah dari default.

---

## 4. Realtime — Polling, bukan WebSocket/SSE

### Keputusan

Polling dua lapis:

1. **Sisi server:** scheduler poll ke UniFi setiap **60 detik** untuk state device/client, menulis hasil ke Postgres.
2. **Sisi klien:** dashboard me-refetch API setiap **15–30 detik** (current view) via TanStack Query `refetchInterval` ketika tab aktif; berhenti saat tab tidak aktif.

Tidak ada WebSocket atau SSE di fase 1.

### Alasan

- **"Paling sederhana yang cukup" adalah kriteria eksplisit di issue ini.** Polling tidak punya state koneksi, tidak butuh penanganan reconnect/backpressure, tidak menambah beban proxy, dan tidak butuh infrastruktur pub/sub.
- **SLA data kita tidak ketat.** Status device berubah dalam hitungan detik, bukan milidetik; operator mengawasi dashboard, bukan mengendalikan sistem real-time. Latensi 15–60 detik sepenuhnya acceptable.
- **Beban bisa diprediksi.** Dengan satu site dan puluhan device, biaya polling trivial dan justru lebih mudah di-debug daripada event stream yang bisa hilang.
- **YAGNI.** Kalau nanti butuh push (mis. halaman topologi live atau notifikasi kritis), SSE lebih mudah ditambahkan incremental daripada membangun WebSocket sekarang.

### Konsekuensi

- Ada jendela staleness (≤ interval). Dashboard harus menampilkan `last synced at` (dari catatan sync run) agar operator tahu kesegaran data — ini juga requirement observability dari epic.
- Perlu desain endpoint list yang efisien (ETag/`updated_since` atau pagination) supaya polling tidak membuang bandwidth; detail ada di ZENI-6.
- Bila butuh reaksi instan di masa depan, jalur upgrade adalah **SSE** (satu arah, cocok untuk update status), bukan WebSocket penuh.

### Alternatif ditolak

- **WebSocket.** Ditolak untuk fase 1: menambah kompleksitas koneksi/lifecycle, reconnect, dan pengelolaan state di server tanpa kebutuhan yang terbukti. Biaya perawatan tidak sebanding dengan manfaat latensi yang tidak diminta.
- **SSE sekarang.** Lebih sederhana dari WebSocket dan pilihan yang baik, tetapi tetap merupakan koneksi long-lived tambahan yang belum dibutuhkan; ditunda sampai ada use case nyata (bukan dipilih diam-diam).
- **Push dari UniFi (webhook).** Tidak diandalkan sebagai mekanisme utama karena ketersediaan/keandalan webhook di controller bergantung model dan versi (masih open question di ZENI-2). Polling tetap jalur utama; webhook, jika tersedia, hanya optimisasi opsional ke depan.

---

## 5. Deployment — Docker + docker compose

### Keputusan

Tiga container di `docker compose`:

- `api` — binary Go
- `db` — PostgreSQL 16 dengan volume persisten
- `caddy` — reverse proxy + TLS, menyajikan static frontend dan mem-proxy `/api`

Target runtime: satu host Linux di jaringan kantor (mini-PC/VM) dengan akses keluar ke UniFi controller. Konfigurasi lewat `.env` (secret di-inject sebagai runtime env, tidak masuk git).

### Alasan

- **Cocok dengan realitas kantor.** Satu host, satu `docker compose up -d`, restart policy sederhana. Tidak ada Kubernetes/orchestrator yang menambah permukaan operasional untuk satu service.
- **Isolasi yang cukup** antara DB dan app tanpa overhead platform; volume Docker untuk persistensi DB.
- **Caddy** memberi TLS otomatis dan konfigurasi minimal (reverse proxy + static SPA), menghindari penanganan sertifikat manual.
- **Jalur keluar jelas:** bila kelak butuh HA atau deploy terpisah, container yang sama bisa dipindah tanpa menulis ulang aplikasi. Ini memenuhi batasan "microservice hanya jika terbukti perlu".

### Konsekuensi

- Single point of failure — acceptable untuk tool internal; mitigasi: restart policy, healthcheck container, dan backup DB terjadwal.
- Secrets dikelola sebagai file `.env`/secret runtime di host (bukan di image, bukan di git). Rotasi API key = update secret + restart.
- Perlu prosedur backup (`pg_dump` harian + retensi) dan smoke test pasca-deploy (endpoint `/health`).

### Alternatif ditolak

- **Kubernetes.** Jauh di luar skala; biaya operasional dan konseptual tidak terbayar.
- **Deploy bare-metal tanpa container.** Lebih sedikit moving part di permukaan, tetapi kehilangan reproduksibilitas lingkungan dan isolasi DB; compose memberi keduanya dengan biaya hampir nol.
- **PaaS/serverless.** Runtime harus mengakses controller UniFi di LAN internal; serverless menyulitkan konektivitas jaringan internal dan menambah ketergantungan eksternal.

---

## 6. Auth Admin — Session cookie + RBAC minimal

### Keputusan

- Login operator via **server-side session**: cookie `HttpOnly`, `Secure`, `SameSite=Lax`; session disimpan di tabel Postgres (`id`, `user_id`, `expires_at`, `created_at`).
- Password di-hash dengan **argon2id**. Tidak ada password default; admin pertama di-bootstrap dari secret env sekali, lalu wajib ganti.
- **RBAC minimal, tiga role:**
  - `viewer` — read-only dashboard.
  - `operator` — viewer + aksi operasional terbatas yang disetujui (write ke UniFi, fase lanjut).
  - `admin` — operasional + manajemen user/role.
- Endpoint write butuh role minimum + **CSRF token** dan masuk audit log.
- API key UniFi **tidak pernah** dikirim ke browser; semua panggilan UniFi terjadi di server.

### Alasan

- Session cookie dengan state di server adalah model paling sederhana dan aman untuk SPA same-origin di belakang satu proxy: tidak ada token di `localStorage` yang bisa dicuri XSS, dan logout/revocation instan (hapus baris session).
- RBAC tiga-role sudah memenuhi batasan "minimal" sambil memisahkan read dari write — penting karena write ke jaringan produksi kantor berisiko (open question scope API key di ZENI-1).
- Menyimpan seluruh akses UniFi di server menegakkan aturan zero-trust: kredensial tetap di boundary server.

### Konsekuensi

- Same-origin deployment (Caddy menyajikan SPA dan `/api` di host yang sama) diperlukan agar `SameSite`/cookie sederhana; ini sudah konsisten dengan keputusan deployment.
- Perlu implementasi CSRF untuk metode non-idempotent.
- Manajemen user awal sederhana (create/reset oleh admin); SSO/OIDC ditunda sampai ada kebutuhan.

### Alternatif ditolak

- **JWT di `localStorage`.** Stateless, tetapi token dapat dicuri via XSS, revocation sulit, dan tidak memberi manfaat apa pun pada skala ini.
- **JWT di cookie + refresh token.** Lebih rumit (rotasi, revocation list) tanpa manfaat dibanding session server-side untuk satu service.
- **Basic auth / API key statis untuk dashboard.** Tidak ada identitas per-operator, tidak ada RBAC, tidak ada audit yang bermakna.
- **Delegasi ke IdP/SSO (OIDC) sekarang.** Menambah dependensi dan integrasi organisasi yang belum dibutuhkan; bisa ditambahkan kemudian tanpa mengubah model session.

---

## Ringkasan konsekuensi menyeluruh

- **Contract-first tetap wajib.** OpenAPI (ZENI-6) menjadi jembatan antara Go dan TypeScript; client TS di-generate agar tidak ada drift tipe.
- **Boundary UniFi terisolasi** di satu adapter (ADR-003 / ZENI-4); stack ini tidak mengubah prinsip tersebut.
- **Tidak ada keputusan di sini yang bersifat breaking** terhadap sistem yang ada (greenfield). Perubahan permanen di masa depan — pindah bahasa, ganti DB, atau menambah broker realtime — harus melalui ADR baru.
- **Open question epic tidak memblokir ADR ini.** Pemilihan stack tidak bergantung pada model gateway UniFi (lokal vs cloud), scope API key, atau URL repo; ketiganya berada di ZENI-1/ZENI-2.

## Tindak lanjut

- Setelah repo ada, dokumen ini dipindah ke `docs/adr/002-tech-stack.md` (status Accepted).
- ADR-003 (ZENI-4) dan DATA (ZENI-5) mengacu ke keputusan ini sebagai constraint.
