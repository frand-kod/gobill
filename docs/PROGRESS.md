# Jurnal Progres Refactor

Jurnal ini dijaga di bawah 1000 kata. Rencana lengkap ada di [plan/](plan/README.md), dan perbandingan per field ada di [UI-PARITY.md](UI-PARITY.md).

**Pembaruan terakhir:** 2026-10-09

## Rencana saat ini

Urutan yang sudah disepakati:
1. Rapikan repo, CI hijau, lalu tag v0.1.1.
2. Uji RADIUS di router: CoA ulang, FreeRADIUS REST, dan voucher hotspot + MAC.
3. Uji STB. **Ditunda oleh pengguna.**
4. Import dump terbaru, lalu jalan paralel 1–3 hari.
5. Keputusan pengguna, lalu cutover dan tag v1.0.0.

Tripay sandbox ditunda sampai setelah v1.0. Audit UX UI sedang berjalan.

## Ringkasan

Semua fase sudah selesai di level kode dan test. Yang tersisa adalah uji lapangan.

| Fase | Status |
|---|---|
| F0 Fondasi | Selesai. Repo sudah punya remote GitHub (`frand-kod/gobill`) |
| F1 Billing inti | Selesai |
| F2 Driver MikroTik | Selesai, lolos uji lapangan mode API (RouterOS 6.49.22) |
| F3 RADIUS | Selesai; auth dan accounting RADIUS bawaan lolos uji lapangan; CoA, FreeRADIUS REST, dan voucher belum diuji |
| F4 Portal, notifikasi, Tripay | Selesai di kode. Tripay belum diuji di sandbox |
| F5 Pelengkap | Selesai (daftar fitur ada di CHANGELOG) |
| F6 Import & rilis | Selesai. Import dengan dump produksi asli cocok (expiry dan total transaksi) |

**Paritas UI:** layar 36 ada, 42 sebagian, 2 belum. Field 188 ada, 86 berbeda, 60 belum (28 di antaranya Non-goal atau Ditunda).

## Sudah selesai (ringkas)

- **Stack:** Go satu binary, SQLite (WAL, `synchronous=FULL`), `sqlc`, `html/template`, Tailwind v4, Alpine.js, Chart.js, Leaflet.
- **Keamanan:** password memakai bcrypt (sha1 lama di-rehash saat login), secret dienkripsi AES-GCM, CSRF lewat stdlib, pembatas brute-force untuk login/voucher/OTP, sesi dicabut saat password, role, atau status berubah, dan error notifikasi disaring sebelum masuk log.
- **Uang:** disimpan sebagai INTEGER rupiah, perubahan saldo atomik, voucher/kupon/callback pembayaran idempoten, dan transaksi pelanggan yang dihapus tetap disimpan.
- **Jam STB:** job expiry, reminder, backup, dan RADIUS ditahan saat jam sistem tidak dipercaya.
- **Review keamanan akhir:** 9 temuan, semua sudah diperbaiki dengan test regresi.

## Uji lapangan (MikroTik "4 keys infra", RouterOS 6.49.22, 2026-10-09)

- **Mode API: lolos.** Mencakup koneksi, sinkron paket, recharge, login HP, queue, deactivate, ganti paket, dan expiry otomatis.
- **RADIUS bawaan: lolos untuk auth dan accounting.** Access-Accept dengan Message-Authenticator diterima router, Session-Timeout dan rate-limit terpasang, accounting masuk ke nuxbill, dan expiry lewat Session-Timeout jalan.
- **CoA Disconnect:** sempat ditolak (NAK) karena NAS-IP-Address salah. Sudah diperbaiki di `52f97f9`, tapi belum diuji ulang.
- **Enam bug dari uji lapangan sudah diperbaiki:** password hotspot kosong, timezone kosong dianggap UTC, jalur REST mereset brute-force, `NUXBILL_RADIUS=` kosong tidak mematikan RADIUS, NAS-IP CoA salah, dan paket NAS tak terdaftar dibuang diam-diam.
- **Pelajaran jaringan:** server nuxbill jangan jadi klien hotspot. Universal NAT membuat router menjangkaunya lewat `to-address`.

## Repo dan CI

- `main` dan tag `v0.1.0` sudah di-push ke https://github.com/frand-kod/gobill. Modul Go sudah diganti menjadi `github.com/frand-kod/gobill`.
- CI pertama gagal di langkah "app.css is up to date" karena `web/static/app.css` usang. Sudah diperbaiki di `49e4644`. CI sedang dicek ulang, belum lolos.
- Belum dirilis (rencana v0.1.1): lisensi GPL-3.0-or-later, `NOTICE`, panduan kontribusi, dan template issue/PR.

