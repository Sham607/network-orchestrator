# ADR-006 — Gerbang aktivasi operasi tulis (fase 2)

- **Status:** Accepted
- **Date:** 2026-10-09
- **Deciders:** Pemilik workspace (produk) + Tech Lead
- **Konteks epic:** [ZENI-12] Vertical slice & E2E harness — fase 1
- **Menjawab:** OQ-3 di `01 — Requirements (Discovery)`
- **Diwajibkan oleh:** ADR-003 §471 — "penambahan write kelak **harus lewat ADR baru**"

## Konteks

Fase 1 **read-only terhadap UniFi**. Itu batas keras, dan hari ini ditegakkan dengan cara yang jujur tapi rapuh: interface adapter hanya punya method `GET`, dan `router.go` hanya mendaftarkan lima route `GET`. Jadi tidak ada jalur tulis — bukan karena ditolak, tapi karena **tidak ada kodenya**.

Kontrak sudah menyiapkan bentuk penolakannya: kedua route aksi membawa `x-status: proposed-gated`, dan dokumen `openapi/openapi.yaml` menjanjikan `403 forbidden` dengan `details.reason: write_disabled` selama gate tertutup. Perlu dicatat bahwa ini belum sepenuhnya benar hari ini — route-nya belum terdaftar sama sekali, sehingga chi membalas **404**, bukan 403. Selisih itu diselesaikan di **ZENI-19**.

ADR-003 §200 sudah menetapkan bahwa penambahan tulis datang sebagai interface `Writer` terpisah, setelah scope API key disetujui, dengan RBAC `admin` + audit wajib. Yang belum ditetapkan ADR mana pun adalah: **apa syarat untuk sampai ke sana, dan siapa yang boleh membuka gate-nya.**

Ini penting justru karena tulisannya menyentuh jaringan kantor yang sedang dipakai orang bekerja. Perbedaan antara salah baca dan salah tulis bukan perbedaan derajat — yang pertama merepotkan, yang kedua mematikan WiFi orang.

## Keputusan

**Gate tulis tetap tertutup sampai seluruh syarat berikut terpenuhi, bukan sebagian:**

1. **Konsol staging khusus ada** dan mencerminkan bentuk produksi, serta operasi tulis yang dimaksud sudah diuji di sana — bukan di produksi.
2. **Akun/kredensial UniFi terpisah khusus tulis**, berbeda dari kunci baca. Kunci baca **tidak boleh** memperoleh scope tulis. Ini prinsip hak akses minimum: kunci yang bocor hari ini hanya membocorkan kemampuan membaca.
3. **Persetujuan formal pemilik workspace** (SYAH HINSYAH AIN MARZIANI).
4. **Kendali keamanan ADR-003 §7 baris 8 terpasang:** RBAC `admin`, CSRF, rate limit pada endpoint tulis, dan audit yang **wajib** menulis setiap aksi.

Setelah keempatnya terpenuhi, gate dibuka lewat **ADR baru yang menggantikan ADR ini** — tidak dengan mengubah statusnya di tempat.

Selama gate tertutup, berlaku aturan berikut:

- **Batasnya harus ditegakkan, bukan kebetulan.** Kedua route aksi membalas `403 forbidden` + `details.reason: write_disabled` sesuai kontrak (ZENI-19 butir 1). "Tidak ada tulis karena tidak ada kodenya" bukan bentuk penegakan; ia tidak bisa dibedakan dari lupa mendaftarkan route.
- **Ada tes yang gagal begitu satu jalur tulis hidup.** ZENI-16 memasangnya di dua lapis: inspeksi tabel route `chi.Walk` dan penolakan `POST`/`PUT`/`PATCH`/`DELETE` di atas HTTP. Keduanya sudah dibuktikan bisa merah dengan menyisipkan route tulis sungguhan.
- `write_disabled` adalah **nilai `details.reason`**, bukan nama kode error. Ia tidak boleh masuk ke enum `ErrorCode` maupun tabel pemetaan kode→HTTP.

## Konsekuensi

- Fase 2 **tidak bisa dimulai dari sisi kode saja.** Ia butuh tindakan pemilik workspace lebih dulu: menyediakan konsol staging dan akun tulis terpisah. Ini konsekuensi yang disengaja — menjadikan persiapan sebagai prasyarat, bukan sebagai langkah yang bisa dilewati saat tenggat mendesak.
- Kunci baca yang dipakai fase 1 tetap tidak berguna untuk menulis, sehingga kebocoran kunci baca tidak berubah menjadi insiden jaringan.
- Konfigurasi runtime akan punya **dua** kredensial UniFi dengan peran berbeda, bukan satu yang dinaikkan haknya. Adapter fase 2 karenanya butuh jalur kredensial terpisah, dan itu perubahan bentuk konfigurasi — bukan sekadar nilai.
- Sampai gate dibuka, `unifi.Writer` tidak ada. Setiap permintaan tulis yang muncul sebelum syaratnya lengkap **dilaporkan sebagai tanda scope yang salah**, bukan dikerjakan diam-diam.
- ADR ini tidak memblokir apa pun di fase 1. Ia hanya memastikan fase 2 tidak bisa "kebetulan aktif".

## Alternatif ditolak

- **Memakai ulang kunci baca untuk operasi tulis.** Ditolak: melanggar hak akses minimum. Kebocoran satu kunci langsung berubah dari "orang bisa membaca inventaris" menjadi "orang bisa me-restart perangkat kantor".
- **Mengaktifkan tulis setelah kodenya ada dan lolos tes.** Ditolak: tes membuktikan kode berperilaku seperti yang ditulis, bukan bahwa tulisannya aman dilakukan pada jaringan yang sedang berjalan. Itu dua pertanyaan berbeda.
- **Menguji tulis langsung di produksi.** Ditolak: sasaran ujinya perangkat yang dipakai orang bekerja. Kesalahan pertama tidak bisa di-undo seperti kesalahan di staging.
- **Membuka gate diam-diam lewat flag konfigurasi.** Ditolak: menghapus jejak keputusan. Membuka gate adalah perubahan kebijakan, dan kebijakan butuh ADR — persis seperti yang ADR-003 §471 minta.
- **Membiarkan route-nya 404 dan mengubah kontrak agar cocok.** Ditolak sebagai *alternatif utama*: kontrak yang menjanjikan penolakan eksplisit lebih berguna bagi klien daripada ketiadaan route, dan penolakan eksplisit itulah yang bisa diuji. (Mengubah kontrak tetap sah sebagai pilihan terakhir, tapi harus diputuskan sadar di ZENI-19, bukan sebagai default.)
