# Jurnal Progres Refactor

Jurnal ini dijaga di bawah 1000 kata. Rencana lengkap ada di [plan/](plan/README.md), dan perbandingan per field ada di [UI-PARITY.md](UI-PARITY.md).

**Pembaruan terakhir:** 2026-10-09

## Ringkasan

Semua fase sudah selesai di level kode dan test. Yang tersisa adalah uji lapangan.

| Fase | Status |
|---|---|
| F0 Fondasi | Selesai. CI belum pernah jalan karena belum ada remote |
| F1 Billing inti | Selesai |
| F2 Driver MikroTik | Selesai di kode, termasuk monitor router. Belum diuji di router nyata |
| F3 RADIUS | Selesai di kode: server bawaan, endpoint FreeRADIUS REST `/radius.php`, CoA, dan login voucher hotspot. Belum diuji dengan NAS nyata |
| F4 Portal, notifikasi, Tripay | Selesai di kode. Tripay belum diuji di sandbox |
| F5 Pelengkap | Selesai: laporan, invoice, widget, user admin, kupon, peta/ODP, pesan/inbox, custom field, halaman statis, backup, maintenance, dan pembersihan log |
| F6 Import & rilis | Selesai. Import diuji dengan dump produksi asli, dan semua tanggal expiry serta total transaksi cocok |

**Paritas UI:**
- Layar: 36 ada, 42 sebagian, dan 2 belum. Ada 12 layar lain yang memang Non-goal atau Ditunda.
- Field: 188 ada, 86 berbeda, dan 60 belum. Sebanyak 28 dari 60 itu Non-goal atau Ditunda.

## Sudah selesai (ringkas)

- **Stack:** Go dalam satu binary, SQLite (WAL, `synchronous=FULL`, `_txlock=immediate`), `sqlc`, `html/template`, Tailwind v4 standalone, Alpine.js, Chart.js, dan Leaflet. Tidak butuh Node.
- **Keamanan:**
  - Password admin dan pelanggan memakai bcrypt. Password sha1 lama otomatis di-rehash saat login pertama.
  - Secret router, pelanggan, dan NAS dienkripsi AES-GCM.
  - CSRF dicegah lewat stdlib. Hanya callback Tripay dan `/radius.php` yang dikecualikan.
  - Ada pembatas brute-force untuk login, voucher, dan OTP.
  - Sesi dicabut saat password, role, atau status berubah.
  - Error notifikasi disaring sebelum ditulis ke log, sehingga token dan API key tidak bocor.
- **Uang:**
  - Disimpan sebagai INTEGER rupiah.
  - Semua perubahan saldo atomik.
  - Voucher, kupon, dan callback pembayaran idempoten.
  - Transaksi milik pelanggan yang sudah dihapus tetap disimpan.
- **Jam STB:** job expiry, reminder, backup, dan RADIUS ditahan atau disesuaikan saat jam sistem tidak dipercaya.
- **Review keamanan akhir:** 9 temuan, semuanya sudah diperbaiki dan punya test regresi.

## Sedang dikerjakan

Tidak ada.

## Akan dikerjakan

1. **Uji lapangan, dilakukan pengguna:**
   - MikroTik CHR atau fisik: aktivasi, expiry, disconnect, sinkronisasi paket dan pool, serta tes koneksi router.
   - RADIUS bawaan: `radtest` untuk PAP dan CHAP, login hotspot dan PPPoE, login voucher hotspot, dan CoA ke port 3799.
   - FreeRADIUS REST: ganti `connect_uri` ke `https://<nuxbill>/radius.php`, lalu jalankan `freeradius -X`.
   - Tripay sandbox. Langkahnya ada di laporan agent dan di halaman setting.
2. **Push ke GitHub** supaya CI dan build ARM jalan. Lalu buat tag `v0.1.0` untuk rilis.
3. **Perbaikan dari hasil uji lapangan.**

## Keputusan yang menunggu pengguna

- **Kolom lama yang sengaja dibuang:** `account_type`, kota/kecamatan/provinsi/kode pos, `price_old` (harga coret), dan `plan_type` (Personal/Business). Tambahkan jika memang dipakai.
- **`hs_auth_method`:** nilainya perlu disamakan dengan PHP lama (`api`/`hchap`), dan driver hotspot perlu dibuat membacanya.
- **Default bisnis:** saat belum diisi, `extend_expiry` dan `enable_balance` dianggap aktif, aktivasi pertama postpaid ditagih Rp0, dan paket yang nonaktif tetap bisa di-recharge oleh admin.
- **URL remote GitHub.**

## Belum pernah diuji di dunia nyata

- CI di GitHub dan build ARM.
- MikroTik, NAS RADIUS, FreeRADIUS, Tripay sandbox, SMTP, dan gateway WA/SMS.
- Tampilan UI terbaru di browser.

## Utang teknis yang disengaja

Ditandai dengan `ponytail:` di kode. Daftarnya: `grep -rn "ponytail:" --include=*.go .`

Yang terpenting:
- RouterOS tanpa connection pooling, dan sertifikat TLS RouterOS tidak diverifikasi.
- Kunci MPPE belum dikirim.
- Pembatas percobaan (login, OTP, voucher RADIUS) dan status pesan massal hanya di memori, jadi ter-reset saat restart.
- Penanda perpanjang mandiri disimpan sebagai satu baris setting per pelanggan.
- Secret integrasi (SMTP, Telegram, Tripay) tersimpan plaintext di tabel settings, sama seperti aplikasi lama.

## Perbedaan perilaku dari PHP lama (disengaja)

- Router dihubungi setelah commit DB.
- Tanggal tidak valid di paket Period dinormalkan.
- Kode voucher dibuat dengan `crypto/rand`, minimal 8 huruf atau 10 angka.
- Bug PHP diperbaiki:
  - ganti username hotspot
  - rename profil PPPoE
  - CHAP yang terbalik di `radius.php`
  - batas perpanjang mandiri hanya mencatat bulan tanpa tahun
- **Login voucher RADIUS:** username harus benar-benar kode voucher, dan voucher tidak bisa mengambil alih akun yang sudah ada.
- **Role admin lebih ketat:** Admin tidak bisa mengangkat SuperAdmin, dan SuperAdmin terakhir dilindungi.
- **Pengganti fitur lama:** PDF diganti halaman cetak HTML, dan plugin diganti webhook.

## Cara kerja

- Opus berperan sebagai orkestrator. Sonnet mengerjakan logika bisnis dan bagian yang menyangkut keamanan, sedangkan Haiku mengerjakan form dan dokumen. Setiap agent bekerja di worktree terpisah.
- Setiap merge harus lolos `go vet ./...` dan `go test ./...`, ditambah race detector untuk billing, radius, dan job.
- Kode `sqlc` selalu di-generate ulang. CSS di-build dengan `sh tools/tailwind.sh`.
- Sebelum rilis, file migrasi boleh diedit langsung. Akibatnya DB dev harus dibuat ulang setelah ada perubahan schema.
- Dump produksi (`docs/*.sql`) di-gitignore dan tidak boleh di-commit.
