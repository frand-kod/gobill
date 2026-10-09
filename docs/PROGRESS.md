# Jurnal Progres Refactor

Jurnal ini dijaga di bawah 1000 kata. Rencana lengkap: [plan/](plan/README.md). Paritas per field: [UI-PARITY.md](UI-PARITY.md).

**Pembaruan terakhir:** 2026-10-09

## Rencana saat ini

1. Rapikan repo, CI hijau, lalu tag v0.1.1.
2. Uji RADIUS di router: FreeRADIUS REST, lalu voucher dan pembatas MAC saat operasi normal.
3. Uji STB. **Ditunda oleh pengguna.**
4. Import dump terbaru, lalu jalan paralel 1–3 hari.
5. Keputusan pengguna, lalu cutover dan tag v1.0.0.

Tripay sandbox ditunda sampai setelah v1.0. Audit UX selesai; screenshot pengguna menunggu.

## Ringkasan

Semua fase selesai di kode dan test; tersisa uji lapangan.

| Fase | Status |
|---|---|
| F0 Fondasi | Selesai |
| F1 Billing inti | Selesai |
| F2 Driver MikroTik | Selesai; lolos uji lapangan mode API |
| F3 RADIUS | Selesai; auth, accounting, CoA Disconnect, dan login voucher lolos uji lapangan; FreeRADIUS REST belum diuji |
| F4 Portal, notifikasi, Tripay | Selesai di kode. Tripay belum diuji di sandbox |
| F5 Pelengkap | Selesai (lihat CHANGELOG) |
| F6 Import & rilis | Selesai; import dump produksi cocok (expiry, total transaksi) |

**Paritas UI:** layar 36 ada, 42 sebagian, 2 belum. Field 188 ada, 86 berbeda, 60 belum (28 di antaranya Non-goal atau Ditunda).

## Sudah selesai (ringkas)

- **Stack:** Go satu binary, SQLite (WAL), `sqlc`, `html/template`, Tailwind v4, Alpine.js, Chart.js, Leaflet.
- **Keamanan:** bcrypt (sha1 lama di-rehash saat login), secret AES-GCM, CSRF stdlib, brute-force untuk login/voucher/OTP, sesi dicabut saat password, role, atau status berubah, error notifikasi disaring sebelum log.
- **Uang:** INTEGER rupiah, saldo atomik, voucher/kupon/callback idempoten, transaksi pelanggan terhapus tetap disimpan.
- **Jam STB:** job expiry, reminder, backup, dan RADIUS ditahan saat jam sistem tidak dipercaya.
- **Review keamanan akhir:** 9 temuan, semua diperbaiki dengan test regresi.
- **UX quick wins** (`docs/UX-AUDIT.md`): dialog konfirmasi native, kunci double-submit, halaman error, test kunci i18n, tabel mobile, auto-refresh pembayaran.

## Uji lapangan (MikroTik "4 keys infra", RouterOS 6.49.22, 2026-10-09)

- **Mode API: lolos.** Koneksi, sinkron paket, recharge, login HP, queue, deactivate, ganti paket, expiry otomatis.
- **RADIUS bawaan: lolos untuk auth dan accounting.** Access-Accept dengan Message-Authenticator diterima, Session-Timeout dan rate-limit terpasang, accounting masuk, expiry lewat Session-Timeout jalan.
- **CoA Disconnect: lolos.** gobill mengirim Disconnect-Request, router membalas Disconnect-ACK, sesi logout ("radius disconnect").
- **Login voucher hotspot via RADIUS: lolos.** Voucher aktif, pelanggan dibuat otomatis, rate-limit dan Session-Timeout terpasang. Kode salah ditolak ("Invalid Voucher..."). Re-login kode sama dan pembatas MAC belum diuji lapangan (dicakup `TestVoucherThrottle`, `TestUserThrottle`); diuji saat operasi normal.
- **Login ulang via cookie** juga lewat RADIUS, jadi pelanggan diputus atau kedaluwarsa tidak bisa kembali via cookie.
- **Enam bug uji lapangan diperbaiki:** password hotspot kosong, timezone kosong = UTC, REST mereset brute-force, `NUXBILL_RADIUS=` kosong tidak mematikan RADIUS, NAS-IP CoA salah (`52f97f9`), paket NAS tak terdaftar dibuang diam-diam.
- **Pelajaran jaringan:**
  - Server nuxbill jangan jadi klien hotspot. Dengan universal NAT, router tidak bisa kirim RADIUS ke IP server ("could not send packet: Operation not permitted") dan diam-diam memakai `/radius` berikutnya (produksi 192.168.99.2). Perbaikan: `ip-binding type=bypassed` untuk MAC server (disarankan), atau `/radius` ke `to-address` hotspot.
  - Dengan `split-user-domain=yes`, router membuang domain: `r@nuxbilltest` tiba sebagai User-Name `r`, jadi pelanggan harus bernama `r`.

