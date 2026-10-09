# ADR-005 — Retensi `client_events`

- **Status:** Accepted
- **Date:** 2026-10-09
- **Deciders:** Pemilik workspace (produk) + Tech Lead
- **Konteks epic:** [ZENI-12] Vertical slice & E2E harness — fase 1
- **Menjawab:** OQ-2 di `01 — Requirements (Discovery)`

## Konteks

`client_events` adalah satu-satunya tabel di skema yang tumbuh **append-only tanpa batas**: setiap transisi connect/disconnect klien menambah satu baris, dan tidak ada yang pernah menghapusnya.

Migrasi `0001_init.sql` sudah menuliskan bahwa pertumbuhannya "bounded: O(transitions)" — itu benar **per satuan waktu**, tapi tidak benar secara total. Dengan 120 device dan klien yang datang-pergi setiap hari, tabel ini akan tumbuh selamanya. Itu bukan masalah hari ini; itu masalah yang pasti datang, dan keputusannya murah diambil sekarang sementara implementasinya bisa menyusul.

Yang membuat ini keputusan, bukan sekadar parameter: menyimpan lebih lama berarti bisa menjawab pertanyaan seperti "apakah WiFi di lantai 3 sering putus bulan lalu", sementara menyimpan lebih pendek berarti database tetap ringan dan pemulihan dari backup tetap cepat.

Catatan penting soal **status hari ini**: tabel ini **masih kosong**, karena fitur presence klien (FR-3) belum dibangun. Jadi ADR ini mengikat ke depan, bukan mengubah perilaku yang sedang berjalan.

## Keputusan

**Fase 1 menyimpan `client_events` selama 30 hari**, dihitung dari `occurred_at`, dan ada job pemangkasan berkala yang menghapus baris yang lebih tua.

Aturan pemakaian:

- Ambangnya **30 hari**, dan angka itu hidup sebagai konfigurasi — bukan konstanta yang tersebar di kode.
- Pemangkasan berjalan sebagai job terjadwal di sisi aplikasi, **bukan** lewat `pg_cron` atau ekstensi database, supaya tetap satu deployable dan tetap teruji tanpa Postgres yang dimodifikasi.
- Pemangkasan adalah `DELETE` di **Postgres kita sendiri**. Ini **bukan** operasi tulis ke UniFi dan tidak menyentuh batas read-only fase 1 (lihat ADR-006).
- Pemangkasan harus **idempoten dan aman diulang**; tidak boleh ada satu pun jalur yang menghapus lebih dari ambang.
- Setiap pemangkasan mencatat jumlah baris yang dihapus ke log terstruktur.

**Yang sengaja tidak diputuskan di sini:** retensi `audit_log`. Itu jejak audit operasional dengan sifat yang berbeda — ia bukti, bukan telemetri — dan retensinya kemungkinan lebih panjang serta tunduk pada kebutuhan kepatuhan yang belum kita tanyakan. Menyamakannya dengan `client_events` hanya karena keduanya append-only akan jadi keputusan yang malas.

## Konsekuensi

- Riwayat kehadiran klien lebih tua dari 30 hari **hilang**. Kalau nanti ada kebutuhan forensik yang lebih panjang, itu ADR baru yang menggantikan yang ini — bukan perubahan diam-diam pada angka konfigurasi.
- **Job pemangkasan butuh perhatian pada index.** `client_events` punya `client_events_client_id_idx (client_id)` dan `client_events_site_time_idx (site_id, occurred_at DESC)`, tapi **tidak ada index yang dipimpin `occurred_at` sendirian**. `DELETE ... WHERE occurred_at < now() - interval '30 days'` karena itu tidak bisa memakai keduanya dengan baik. Implementasinya harus memilih: menambah index pada `occurred_at`, atau memangkas per site memakai index komposit yang sudah ada. Ini konsekuensi nyata dari keputusan ini, dan lebih baik diketahui sebelum job-nya ditulis daripada sesudahnya.
- `client_events.client_id` memakai `ON DELETE CASCADE` ke `clients`. Artinya menghapus satu klien sudah menghapus riwayatnya, terlepas dari job pemangkasan. Keduanya tidak saling bertentangan, tapi berarti "riwayat 30 hari" adalah batas atas, bukan jaminan.
- Ukuran tabel jadi terbatas pada ~30 hari transisi, sehingga waktu pemulihan backup tidak tumbuh tanpa batas.
- FR-3 (presence klien) belum dibangun, jadi belum ada satu pun baris yang dipangkas. ADR ini tidak memblokir apa pun hari ini.

## Alternatif ditolak

- **Simpan selamanya.** Ditolak: pertumbuhan tanpa batas pada tabel yang paling sering ditulis. Keputusannya hanya tertunda, dan ditunda ke saat tabelnya sudah besar — saat paling mahal untuk mengubahnya.
- **Snapshot seluruh daftar klien setiap polling, bukan menyimpan transisi.** Ditolak: itu justru sumber masalah yang ingin dihindari — satu baris per klien per siklus, dua orde lebih besar daripada mencatat transisi saja. Migrasi `0001_init.sql` sudah menolak pendekatan ini secara eksplisit.
- **Partisi tabel per bulan.** Ditolak: pada skala ini (satu kantor, ratusan device) partisi menambah kerumitan operasional yang nyata — pemeliharaan partisi, migrasi, dan pemulihan — untuk masalah yang bisa diselesaikan satu `DELETE` terjadwal. Bisa ditinjau ulang kalau skalanya berubah.
- **Memakai `pg_cron` atau ekstensi database.** Ditolak: mengunci kita pada Postgres yang dimodifikasi, dan membuat perilaku aplikasi tidak lagi terlihat dari kode aplikasi maupun teruji di CI.
- **Retensi berbeda per site atau per jenis event.** Ditolak: tidak ada kebutuhan yang membenarkannya. Menambah dimensi konfigurasi tanpa alasan hanya membuat perilakunya lebih sulit ditebak.
