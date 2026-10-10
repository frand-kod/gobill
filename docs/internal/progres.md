# Status Progres

Jurnal ini dijaga di bawah 1000 kata. Rencana awal: [rencana/](rencana/README.md). Paritas per layar dan field: [paritas-ui.md](paritas-ui.md). Audit perilaku bisnis: [paritas-bisnis.md](paritas-bisnis.md).

**Untuk:** pengembangan. Hanya tersedia dalam bahasa Indonesia.

**Pembaruan terakhir:** 2026-10-10, persiapan rilis v0.1.4 (lihat [CHANGELOG.md](../../CHANGELOG.md)).

## Status

- Semua fitur yang direncanakan untuk v0.1.4 ada di kode dan test. Dokumen operator sudah diselaraskan dengan kode.
- Belum ada uji lapangan untuk perubahan v0.1.4. Uji lapangan terakhir (2026-10-09) ada di bagian "Uji lapangan" di bawah.
- Tag `v0.1.4` dibuat setelah review pengguna.

## Sudah selesai di v0.1.4

- **Keamanan:** `/radius.php` hanya loopback jika allow-list kosong; `NUXBILL_HTTPS` aktif default; pembatas login per IP dan per username; `X-Forwarded-For` hanya dari proxy tepercaya; pembatas tebak voucher per NAS; pembatas OTP kontak; 2FA TOTP opsional untuk admin dengan kode pemulihan; kata sandi pelanggan minimal 8; kata sandi admin pertama di file, bukan log.
- **Fitur:** `start_on_first_login`; QRIS statis dengan QR terkunci nominal dan tautan WA; `app_url` terisi otomatis; sakelar `notify_customers` dan `notify_otp`; impor PHPNuxBill dari JSON (CLI dan UI, dengan pratinjau dan backup otomatis); restore database dari UI dengan restart.
- **Database:** migrasi 0011 sampai 0014 (`start_on_first_login`, indeks, snapshot pelanggan di `payment_requests`, 2FA admin); pragma SQLite; retensi log bawaan 90 hari; sesi RADIUS basi ditutup otomatis.
- **Operasional:** `/health` minimal; backup mirror (`NUXBILL_BACKUP_MIRROR`) dengan percobaan ulang tiap jam; halaman Status Sistem dan JSON-nya; `/metrics` dengan bearer token; alert operator (disk, NAS diam, job dan kanal gagal, brute force, backup, restart tidak normal, callback pembayaran untuk pelanggan yang sudah dihapus) lewat Telegram dan/atau WhatsApp.
- **Temuan audit bisnis** P1 (recharge pelanggan non-Active ditolak), C2 (template notifikasi ikut impor), dan IM1 (atribut pelanggan ikut impor) sudah dikerjakan.

## Menunggu pengguna

