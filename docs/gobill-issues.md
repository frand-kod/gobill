# Draf issue gobill

Hasil audit v0.1.17 (2026-10-17), diperbarui setelah commit `36d9895`. Satu bagian = satu issue. Label usulan ada di baris pertama setiap issue. Bagian bertanda **Selesai** tidak perlu dibuat jadi issue.

---

## 1. ~~Tanda tangani rilis~~ Selesai (v0.1.18, `36d9895`)
`security` `update`

`gobill update` memeriksa `SHA256SUMS.sig` (ed25519) dengan kunci di `internal/update/release.pub` sebelum checksum. Kunci privat disimpan offline, dan `make sign` menandatangani draft rilis. Lanjutannya ada di issue 16.
---

## 2. Lockout per username bisa dipakai untuk mengunci admin
`security` `auth` · Prioritas: sedang (tinggi jika issue 3 belum selesai)

**Masalah.** `internal/web/server.go:29` (`maxFailedLogins=10`, `loginWindow=15m`) dan `auth.go` mengunci login per IP dan per username. Siapa pun bisa mengunci username `admin` terus-menerus dengan 10 tebakan salah setiap 15 menit. Limiter juga hanya disimpan di memori, sehingga hilang saat restart. Kode sudah menandainya dengan komentar `ponytail:`.

**Usulan.**
- Kunci kombinasi IP dan username.
- Untuk username saja, beri jeda progresif, bukan lockout penuh.
- Opsional: akun yang memakai 2FA dikecualikan dari lockout per username.

---

## 3. Tidak ada peringatan jika `trust_proxy=no` di belakang reverse proxy
`security` `ux` · Prioritas: tinggi

**Masalah.** Di belakang cloudflared atau Nginx di host yang sama, `RemoteAddr` selalu `127.0.0.1` jika `trust_proxy` belum diset `yes`. Akibatnya:
- satu penyerang bisa mengunci login semua orang,
- log audit kehilangan IP asli,
- rate limit OTP di portal menjadi global.

Kebutuhan ini sudah ditulis di `docs/id/installation.md:94`, tetapi aplikasi diam saja.

**Usulan.** Jika request dari loopback membawa `CF-Connecting-IP`, `X-Forwarded-For`, atau `Forwarded` sementara `trust_proxy=no`, tampilkan peringatan di Status Sistem. Tampilkan juga peringatan di setup wizard dan catat sekali di log.

---

## 4. ~~SECURITY.md~~ Selesai sebagian (v0.1.18)
`security` `docs`

`SECURITY.md` sudah ada. Yang tersisa: aktifkan GitHub Private Vulnerability Reporting di Settings → Security. Ini pengaturan repo, bukan kode.
---

## 5. CoA ganti paket terdokumentasi, tetapi tidak diimplementasikan
`bug` `radius` `docs` · Prioritas: dokumentasi tinggi, implementasi sedang

**Masalah.** `docs/id/installation.md:82` menyebut fungsi CoA sebagai *"memutus sesi dan mengubah paket"*, dan README juga menyebut "CoA". Kode hanya mengirim `Disconnect-Request` (`internal/radius/disconnect.go`). `device.Radius.UpdatePlan` adalah no-op (`internal/device/radius.go:23`), sehingga untuk plan `Radius` perubahan kecepatan baru berlaku setelah pengguna login ulang.

**Usulan.** Implementasikan `CoA-Request` dengan `Mikrotik-Rate-Limit` (dan `Session-Timeout`), dengan fallback ke Disconnect. Kalau tidak diimplementasikan, perbaiki dokumentasinya.

---

## 6. `staleAfter` belum mengikuti interval interim NAS
`radius` · Prioritas: sedang

**Sudah:** sejak v0.1.18, `GOBILL_STALE_AFTER` (default `10m`) dan `GOBILL_NAS_ONLINE_WINDOW` (default `15m`) bisa diatur lewat env. Halaman Status dan `/metrics` kini memakai `radius.StaleAfter` yang sama.

**Masalah tersisa.** Nilainya tetap satu untuk semua NAS. Pengaturan `frrest_interim_update` hasil impor PHPNuxBill tidak dibaca (paritas C3). Syarat interval juga belum disebut di `docs/*/mikrotik.md`.

**Usulan.** Hitung otomatis per NAS dari `Acct-Interim-Interval` yang terlihat, misalnya 2× interval ditambah margin. Env var tetap jadi pintu darurat. Dokumentasikan di `mikrotik.md`.
---

