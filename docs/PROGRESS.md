# Jurnal Progres Refactor

Jurnal ini mencatat apa yang sudah, sedang, dan akan dikerjakan. Panjangnya dijaga di bawah 1000 kata, dan isinya diperbarui setiap ada perkembangan.

Rencana lengkap ada di [plan/](plan/README.md).

**Pembaruan terakhir:** 2026-10-08

## Ringkasan

| Fase | Status |
|---|---|
| F0 Fondasi | Selesai. CI belum pernah jalan karena belum ada remote |
| F1 Billing inti | Selesai di level kode dan test unit. Belum diuji manual dari awal sampai akhir |
| F2 Driver MikroTik | Kode dan test dengan router palsu sudah ada. Belum diuji di router nyata |
| F3 RADIUS built-in | Kode dan test sudah ada. Belum diuji dengan `radtest` atau NAS nyata |
| F4 Portal & pembayaran | Package Tripay dan notifikasi sudah ada. Portal pelanggan belum dibuat |
| F5 Pelengkap | Baru dashboard dasar |
| F6 Migrasi data & rilis | Belum dimulai |

## Sudah selesai

- **Fondasi**
  - SQLite (`modernc.org/sqlite`, tanpa CGO) dengan migrasi otomatis.
  - Kode query dibuat dengan `sqlc`.
  - i18n memakai JSON bahasa lama.
  - Login admin (bcrypt + scs) dengan pembatas brute-force dan perlindungan CSRF dari stdlib.
- **Keamanan data**
  - `internal/secret` (AES-GCM) untuk password router, secret pelanggan, dan secret NAS.
  - Kunci diambil dari `NUXBILL_SECRET_KEY`. Jika kosong, kunci dibuat otomatis di `<db>.key`.
- **Schema**
  - Tabel pelanggan, paket, bandwidth, pool, router, langganan, transaksi, voucher, log, NAS, dan sesi RADIUS.
  - Uang disimpan sebagai INTEGER rupiah. Waktu disimpan sebagai unix UTC.
  - Ada FK dan CHECK, plus satu langganan aktif per pelanggan + router + tipe.
- **Billing**
  - Masa aktif (Mins/Hrs/Days/Months/Period) ditulis ulang dari `Package.php`, dicocokkan dengan output PHP asli.
  - Recharge, recharge dari saldo, dan redeem voucher.
  - Job expiry yang idempoten dengan auto-renewal.
- **Clock guard:** job expiry ditahan jika jam mundur atau NTP belum sinkron. Dashboard menampilkan banner peringatan.
- **Device**
  - Driver MikroTik Hotspot dan PPPoE.
  - Tes koneksi router.
  - Sinkronisasi profil paket dan IP pool ke router.
- **RADIUS**
  - Auth PAP, CHAP, dan MS-CHAPv2. Accounting satu baris per sesi.
  - Atribut reply: `Mikrotik-Rate-Limit`, `Session-Timeout`, dan limit data.
  - Shared users dengan pengecualian reconnect dari IP atau MAC yang sama. Sesi yang tidak diperbarui lebih dari 10 menit diabaikan.
- **Admin UI** (Tailwind v4 standalone, Alpine.js, Chart.js, tanpa Node)
  - CRUD pelanggan, paket, bandwidth, router, pool, voucher (cetak dengan QR), dan NAS.
  - Daftar transaksi dan log aktivitas.
  - Recharge dari halaman detail pelanggan.
  - Dashboard dengan 4 kotak angka dan 2 grafik.
  - Mode terang dan gelap.
- **Pembayaran:** package `internal/payment` berisi interface gateway dan implementasi Tripay (buat transaksi, cek status, daftar channel, verifikasi signature callback).
- **Notifikasi:** package `internal/notify` untuk Telegram, WA/SMS lewat URL gateway, email (go-mail), dan webhook keluar bertanda tangan HMAC.

## Sedang dikerjakan

Tidak ada. Pekerjaan dijeda atas permintaan pengguna.

## Akan dikerjakan (urutan)

1. **Sambungkan notifikasi ke billing**
   - Recharge berhasil: kirim notifikasi ke pelanggan dan webhook `payment.paid`.
   - Langganan expired: kirim notifikasi dan webhook `recharge.expired`.
   - Auto-renewal gagal: kirim pesan ke Telegram admin.
   - Tambah job reminder harian (H-1, H-3, H-7) sebagai pengganti `cron_reminder.php`.