1. **Uji paralel.** Impor data terbaru, set `notify_customers` = Tidak, lalu jalankan NuxBill berdampingan dengan PHPNuxBill selama 1 sampai 3 hari dalam mode baca. Bandingkan sesi, expiry, dan saldo. Langkah lengkap ada di [migrasi-phpnuxbill.md](../id/migration-phpnuxbill.md#checklist-jalan-paralel).
2. **Drill restore.** Uji pemulihan dari file mirror dan dari UI restore di instance uji. Catat hasilnya di sini.
3. **Screenshot UI.** Pengguna mengambil screenshot halaman baru (Status Sistem, 2FA, impor, restore, QRIS, pengaturan alert) untuk direview.
4. **Bersihkan router uji.** Hapus user `claude-test` di MikroTik, profil dan entry `/radius` uji, dan pastikan `split-user-domain=no` di profil yang dipakai pelanggan.
5. **Keputusan rilis.** Review CHANGELOG v0.1.4, lalu tag dan publish.

## Belum pernah diuji di dunia nyata

- Build dan jalan di STB ARM (RAM, startup tanpa RTC, listrik padam, backup ke USB).
- Mirror backup ke NAS atau rclone, dan alert saat mirror gagal.
- Login 2FA dengan aplikasi authenticator sungguhan di browser.
- Scrape `/metrics` oleh Prometheus dan alert lewat WhatsApp.
- FreeRADIUS lewat `/radius.php`, termasuk login voucher lewat REST.
- Tripay sandbox, SMTP, gateway WA dan SMS.
- Pembayaran QRIS nyata (konfirmasi tetap manual).

## Parkir (ditunda dengan sengaja)

- **Verifikasi TLS RouterOS.** Sertifikat RouterOS pada port 8729 belum diverifikasi.
- **Guard SSRF untuk URL notifikasi.** `webhook_url`, `wa_url`, `sms_url`, dan `alt_wga_server_url` belum dibatasi ke alamat publik. Risikonya rendah karena hanya admin yang bisa mengubahnya.
- **Batch penulisan accounting.** Setiap paket accounting interim masih menulis satu baris. Batch baru perlu jika beban naik.
- **Tabel invoices dan ledger.** Tidak ada tabel faktur atau buku besar terpisah. Faktur dibuat dari baris `transactions` (nomor `INV-YYMM-NNNNNN`).

Lain-lain yang belum ada: kupon, ODP, dan inbox belum diimpor dari PHPNuxBill; pajak (`enable_tax`) belum diterapkan; RadSec belum dilayani secara native; kunci MPPE belum dikirim; `hs_auth_method` hanya tampilan, tidak dibaca server RADIUS.

## Utang teknis yang disengaja

Ditandai `ponytail:` di kode (`grep -rn ponytail: .`). Yang paling relevan:

- Pembatas percobaan (login, voucher, OTP), status pesan massal, dan status alert hanya di memori. Reset saat restart. Alert yang masih berlangsung dikirim ulang setelah restart.
- Secret integrasi (SMTP, Telegram, Tripay, metrics token) tersimpan plaintext di tabel `settings`, sama seperti aplikasi lama. Lindungi file database dan backup.
- Penanda perpanjang mandiri disimpan sebagai satu baris setting per pelanggan.

## Perbedaan perilaku dari PHP lama (disengaja)

- Router dihubungi setelah commit DB. Tanggal tidak valid di paket Period dinormalkan.
- Bug PHP diperbaiki: ganti username hotspot, rename profil PPPoE, CHAP terbalik di `radius.php`, batas perpanjang mandiri yang hanya mencatat bulan.
- Login voucher RADIUS: username harus kode voucher, dan voucher tidak bisa mengambil alih akun yang sudah ada.
- Role admin lebih ketat: Admin tidak bisa mengangkat SuperAdmin, SuperAdmin terakhir dilindungi.
- Pengganti fitur lama: PDF diganti halaman cetak HTML, plugin diganti webhook.

## Uji lapangan (MikroTik, RouterOS 6.49.22, 2026-10-09)

- **Mode API:** lolos. Koneksi, sinkron paket, recharge, queue, deactivate, ganti paket, expiry otomatis.
- **RADIUS bawaan:** lolos untuk auth dan accounting, Session-Timeout, rate-limit, dan expiry lewat Session-Timeout.
- **CoA Disconnect:** lolos. Router membalas Disconnect-ACK dan sesi logout.
- **Login voucher hotspot lewat RADIUS:** lolos. Kode salah ditolak. Login ulang dengan kode sama dan pembatas MAC belum diuji lapangan.
- **Pelajaran jaringan:** server NuxBill jangan jadi klien hotspot (NAT universal membuat RADIUS dan CoA gagal; pakai `ip-binding type=bypassed`). Dengan `split-user-domain=yes`, router membuang domain dari User-Name.

## Cara kerja

- Migrasi di `internal/db/migrations/` beku sejak v0.1.0. Hash di `migrations.sum`, dijaga `TestMigrationsFrozen`. Schema baru masuk file bernomor berikutnya.
- Setiap perubahan lolos `go vet` dan `go test ./...`. Panduan build dan rilis ada di [pengembangan.md](pengembangan.md).
- Dump produksi (`docs/*.sql`, `docs/phpnuxbill_*.json`) di-gitignore dan tidak boleh di-commit.

## Lihat juga

- [paritas-ui](paritas-ui.md)
- [paritas-bisnis](paritas-bisnis.md)
- [README](rencana/README.md)
- [pengembangan](pengembangan.md)
- [CHANGELOG](../../CHANGELOG.md)
