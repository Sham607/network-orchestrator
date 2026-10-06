# ADR-001 — UniFi API Surface & Auth

- **Status:** Accepted
- **Date:** 2026-10-06
- **Deciders:** Tech Lead
- **Konteks epic:** [ZENI-1] Network Orchestrator — Foundation: Stack, Architecture & Data Model

> Dokumen ini dipindah ke repo dari hasil `ZENI-2` saat menutup stage 1.
> Bagian "Koreksi pasca-verifikasi" mem-batalkan usulan awal "wajib `lan-worker` di LAN".




**Perubahan besar dari asumsi awal:** semua temuan di bawah diverifikasi **live dari runtime ini** — termasuk akses ke Network Integration API **per-console**. Yang ternyata membuka itu adalah **Connector Proxy cloud**, yang jalan pakai API key yang sudah kita punya dan **tidak butuh runtime berada di LAN kantor**. Jadi blocker "butuh base URL controller lokal" sudah teratasi: kita tidak perlu key lokal terpisah untuk read.

## Konteks

Kita bangun control plane internal untuk ekosistem UniFi (~21 console/site, ~120 perangkat). Deployment (termasuk staging) akan berada di jaringan kantor — keputusan user. Yang harus diputuskan: permukaan API mana, auth, topologi, endpoint map, read vs write, dan rate limit/polling.

## Keputusan

1. **Auth:** pakai **Network Integration API resmi** dengan header `X-API-KEY`. Tiga permukaan berbeda tetap dipisah, tapi **satu key cloud cukup untuk dua di antaranya** (Site Manager + Connector Proxy).
2. **Transport:** adapter punya **dua transport**: `cloud` (fleet) dan `console` (per-site detail + write). `console` diakses lewat **Connector Proxy** sebagai default — bukan harus LAN.
3. **Fase 1 = read-only.** Semua endpoint write ditunda sampai ada approval eksplisit + audit.
4. **Sinkronisasi:** polling saja (v1 tidak punya webhook/event endpoint).

## Fakta terverifikasi

**Akun/key.** Satu API key runtime (`X-API-KEY`, dari env `unifi_apikey`) berhasil untuk `https://api.ui.com/v1/...` **dan** `https://api.ui.com/v1/connector/consoles/{consoleId}/proxy/network/integration/v1/...`. Console target sudah memenuhi minimum Integration API: `GET .../integration/v1/info` → `{"applicationVersion":"10.6.106"}` (≥ 9.3). Firmware console `5.1.33` (≥ 5.0.3 untuk connector).

**Permukaan.**

| Permukaan | Base URL | Auth | Cakupan | Status |
|---|---|---|---|---|
| Site Manager (cloud) | `https://api.ui.com/v1/` | `X-API-KEY` | fleet: hosts, sites, devices (+statistik site) | **terverifikasi** |
| Connector Proxy | `https://api.ui.com/v1/connector/consoles/{consoleId}/proxy/network/integration/v1` | `X-API-KEY` | per-console Integration API, tanpa LAN | **terverifikasi** |
| Integration API lokal | `https://<console>/proxy/network/integration/v1` | `X-API-KEY` | read + write | belum dites (butuh LAN) |
| Classic Controller API | `/proxy/network/api/s/{site}/...` | cookie + CSRF | legacy penuh | **ditolak** |

**Peta endpoint v1 — hasil probe nyata (bukan asumsi).**

Read (200 OK):
- `GET /info`
- `GET /sites`
- `GET /sites/{siteId}/devices` (paginated)
- `GET /sites/{siteId}/devices/{deviceId}`
- `GET /sites/{siteId}/clients` (paginated)
- `GET /sites/{siteId}/clients/{clientId}`
- `GET /sites/{siteId}/networks` (+ `/{networkId}`)
- `GET /sites/{siteId}/wans`
- `GET /sites/{siteId}/device-tags`

Write (route ada; `GET` → `405 METHOD_NOT_ALLOWED`, sengaja **tidak dieksekusi** di produksi):
- `POST /sites/{siteId}/devices/{deviceId}/actions`
- `POST /sites/{siteId}/clients/{clientId}/actions`

**Tidak ada di v1 (404):** `wifi`, `vouchers`, `events`, `statistics`, `health`, `dns`, `firewall`, `rogue-aps`, `usergroups`, `radius`, `peers`, `vpn`, `pending-devices`, `lan-networks`, `wifi-broadcasts`. Ini penting — lihat "Konsekuensi".