## Akan dikerjakan (checklist sebelum menggantikan PHPNuxBill)

1. Uji ulang CoA Disconnect di MikroTik.
2. Uji jalur FreeRADIUS REST: arahkan `connect_uri` server 192.168.99.2 ke nuxbill di instance uji, lalu jalankan `freeradius -X`.
3. Uji login voucher hotspot lewat RADIUS dan pembatas MAC.
4. Import dump produksi terbaru ke STB, lalu bandingkan pelanggan aktif, expiry, dan saldo dengan sistem lama.
5. Jalankan paralel 1–3 hari dalam mode baca (accounting saja), lalu bandingkan sesi dan expiry.
6. Build ARM dan uji di STB (ditunda): RAM, startup tanpa RTC, listrik padam, backup ke USB.
7. Pastikan CI hijau di GitHub, lalu tag v0.1.1.
8. Rencana cutover dan rollback: ganti `connect_uri` atau `/radius address`. Rollback cukup kembali ke entri lama.
9. Bersihkan sisa uji di router: `split-user-domain=no` di HSProfMaster, `/radius` `nuxbill-test`, profil `test` dan `test2`, dan user `claude-test`.

## Keputusan yang menunggu pengguna

- **Kolom lama yang sengaja dibuang:** `account_type`, kota/kecamatan/provinsi/kode pos, `price_old` (harga coret), dan `plan_type` (Personal/Business). Tambahkan jika memang dipakai.
- **`hs_auth_method`:** nilainya perlu disamakan dengan PHP lama (`api`/`hchap`), dan driver hotspot perlu dibuat membacanya.
- **Default bisnis:** `extend_expiry` dan `enable_balance` aktif jika belum diisi, aktivasi pertama postpaid ditagih Rp0, dan paket nonaktif tetap bisa di-recharge admin.

## Belum pernah diuji di dunia nyata

- Build ARM dan uji di STB.
- MikroTik, NAS RADIUS, FreeRADIUS, Tripay sandbox, SMTP, dan gateway WA/SMS.
- Tampilan UI terbaru.

## Utang teknis yang disengaja

Ditandai dengan `ponytail:` di kode (`grep -rn "ponytail:" --include=*.go .`). Yang terpenting:
- RouterOS tanpa connection pooling, dan sertifikat TLS RouterOS tidak diverifikasi.
- Kunci MPPE belum dikirim.
- Pembatas percobaan dan status pesan massal hanya di memori, jadi ter-reset saat restart.
- Penanda perpanjang mandiri disimpan sebagai satu baris setting per pelanggan.
- Secret integrasi (SMTP, Telegram, Tripay) plaintext di tabel settings, sama seperti aplikasi lama.

## Perbedaan perilaku dari PHP lama (disengaja)

- Router dihubungi setelah commit DB.
- Tanggal tidak valid di paket Period dinormalkan.
- Bug PHP diperbaiki: ganti username hotspot, rename profil PPPoE, CHAP terbalik di `radius.php`, dan batas perpanjang mandiri yang hanya mencatat bulan.
- **Login voucher RADIUS:** username harus benar-benar kode voucher, dan voucher tidak bisa mengambil alih akun yang sudah ada.
- **Role admin lebih ketat:** Admin tidak bisa mengangkat SuperAdmin, dan SuperAdmin terakhir dilindungi.
- **Pengganti fitur lama:** PDF diganti halaman cetak HTML, dan plugin diganti webhook.

## Cara kerja

- Opus berperan sebagai orkestrator. Sonnet mengerjakan logika bisnis dan keamanan, Haiku mengerjakan form dan dokumen. Setiap agent bekerja di worktree terpisah.
- Setiap merge harus lolos `go vet ./...` dan `go test ./...`, ditambah race detector untuk billing, radius, dan job.
- Kode `sqlc` selalu di-generate ulang. CSS di-build dengan `make css`.
- Sejak v0.1.0, file migrasi di `internal/db/migrations/` tidak boleh diubah. Perubahan schema masuk file baru bernomor berikutnya dan hash-nya dicatat di `internal/db/migrations.sum`. `TestMigrationsFrozen` menggagalkan build jika file rilis berubah.
- Versi mengikuti SemVer dan dicatat di `CHANGELOG.md`.
- Dump produksi (`docs/*.sql`) di-gitignore, jangan di-commit.