## 7. Callback Tripay tidak memeriksa nominal
`payment` `hardening` · Prioritas: rendah

`internal/payment/tripay.go:180` hanya membaca `merchant_ref` dan `status` dari callback. Signature HMAC sudah diverifikasi, jadi risikonya rendah.

**Usulan.** Sebagai lapisan pertahanan tambahan, cocokkan `total_amount` dengan `payment_requests.amount`. Kalau berbeda, tolak, catat di log, dan tambah metrik `payment_callbacks_total{result="amount_mismatch"}`.

---

## 8. Fuzz test untuk parser RADIUS dan Message-Authenticator
`test` `security` · Prioritas: sedang

Belum ada `func Fuzz*`, padahal jalur UDP 1812/1813 menerima input langsung dari jaringan.

**Usulan.** Fuzz `handleAuth` dan `handleAcct` dengan paket mentah, `checkMA` (`messageauth.go:57`), dan `parsePrefix` (coverage 33%). Targetnya: tidak ada panic dan tidak ada alokasi tanpa batas.

---

## 9. ~~Jalur update/rollback 0% teruji~~ Basi
`test` `update`

`cmd/gobill/update_test.go` sudah menguji update sukses, checksum salah, SHA256SUMS hilang, revert saat health gagal, rollback dengan dan tanpa restore, dan rollback tanpa `.prev`. Sejak v0.1.18 ada juga test untuk signature dari kunci lain. Yang belum tersentuh hanya pembungkus tipis `runUpdate`, `newUpdater`, `systemctl`, dan `waitHealthy`. Tidak perlu jadi issue.
---

## 10. Test integrasi driver MikroTik dengan RouterOS CHR
`test` `device` · Prioritas: sedang · Digabung ke issue 18

`device/mikrotik.go` `exec` hanya 10.5% teruji. Fungsi berikut 0%: `NewMikrotikHotspot`, `NewMikrotikPPPoE`, PPPoE `ChangeUsername`, dan `Connect`. Menurut CHANGELOG, yang sudah diuji di lapangan baru RouterOS 6.49. CoA dan jalur REST masih tertulis *untested*.

**Usulan.** Buat job CI opsional atau nightly dengan CHR v6 dan v7 di QEMU atau container. Cakup sync profil, recharge, expiry, ganti paket, dan Disconnect lewat CoA.

---

## 11. Coverage kepemilikan pembayaran di portal masih rendah
`test` `payment` `security` · Prioritas: rendah (`ownPayment` sudah benar; ini soal coverage)

`web/portal_payment.go`: `ownPayment` 43%, `payOrder` 53%, `pPayCheck` 55%. `portal_profile.go`: `contactTaken` 22%, `pContactOTP` 51%.

**Usulan.** Tambahkan test IDOR (pelanggan A mengakses atau memeriksa pembayaran milik B), pembayaran ganda, status kedaluwarsa, dan OTP brute force.

---

## 12. Benchmark accounting berkelanjutan dan angka perangkat ARM
`bench` `docs` · Prioritas: rendah

`bench/` belum punya beban accounting berkelanjutan. Data lapangan yang bisa dipakai, dari STB ZTE B860H (S905X, 4×A53 1.2 GHz, RAM 1.7 GB, eMMC), gobill v0.1.17, dan RouterOS 7.23:

| Tes | Hasil |
|---|---|
| Auth paralel 20 | ~1.100 req/s, 0 hilang |
| Accounting paralel 20 / 100 | ~740 / ~970 req/s, 0 hilang |
| Sustained 10 menit, 18.000 sesi, Interim 300/s | 179.400/179.400 OK, p50 41 ms, p99 ≤98 ms, RSS 48→82 MB stabil, DB 9.4 MB tetap |

Generator berjalan di host yang sama dengan gobill.

**Usulan.** Tambahkan mode `bench/radius -sustain` dan tabel hasil ARM di README.

---

## 13. ~~Workflow rilis berbasis tag~~ Selesai (v0.1.18)
`ci`

Job `release` di `ci.yml` sudah berjalan pada tag `v*`. Sejak v0.1.18 job itu menolak build tanpa kunci rilis (`releasesign check-key`), mengambil catatan dari CHANGELOG, dan membuat draft yang dipublikasikan oleh `make sign`. Attestation SLSA atau provenance bisa ditambahkan nanti, tetapi issue 16 lebih penting.
---

## 14. Rangkum fitur yang belum didukung untuk pengguna
`docs` · Prioritas: sedang

