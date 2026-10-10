# Rencana Refactor PHPNuxBill

Dokumen ini adalah titik masuk untuk rencana rewrite PHPNuxBill ke stack modern.
> Disalin dari repo `phpnuxbill` (`docs/internal/rencana/`). Path seperti `system/...`, `radius.php`, dan `install/...` merujuk ke kode lama di `../phpnuxbill`.

**Untuk:** pengembangan. Hanya tersedia dalam bahasa Indonesia.


## Tujuan

1. **Stabil di perangkat kecil.** Target utama adalah STB Armbian (ARM, RAM 1–2 GB, eMMC). Aplikasi juga harus jalan di VPS dan Docker.
2. **Mudah di-maintain.** Satu bahasa, satu binary, dan dependency sedikit. Kode harus bisa dibaca developer yang berlatar Laravel/PHP/TS.
3. **Aman.** Tidak ada SQL injection. Password admin di-hash dengan benar. Secret pelanggan dienkripsi. CSRF dilindungi secara default.
4. **Data billing benar.** Uang disimpan sebagai integer. Waktu expiry tunggal dan konsisten. Ada guard terhadap jam sistem yang salah.
5. **Teruji.** Logika billing (masa aktif, saldo, voucher) punya unit test sejak hari pertama.
6. **Fitur setara (parity)** dengan PHPNuxBill untuk fitur yang dipakai di lapangan. Lihat checklist di [audit-legacy.md](audit-legacy.md).

## Non-goals

- Kompatibilitas plugin PHP lama. Plugin dan payment gateway di-port bertahap ke Go.
- Dukungan shared hosting/cPanel dan Windows.
- Theme engine dan plugin manager runtime.
- Online updater. Update dilakukan dengan mengganti binary.

## Ringkasan keputusan

| Aspek | Keputusan |
|---|---|
| Strategi | Rewrite total, lalu migrasi data satu kali dari MySQL lama |
| Bahasa | Go (rilis stabil terbaru, ≥ 1.25) |
| Framework | Tidak ada. `net/http` stdlib |
| UI | `html/template` + htmx (server-rendered) |
| Database | SQLite (`modernc.org/sqlite`, tanpa CGO) |
| RADIUS | Server built-in (`layeh.com/radius`). FreeRADIUS opsional |
| Deploy | Satu binary + unit systemd |

Detail dan alasan: [keputusan-stack.md](keputusan-stack.md).

## Daftar dokumen

| File | Isi |
|---|---|
| [audit-legacy.md](audit-legacy.md) | Audit kode lama: arsitektur, titik integrasi, masalah, checklist fitur |
| [keputusan-stack.md](keputusan-stack.md) | ADR: perbandingan opsi dan keputusan stack |
| [arsitektur-awal.md](arsitektur-awal.md) | Struktur aplikasi baru, interface, schema, aturan khusus STB |
| [roadmap.md](roadmap.md) | Fase kerja dan kriteria selesai |

## Keputusan terbuka

Keputusan berikut tidak memblokir F0, tetapi harus diputuskan sebelum fase terkait.

| # | Pertanyaan | Default | Dibutuhkan sebelum |
|---|---|---|---|
| 1 | Lokasi kode baru | Repo baru (`nuxbill-go`). Repo ini tetap jadi referensi sampai parity tercapai | F0 |
| 2 | Payment gateway pertama yang di-port | **Tripay** (diputuskan 2026-10-08) | F4 |
| 3 | Dukungan PostgreSQL untuk instalasi besar | Tidak. SQLite cukup untuk ribuan pelanggan per instance | Saat ada kebutuhan nyata |
| 4 | Driver MikroTik VPN (`MikrotikVpn.php`) ikut di-port? | Ditunda sampai ada pengguna yang memintanya | F2 |

## Lihat juga

- [progres](../progres.md)
- [arsitektur](../arsitektur.md)
