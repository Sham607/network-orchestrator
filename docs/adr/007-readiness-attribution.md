# ADR-007 — Atribusi readiness: bergeser hanya kalau ada bukti kegagalan sisi kita

- **Status:** Accepted
- **Date:** 2026-10-11
- **Deciders:** Tech Lead, disetujui pemilik workspace (room chat, 2026-10-11)
- **Konteks epic:** [ZENI-23] [EPIC] FR-3: Client Presence & Topology Cache
- **Supersedes:** satu klausa ADR-004 — "cache basi ⇒ `unifi_unreachable`". Sisanya berlaku penuh.

## Konteks

ADR-004 mengunci pembagian ini: database mati ⇒ `service_unavailable` (`details.reason: database_unreachable`); cache basi karena controller offline ⇒ `unifi_unreachable` (`details.reason: cache_stale`). ADR-004 ditulis sebelum sync worker ada, dengan asumsi yang wajar saat itu: **satu-satunya** sebab cache bisa basi adalah controller yang tidak menjawab. Alasannya tetap berlaku: kode 503 harus menunjuk dependensi yang benar-benar rusak, supaya alerting dan operator tidak dikirim ke sistem yang salah.

Verifikasi deploy FR-3 (2026-10-11) menemukan kasus yang tidak tercakup: **controller menjawab, tapi cache tidak bisa diisi karena sisi kita sendiri yang gagal.** Saat itu skema belum dimigrasi, `ApplySite` gagal untuk 20/20 site, dan run tercatat `error_kind: internal_error`. `/ready` membalas:

```
503 unifi_unreachable   details.reason: cache_stale
```

sementara `/sync/status` di detik yang sama melaporkan `unifi.reachable: true` dan `last_run.error_kind: internal_error`. Operator yang mengikuti pesan itu akan memeriksa kredensial dan koneksi UniFi — dua hal yang sehat — sementara yang rusak adalah database kita sendiri.

Ini kelas kesalahan yang ADR-004 tolak untuk kasus cerminnya: ADR-004 mencegah "database mati disebut `unifi_unreachable`"; yang belum dicegah adalah "kegagalan sisi kita disebut `unifi_unreachable`".

Satu hal yang **sudah benar** sebelum ADR ini dan tidak diubah: endpoint list (`sites`, `devices`, `clients`) memakai aturan `stale && !reachable` (`cacheStaleAndControllerOffline`), jadi mereka hanya menyalahkan controller saat controller benar-benar diam, dan menyajikan cache dengan `200` + `synced_at` per baris selama query-nya masih bisa dijawab. Pada keadaan di atas query-nya sendiri gagal (relasi `sites` tidak ada), sehingga mereka membalas `503 service_unavailable` (`database_unreachable`) — juga penunjukan yang benar. Yang salah hanya `/ready`, yang memakai `stale` sendirian.

## Keputusan

**Atribusi readiness bergeser dari controller hanya kalau ada bukti positif bahwa sisi kita yang gagal.** Bukti itu adalah run terakhir yang gagal dengan `error_kind: internal_error` — kelas kegagalan yang, menurut definisinya di worker, tidak datang dari adapter.

| Kondisi | `code` | `details.reason` |
|---|---|---|
| ping database gagal | `service_unavailable` | `database_unreachable` |
| cache basi, tanpa bukti kegagalan sisi kita | `unifi_unreachable` | `cache_stale` |
| cache basi **dan** run terakhir gagal `internal_error` **dan** controller tampak menjawab | `service_unavailable` | `cache_not_fresh` |

Aturan pemakaian:

1. Baris ketiga menambahkan `last_error_kind` (`internal_error`) di `details` sebagai bukti yang menyertainya, bersama `last_successful_sync_at`. Dua baris lain tidak berubah sama sekali — bentuk responsnya persis seperti sebelum ADR ini.
2. **`unifi.reachable` bukan bukti, dan tidak boleh dipakai sendirian.** Ia di-refresh sekali per siklus, jadi ia tertinggal: controller yang baru saja mati masih dilaporkan `reachable: true` sampai probe berikutnya gagal. Karena `stale` bisa menjadi `true` tanpa siklus baru selesai (cache cukup menua), ada jendela nyata di mana `stale: true` berdampingan dengan `reachable: true` dan `last_run.status: success`. Di jendela itu tidak ada satu pun kegagalan untuk ditunjuk, dan ADR-004 tetap berlaku. Jendela ini bukan hipotetis: e2e `TestControllerGoingOfflineMakesTheCacheStale` masuk ke dalamnya, dan versi pertama keputusan ini — yang memakai `stale && reachable` sendirian — ditolak olehnya.
3. `internal_error` tetap **500** dan tetap berarti kegagalan tak terduga di kode kita. ADR ini tidak menambah kode error; ia menambah satu nilai `details.reason`.
4. Endpoint list tidak berubah sama sekali.

