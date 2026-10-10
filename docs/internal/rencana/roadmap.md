# 04 — Roadmap

Setiap fase menghasilkan binary yang bisa dijalankan. Fase berikutnya baru dimulai setelah kriteria selesai fase sebelumnya terpenuhi. Checklist fitur per controller lama ada di [audit-legacy.md](audit-legacy.md#checklist-fitur-parity).

**Untuk:** pengembangan. Hanya tersedia dalam bahasa Indonesia.

## F0 — Fondasi

Isi:
- Repo baru, `go.mod`, struktur folder sesuai [arsitektur-awal.md](arsitektur-awal.md).
- Config dari env, `slog`, graceful shutdown.
- Migrator (`PRAGMA user_version`) dan migrasi `0001_init.sql` (admin, settings).
- Login/logout admin (bcrypt + scs), middleware role, CSRF.
- Halaman settings dasar dan i18n (JSON lama).
- CI: `go vet`, `go test ./...`, dan cross-build `linux/arm64`, `linux/arm` (GOARM=7), `linux/amd64`.

Selesai jika:
- Binary jalan di STB, bisa login admin, dan bisa mengubah satu setting.
- CI hijau untuk ketiga arsitektur.

## F1 — Billing inti

Isi:
- CRUD pelanggan, paket (Hotspot/PPPoE/Balance, prepaid/postpaid), bandwidth, IP pool, router.
- Aktivasi dan perpanjangan paket, transaksi, saldo, voucher (generate, cetak dengan QR, aktivasi).
- Log aktivitas dan pencarian pelanggan.
- Job `clockGuard` dan `expiry` (driver masih `Dummy`).

Selesai jika:
- Unit test kalkulasi masa aktif mencakup Mins, Hrs, Days, Months, dan Period. Perilakunya dibandingkan dengan `system/autoload/Package.php`, termasuk akhir bulan dan tanggal 29–31.
- Unit test `clockGuard`: job `expiry` tidak berjalan saat jam mundur.
- Job `expiry` idempoten (ada test-nya).
- Semua uang tersimpan sebagai integer.

## F2 — Driver device MikroTik

Isi:
- Implementasi `Device` untuk `MikrotikHotspot` dan `MikrotikPPPoE` memakai `go-routeros`.
- Tes koneksi router dan status online/disconnect.
- Sinkronisasi profil bandwidth ke MikroTik.

Selesai jika:
- Test driver memakai fake RouterOS (server TCP lokal atau interface klien yang di-fake).
- Uji manual di satu MikroTik nyata (CHR atau perangkat fisik): aktivasi, expiry, dan disconnect.

## F3 — RADIUS built-in

Isi:
- Server auth (PAP, CHAP, MS-CHAPv2) dan accounting dengan `layeh.com/radius`.
- Manajemen NAS beserta secret per NAS.
- Atribut reply: rate limit dari bandwidth, sisa waktu (`Session-Timeout`), dan `Mikrotik-Rate-Limit`.

Selesai jika:
- Test auth memakai paket RADIUS hasil rekaman atau buatan.
- Uji manual: hotspot dan PPPoE MikroTik login via RADIUS ke STB.
- `radtest`/`radclient` berhasil untuk PAP dan CHAP.

## F4 — Portal pelanggan & pembayaran

Isi:
- Login pelanggan, dashboard, riwayat, ganti password.
- Registrasi mandiri dengan OTP via WA/SMS.
- Order paket, interface `PaymentGateway`, dan **satu gateway pertama: Tripay** (lihat [README](README.md)).
- Notifikasi Telegram, WA/SMS via URL gateway, dan SMTP.
- Outgoing webhook per event.

Selesai jika:
- Alur order sampai pembayaran sampai aktivasi otomatis berhasil di sandbox gateway.
- Callback dengan signature salah ditolak (ada test-nya).

## F5 — Pelengkap

Isi:
- Laporan transaksi, export CSV, halaman print (invoice dan laporan).
- Dashboard dengan widget tetap: pendapatan bulanan, pelanggan aktif/expired, stok voucher.
- Peta pelanggan dan ODP, kupon, custom field, inbox dan pesan massal, halaman statis.
- Job `reminder` dan `backup`.

Selesai jika:
- Checklist parity di [audit-legacy.md](audit-legacy.md) terpenuhi untuk semua baris kecuali `Tunda` dan non-goal.

## F6 — Migrasi data & rilis

Isi:
- Perintah `nuxbill import --mysql-dsn=...` yang membaca DB PHPNuxBill lama dan menulis ke SQLite baru:
  - harga (`varchar`) dikonversi ke integer, dan baris yang gagal dikonversi dilaporkan
  - `expiration` + `time` digabung menjadi `expires_at` dengan timezone dari setting lama
  - secret pelanggan dienkripsi
  - password admin ditandai `legacy_sha1`
- Laporan hasil import: jumlah baris per tabel, baris yang dilewati, dan alasannya.
- Uji paralel: impor data nyata, lalu bandingkan daftar pelanggan aktif dan tanggal expiry dengan sistem lama.
- Paket rilis: binary per arsitektur, unit systemd, script install, dan panduan instalasi STB (termasuk NTP dan backup).

Selesai jika:
- Import data nyata tanpa selisih jumlah pelanggan aktif dan tanggal expiry.
- Instalasi bersih di STB mengikuti panduan berhasil dalam waktu kurang dari 15 menit.

## Tidak dikerjakan / ditunda

| Item | Status | Alasan |
|---|---|---|
| Plugin manager runtime | Tidak di-port | Diganti dengan interface yang dikompilasi dan webhook |
| Theme engine / `ui_custom` | Tidak di-port | Satu tema. Branding cukup lewat logo dan nama usaha |
| Online updater | Tidak di-port | Update dilakukan dengan mengganti binary |
| Face detection | Tunda | Dipakai sedikit dan butuh library berat |
| Driver MikroTik VPN | Tunda | Menunggu permintaan pengguna |
| Endpoint `rlm_rest` kompatibel FreeRADIUS | Tunda | RADIUS built-in sudah cukup |
| PostgreSQL | Tunda | SQLite cukup. Lihat ADR |
| Windows / shared hosting | Tidak didukung | Non-goal |

## Lihat juga

- [README](README.md)
- [audit-legacy](audit-legacy.md)
- [arsitektur-awal](arsitektur-awal.md)