## Repo dan CI

- `main` dan `v0.1.0` sudah di-push ke github.com/frand-kod/gobill (modul Go sudah diganti).
- CI gagal di langkah app.css (usang). Diperbaiki di `49e4644`, sedang dicek ulang.
- Belum dirilis (v0.1.1): lisensi GPL-3.0-or-later, `NOTICE`, panduan kontribusi, template issue/PR.

## Akan dikerjakan (checklist sebelum menggantikan PHPNuxBill)

1. Uji FreeRADIUS REST: arahkan `connect_uri` server 192.168.99.2 ke nuxbill di instance uji, jalankan `freeradius -X`.
2. Uji ulang voucher dengan kode sama dan pembatas MAC saat operasi normal.
3. Import dump produksi terbaru ke STB, bandingkan pelanggan aktif, expiry, dan saldo dengan sistem lama.
4. Jalan paralel 1–3 hari mode baca (accounting saja), bandingkan sesi dan expiry.
5. Build ARM dan uji di STB (ditunda): RAM, startup tanpa RTC, listrik padam, backup ke USB.
6. Pastikan CI hijau di GitHub, lalu tag v0.1.1.
7. Cutover dan rollback: ganti `connect_uri` atau `/radius address`. Rollback cukup kembali ke entri lama.
8. Bersihkan sisa uji di router: `split-user-domain=no` di HSProfMaster, profil `test` dan `test2`, user `claude-test`, `/radius` `nuxbill-test` (kini ke 192.168.20.183, secret baru). Pelanggan uji `r`, `a`, `YW2H6BRK` hanya ada di DB uji sementara.

## Keputusan yang menunggu pengguna

- **Kolom lama sengaja dibuang:** `account_type`, kota/kecamatan/provinsi/kode pos, `price_old`, `plan_type`.
- **`hs_auth_method`:** samakan nilainya dengan PHP lama (`api`/`hchap`), driver hotspot perlu membacanya.
- **Default bisnis:** `extend_expiry` dan `enable_balance` aktif jika kosong; aktivasi pertama postpaid Rp0; paket nonaktif tetap bisa di-recharge admin.

## Belum pernah diuji di dunia nyata

Build ARM dan STB, FreeRADIUS REST, Tripay sandbox, SMTP, gateway WA/SMS, re-login voucher dan pembatas MAC, serta tampilan UI terbaru.

## Utang teknis yang disengaja

Ditandai `ponytail:` di kode (`grep -rn ponytail: .`). Yang terpenting:
- RouterOS tanpa connection pooling, sertifikat TLS RouterOS tidak diverifikasi.
- Kunci MPPE belum dikirim.
- Pembatas percobaan dan status pesan massal hanya di memori, ter-reset saat restart.
- Penanda perpanjang mandiri disimpan sebagai satu baris setting per pelanggan.
- Secret integrasi (SMTP, Telegram, Tripay) plaintext di tabel settings, sama seperti aplikasi lama.

## Perbedaan perilaku dari PHP lama (disengaja)

- Router dihubungi setelah commit DB. Tanggal tidak valid di paket Period dinormalkan.
- Bug PHP diperbaiki: ganti username hotspot, rename profil PPPoE, CHAP terbalik di `radius.php`, batas perpanjang mandiri yang hanya mencatat bulan.
- **Login voucher RADIUS:** username harus kode voucher, dan voucher tidak bisa mengambil alih akun yang sudah ada.
- **Role admin lebih ketat:** Admin tidak bisa mengangkat SuperAdmin, SuperAdmin terakhir dilindungi.
- **Pengganti fitur lama:** PDF diganti halaman cetak HTML, plugin diganti webhook.

## Cara kerja

- Opus orkestrator. Sonnet untuk logika bisnis dan keamanan, Haiku untuk form dan dokumen. Tiap agent: worktree terpisah.
- Setiap merge lolos `go vet`, `go test`, dan race detector (billing, radius, job). `sqlc` di-generate ulang, CSS lewat `make css`.
- Migrasi di `internal/db/migrations/` beku sejak v0.1.0 (hash di `migrations.sum`, dijaga `TestMigrationsFrozen`). Schema baru masuk file bernomor berikutnya.
- Versi SemVer, dicatat di `CHANGELOG.md`.
- Dump produksi (`docs/*.sql`) di-gitignore, jangan di-commit.
