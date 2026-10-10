# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[Semantic Versioning](https://semver.org/).

Versions 0.x mean pre-1.0: breaking changes may happen in minor versions.

## [Unreleased]

## [0.1.4] - 2026-10-10

### Keamanan

- `/radius.php`: `radius_rest_allow` kosong sekarang berarti hanya loopback (sebelumnya semua IP boleh).
- `NUXBILL_HTTPS` aktif secara default: cookie sesi selalu diberi flag `Secure`. `NUXBILL_HTTPS=0` untuk opt-out.
- Pembatas login per username (10 kegagalan dalam 15 menit) selain per IP. Tabel kegagalan dibatasi ukurannya.
- `trust_proxy=yes` hanya membaca `X-Forwarded-For` dari loopback atau dari `trusted_proxies`. Allow-list `/radius.php` tidak pernah memakai `X-Forwarded-For`.
- Pembatas tebak voucher per NAS (100 kegagalan dalam 15 menit), selain per NAS dan MAC.
- Pembatas kirim OTP kontak: per IP 5 kali dalam 15 menit; per nomor jeda 60 detik dan maksimal 5 kali per jam.
- Verifikasi dua langkah (TOTP) opsional untuk akun admin, dengan 8 kode pemulihan sekali pakai. SuperAdmin bisa mereset 2FA admin lain.
- Kata sandi pelanggan minimal 8 karakter (registrasi, ganti, reset, dan edit oleh admin).
- Kata sandi admin pertama tidak lagi dicetak ke log, tetapi ditulis ke `initial-admin-password.txt` (mode 0600) di folder database.
- Cetak voucher hanya untuk peran staf (SuperAdmin, Admin, Agent, Sales).
- Lupa kata sandi memberi balasan yang sama untuk akun yang ada maupun tidak.
- `/health` hanya memuat `status` dan `db`. Versi dan sisa disk pindah ke `/metrics` dan `/admin/status`.

### Fitur

- `start_on_first_login`: masa aktif paket RADIUS mulai saat login RADIUS pertama, bukan saat recharge.
- QRIS statis: unggah foto QRIS merchant di Settings > Payment Gateway. Sistem menyimpan teks QRIS, membuat QR terkunci nominal untuk setiap recharge (halaman `/qris`), dan mengirim tautannya lewat WhatsApp. Pembayaran QRIS tetap dikonfirmasi manual.
- `app_url` terisi otomatis dari alamat yang dipakai admin pertama kali login. Field "App URL" ada di Settings.
- Sakelar global `notify_customers` (semua pesan ke pelanggan) dan `notify_otp` (kode OTP).
- Impor PHPNuxBill dari file backup JSON: `nuxbill import --json=...`, dan halaman admin (Settings > Miscellaneous) dengan pratinjau, konfirmasi, serta backup otomatis sebelum impor.
- Restore database dari UI (SuperAdmin) dengan restart otomatis. Data saat ini dibackup dulu.
- Salinan mirror backup harian di luar perangkat (`NUXBILL_BACKUP_MIRROR`), dengan percobaan ulang tiap jam.

### Database & performa

- Migrasi 0011: kolom `subscriptions.pending_start` untuk `start_on_first_login`.
- Migrasi 0012: indeks untuk pencarian login RADIUS, sesi, langganan, voucher, dan transaksi.
- Migrasi 0013: `payment_requests` menyimpan snapshot pelanggan. Relasi ke pelanggan memakai `ON DELETE SET NULL`, sehingga pembayaran tetap tercatat setelah pelanggan dihapus.
- Migrasi 0014: kolom dan tabel 2FA admin (`admin_recovery_codes`).
- Pragma SQLite: WAL, `synchronous=FULL`, `busy_timeout`, dan foreign key aktif.
- Retensi: `log_keep_days` bawaan 90 hari bila belum diisi (sebelumnya disimpan selamanya). Log, sesi RADIUS tertutup, pesan inbox terbaca, dan pembayaran belum lunas yang lama dihapus per batch 5000 baris.
- Sesi RADIUS terbuka yang tidak diperbarui selama 1 jam ditutup otomatis pada waktu pembaruan terakhirnya.

### Operasional & monitoring

