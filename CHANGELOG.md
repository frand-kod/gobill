# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[Semantic Versioning](https://semver.org/).

Versions 0.x mean pre-1.0: breaking changes may happen in minor versions.

## [Unreleased]

### Fitur

- **Cetak voucher untuk struk thermal**: halaman cetak voucher kini punya tata letak 58 mm dan 80 mm (`?layout=58` atau `?layout=80`), selain A4 (bawaan). Satu voucher per blok dengan QR kecil.
### Ditambahkan

- **Pengingat menyimpan kunci enkripsi** (#39): mirror backup tidak menyalin `gobill.db.key`, dan sebelumnya aplikasi tidak mengingatkan. Pengaturan > Sistem dan Status Sistem kini menampilkan peringatan untuk SuperAdmin sampai kunci ditandai sudah disimpan (muncul lagi bila kunci berganti), lengkap dengan sidik jari kunci. Halaman baru **Kunci enkripsi** mengunduh kunci setelah konfirmasi password (dan 2FA), tercatat di log aktivitas. Penolakan restore karena kunci berbeda menyebut sidik jari kunci aplikasi.
### Ditambahkan

- **Alert gangguan massal per NAS** (#30, mode RADIUS): jika `alert_mass_drop_min` (bawaan 5, `0` = mati) pelanggan atau lebih di satu NAS kehilangan sesi dalam `alert_mass_drop_window` menit (bawaan 30) dan belum kembali, operator menerima satu alert berisi daftar username. NAS yang diam total tetap ditangani alert NAS diam. Tanpa migrasi.

## [0.1.19] - 2026-10-23

### Perubahan perilaku

- **Rollback ke v0.1.18 butuh backup pra-update**: secret di pengaturan dienkripsi saat start pertama. v0.1.18 tidak bisa membacanya, jadi pulihkan backup yang ditawarkan `gobill rollback`. Lihat [upgrade](docs/id/upgrade.md#catatan-versi).

### Keamanan

- **Go 1.26.10**: memperbaiki GO-2026-6809 (`crypto/x509` kini menolak intermediate certificate yang bocor), yang terjangkau lewat `gobill update` saat mengunduh rilis.
- **Pengaturan rahasia dienkripsi**: token Telegram, password SMTP, secret webhook, password GOWA, kunci Tripay dan Duitku, dan token metrics kini disimpan terenkripsi (AES-GCM, kunci yang sama dengan secret NAS, awalan `enc1:`) dan didekripsi saat dibaca. Nilai lama yang masih teks biasa tetap terbaca dan dienkripsi saat start. Impor PHPNuxBill menyimpannya terenkripsi. Restore dari backup dengan kunci lain ditolak.

- **Sertifikat router API-SSL di-pin**: sebelumnya koneksi TLS ke router (port 8729) tidak memverifikasi sertifikat sama sekali, sehingga penyerang di jalur jaringan bisa mencuri sandi API. Kini koneksi TLS pertama menyimpan sidik jari SHA-256 sertifikat router (trust on first use, kolom baru `routers.cert_pin`), dan koneksi berikutnya ditolak sebelum sandi dikirim jika sidik jarinya berbeda. Jika sertifikat diganti sengaja, centang **Reset pin sertifikat** di halaman ubah router.
- **Callback pembayaran memeriksa jumlah**: Tripay (`total_amount` dikurangi `fee_customer`) dan Duitku harus sama dengan jumlah pesanan. Callback PAID tanpa jumlah juga ditolak. Jika tidak cocok, callback ditolak (400), dicatat, dan dihitung di `payment_callbacks_total{result="amount_mismatch"}`.
- **`make sign` memeriksa hasil CI**: sebelum menandatangani, `tools/reproduce.sh` membangun ulang biner rilis secara lokal (versi Go dan flag sama dengan CI, dua kali untuk memastikan hasilnya reproduksibel) dan hanya lanjut jika semua hash sama dengan `SHA256SUMS` dari CI. Versi Go lokal harus sama dengan `go.mod`.
- **`install.sh` memverifikasi unduhan**: untuk URL rilis GitHub, `install.sh` memeriksa tanda tangan ed25519 `SHA256SUMS.sig` dengan `openssl` (kunci publik tertanam di skrip), lalu SHA-256 binary terhadap `SHA256SUMS`, sebelum memasang sebagai root. Binary tidak pernah dijalankan sebelum lolos. Tanpa `openssl` atau tanda tangan valid, pemasangan dibatalkan; `GOBILL_INSECURE=1` melewati pemeriksaan tanda tangan (checksum tetap diperiksa).
- **Login tidak bisa dikunci orang lain, aman untuk CGNAT**: kunci setelah 10 kegagalan kini per pasangan IP dan username, ditambah batas longgar 100 kegagalan per IP per 15 menit. Pelanggan di balik satu IP CGNAT tidak saling mengunci. Satu username hanya diperlambat (0,25 detik per kegagalan, maksimal 3 detik). Berlaku untuk login admin, langkah 2FA, dan login portal.

### Ditambahkan

- **Gateway pembayaran Duitku** (#15): pilih Duitku di **Pengaturan > Pembayaran** (merchant code, API key, mode sandbox/production, metode bawaan). Mendukung pembuatan transaksi, cek status, daftar metode pembayaran, dan callback `POST /callback/duitku` yang diverifikasi dengan tanda tangan HMAC-SHA256. `callbackUrl` dibangun dari `app_url`. Belum diuji terhadap sandbox Duitku sungguhan.
- **CoA saat paket Radius berubah**: isi ulang, ganti paket, atau edit paket/bandwidth oleh admin mengirim CoA-Request (RFC 5176) dengan `Mikrotik-Rate-Limit` dan `Session-Timeout` baru ke sesi online, di background. Diuji di RouterOS 7.24.5: kecepatan berubah tanpa memutus sesi. Jika NAS menolak atau tidak menjawab, sesi diputus dengan Disconnect-Request.
- **Peringatan `trust_proxy`**: jika permintaan dari loopback membawa `CF-Connecting-IP`, `X-Forwarded-For`, `X-Real-IP` atau `Forwarded` saat `trust_proxy` = `no`, Status Sistem dan Setup cepat menampilkan peringatan, dan satu baris dicatat di log.
- **Lab uji CHR** (`lab/`): RouterOS CHR di podman untuk menguji sinkronisasi, PPPoE lewat RADIUS, Disconnect, CoA, dan gobill sebagai service.
- **Fuzz test** (`make fuzz`) untuk paket RADIUS, Message-Authenticator, dan endpoint REST.

### Diperbaiki

- **Sesi pertama setelah router menyala tidak lagi tertutup sendiri**: Accounting-On kini hanya menutup sesi yang tidak diperbarui dalam 5 detik terakhir, sehingga Start yang datang bersamaan tidak ikut tertutup.
- **Pelanggan auto-renew tidak lagi menerima pesan "paket habis"** (dan webhook `recharge.expired`) saat perpanjangan berhasil, juga tidak menerima pemberitahuan sebelum habis bila saldonya cukup.
- **Pengiriman pesan**: panggilan ke gateway WhatsApp/SMS punya timeout 20 detik; satu pelanggan yang error tidak lagi menghentikan pengingat hari itu; pengingat dikirim berurutan dengan jeda `message_delay`; pemberitahuan habis yang gagal dicoba ulang; channel kosong dicatat sebagai peringatan.
- **Log pesan lengkap**: pesan admin, pesan massal, OTP, dan pesan selamat datang kini tercatat di halaman Logs.
- **Isi pesan**: saldo pada pesan transfer memakai format `Rp`; nomor WhatsApp dibersihkan dari karakter non-angka; pesan admin mengisi `[[username]]`, `[[company]]`, `[[url]]`, `[[payment_link]]` (placeholder tak dikenal dikosongkan); OTP mode "both" mencoba SMS bila WhatsApp gagal; subjek email admin memakai prefix `[company]`.
- **Email ganda di portal**: pemeriksaan "email sudah dipakai pelanggan lain" saat verifikasi kontak tidak pernah cocok. Kini memakai query khusus yang tidak peka huruf besar/kecil.
- **Cek status pembayaran di portal** dibatasi sekali per 30 detik per transaksi, agar merchant tidak diblokir gateway.
- **Edit paket Radius tanpa router** kini tetap mengirim CoA ke sesi online.

### Diubah

- **Batas sesi basi per NAS**: dihitung dari jarak Interim-Update yang teramati (sekitar 2x interval + 1 menit); `GOBILL_STALE_AFTER` menjadi batas bawah.

### Dokumentasi

- Panduan MikroTik menjelaskan bahwa router di belakang NAT atau CGNAT tidak bisa menerima CoA, sehingga Disconnect dan isolir paksa gagal, beserta solusinya lewat tunnel.

## [0.1.18] - 2026-10-17

### Keamanan

- **Pengaturan rahasia dienkripsi**: token Telegram, password SMTP, secret webhook, password GOWA, kunci Tripay dan Duitku, dan token metrics kini disimpan terenkripsi (AES-GCM, kunci yang sama dengan secret NAS, awalan `enc1:`) dan didekripsi saat dibaca. Nilai lama yang masih teks biasa tetap terbaca dan dienkripsi saat start. Impor PHPNuxBill menyimpannya terenkripsi. Restore dari backup dengan kunci lain ditolak.

- **Rilis ditandatangani**: `gobill update` kini memeriksa tanda tangan ed25519 `SHA256SUMS.sig` dengan kunci publik yang tertanam di binary, sebelum memeriksa checksum. Rilis tanpa tanda tangan yang valid ditolak. Token GitHub yang bocor tidak lagi cukup untuk memasang binary palsu.
- **`SECURITY.md`**: jalur privat untuk melaporkan celah keamanan.

### Ditambahkan

- **`gobill config-example`**: menampilkan template `config.env` dengan penjelasan lengkap (bahasa Inggris) untuk setiap variabel. `install.sh` memakainya pada instalasi baru.
- **`GOBILL_STALE_AFTER`** dan **`GOBILL_NAS_ONLINE_WINDOW`** (lanjutan): mengganti batas sesi basi (default `10m`) dan jendela online NAS (default `15m`). Nilai yang salah menghentikan start.

### Diubah

- **Alur rilis**: CI membuat rilis sebagai draft dengan catatan dari CHANGELOG. Rilis baru terlihat oleh `gobill update` setelah ditandatangani dan dipublikasikan dengan `make sign`.
- Halaman Status dan metrik `/metrics` memakai batas sesi basi yang sama dengan RADIUS, bukan angka 600 detik yang ditulis ulang.

### Perubahan perilaku

- Setelah update ke v0.1.18, `gobill update` hanya memasang rilis yang ditandatangani.

## [0.1.17] - 2026-10-16

### Diperbaiki

- **Enter di form registrasi kini mendaftar**: sebelumnya Enter mengirim ulang OTP saat verifikasi OTP aktif.
- **Halaman pembayaran hanya reload saat tab terlihat**: pelanggan tidak lagi kehilangan posisi scroll dan kuota saat tab di latar belakang.
- **Satu input kupon per paket**: kartu paket tidak lagi menampilkan dua input kupon saat saldo dan payment gateway sama-sama aktif.

### Diubah

- **Portal lebih nyaman di HP**: kolom tabel sekunder disembunyikan di layar kecil, label bottom nav diperpendek, dan badge pesan baru tidak lagi berwarna merah.
- **Form password**: tombol Show, `minlength`, dan hint panjang minimum dari setting `customer_password_min`.
- **Lupa password**: tombol kirim ulang kode di langkah verifikasi.
- **Lainnya**: hint pada field kontak yang terkunci, saldo tampil di halaman beli untuk teman, tab antara Riwayat Order dan Riwayat Aktivasi, serta link login admin dihapus dari halaman login pelanggan.

## [0.1.16] - 2026-10-15

### Keamanan

- **Pengaturan rahasia dienkripsi**: token Telegram, password SMTP, secret webhook, password GOWA, kunci Tripay dan Duitku, dan token metrics kini disimpan terenkripsi (AES-GCM, kunci yang sama dengan secret NAS, awalan `enc1:`) dan didekripsi saat dibaca. Nilai lama yang masih teks biasa tetap terbaca dan dienkripsi saat start. Impor PHPNuxBill menyimpannya terenkripsi. Restore dari backup dengan kunci lain ditolak.

- **Batas `shared_users` tidak lagi terlewati saat query gagal**: jika daftar sesi aktif tidak bisa dibaca, login ditolak, sama seperti batas waktu dan kuota. Sebelumnya error diabaikan dan login dianggap tanpa sesi lain.

### Diperbaiki

- **Harga tidak lagi dihitung tanpa pajak atau tagihan saat database gagal dibaca**: recharge, preview, order gateway, dan auto-renew kini gagal dengan error. Sebelumnya setting pajak atau atribut tagihan pelanggan yang gagal dibaca dianggap kosong, sehingga pelanggan bisa ditagih kurang.

## [0.1.15] - 2026-10-14

### Keamanan

- **Pengaturan rahasia dienkripsi**: token Telegram, password SMTP, secret webhook, password GOWA, kunci Tripay dan Duitku, dan token metrics kini disimpan terenkripsi (AES-GCM, kunci yang sama dengan secret NAS, awalan `enc1:`) dan didekripsi saat dibaca. Nilai lama yang masih teks biasa tetap terbaca dan dienkripsi saat start. Impor PHPNuxBill menyimpannya terenkripsi. Restore dari backup dengan kunci lain ditolak.

- **`/radius.php` tidak lagi terbuka lewat reverse proxy di host yang sama**: dengan `radius_rest_allow` kosong, request yang membawa header proxy (`X-Forwarded-For`, `X-Real-IP`, `Forwarded`, `CF-Connecting-IP`) ditolak. Sebelumnya, di belakang Nginx atau cloudflared lokal, semua klien internet terlihat datang dari loopback.
- **Header keamanan di semua respons**: `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, dan `Referrer-Policy: same-origin`.

### Performa

- **RADIUS UDP 8x lebih kuat**: batas login per detik dengan 2 CPU dan 50.000 pelanggan naik dari sekitar 250/s menjadi 2000/s. Diukur dengan `bench/radius`.
  - `sqlite_stat4` dihapus setelah `PRAGMA optimize`. Sampel STAT4 membuat SQLite meng-compile ulang query login di setiap eksekusi.
  - Daftar NAS di-cache selama 2 detik. Sebelumnya tabel NAS dibaca tiga kali per paket.
  - Cache retransmit tidak lagi memindai semua entri per paket.
  - Penghitung login gagal tidak lagi memindai semua entri per kegagalan. Serangan spray username memakan sebagian besar CPU.
  - Paket di atas kapasitas langsung di-drop (`radius_packets_shed_total`), sehingga memori tetap terbatas dan sisa paket tetap dijawab sebelum NAS menyerah.

- **Accounting RADIUS 10x lebih cepat di disk sungguhan**: paket accounting yang datang bersamaan kini ditulis dalam satu transaksi (group commit), dengan satu `fsync` untuk semuanya. Balasan ke NAS tetap dikirim setelah data tersimpan. Diukur di 1 core dengan DB di ext4 NVMe dan 5000 pelanggan: 150/s menjadi 1500/s. Di SD card atau eMMC, yang `fsync`-nya lebih lambat, selisihnya lebih besar.
- **Login RADIUS lebih ringan**: data pelanggan dan paket aktif diambil dengan satu query, bukan dua. Secret NAS dan setting yang dipakai RADIUS di-cache 2 detik. Login di 1 core naik dari 200/s (v0.1.14) menjadi 1100/s.
- **Tidak macet saat menunggu disk di perangkat 1 core**: gobill memakai minimal 2 thread Go (`GOMAXPROCS`). Dengan satu thread, setiap `fsync` SQLite menghentikan seluruh aplikasi, termasuk pembacaan paket RADIUS.
- **Buffer socket RADIUS 4 MB**: lonjakan paket tidak lagi dibuang kernel (`RcvbufErrors`). Linux membatasinya dengan `net.core.rmem_max`.
- **Dashboard admin 3x lebih cepat dengan banyak transaksi**: index baru `transactions_income_idx` (migrasi 0019) membuat total pendapatan (hari ini, bulan ini, dan 12 grafik bulanan) dihitung dari index saja. Dengan 60.000 transaksi, dashboard naik dari 3,4 menjadi 11,3 halaman/s.
- **Daftar pelanggan admin 2,8x lebih cepat**: tampilan default (tanpa pencarian, filter, atau urutan) membaca satu halaman langsung dari urutan id, tanpa mengurutkan semua pelanggan. Totalnya memakai hitungan biasa. Pencarian kosong tidak lagi menjalankan `LIKE` di setiap baris. Dengan 5000 pelanggan, halaman naik dari 28 menjadi 78/s.
- **Memori menyesuaikan perangkat**: `GOMEMLIMIT` otomatis diisi dari batas cgroup atau RAM sistem, dan cache SQLite dikecilkan pada budget 256 MB atau kurang. Dengan batas 64 MB dan 5000 pelanggan di satu core, gobill sebelumnya mati kena OOM saat beban tinggi. Sekarang gobill tetap berjalan.

### Perbaikan

- **Retransmit tetap dikenali saat trafik tinggi**: cache retransmit penuh mulai sekitar 333 paket/s, lalu berhenti mendeteksi duplikat. Accounting dan voucher bisa terproses dua kali saat banyak router reconnect bersamaan.
- **Memori penghitung login gagal kini terbatas**: spray username tidak lagi membuat map tumbuh tanpa batas.

### Lainnya

- `GOBILL_PPROF` menyalakan profiler Go di alamat lokal untuk benchmark.
- `bench/radius`: load generator RADIUS (ramp, spike, mixed dengan serangan).
- CI menjalankan test dengan `-race`.

## [0.1.14] - 2026-10-13

### Perbaikan

- **SMS kini memakai kode negara**: nomor `08...` diubah dengan `country_code_phone`, sama seperti WhatsApp. Gateway SMS yang butuh `62...` tidak lagi gagal.
- **Hapus pelanggan mengeluarkannya dari router**: pelanggan yang dihapus kini dihapus dari router, walaupun paketnya punya profil expired. Sebelumnya pelanggan itu dipindah ke profil expired dan tertinggal di router.
- **Recharge admin tidak menawarkan Balance saat saldo dimatikan**: metode Balance hanya muncul bila `enable_balance` bukan `no`. Request yang tetap memakai Balance ditolak tanpa memotong saldo.

## [0.1.13] - 2026-10-13

### Dokumentasi

- README dibuat netral: bagian tentang kekurangan PHPNuxBill diganti dengan tujuan gobill, dan bagian baru **Untuk pengguna PHPNuxBill** berisi cara impor, tautan ke advisory keamanan resmi PHPNuxBill, serta langkah mengurangi risiko.
- Audit kode lama tidak lagi merinci dua temuan keamanan PHPNuxBill yang belum diumumkan. Temuan itu dilaporkan secara privat ke pengelola PHPNuxBill.

## [0.1.12] - 2026-10-13

### Perbaikan

- **Admin tidak lagi ter-logout saat request dibatalkan**: jika browser membatalkan request, misalnya karena pindah halaman sebelum halaman selesai dimuat, error database dianggap sesi rusak dan sesi admin dihapus. Kini error selain "akun tidak ada" menampilkan halaman error tanpa menghapus sesi. Hal yang sama berlaku untuk portal pelanggan.
- **RADIUS REST sekitar 2,5 kali lebih cepat di bawah beban**: query yang sering dipakai kini disiapkan sekali sebagai prepared statement, bukan di-parse ulang setiap kali. Pool koneksi SQLite juga tetap terbuka, sehingga koneksi tidak dibuka ulang di tengah beban.
- **Login RADIUS lambat setelah instalasi atau impor**: database baru belum punya statistik query planner, sehingga setiap login RADIUS memindai semua langganan aktif. Dengan 5.000 pelanggan, throughput hanya sekitar 3% dari normal sampai pembersihan harian pertama berjalan. Statistik kini diperbarui saat start dan setelah impor.
- **Impor dari PHPNuxBill jauh lebih cepat**: password pelanggan kini di-hash dengan bcrypt di semua core CPU, tidak lagi satu per satu. Sebelumnya impor 5.000 pelanggan butuh sekitar 25 menit di laptop.
- **Daftar pelanggan lebih cepat**: query daftar pelanggan kini mengurutkan id saja, lalu menghitung paket dan masa aktif hanya untuk baris di halaman itu. Dengan 5.000 pelanggan, waktu muat halaman turun dari sekitar 170 ms menjadi sekitar 70 ms.

### Dokumentasi

- README ditulis ulang: ucapan terima kasih untuk PHPNuxBill, alasan penulisan ulang, hasil benchmark, dan tech stack.
- Skrip benchmark `bench/run.sh` membandingkan PHPNuxBill dan gobill dengan data dan host yang sama.

## [0.1.11] - 2026-10-12

### Fitur

- **Isolir: isolasi penuh dengan daftar izin**: pelanggan yang diisolir kini tidak bisa memakai aplikasi atau situs apa pun, kecuali host di pengaturan baru **Diizinkan saat diisolir** (bawaan WhatsApp, supaya pelanggan bisa menghubungi admin). Tambahkan host aplikasi bank di sana. Perintah RouterOS kini satu blok yang ditempel sekaligus dan berurutan, di atas aturan accept umum. Aturan drop opsional dihapus karena sudah termasuk di blok.
- Batasan RouterOS 6: host wildcard dipelajari dari koneksi TLS saat aplikasi terhubung, dan media WhatsApp dari host CDN yang berubah kadang gagal. Lihat [Setup MikroTik](docs/id/mikrotik.md#isolir).

### Perbaikan

- Profil bandwidth isolir bernama netral **Isolir** (sebelumnya "Isolir 64k"), karena kecepatannya bisa diubah di tab Isolir. Nama lama diganti saat kecepatan isolir disimpan.
- Pesan isolir bawaan tidak lagi menjanjikan internet pelan untuk WhatsApp dan aplikasi bank. Berlaku untuk instalasi yang belum pernah menyimpan pesan.

## [0.1.10] - 2026-10-12

### Perbaikan

- **Pelanggan isolir bisa logout dari hotspot**: aturan NAT isolir kini memakai `dst-address-type=!local`, sehingga halaman login dan logout hotspot di router tetap bisa dibuka. Sebelumnya semua port 80 dialihkan ke halaman isolir, termasuk `http://192.168.20.1/logout`. Pengaturan baru **Alamat hotspot** menambah tombol **Keluar / pakai akun lain** di halaman isolir, dan variabel `[[logout_url]]` untuk HTML sendiri.
- **Halaman isolir tidak lagi menawarkan pembayaran**: QRIS, pengaturan "Tampilkan QRIS", dan variabel `[[qris_image]]` dihapus dari halaman isolir dan pratinjaunya. Tombol **Hubungi admin** kini menjadi aksi utama di atas form voucher. Data merchant asli dihapus dari semua tes.
- **Kartu QRIS** di halaman pembayaran `/qris`: menampilkan nama perusahaan, nama merchant, NMID, penyedia, nominal yang terkunci, dan pengecekan penerima sebelum membayar. Bisa dicetak tanpa elemen lain.

## [0.1.9] - 2026-10-12

### Fitur

- **Portal pelanggan ditata ulang (HP lebih dulu)**: kartu paket dengan sisa waktu, bar masa aktif, saldo, dan satu tombol Beli/Perpanjang; kartu koneksi live (online/offline, perangkat, kecepatan sekarang dengan meter 5 segmen dibanding kecepatan paket, data sesi ini) yang diperbarui tiap 15 detik; kartu pemakaian (kuota terpakai, hari ini, grafik 7 hari); riwayat koneksi pindah ke halaman sendiri, dikelompokkan per hari; tab navigasi di bawah layar HP.

### Perbaikan

- Sesi online: angka ringkasan, meter, dan pembaruan tiap 10 detik kini tampil. Script halaman dimuat sebelum Alpine, sehingga komponen live tidak lagi kosong.

## [0.1.8] - 2026-10-12

### Perbaikan

- File CSS/JS kini memakai URL berversi dan gobill mengirim header cache sendiri, sehingga setelah upgrade browser atau Cloudflare tidak lagi menyajikan tampilan lama.

## [0.1.7] - 2026-10-12

### Fitur

- **Setup cepat**: wizard langkah demi langkah untuk SuperAdmin (usaha, router/NAS dengan perintah MikroTik siap salin dan indikator paket RADIUS live, kecepatan, paket, WhatsApp, QRIS, Tripay, Isolir, voucher uji). Menggantikan checklist di dashboard. Setelah impor PHPNuxBill, langkah yang sudah berisi data menjadi verifikasi.
- **Sesi online live**: meter kecepatan 5 segmen per sesi (hijau/kuning/merah dibanding kecepatan paket), ringkasan per NAS dan paket, throughput total, sparkline 24 jam, diperbarui tiap 10 detik tanpa reload.
- **Semua tabel**: "Menampilkan X–Y dari N" dan pilihan 20/50/100 baris; filter tetap saat pindah halaman.
- **Message-Authenticator per NAS**: Otomatis (bawaan; wajib setelah NAS pernah mengirimnya), Selalu wajib, atau Tidak wajib (NAS lama). Atribut ganda atau rusak selalu ditolak.
- **Diagnosa secret RADIUS salah**: log dengan IP dan nama NAS (dibatasi sekali per menit), metric `radius_bad_secret_total`, lencana di Status Sistem, dan alert operator setelah 10 paket berturut-turut.
- **Update satu perintah**: `sudo gobill update` mengambil rilis terbaru dari GitHub, memeriksa checksum `SHA256SUMS`, membuat backup database, memasang binary, lalu memeriksa `/health`. Jika pemeriksaan gagal, binary dan database dipulihkan otomatis. `--version vX.Y.Z` memasang rilis tertentu.
- **Rollback**: `sudo gobill rollback` kembali ke binary sebelumnya, dan menawarkan pemulihan backup pra-update jika update sempat menjalankan migrasi.
- **Pemeriksaan versi baru**: sekali sehari gobill memeriksa rilis terbaru di GitHub. SuperAdmin melihat lencana di header jika ada versi baru. Matikan dengan pengaturan `update_check`.
- Rilis menyertakan file `SHA256SUMS`. Salinan `sha256sums.txt` tetap ada.
- **Paket Isolir milik sistem**: disembunyikan dari daftar paket, bandwidth, pencarian, dan semua pilihan paket, serta ditolak di server. Kecepatannya diatur di tab Isolir.
- **Template pesan**: daftar variabel per template yang bisa diketuk untuk disisipkan, tombol kembali ke bawaan, dan template kosong otomatis memakai teks bawaan.
- **Nomor WhatsApp bersama**: pengaturan "Satu nomor WhatsApp per akun" (bawaan: tidak). Lupa username mengirim semua akun pada nomor itu.
- Pesan `invoice_balance` dikirim saat isi saldo, seperti PHPNuxBill.
- Pengaturan baru di Sistem & keamanan: `radius_rest_allow` (khusus SuperAdmin), `log_keep_days`, `backup_keep`.
- Form paket ditata ulang: Layanan dan Cara bayar, masa aktif satu baris, bagian "Saat paket habis", perangkat dipilih lebih dulu, ringkasan live, tombol simpan menempel.
- Navigasi lebih halus: prefetch saat kursor atau jari menyentuh tautan, transisi antar halaman, tab Pengaturan berganti di tempat, sidebar mengingat grup yang terbuka dan posisi scroll, ikon sidebar berwarna per grup.
- Dashboard: Aktif/Kedaluwarsa dan total pelanggan dalam satu kartu.
- `govulncheck` berjalan di CI.

### Perubahan perilaku

- **Alamat login berubah**: login pelanggan sekarang di `/login`, dan login admin di `/admin/login`. Login 2FA admin pindah ke `/admin/login/2fa`. `/portal/login` dihapus. `GET /` membuka `/admin` untuk admin yang sudah login, `/portal` untuk pelanggan yang sudah login, dan `/login` untuk yang lain. Tautan login di pesan WhatsApp memakai `app_url` + `/login`.
- **Tab Pengaturan ditata ulang**: Umum, Lokalisasi, Pelanggan & tagihan, Pembayaran, Integrasi, Notifikasi, Isolir, Promosi, Tampilan, Sistem & keamanan. Tab Lain-lain dihapus. Backup, impor, dan restore pindah ke `/admin/settings/system/...`. Menu Payment Gateway di sidebar menjadi "Pembayaran online" di grup Laporan.
- **Nomor telepon dinormalkan** ke format `62…` saat disimpan, dan data lama dinormalkan sekali saat start.
- **Perintah MikroTik di tab Isolir** memakai alamat LAN dan port dari `GOBILL_HTTP`, bukan `app_url`. Isi `GOBILL_HTTP` dengan IP LAN, misalnya `192.168.99.2:8090`. Aturan drop untuk isolasi penuh kini opsional.
- Migrasi 0018 (kolom `nas.ma_seen`) berjalan otomatis. Rollback dari versi ini butuh restore backup database.
- Go 1.26.9.

### Perbaikan

- Klien terisolir kini melihat halaman isolir walau membuka situs atau path apa pun, bukan redirect ke `/admin` (#1).
- Potong saldo dalam satu query atomik, sehingga dua pembayaran bersamaan tidak bisa membuat saldo minus (#3).
- Counter accounting RADIUS tidak bisa turun oleh Interim/Stop yang datang terlambat; counter negatif dianggap 0 (#3).
- Gagal menghitung pemakaian kuota data atau waktu kini menolak login, bukan dianggap 0 (#5).
- Cache peringatan NAS tak dikenal dibatasi; body `/radius.php` dibatasi 64 KiB dan form rusak ditolak (#4).
- Prefetch tidak mengubah sesi, sehingga pesan sekali tampil tidak hilang.
- Footer portal menampilkan `v0.1.x`, bukan `vv0.1.x`.
- `filippo.io/edwards25519` v1.1.1.

## [0.1.6] - 2026-10-11

### Fitur

- **Isolir**: tab pengaturan sendiri (khusus SuperAdmin). Pelanggan yang paketnya habis dipindah ke paket isolir (bawaan 64 kbps, 30 hari) dengan address-list MikroTik `isolir`. Halaman `/isolir` menampilkan nama, paket terakhir, QRIS bernominal, tombol chat admin, form voucher, dan promosi. Mode HTML kustom dengan variabel `[[...]]` berjalan dalam sandbox browser. Perintah MikroTik siap salin.
- **Promosi**: tab sendiri (khusus SuperAdmin), blok gambar + teks + link, tampil di halaman isolir dan opsional di dashboard portal sebagai kartu kecil yang bisa ditutup. Migrasi 0016.
- **Akses paket**: setiap paket **Publik** (dijual di portal) atau **Khusus admin** (hanya admin yang memberikan). Paket khusus admin diperpanjang otomatis bila auto-renew pelanggan aktif; paket Rp 0 gratis. Migrasi 0017.
- **Daftar pelanggan 3 kolom**: username, password, nomor WhatsApp. Akun langsung aktif. Panjang minimal password diatur `customer_password_min` (bawaan 4); password sama dengan username dan password umum selalu ditolak.
- **Batas perangkat penuh**: pilihan *Keluarkan perangkat terlama* (bawaan) atau *Tolak login baru*, untuk ponsel dengan MAC acak.
- **Tambah pelanggan + langsung aktifkan paket** (Cash) dalam satu form, dan opsi kirim username + password lewat WA saat admin membuat akun.
- **Isi ulang massal** paket yang sama dari daftar pelanggan, dengan halaman konfirmasi dan token sekali pakai.
- **Reset password admin**: `gobill reset-admin-password <username> [--disable-2fa]` dan tombol khusus SuperAdmin.
- Diagnosa pelanggan lebih lengkap, kolom **Tanggal habis** di daftar pelanggan, badge **Hari ini**, link 2FA di menu profil, checklist setup awal mencakup WhatsApp, QRIS, Tripay, dan Isolir.

### Perubahan perilaku

- Pesan expired dikirim **10 menit** sebelum paket habis secara bawaan (`expired_notify_minutes_before`; `0` = saat habis).
- Portal tidak pernah menjual paket Rp 0. Paket publik Rp 0 tidak diperpanjang otomatis.
- Aturan password pelanggan juga berlaku saat admin mengisi password.
- Istilah: "Tanggal habis" dan "Tambah Saldo".

### Upgrade dari 0.1.5

1. Backup dulu. Migrasi 0016 dan 0017 berjalan otomatis saat start.
2. Ubah paket internal (staf, teknisi) menjadi **Khusus admin**.
3. Untuk *Keluarkan perangkat terlama* agar benar-benar memutus perangkat lama, daftarkan router sebagai NAS dengan secret-nya.

## [0.1.5] - 2026-10-11

### Perubahan nama

- Aplikasi sekarang bernama **gobill** di semua tempat: binary `gobill`, variabel lingkungan `GOBILL_*`, `/etc/gobill`, `/var/lib/gobill`, layanan `gobill.service`, user sistem `gobill`, database bawaan `gobill.db`, cookie `gobill_session`, dan nama file backup `gobill-*`.

### Fitur

- Pencarian global di navbar (`/` atau `Ctrl+K`): pelanggan, paket, router, NAS, voucher, invoice, langganan, halaman menu, setiap kolom pengaturan, dan aksi cepat. Hasil mengikuti role.
- Pesan expired bisa dikirim lebih awal: `expired_notify_minutes_before`. Satu pesan per periode, tidak ada pesan tambahan (migrasi 0015).
- Tombol uji untuk setiap integrasi: Telegram, gateway pesan, email, webhook, GOWA, dan cek koneksi Tripay.
- Satu URL gateway pesan untuk WhatsApp dan SMS (`wa_url`). Bagian GOWA diberi nama jelas dengan tautan ke proyeknya.
- Pengaturan `admin_extend`: siapa yang boleh memakai tombol Perpanjang di daftar langganan, atau matikan.
- Gambar QRIS aktif ditampilkan di pengaturan Payment Gateway.
- Halaman detail pelanggan lebih padat: strip status, data dua kolom, kartu Recharge di samping.
- Panduan dalam aplikasi dua bahasa (ID dan EN) dengan pemilih dokumen. Dokumentasi ditata ulang ke `docs/id`, `docs/en`, dan `docs/internal`.
- `tools/dev.sh` untuk menjalankan instance pengembangan lokal.

### Perbaikan

- Simpan pengaturan General dengan beberapa gambar sekaligus tidak lagi ditolak.
- Cek router yang berhasil langsung mencatat router online.
- Kartu Voucher Stock hanya menampilkan paket yang punya voucher.
- Kotak hari di tombol Perpanjang diberi label.

### Upgrade dari 0.1.4

1. Ganti variabel lingkungan `NUXBILL_*` menjadi `GOBILL_*` di file konfigurasi.
2. Pindahkan `/etc/nuxbill` ke `/etc/gobill` dan `/var/lib/nuxbill` ke `/var/lib/gobill`, lalu pasang `gobill.service` menggantikan `nuxbill.service`.
3. Pelanggan dan admin perlu login ulang karena nama cookie berubah.
4. Migrasi 0015 berjalan otomatis saat start. Backup dulu.

## [0.1.4] - 2026-10-10

### Keamanan

- **Pengaturan rahasia dienkripsi**: token Telegram, password SMTP, secret webhook, password GOWA, kunci Tripay dan Duitku, dan token metrics kini disimpan terenkripsi (AES-GCM, kunci yang sama dengan secret NAS, awalan `enc1:`) dan didekripsi saat dibaca. Nilai lama yang masih teks biasa tetap terbaca dan dienkripsi saat start. Impor PHPNuxBill menyimpannya terenkripsi. Restore dari backup dengan kunci lain ditolak.

- `/radius.php`: `radius_rest_allow` kosong sekarang berarti hanya loopback (sebelumnya semua IP boleh).
- `GOBILL_HTTPS` aktif secara default: cookie sesi selalu diberi flag `Secure`. `GOBILL_HTTPS=0` untuk opt-out.
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
- Impor PHPNuxBill dari file backup JSON: `gobill import --json=...`, dan halaman admin (Settings > Miscellaneous) dengan pratinjau, konfirmasi, serta backup otomatis sebelum impor.
- Restore database dari UI (SuperAdmin) dengan restart otomatis. Data saat ini dibackup dulu.
- Salinan mirror backup harian di luar perangkat (`GOBILL_BACKUP_MIRROR`), dengan percobaan ulang tiap jam.

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

1. Backup database dan file `gobill.db.key` dulu. Migrasi 0011 sampai 0014 berjalan otomatis saat start.
2. `radius_rest_allow` kosong sekarang hanya loopback. Jika FreeRADIUS berjalan di host lain, isi dengan IP-nya. Jika tidak, `/radius.php` menolak permintaan.
3. `GOBILL_HTTPS` aktif secara default. Jika UI diakses lewat HTTP polos ke IP LAN, set `GOBILL_HTTPS=0`, karena browser tidak mengirim cookie `Secure` di sana.
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
- `gobill import --notifications` imports the PHP notification templates; customer attributes (`Bill`, `Invoice`, `Expired Date`) are imported and honoured.
- Recharge page with a customer picker and a customer summary panel that warns about an active package (double charge guard); the same picker on Add balance and Redeem voucher.
- Bulk delete for vouchers and coupons, prune of used vouchers older than 3 months, message to selected customers, bulk disconnect of online sessions.
- Live search, filters, sorting and reset on lists; global customer search in the header.
- Customer "Diagnosa" card, dashboard "Network" card with router/NAS health and a live router check, collapsible dashboard cards, rolling 12-month charts.
- In-app guide (Panduan) rendered from the operator docs.
- Theme control in the header: light/dark/system, accent colour, density, text size; dark logo variants; PWA manifests.
- Short explanation on every page, toast notifications, footer with version.

### Changed

- Business behaviour aligned with PHPNuxBill after an audit (docs/internal/paritas-bisnis.md): extend restarts expired subscriptions, recharge refused for non-Active customers, one price formula (plan or Invoice, coupon, tax, bills), payment methods from `payment_usings` plus Recharge Zero, dashboard income excludes balance-paid rows and uses `reset_day`, data usage resets on reactivation, cumulative time limit in the built-in RADIUS.
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
- `GOBILL_RADIUS=` (empty) did not disable the UDP listener.
- CoA sent the wrong NAS-IP-Address. Also decodes Error-Cause and logs unknown NAS.
- Requests from unregistered NAS were dropped without a log entry.