## Konsekuensi

- Enum `ErrorCode` tidak bertambah, jadi klien yang bercabang pada `code` tidak terpengaruh. Yang bertambah hanya satu nilai `details.reason`; `openapi/openapi.yaml` diperbarui pada deskripsi dan contoh. Karena `details` bersifat bebas (`additionalProperties: true`), tipe yang digenerate hanya berubah pada komentar dokumentasi — `npm run openapi:check` tetap hijau setelah `openapi:gen`.
- Bukti live (2026-10-11, binary nyata + controller cloud nyata + database hidup tanpa skema, jadi setiap tulis gagal): binary sebelum ADR ini membalas `503 unifi_unreachable` / `cache_stale`; binary sesudahnya membalas `503 service_unavailable` / `cache_not_fresh` dengan `last_error_kind: internal_error`, sementara `/sync/status` di detik yang sama melaporkan `unifi.reachable: true`, `last_run.error_kind: internal_error`, `last_run.error_message: "database error (SQLSTATE 42P01)"`, `sites_failed: 20/20`, dan breaker tetap `closed`.
- `scripts/smoke.sh` tidak berubah: ia menguji database mati, yaitu baris pertama tabel.
- Unit test di `apps/api/internal/api` memaku ketiga baris: `TestReadyBlamesOurOwnSideWhenOurWritesFail` (baris ketiga), `TestReadyBlamesUnifiWhileReachabilityHasNotCaughtUp` (jendela pada poin 2 — gagal pada versi pertama keputusan ini), dan `TestReadyStillBlamesUnifiWhenTheControllerIsSilent` (baris kedua).
- `inventory.ErrKindInternal` kini diekspor: kesalahan yang tidak datang dari adapter adalah satu-satunya `error_kind` yang menjadi bukti melawan sisi kita, jadi readiness memang harus membaca kosakata itu, bukan menyalin literal `"internal_error"`.
- `/sync/status` tetap sumber kebenaran untuk diagnosis mendalam; `/ready` tetap ramah alerting.

## Alternatif ditolak

- **Pakai `unifi.reachable` sendirian: "controller menjawab ⇒ jangan sebut UniFi".** Ini versi pertama keputusan ini, dan e2e menolaknya dengan benar. `reachable` adalah sinyal yang tertinggal satu siklus, jadi ia bisa `true` pada controller yang sudah mati — memakainya sendirian akan menghasilkan kebohongan yang berlawanan arah: operator disuruh memeriksa database kita sementara UniFi yang tidak menjawab.
- **Pakai `consecutive_failures == 0` sebagai penanda "sisi kita yang gagal".** Ditolak: field itu sengaja hanya menghitung kegagalan **controller**, karena breaker tidak boleh trip akibat kesalahan kita (`docs/data-model.md`). Pada verifikasi live di atas ia bernilai `0` justru saat 20/20 site gagal karena database kita — kebalikan dari yang dibutuhkan.
- **Tetap `unifi_unreachable`, cukup tambah `details.controller_reachable: true`.** Ditolak: alerting dan orchestrator bercabang pada `code`, bukan `details`. `details` dibaca manusia setelah ia curiga; kode yang salah tetap mengirim orang ke sistem yang salah.
- **`internal_error` / 500 untuk kasus ini.** Ditolak: melanggar ADR-003 §6 dan ADR-004 sekaligus. Cache yang belum terisi adalah kondisi yang **diharapkan dan bisa pulih**, bukan bug; 500 akan menandai proses kita rusak dan memicu restart loop di orchestrator.
- **Kode error baru, misalnya `cache_write_failed`.** Ditolak dengan alasan yang sama seperti ADR-004 menolak kode per-dependensi: daftar kode tumbuh mengikuti jumlah dependensi, padahal `details.reason` sudah membawa kekhususan itu, dan setiap kode baru menambah satu cabang wajib di setiap klien.
- **Beberapa nilai reason untuk kegagalan sisi kita (`cache_write_failed` vs `cache_not_written` vs …).** Ditolak: perbedaannya sudah dibawa `last_error_kind`, dan memecahnya membuat kode readiness lebih rumit daripada masalah yang diselesaikannya.
- **Biarkan `/ready` apa adanya, dan suruh dashboard membaca `/sync/status` untuk penjelasan.** Ditolak: alerting, Kubernetes, dan load balancer membaca `/ready`, bukan dashboard. Satu endpoint yang membalas kebohongan tidak menjadi benar karena ada endpoint lain yang jujur.
- **Ubah juga endpoint list agar 503 saat `stale && reachable`.** Ditolak: itu regresi. Cache yang basi tapi masih bisa disajikan lebih berguna daripada 503, dan perilaku "sajikan + tandai basi" sudah ditetapkan ZENI-15 serta diuji e2e.