- Halaman Status Sistem (`/admin/status`, JSON di `/admin/status.json`): aplikasi, penyimpanan, RADIUS, job, notifikasi, pembayaran, keamanan, dan backup.
- Endpoint `/metrics` format Prometheus, dilindungi bearer token yang dibuat di Settings > Integrations. Tanpa token, endpoint ini 404.
- Alert operator otomatis (Telegram, WhatsApp, atau keduanya lewat `alert_channel`): disk hampir penuh, NAS diam (`alert_nas_silent_minutes`), job atau kanal notifikasi gagal berulang, brute force, backup lokal terlambat atau mirror gagal, restart setelah berhenti tidak normal, dan callback pembayaran lunas untuk pelanggan yang sudah dihapus.
- Endpoint `/health` untuk uptime monitor.
- `deploy/install.sh` menunjuk ke `initial-admin-password.txt`, bukan ke log.

### Perubahan perilaku dan konfigurasi (perlu diperhatikan saat upgrade dari 0.1.3)

1. Backup database dan file `nuxbill.db.key` dulu. Migrasi 0011 sampai 0014 berjalan otomatis saat start.
2. `radius_rest_allow` kosong sekarang hanya loopback. Jika FreeRADIUS berjalan di host lain, isi dengan IP-nya. Jika tidak, `/radius.php` menolak permintaan.
3. `NUXBILL_HTTPS` aktif secara default. Jika UI diakses lewat HTTP polos ke IP LAN, set `NUXBILL_HTTPS=0`, karena browser tidak mengirim cookie `Secure` di sana.
4. Di belakang reverse proxy: set `trust_proxy=yes`. Jika proxy tidak di host yang sama, isi juga `trusted_proxies`. Tanpa ini, IP klien yang tercatat dan pembatas login adalah IP proxy.
5. Kata sandi pelanggan baru dan yang diganti harus minimal 8 karakter. Kata sandi lama tidak diubah.
6. Kata sandi admin pertama ada di `initial-admin-password.txt` di folder database, bukan di log. Hapus file itu setelah login dan ganti kata sandi.
7. `/health` hanya mengembalikan `status` dan `db`. Monitor yang membaca versi atau sisa disk dari `/health` perlu pindah ke `/metrics` atau `/admin/status.json`.
8. `log_keep_days` bawaan 90 hari. Jika ingin menyimpan selamanya, set `0`.
9. Pembatas login juga berlaku per username. Sepuluh kegagalan dalam 15 menit memblokir username itu sementara.
10. Alert operator baru aktif setelah `telegram_bot` atau kanal WhatsApp diatur di Integrations. Pastikan nomor dan ID tujuan sudah benar agar tidak ada alert yang hilang.
11. Peran Report tidak bisa lagi mencetak voucher.

## [0.1.3] - 2026-10-10

### Added

- Redesigned login pages: admin "control room" split layout and a phone-first customer login with WhatsApp help.
- Customer portal dashboard: active plan countdown, data/time usage bars, connection status with masked MAC, paginated connection history, last login, recent transactions.
- Recharge confirmation as a modal on the recharge page and customer page.
- Reports compute totals in SQL and paginate rows; portal order and activation history paginated; "view all transactions" per customer.

### Changed

- Invoice numbers are now `INV-YYMM-NNNNNN` (global sequence; imported invoices keep their numbers).
- Light mode uses softer off-white surfaces; custom accent colours are toned down in dark mode (contrast still WCAG AA).
- Settings > General simplified: grouped sections, login-page overrides under "advanced", unused legacy fields hidden (values kept).

## [0.1.2] - 2026-10-10

Still pre-1.0: behaviour parity with PHPNuxBill, UI and operator workflow. Not yet field-tested against the WhatsApp server or in a parallel run.

### Added

