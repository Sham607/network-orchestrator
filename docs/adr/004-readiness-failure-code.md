# ADR-004 — Readiness Failure Code

- **Status:** Accepted
- **Date:** 2026-10-07
- **Deciders:** Tech Lead
- **Konteks epic:** [ZENI-1] Network Orchestrator — Foundation
- **Supersedes:** daftar kode error di ADR-003 §5 (menambah satu kode). Sisanya tidak berubah.

## Konteks

ADR-003 §6 sudah menetapkan bahwa `GET /api/v1/ready` membalas **503** ketika database tidak reachable atau cache sudah basi, "dengan alasan terstruktur". Itu keputusan yang sudah accepted.

Masalahnya ada di ADR-003 §5: daftar kode error di sana adalah **daftar tetap**, dan di dalamnya tidak ada kode 503 generik.

| Kode 503 yang ada | Kenapa tidak cocok |
|---|---|
| `unifi_unreachable` | Artinya spesifik *controller UniFi* tidak reachable. Postgres yang mati bukan controller yang mati. |
| `unifi_schema_drift` | Bentuk respons controller berubah. Tidak ada hubungannya. |
| `internal_error` | Dipetakan ke **500** dan berarti "kegagalan tak terduga di sisi server". |

Handler `/ready` saat itu memakai `internal_error`, sehingga membalas **500**. Kode bertentangan dengan ADR-003 §6, dan `scripts/smoke.sh` menerima `500` **atau** `503` sehingga CI tidak pernah menangkapnya.

Dua jalan keluar:

1. Melunakkan ADR-003 §6: readiness gagal cukup dibalas 500.
2. Menambah satu kode 503 generik untuk "dependensi yang dibutuhkan sedang tidak tersedia".

Pilihan 1 ditolak karena mencampur dua hal yang berbeda secara operasional. **Readiness gagal adalah kondisi yang diharapkan dan bisa pulih** — Postgres sedang restart, misalnya. `internal_error` berarti ada yang tidak terduga di kode kita. Kalau keduanya dibalas dengan kode yang sama, alerting tidak bisa membedakan "database sedang naik" dari "ada bug", dan operator akan terbangun untuk kejadian yang salah. 503 juga status yang benar secara protokol untuk "layanan sementara tidak tersedia".

## Keputusan

Menambahkan satu kode ke daftar ADR-003 §5:

| code | HTTP | Kapan dipakai |
|---|---|---|
| `service_unavailable` | 503 | Dependensi yang dibutuhkan API sedang tidak tersedia — mis. Postgres tidak reachable saat readiness check |

Aturan pemakaian:

- `GET /api/v1/ready` memakai `service_unavailable` ketika ping database gagal.
- `details.reason` membawa alasan terstruktur (`"database_unreachable"`), memenuhi permintaan ADR-003 §6 akan "alasan terstruktur" tanpa menambah kode baru per dependensi.
- `internal_error` tetap 500 dan tetap berarti **kegagalan tak terduga**.
- `unifi_unreachable` dan `unifi_schema_drift` tetap 503, tetap **khusus** masalah controller UniFi.
- Pesan tetap tersanitasi; `details.request_id` tetap selalu ada.

Pembagian ini juga mengunci perilaku yang belum diimplementasikan: ketika sync worker ada dan controller sedang offline sehingga cache basi, readiness gagal dengan `unifi_unreachable` (bukan `service_unavailable`) — karena penyebabnya memang controller. `service_unavailable` untuk dependensi internal API sendiri.

## Konsekuensi

- Daftar kode di ADR-003 §5 bertambah satu; keputusan lain di ADR-003 tidak berubah.
- `openapi/openapi.yaml` disinkronkan: enum `ErrorCode`, tabel deskripsi di atas file, dan komponen respons `ServiceUnavailable`.
- `scripts/smoke.sh` meng-assert **tepat** `503` + `code: service_unavailable`, sehingga regresi 500 tidak bisa lolos lagi.
- Ini **penambahan nilai enum**, bukan perubahan bentuk: klien yang bercabang pada `code` tidak terpengaruh. Klien yang melakukan switch eksaustif harus menambah satu cabang.
- Gating staleness di `/ready` belum disentuh — handler masih hanya ping database. Itu menunggu sync worker, dan kodenya sudah ditetapkan di atas.

## Alternatif ditolak

- **Tetap `internal_error` / 500.** Ditolak: melanggar ADR-003 §6 yang sudah accepted, dan menyamakan "dependency sedang down" dengan "ada bug di kode kita".
- **Memakai `unifi_unreachable` untuk database yang mati.** Ditolak: kodenya berarti controller UniFi. Memakainya untuk Postgres membuat alerting dan operator mencari masalah di tempat yang salah.
- **Melunakkan ADR-003 §6 agar readiness gagal dibalas 500.** Ditolak: 503 adalah status yang benar dan sudah dipakai compose serta orchestrator lain; 500 akan menandai proses kita sebagai rusak, bukan dependensinya.
- **Kode baru per dependensi (`database_unreachable`, `redis_unreachable`, …).** Ditolak: daftar kode tumbuh mengikuti jumlah dependensi, padahal `details.reason` sudah membawa kekhususan itu tanpa menambah kode yang harus ditangani klien.
