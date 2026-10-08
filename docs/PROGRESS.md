# Jurnal Progres Refactor

Jurnal progres, dijaga di bawah 1000 kata.

Rencana lengkap ada di [plan/](plan/README.md).

**Pembaruan terakhir:** 2026-10-08

## Ringkasan

| Fase | Status |
|---|---|
| F0 Fondasi | Selesai. CI belum pernah jalan karena belum ada remote |
| F1 Billing inti | Selesai, termasuk top-up saldo admin, daftar dan edit langganan, serta daftar pelanggan dengan CSV. Belum diuji manual dari awal sampai akhir |
| F2 Driver MikroTik | Kode dan test dengan router palsu sudah ada. Belum diuji di router nyata |
| F3 RADIUS built-in | Selesai di level kode: paket RADIUS, Disconnect-Request (RFC 5176), sesi online, dan pemakaian data. Belum diuji dengan NAS nyata |
| F4 Portal & notifikasi | Portal (order via saldo), halaman setting, dan notifikasi tersambung. Tripay opsional, dikerjakan paling akhir |
| F5 Pelengkap | Sebagian: laporan + CSV/cetak, invoice, widget dashboard, user admin, ganti password, backup harian, mode maintenance. Belum: kupon, peta/ODP, pesan, custom field, halaman statis |
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
- **Pembayaran (belum dipakai):** package `internal/payment` berisi interface gateway dan implementasi Tripay (buat transaksi, cek status, daftar channel, verifikasi signature callback).
- **Notifikasi:** Telegram, WA/SMS (URL gateway), email, dan webhook HMAC. Tersambung ke recharge, expiry, dan auto-renewal gagal. Ada job reminder harian H-1/3/7.
- **Setting:** sub-halaman umum, lokalisasi, template notifikasi, integrasi, dan lain-lain. Perubahan langsung berlaku tanpa restart.
- **Portal pelanggan:** login, registrasi (OTP jika gateway WA/SMS ada), dashboard, profil, riwayat, dan order paket via saldo.

## Sedang dikerjakan

Tidak ada.

## Akan dikerjakan (urutan)

Prioritas: fitur inti (billing, MikroTik, RADIUS). Tripay opsional dan dikerjakan paling akhir.

1. **Uji lapangan:** MikroTik (CHR atau fisik) dan `radtest`/`radclient`, lalu login hotspot dan PPPoE via RADIUS.
2. **Sisa F5:** kupon, peta/ODP, pesan massal dan inbox, custom field, halaman statis, lupa password portal.
3. **F6:** `nuxbill import` dari MySQL lama, unit systemd, script install, dan panduan STB.
4. **Tripay:** setting pembayaran, order, dan callback (package-nya sudah siap).
5. **Build ARM** hanya di CI.

## Selisih UI dengan PHPNuxBill lama

Template lama ada di `../phpnuxbill/ui/ui`, sekitar 157 file. Perbandingan per field: [UI-PARITY.md](UI-PARITY.md).

Status per 2026-10-08: 121 field ada, 63 berbeda, dan 148 belum. Layar: 20 ada, 35 sebagian, dan 26 belum. Kekurangan terbesar ada di laporan, kupon, peta/ODP, pesan, user admin, dan sebagian portal (lupa password, inbox, invoice).

## Belum pernah diuji di dunia nyata

- CI di GitHub, karena belum ada remote.
- Koneksi ke MikroTik nyata, RADIUS lewat socket UDP sungguhan, dan Tripay sandbox.
- Email SMTP. Yang sudah dites baru validasi config-nya.

## Utang teknis yang disengaja

Ditandai dengan komentar `ponytail:` di kode. Daftar lengkapnya: `grep -rn "ponytail:" --include=*.go .`
Terpenting: RouterOS tanpa pooling, TLS RouterOS tidak diverifikasi, kunci MPPE belum dikirim.

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
- **Remote GitHub dan akses uji:** URL remote, MikroTik/CHR, dan kredensial sandbox Tripay.

## Cara kerja

- Setiap merge harus lolos `go vet ./...` dan `go test ./...`.
- Kode `sqlc` selalu di-generate ulang, tidak boleh diedit manual. CSS di-build ulang dengan `sh tools/tailwind.sh`.
- Jika alur bisnis tidak jelas, ikuti kode di `../phpnuxbill`.
- Sebelum rilis, file migrasi boleh diedit langsung. Akibatnya DB dev harus dihapus dan dibuat ulang setelah ada perubahan schema.