**Pagination:** `?offset=&limit=` → `{offset,limit,count,totalCount,data[]}`. Default `limit=25`; nilai besar (mis. 200) diterima.

## Contoh response asli (disanitasi)

Device list — `GET /sites/{siteId}/devices`:
```json
{"offset":0,"limit":25,"count":5,"totalCount":5,"data":[
  {"id":"<device-uuid>","macAddress":"<masked>","ipAddress":"<masked>","name":"<ap-name>",
   "model":"U6 Pro","state":"ONLINE","supported":true,"firmwareVersion":"6.8.2",
   "firmwareUpdatable":false,"features":["accessPoint"],"interfaces":["radios"]}
]}
```

Device detail — `GET /sites/{siteId}/devices/{deviceId}` (radio + uplink tersedia):
```json
{"id":"<device-uuid>","name":"<ap-name>","model":"U6 Pro","state":"ONLINE",
 "firmwareVersion":"6.8.2","adoptedAt":"2025-06-02T07:02:25Z","provisionedAt":"2026-08-05T02:06:38Z",
 "uplink":{"deviceId":"<device-uuid>"},"features":{"accessPoint":{}},
 "interfaces":{"radios":[
   {"wlanStandard":"802.11ax","frequencyGHz":2.4,"channelWidthMHz":20,"channel":11},
   {"wlanStandard":"802.11ax","frequencyGHz":5,"channelWidthMHz":40,"channel":161}]}}
```

Client detail — `GET /sites/{siteId}/clients/{clientId}`:
```json
{"type":"WIRELESS","id":"<client-uuid>","name":"<client-name>","connectedAt":"2026-09-14T02:06:45Z",
 "ipAddress":"<masked>","macAddress":"<masked>","uplinkDeviceId":"<device-uuid>","access":{"type":"DEFAULT"}}
```

Network detail — `GET /sites/{siteId}/networks/{networkId}`:
```json
{"management":"GATEWAY","id":"<network-uuid>","name":"Default","enabled":true,"vlanId":1,
 "metadata":{"origin":"SYSTEM_DEFINED","configurable":true},"default":true}
```

Error modes:
- Cloud, key invalid → `401 {"code":"unauthorized","httpStatusCode":401,"message":"unauthorized","traceId":"<id>"}`
- Integration API, path tak ada → `404 {"statusCode":404,"statusName":"NOT_FOUND","code":"api.request.error","message":"No endpoint GET ...","timestamp":"...","requestPath":"..."}`
- Method salah → `405 {"statusCode":405,...,"message":"Request method 'GET' is not supported"}`

## Read/write untuk fase 1

**Read (fase 1, semua boleh):** `info` (version gate), `sites`, `devices`, `devices/{id}`, `clients`, `clients/{id}`, `networks`, `wans`, `device-tags`, plus Site Manager `hosts`/`sites`/`devices` untuk fleet + statistik.

**Write (ditunda ke fase 2, butuh approval + key terpisah + audit):** device `actions` (mis. `RESTART`) dan client `actions` (mis. `BLOCK`/`UNBLOCK`). Nama aksi yang didukung perlu diverifikasi di staging lebih dulu — tidak dieksekusi di produksi.

## Alternatif yang ditolak

- **Classic Controller API (cookie + CSRF).** Ditolak: auth sesi + CSRF rapuh untuk automation, tak resmi, dan tidak memberi keuntungan yang kita butuhkan di fase 1. Kalau nanti perlu konfigurasi yang tak ada di v1 (SSID/PoE/firewall), itu keputusan ADR baru — bukan default.
- **Wajib runtime di LAN + Integration API lokal langsung.** Ditolak sebagai syarat: Connector Proxy terbukti jalan dari luar LAN. Akses LAN langsung tetap disimpan sebagai opsi latensi lebih baik untuk runtime yang sudah di kantor.
- **Connector Proxy saja tanpa cloud Site Manager.** Ditolak: fleet view (21 console + statistik per-site seperti WAN uptime, ISP, txRetry, jumlah klien) hanya ada di Site Manager cloud.

## Konsekuensi & koreksi asumsi