2. **Halaman setting yang belum ada:** notifikasi, SMTP, webhook, Tripay, dan timezone. Saat ini timezone hanya dibaca saat start.
3. **Portal pelanggan (F4)**
   - Login, dashboard, riwayat, ganti password, dan inbox.
   - Order paket, pilih channel Tripay, callback, lalu aktivasi otomatis.
   - Simpan `reference` Tripay di transaksi.
   - Registrasi dengan OTP.
4. **Uji lapangan**
   - MikroTik CHR atau perangkat fisik: aktivasi, expiry, disconnect, dan sinkronisasi profil dan pool.
   - `radtest`/`radclient` untuk PAP dan CHAP, lalu login hotspot dan PPPoE via RADIUS.
   - Tripay sandbox.
5. **F5**
   - Laporan dan export CSV, invoice yang bisa dicetak.
   - Widget dashboard lama yang belum ada: pelanggan expired, stok voucher, log aktivitas, monitor cron, monitor cron MikroTik, dan grafik insight pelanggan.
   - Peta dan ODP, kupon, custom field, pesan massal, halaman statis, ganti password admin, dan backup harian (`VACUUM INTO`).
6. **F6:** perintah `nuxbill import` dari MySQL lama, unit systemd, script install, dan panduan instalasi di STB.
7. **Build ARM** dijalankan di CI saja, karena build lokal di amd64 terlalu lama.

## Selisih UI dengan PHPNuxBill lama

Template lama ada di `../phpnuxbill/ui/ui`, sekitar 157 file.

- **Admin, sudah ada padanannya:** dashboard, customers, plan (hotspot, pppoe, balance), bandwidth, pool, routers, voucher, print, logs, settings dasar, dan radius (NAS).
- **Admin, belum ada:**
  - coupons, maps, odp, message, dan reports
  - paymentgateway (halaman setting)
  - change-password, maintenance, dan community
  - port dan vpn (VPN ditunda)
  - halaman 404 dan error yang rapi
- **Portal pelanggan:** belum ada sama sekali. Template lama yang perlu dipadankan:
  - login, register (dengan atau tanpa OTP), forgot
  - dashboard, profile, inbox, activation
  - orderPlan, orderBalance, orderHistory, orderView, selectGateway
  - invoice, sendPlan, phone-update, email-update

## Belum pernah diuji di dunia nyata

- CI di GitHub, karena belum ada remote.
- Koneksi ke MikroTik nyata, RADIUS lewat socket UDP sungguhan, dan Tripay sandbox.
- Email SMTP. Yang sudah dites baru validasi config-nya.
- Tampilan UI baru di browser (menunggu screenshot pengguna).

## Utang teknis yang disengaja

Ditandai dengan komentar `ponytail:` di kode. Daftar lengkapnya: `grep -rn "ponytail:" --include=*.go .`
Yang paling penting: koneksi RouterOS tanpa pooling, sertifikat TLS RouterOS tidak diverifikasi, dan kunci MPPE untuk PPPoE belum dikirim.

## Perbedaan perilaku dari PHP lama (disengaja)

- Router dihubungi setelah commit DB. Jika router gagal, recharge tetap tercatat dan kegagalannya masuk log.
- Tanggal tidak valid di paket Period (misalnya 31 April) dinormalkan ke tanggal 1 bulan berikutnya.
- Kode voucher dibuat dengan `crypto/rand` tanpa karakter yang mirip, bukan md5.
- Bug PHP diperbaiki: ganti username hotspot sekarang benar-benar mengganti, dan rename profil PPPoE ikut mengganti nama di router.
- Paket expired ditolak RADIUS, kecuali jam sistem sedang tidak dipercaya.

## Keputusan yang menunggu pengguna

- **Bisnis:**
  - Apakah default `extend_expiry` dan `enable_balance` tetap aktif saat belum diisi?
  - Apakah aktivasi pertama paket postpaid tetap Rp0?
  - Apakah paket nonaktif tetap boleh di-recharge admin?
- **Remote GitHub:** URL untuk push dan menjalankan CI.
- **Akses uji:** MikroTik/CHR dan kredensial sandbox Tripay.

## Cara kerja

- Opus berperan sebagai orkestrator. Agent Sonnet menulis kode, masing-masing di git worktree terpisah, dan paralel hanya jika package-nya tidak saling bergantung.
- Setiap merge harus lolos `go vet ./...` dan `go test ./...`.
- Kode `sqlc` selalu di-generate ulang, tidak boleh diedit manual. CSS di-build ulang dengan `sh tools/tailwind.sh`.
- Jika alur bisnis tidak jelas, ikuti kode di `../phpnuxbill`.