`docs/internal/paritas-bisnis.md` lebih lengkap daripada README soal celah fitur. Contohnya: `enable_coupons` dan `frrest_interim_update` tidak dibaca, belum ada sync massal ke router (R6), dan CoA ganti paket belum ada.

**Usulan.** Tambahkan bagian "Belum didukung" di README dan `migration-phpnuxbill.md`. Importer juga sebaiknya mencantumkan pengaturan yang diabaikan di laporannya (paritas IM2).

---

## 15. Payment gateway tambahan: Duitku, lalu Midtrans dan Xendit
`feature` `payment` · Prioritas: rendah

Saat ini gateway hanya Tripay dan QRIS statis. PHPNuxBill mendukung banyak gateway lewat plugin, dan ini menjadi penghambat migrasi. **Usulan.** Mulai dari Duitku. Duitku ditandai "Rekomendasi" untuk Indonesia di katalog plugin PHPNuxBill dan ditambahkan oleh fork RT/RW Net lokal; iPaymu juga muncul di fork yang sama. Setelah itu Midtrans Snap dan Xendit Invoice lewat interface `payment.PaymentGateway` (`internal/payment/payment.go:51`) yang sudah ada.

---

## 16. `make sign` menandatangani hasil CI tanpa memeriksanya
`security` `ci` · Prioritas: sedang

**Masalah.** `make sign` mengunduh `SHA256SUMS` dari draft rilis lalu langsung menandatanganinya. Jika workflow CI dibobol, misalnya lewat akun GitHub yang bocor, binary jahat tetap ikut ditandatangani oleh pengelola.

**Usulan.** `make sign` ikut build ketiga binary di lokal dengan flag yang sama (`-trimpath`, `CGO_ENABLED=0`, `-X main.version=$TAG`), lalu membandingkan hash dengan `SHA256SUMS` dari CI. Tanda tangan hanya dibuat jika semua hash sama. Syaratnya: versi Go lokal sama dengan `go.mod`.

---

## 17. `install.sh` tidak memverifikasi binary yang diunduh
`security` `deploy` · Prioritas: sedang

**Masalah.** `deploy/install.sh` mengunduh binary dari URL lalu langsung memasangnya sebagai root. Instalasi pertama tidak dilindungi checksum maupun signature, padahal update sesudahnya dilindungi.

**Usulan.** Jika URL menunjuk ke rilis GitHub, unduh juga `SHA256SUMS` dan cek dengan `sha256sum`. Setelah binary terpasang, jalankan pemeriksaan signature dengan binary itu sendiri, misalnya `gobill verify-release`, atau dokumentasikan cara cek manual.

---

## 18. Lab RouterOS CHR untuk test integrasi
`test` `radius` `device` · Prioritas: tinggi · **Sebagian besar selesai**

**Sudah.** `lab/run.sh up|test|shell|down`, CHR 7.24.5 (SHA256 resmi), gobill di container dengan healthcheck, dan klien PPPoE userspace. Mode `direct` lulus 18/18 dan mode `nat` lulus 16/16. Lab ini menemukan bug race accounting (issue 25). **Sisa:** job CI nightly, CHR v6, skenario dari issue 24.

Pelengkap issue 10. Lab podman rootless di `lab/`: RouterOS CHR dalam QEMU (`/dev/kvm`), gobill di container seperti service, dan klien PPPoE simulasi. Ada dua mode: `direct` dan `nat` (router hanya bisa membuka koneksi keluar). Smoke test mencakup Ping/Health API, sync paket dan pelanggan, login PPPoE lewat RADIUS, Disconnect, serta isolasi mode `nat`. Dokumentasi lab ditulis setelah pengujian selesai.

---

## 19. WireGuard bawaan untuk menyambungkan router
`feature` `device` `security` · Prioritas: tinggi