- **Adapter tetap dua transport** (`cloud` + `console`), dipilih per kapabilitas. Cloud = fleet/statistik; console = device/client/network detail + write.
- **Client monitor terbatas:** v1 **tidak** memberi signal strength / SSID / AP-name per klien — hanya `type`, `name`, `macAddress`, `ipAddress`, `connectedAt`, `uplinkDeviceId`, `access.type`. Jadi fitur "sinyal klien" dan "klien per SSID" **tidak** bisa dari v1. Koreksi terhadap peta fitur yang saya posting sebelumnya.
- **SSID/WLAN edit, voucher, PoE, firewall TIDAK tersedia di v1** (endpoint `wifi`/`vouchers` 404). Kendali fase 2 lewat v1 praktis terbatas ke *device actions* dan *client actions*. Fitur konfigurasi butuh jalur lain → ADR terpisah.
- **Statistik:** `statistics/health` tidak ada di v1 console; angka agregat per-site (txRetry, wanUptime, counts) tersedia lewat Site Manager cloud. Histori/tren harus kita simpan sendiri di DB (UniFi tetap source of truth).
- **Tidak ada webhook/event di v1** → polling. Interval usulan: fleet cloud 60s, detail console 30s, detail on-demand. Hormati `429` + `Retry-After`. Batas connector proxy belum diketahui — perlu diukur & di-backoff.
- **Credential:** key tak punya scoping per-key; ia mewarisi role admin pembuatnya. Untuk read-only pakai key dari admin **View Only**; key write dipisah. Jangan log/git/plaintext DB.
- **TLS:** lewat Connector Proxy tidak ada isu cert. Kalau nanti akses console langsung, cert self-signed → pin CA console, jangan matikan verifikasi global.

## Open items

1. Konfirmasi **fase 1 cukup read-only** (rekomendasi saya: ya) → write tanpa approval berarti nol risiko ke jaringan produksi.
2. Kalau write disetujui: siapkan **staging console** + key write terpisah, lalu verifikasi nama aksi (`RESTART`, `BLOCK`, dst.) di staging sebelum produksi.
3. Ukur limit/backoff Connector Proxy saat implementasi adapter.

## Acceptance

- Keputusan + alternatif ditolak + alasan: terpenuhi di dokumen ini.
- Response asli endpoint read-only, disanitasi tanpa API key: terpenuhi (device/client/network/detail + error modes).
- Daftar endpoint read/write fase 1: terpenuhi.

## Koreksi pasca-verifikasi (2026-10-06)

Temuan awal "console tidak bisa di-reach dari runtime → wajib worker di LAN" **salah** — tes memakai path yang keliru (`api.ui.com/proxy/network/integration/v1/...` → 404) lalu menyimpulkan "tidak ada proxy".

Path yang benar dan **terverifikasi live** dengan key cloud yang sama:

```
https://api.ui.com/v1/connector/consoles/{consoleId}/proxy/network/integration/v1/...
```

| Endpoint lewat Connector Proxy | Hasil |
|---|---|
| `GET /info` | 200 — `applicationVersion 10.6.106` |
| `GET /sites` | 200 |
| `GET /sites/{siteId}/devices` | 200 |
| `GET /sites/{siteId}/clients` | 200 |
| `GET /sites/{siteId}/networks`, `/wans`, `/device-tags` | 200 |
| `GET /sites/{siteId}/wifi`, `/vouchers` | 404 |
| `GET /sites/{siteId}/devices/{id}/actions` | 405 (route ada, POST-only) |

Konsekuensi:

1. **`lan-worker` dibatalkan.** Satu deployable. Akses LAN jadi opsional (optimasi latensi), bukan syarat.
2. **Write route tersedia lewat proxy** — `POST .../devices/{id}/actions` dan `.../clients/{id}/actions`. Ini menaikkan sensitivitas key: satu key bocor = jangkauan **write** ke seluruh fleet. Karena itu fase 1 tetap **read-only**, dan write di-gate `proposed-gated` (lihat kontrak OpenAPI).
3. **Jangan expose console ke internet** — tetap berlaku, dan tidak pernah dibutuhkan karena proxy resmi sudah menangani jalurnya.

Pelajaran yang dicatat: 404 pada satu path bukan bukti tidak adanya jalur; klaim "tidak bisa" butuh probe terhadap path resmi, bukan satu percobaan.