- WhatsApp sent straight to the "Alternative WhatsApp Gateway" server (`alt_wga_*` settings), with a test button; a `wa_url` pointing at the old PHP plugin is ignored.
- Daily summary for the operator via Telegram and/or WhatsApp.
- `nuxbill import --notifications` imports the PHP notification templates; customer attributes (`Bill`, `Invoice`, `Expired Date`) are imported and honoured.
- Recharge page with a customer picker and a customer summary panel that warns about an active package (double charge guard); the same picker on Add balance and Redeem voucher.
- Bulk delete for vouchers and coupons, prune of used vouchers older than 3 months, message to selected customers, bulk disconnect of online sessions.
- Live search, filters, sorting and reset on lists; global customer search in the header.
- Customer "Diagnosa" card, dashboard "Network" card with router/NAS health and a live router check, collapsible dashboard cards, rolling 12-month charts.
- In-app guide (Panduan) rendered from the operator docs.
- Theme control in the header: light/dark/system, accent colour, density, text size; dark logo variants; PWA manifests.
- Short explanation on every page, toast notifications, footer with version.

### Changed

- Business behaviour aligned with PHPNuxBill after an audit (docs/BUSINESS-PARITY.md): extend restarts expired subscriptions, recharge refused for non-Active customers, one price formula (plan or Invoice, coupon, tax, bills), payment methods from `payment_usings` plus Recharge Zero, dashboard income excludes balance-paid rows and uses `reset_day`, data usage resets on reactivation, cumulative time limit in the built-in RADIUS.
- Deleting a customer keeps the transactions and removes active plans from the router.
- Username can be edited and is synced to the router.
- New design system (tokens, typography, spacing), light sidebar, phone layout without horizontal overflow.
- `internal/web` and `internal/billing` files grouped by feature (move only).

### Fixed

- Router failure during recharge is now shown to the operator instead of "Recharge Successful".
- Notification placeholders like `[[price]]` are filled; unknown ones are removed.
- Settings show the stored value of every field, including image previews.

## [0.1.1] - 2026-10-09

### Added

- License GPL-3.0-or-later, NOTICE, CONTRIBUTING, issue/PR templates.

### Changed

- Go module path is now `github.com/frand-kod/gobill` (was `github.com/frand-kod/nuxbill-go`).

### Fixed

- CI: committed `web/static/app.css` was stale, so the CSS freshness check failed.

## [0.1.0] - 2026-10-09

First release: a single-binary rewrite of PHPNuxBill (MikroTik hotspot and PPPoE billing).

### Added

- Billing core: plans, customers, recharge, deposit and subscriptions, with balances stored as integer rupiah and atomic balance changes.
- MikroTik driver with RouterOS API mode. *Field-tested* (RouterOS 6.49.22): connection, profile sync, recharge, queues, expiry, and plan changes.
- Built-in RADIUS server (auth and accounting), plus FreeRADIUS-compatible REST endpoint `/radius.php`, hotspot voucher login, and CoA Disconnect. *Field-tested:* built-in RADIUS auth and accounting. *Untested:* CoA re-test after the NAS-IP fix, and FreeRADIUS REST.
- Customer portal: balance orders, voucher activation, extend, balance transfer, OTP, and inbox.
- Tripay payment gateway flow. *Untested:* Tripay sandbox.
- Admin UI: dashboard widgets, reports and invoices, coupons, maps and ODP, custom fields, static pages, admin users, router monitor, and admin logs.
- Notifications and a reminder job, with webhook in place of the old plugin system.
- Import from a PHPNuxBill MySQL database, run in a single transaction with a dry-run report.
- Daily database backup, maintenance mode, and clock guard for hardware without an RTC. *Untested:* STB hardware.
- Release CI for linux amd64, arm64, and armv7, with `sha256sums.txt`.

### Security

- bcrypt for admin and customer passwords. Legacy SHA-1 hashes are rehashed on first login.
- AES-GCM encryption for router, customer, and NAS secrets.
- CSRF protection, brute-force throttles for login, voucher, and OTP, and RADIUS per-user auth throttles.
- Sessions are revoked when a password, role, or status changes.
- RADIUS hardening: strong NAS secrets, duplicate request detection, and Message-Authenticator on responses (BlastRADIUS).
- Notification errors are redacted before logging, so tokens and API keys do not leak.

### Fixed

- Hotspot and PPPoE users created with an empty password.
- Empty timezone setting fell back to UTC.
- The REST path reset brute-force throttles; now one shared RADIUS server holds them.
- `NUXBILL_RADIUS=` (empty) did not disable the UDP listener.
- CoA sent the wrong NAS-IP-Address. Also decodes Error-Cause and logs unknown NAS.
- Requests from unregistered NAS were dropped without a log entry.