**Masalah.** Keluhan yang paling sering muncul di forum PHPNuxBill adalah "Error connecting to RouterOS" (discussions #97, #403, #418, #436, #447) serta pertanyaan VPS dan port (#421, #434, #381). Router di belakang NAT atau CGNAT tidak bisa dijangkau. Panduan WireGuard di `docs/id/security.md:80` masih manual dan butuh paket di host.

**Usulan.**
- WireGuard userspace di dalam binary (`golang.zx2c4.com/wireguard` + `tun/netstack`), tanpa root, tanpa `/dev/net/tun`, dan tanpa paket di host. Cara ini juga jalan di Docker.
- Mati secara default (`GOBILL_WG=off`). Build tag `nowg` membuang fitur ini dari binary.
- Router yang membuka koneksi ke gobill. RADIUS, CoA, dan API RouterOS berjalan di dalam tunnel.
- Subnet tunnel bisa diatur. Kunci privat disimpan lewat `internal/secret`.
- Setup wizard memeriksa apakah ada alamat publik dan UDP terbuka. Cloudflared tidak meneruskan UDP, dan server tanpa IP publik tidak bisa dipakai.

**Ukur sebelum merge:** ukuran binary dan memori. Klaim 48 MB di README harus diuji ulang dengan `bench/radius` lewat tunnel.

**Letak pengaturan.**
- Tab baru **Pengaturan → WireGuard** (`/admin/settings/wireguard`), sejajar dengan tab `integrations` dan `isolir` yang sudah ada. Isinya: aktif atau mati, port listen, endpoint publik (domain), subnet tunnel, dan hasil pemeriksaan UDP dan alamat publik.
- Aksi per router (provision, provision ulang, cabut, status per tahap) ada di halaman router, bukan di tab ini.
- `GOBILL_WG=off` di env tetap menjadi kill-switch yang mengalahkan pengaturan UI.
- Mengubah port, endpoint, atau subnet memutus semua router yang sudah di-provision. Sebelum disimpan, tampilkan jumlah router yang terdampak. Setelah disimpan, tandai router itu "perlu provision ulang".

Syarat wajib dari skenario buruk ada di issue 24.

---

## 20. Provisioning router dengan satu baris
`feature` `device` `ux` · Prioritas: tinggi · Bergantung pada: 19

**Usulan.**
- Admin klik **Tambah router**, lalu gobill membuat keypair, IP tunnel, RADIUS secret, dan user API.
- Admin cukup menempel satu baris: `/tool fetch url=".../provision/<token>" dst-path=gobill.rsc; /import gobill.rsc`. Token berlaku 15 menit dan hanya sekali pakai, sehingga rahasia tidak lewat clipboard.
- Skrip aman dijalankan ulang. Skrip memeriksa versi RouterOS (v7) dan berhenti dengan pesan jelas untuk v6. Skrip juga menghapus konfigurasi `wg-gobill`, user `gobill`, dan `/radius` lama. Rule firewall dipasang dengan `place-before=0`. User API memakai group terbatas, dan service `api` dibatasi ke alamat tunnel.
- Halaman status menampilkan tiap tahap: handshake ✓, login API ✓, tes RADIUS ✓, CoA ✓. Tahap yang gagal punya pesan spesifik.
- Tersedia tombol **Provision ulang**. Menghapus router berarti menghapus peer dan mencabut user API.
- Untuk RouterOS 6: dokumentasikan alternatif L2TP/IPsec atau SSTP.
- Syarat wajib dari skenario buruk ada di issue 24.

---

## 21. Sinkron massal ke router ("Dorong semua")
`feature` `device` · Prioritas: sedang

Paritas R6 (lihat issue 14). Untuk router baru, router yang di-reset, atau router pengganti dalam mode API: jalankan `SyncPlan` untuk setiap paket dan `SyncCustomer` untuk setiap langganan aktif milik router itu (`internal/billing/sync.go`). Tampilkan progres dan ringkasan berhasil, gagal, beserta alasannya. Router dalam mode RADIUS tidak butuh langkah ini.

---

## 22. Import dan rekonsiliasi dengan isi router
`feature` `importer` `device` · Prioritas: sedang · Bergantung pada: 19 atau API yang bisa dijangkau

**Masalah.** Banyak calon pengguna belum memakai billing apa pun. Data mereka hanya ada di MikroTik. Plugin "Mikrotik Import" PHPNuxBill membuktikan kebutuhan ini. Importer gobill saat ini hanya membaca DB PHPNuxBill.

**Usulan.** Satu layar rekonsiliasi per router. Data dibaca lewat API: `/ppp/secret`, `/ppp/profile`, `/ip/hotspot/user`, `/ip/hotspot/user/profile`, dan `/queue/simple`. Lalu dicocokkan berdasarkan username:

| Kondisi | Default |
|---|---|
| Ada di kedua sisi, sama | Tidak ada tindakan |
| Ada di kedua sisi, profil atau password berbeda | Ikut gobill. Admin bisa memilih per baris. |
| Hanya ada di gobill | Dorong ke router (mode API) |
| Hanya ada di router | Import, abaikan, atau nonaktifkan. Tidak pernah dihapus otomatis. |
| Username sama tetapi orangnya berbeda | Konflik. Admin wajib memutuskan, misalnya dengan prefiks per site. |

- Selalu ada dry-run dulu.
- Harga dan masa aktif tidak ada di router, jadi admin mengisinya per profil di pratinjau.
- Format `rate-limit` yang tidak dikenal ditandai, tidak ditebak.
- Setelah rekonsiliasi pertama, gobill menjadi sumber kebenaran.

---

## 23. Migrasi router ke mode RADIUS secara bertahap
`feature` `radius` · Prioritas: sedang · Bergantung pada: 22

Urutannya: import → tes login RADIUS dengan satu akun → nyalakan `use-radius` → nonaktifkan secret dan user lokal. Secret dan user lokal tidak dihapus, sehingga tersedia tombol rollback. Sesi yang sedang berjalan tidak terputus dan baru lewat RADIUS saat login ulang. Untuk hotspot: `use-radius=yes` di profil, dan walled garden untuk halaman bayar serta isolir.

---

## 24. Skenario buruk WireGuard dan provisioning
`security` `device` `test` · Prioritas: tinggi · Bagian dari: 19, 20

**Syarat wajib sebelum 19 dan 20 dirilis.**

1. **gobill mati sama dengan semua login baru gagal.** Sesi yang sedang berjalan tetap hidup, tetapi reconnect dan voucher baru gagal. Tulis risiko ini dengan jujur di dokumentasi. Dokumentasikan juga opsi membiarkan secret lokal aktif sebagai cadangan (kontrol isolir jadi lebih lemah, keputusan ada di admin). Kirim alert jika router yang biasanya aktif berhenti mengirim request RADIUS.
2. **Jam router mundur setelah reboot.** WireGuard menolak handshake dengan timestamp yang lebih lama dari handshake terakhir. MikroTik tanpa RTC (seri hAP dan sejenisnya) kembali ke jam awal setelah mati listrik, sehingga tunnel tidak bisa tersambung sampai gobill restart. Skrip provisioning memasang NTP client. Cek apakah `wireguard-go` mengizinkan reset status handshake per peer. Jika bisa, sediakan tombol **Reset sesi peer**.
3. **Router dicuri atau konfigurasinya bocor.**
   - `AllowedIPs` per peer hanya satu IP.
   - Tidak ada routing antar peer.
   - Di sisi tunnel, gobill hanya mendengarkan 1812/1813. HTTP admin tidak dibuka ke tunnel.
   - RADIUS secret per router.
   - **Cabut router** langsung memutus peer.
4. **Token provisioning dipakai orang lain lebih dulu.** Tampilkan "dipakai dari IP X pada jam Y". Beri peringatan jika router tidak online setelah token dipakai. Provision ulang selalu membuat kunci baru dan membatalkan kunci lama.

**Perlu ada, boleh menyusul.**

5. **UDP 51820 diblokir atau dibatasi ISP/operator seluler.** Port bisa diubah (misalnya 443/udp). Status per tahap harus membedakan "handshake tidak pernah terjadi" dari "API gagal".
6. **Pindah VPS atau IP VPS berubah.** Endpoint berupa domain. Perilaku re-resolve DNS di RouterOS 7 harus diverifikasi. Kunci WireGuard gobill ikut di `gobill backup` dan di panduan migrasi.
7. **Restore dari backup lama.** Setelah restore, peringatkan admin tentang peer yang melakukan handshake tetapi tidak dikenal. Cegah pembagian ulang IP yang masih dipakai router lain.
8. **`GOBILL_WG` dimatikan padahal masih ada router yang memakainya.** Tampilkan konfirmasi berisi jumlah router yang terdampak.
9. **Skala.** Ukur memori netstack dengan 1, 50, dan 200 peer.

**Skenario tambahan untuk lab (issue 18).** Jam CHR dimundurkan lalu reboot (nomor 2), port UDP diblokir (nomor 5), dan endpoint berganti IP (nomor 6).

---

## 25. ~~Sesi pertama tertutup oleh Accounting-On~~ Selesai
`bug` `radius`

Ditemukan oleh lab (issue 18). Router mengirim Accounting-On dan Start pertama bersamaan. Jika Start tercatat lebih dulu, `CloseRadiusSessionsByNAS` menutup sesi baru, dan interim tidak pernah membukanya lagi. Akibatnya sesi hilang dari daftar online dan tidak bisa di-Disconnect. Perbaikan: Accounting-On hanya menutup sesi yang `updated_at`-nya lebih lama dari 5 detik (`acctOnWindow`), dengan test `TestAccountingOnKeepsRacingStart`.
