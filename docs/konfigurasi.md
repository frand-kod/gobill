# Konfigurasi

Untuk operator. Ada dua lapis: variabel lingkungan `NUXBILL_*` (dibaca sebelum database dibuka, di systemd ada di `/etc/nuxbill/config.env`) dan pengaturan di UI admin (disimpan di tabel `settings`).

## Variabel lingkungan

Daftar lengkap yang dibaca kode (`cmd/nuxbill`).

| Variabel | Bawaan | Fungsi |
|---|---|---|
| `NUXBILL_DB` | `./nuxbill.db` (Docker: `/data/nuxbill.db`) | Lokasi file SQLite. Berlaku juga untuk `nuxbill import` |
| `NUXBILL_HTTP` | `:8080` | Alamat listen HTTP |
| `NUXBILL_HTTPS` | aktif | Cookie sesi selalu diberi flag `Secure`. Isi `0` untuk opt-out, hanya jika UI diakses lewat HTTP polos (mis. IP LAN). Browser tidak mengirim cookie `Secure` di `http://` kecuali `localhost`. Saat `0`, start mencatat peringatan |
| `NUXBILL_SECRET_KEY` | kosong | Kunci AES-GCM untuk secret (router, pelanggan, NAS). Kosong: dibuat otomatis di `<NUXBILL_DB>.key`. Jika diisi, nilainya menggantikan file `.key` dan harus disimpan aman. Dipakai juga oleh `nuxbill import` |
| `NUXBILL_RADIUS` | `:1812` | Alamat listen RADIUS UDP (auth; accounting di port+1). Kosong atau `off` mematikan listener UDP; `/radius.php` tetap jalan |
| `NUXBILL_BACKUP_DIR` | `<folder DB>/backup` | Folder backup harian dan backup otomatis sebelum impor, restore, atau pemulihan |
| `NUXBILL_BACKUP_MIRROR` | kosong (mati) | Salinan kedua backup harian di folder lain (USB, NAS, rclone). Folder harus sudah ada; lihat [instalasi.md](instalasi.md#4-backup-ke-usb-atau-nas) |
| `NUXBILL_TEST_MYSQL_DSN`, `NUXBILL_TEST_PHP_SQL` | kosong | Hanya untuk test importer (lihat [pengembangan.md](pengembangan.md)). Tidak dibaca saat aplikasi berjalan |

Contoh `/etc/nuxbill/config.env`:

    NUXBILL_DB=/var/lib/nuxbill/nuxbill.db
    NUXBILL_HTTP=:8080
    NUXBILL_RADIUS=:1812
    NUXBILL_SECRET_KEY=<SECRET>

Opsi CLI: `nuxbill --version`, dan `nuxbill import ...` ([migrasi-phpnuxbill.md](migrasi-phpnuxbill.md)).

## Admin pertama

Saat database kosong (tanpa admin), aplikasi membuat user `admin` (SuperAdmin) dengan password acak 16 karakter. Password itu tidak dicetak ke log. Ia ditulis ke `initial-admin-password.txt` di folder database (mode 0600). Login, ganti password, lalu hapus file itu. Jika file sudah ada dan database masih kosong, start berhenti dengan pesan; hapus file lama dulu.

## Pengaturan di UI

Ubah di menu Settings. Kunci di bawah tersimpan di tabel `settings`. Yang tidak disebut bawaannya kosong atau `no`.

### Umum dan lokalisasi

| Pengaturan | Fungsi |
|---|---|
| `company_name` | Nama usaha. Tampil di invoice, pesan, dan halaman login |
| `app_url` | Alamat publik aplikasi, mis. `https://billing.example.com`. Terisi otomatis dari alamat yang dipakai admin pertama kali login, kecuali alamat LAN atau localhost. Dipakai untuk tautan di WhatsApp (QRIS, invoice) |
| `timezone` | Zona waktu tampilan (mis. `Asia/Jakarta`). Waktu disimpan UTC. Kosong dianggap UTC, jadi isi saat pertama kali setup |
| `country_code_phone` | Kode negara untuk nomor yang diawali `0`, mis. `62` |
| `default_plan_device` | Device bawaan paket baru: `MikrotikHotspot`, `MikrotikPppoe`, `Dummy`, `Radius`, atau kosong (ikut tipe paket) |
| `maintenance_mode`, `maintenance_mode_logout`, `maintenance_date` | Mode perawatan: portal pelanggan ditutup. `maintenance_mode_logout` juga mengeluarkan pelanggan yang sedang login |
| `reminder_hour` | Jam kirim pengingat harian (0-23, bawaan 7) |
| `clock_guard` | `off` mematikan guard jam (hanya jika ada RTC akurat). Bawaan `on` |
| `log_keep_days` | Hapus log, sesi RADIUS tertutup, pesan inbox terbaca, dan pembayaran belum lunas yang lebih lama dari N hari. Bawaan 90 bila belum pernah diisi. `0` = simpan selamanya. Pembayaran lunas tidak pernah dihapus |
| `backup_keep` | Jumlah file backup harian yang disimpan, di folder utama dan di mirror (bawaan 7) |

### Admin dan sesi

| Pengaturan | Fungsi |
|---|---|
| `session_timeout_duration` | Batas diam sesi admin dan portal, dalam menit. Bawaan 120 |
| `single_session` | `yes` agar satu admin hanya bisa punya satu sesi aktif |

Peran admin dan 2FA diatur per akun di menu Pengguna Admin dan `/admin/2fa`, bukan di Settings. Lihat [keamanan.md](keamanan.md#verifikasi-dua-langkah-2fa-admin).

### Jaringan dan proxy

| Pengaturan | Fungsi |
|---|---|
| `radius_rest_allow` | Daftar IP/CIDR (pisahkan koma) yang boleh memanggil `/radius.php`. Kosong = hanya loopback (127.0.0.0/8, ::1); isi dengan IP FreeRADIUS jika berjalan di host lain. Saat start, NuxBill mencatat peringatan jika kosong |
| `trust_proxy` | `yes` agar `X-Forwarded-For` dipercaya, tetapi hanya jika koneksi langsung datang dari loopback atau dari IP/CIDR di `trusted_proxies`. Bawaan `no`. Entri paling kanan dipakai sebagai IP klien, jadi proxy harus menulis IP asli di sana |
| `trusted_proxies` | IP atau CIDR reverse proxy, dipisah koma, mis. `172.16.0.0/16, 10.0.0.2`. Loopback selalu dipercaya. Allow-list `/radius.php` tetap memakai alamat koneksi asli, bukan hasil `X-Forwarded-For` |
| `router_check` | `no` mematikan pengecekan router. Bawaan aktif. Router yang mati memicu alert (lihat [monitoring.md](monitoring.md)) |

Contoh Nginx di depan aplikasi di host yang sama: set `trust_proxy=yes`, dan proxy menulis `proxy_set_header X-Forwarded-For $remote_addr;`. Jika Cloudflare ada di depan nginx, atur nginx `set_real_ip_from` (rentang IP Cloudflare) dan `real_ip_header CF-Connecting-IP`.

### Bisnis

| Pengaturan | Fungsi |
|---|---|
| `extend_expiry` | Perpanjang paket yang masih aktif menambah dari tanggal kedaluwarsa. Bawaan aktif jika belum diisi |
| `admin_extend` | Siapa yang boleh memakai tombol Perpanjang (tambah hari gratis) di daftar langganan: `staff` (bawaan: Admin, Agent, Sales), `managers` (SuperAdmin dan Admin), `super` (hanya SuperAdmin), `off` (tombol disembunyikan) |
| `enable_balance` | Sistem saldo (recharge saldo, transfer saldo, perpanjang otomatis dari saldo). Bawaan aktif jika belum diisi |
| `start_on_first_login` | `yes` agar masa aktif paket RADIUS mulai saat login pertama, bukan saat recharge (bawaan `no`). Lihat bagian di bawah |
| `disable_registration`, `disable_voucher`, `allow_phone_otp`, `registration_username`, `phone_otp_type` | Pembatasan dan metode di portal pelanggan. `phone_otp_type` memilih `sms` atau `wa` |
| `voucher_format` | Format kode voucher bawaan (`up`, `low`, `rand`, `numbers`) |
| `hs_auth_method` | Metode auth hotspot yang ditampilkan di Miscellaneous (`pap` atau `chap`). Tidak mengubah perilaku server RADIUS bawaan, yang menerima PAP, CHAP, dan MS-CHAPv2 |
| `check_customer_online` | `yes` menampilkan di halaman pelanggan apakah pelanggan sedang terhubung (lewat device paket) |

### Start on first login (`start_on_first_login`)

Jika `yes`, recharge paket RADIUS (device `Radius`) baru atau setelah paket habis tidak langsung memulai masa aktif. Langganan ditandai menunggu, dan login RADIUS pertama pelanggan setelah itu memulai masa aktif dari saat login, dengan durasi penuh paket. Selama menunggu, pelanggan tetap bisa login, tidak dianggap kedaluwarsa oleh job expiry, dan tanggal kedaluwarsa tampil sebagai "Mulai saat login pertama" (di admin, portal, dan pesan WA recharge).

- Hanya paket RADIUS. Paket MikroTik (Hotspot/PPPoE dengan router) selalu mulai saat recharge, pengaturan ini tidak berlaku untuk mereka.
- Recharge ulang paket yang belum dipakai menunggu lagi dari awal. Perpanjang paket yang sedang berjalan tetap mengikuti `extend_expiry`.

### Sakelar notifikasi dan OTP

Di Settings > Notifications, bagian "Global switches" paling atas:

| Pengaturan | Fungsi |
|---|---|
| `notify_customers` | `no` menghentikan semua pesan ke pelanggan: pengingat, kedaluwarsa, faktur dan tautan QRIS, pesan selamat datang dan saldo, serta pesan manual. Ringkasan dan notifikasi operator tetap jalan |
| `notify_otp` | `no` mematikan kode OTP untuk pendaftaran, lupa kata sandi, dan ganti kontak. Fitur itu menampilkan bahwa kode tidak tersedia |
| `expired_notify_minutes_before` | Pesan expired dikirim N menit sebelum paket berakhir, agar pelanggan yang masih online tetap menerimanya. Pesannya tetap satu per periode (tidak ada pesan tambahan), dan paket tetap berakhir pada waktunya. Bawaan 0 = saat paket berakhir, rentang 0-1440. Perpanjangan atau recharge baru mengirim pesan lagi pada periode barunya |

Keduanya bawaan `yes` jika belum diisi.

Tip: setelah impor dari PHPNuxBill untuk uji paralel, set `notify_customers` = `no` agar pelanggan tidak menerima pesan ganda.

### Alert operator dan ringkasan harian

| Pengaturan | Fungsi |
|---|---|
| `alert_channel` | `telegram` (bawaan), `wa`, atau `both`. Telegram memakai `telegram_target_id`; WhatsApp memakai server WA atau `wa_url` |
| `alert_wa_to` | Nomor WhatsApp untuk alert. Kosong = memakai `daily_summary_wa_to` |
| `alert_nas_silent_minutes` | NAS yang pernah mengirim paket RADIUS lalu diam selama N menit memicu alert. Bawaan 15, rentang 1-1440 |
| `daily_summary_enabled`, `daily_summary_time`, `daily_summary_channel`, `daily_summary_wa_to` | Ringkasan harian untuk operator (bukan pelanggan): `yes`/`no` (bawaan `no`), jam `HH:MM` zona waktu server (bawaan `07:00`), saluran `telegram`/`wa`/`both`, nomor WA operator. Dikirim sekali sehari (tanggal terakhir di `daily_summary_last`, aman saat restart), ditunda bila jam sistem tidak tepercaya. Tombol "Kirim ringkasan sekarang" di Settings > Notifications untuk uji coba |

### Monitoring

| Pengaturan | Fungsi |
|---|---|
| `metrics_token` | Bearer token untuk `/metrics`. Dibuat dan dinonaktifkan di Settings > Integrations (SuperAdmin). Kosong = `/metrics` mati (404). Nilainya tidak pernah ditampilkan ulang |

Detail halaman Status, `/health`, dan alert ada di [monitoring.md](monitoring.md).

### Pembayaran dan QRIS

| Pengaturan | Fungsi |
|---|---|
| `payment_gateway` | `""` (mati) atau `tripay` |
| `tripay_mode`, `tripay_merchant_code`, `tripay_api_key`, `tripay_private_key`, `tripay_channel` | Gateway Tripay. Kunci API dan private key tersimpan plaintext di tabel `settings` (lihat [keamanan.md](keamanan.md)) |
| `qris_payload` | Teks QRIS statis merchant. Diisi lewat unggah foto QRIS (lihat bagian di bawah). Gambarnya tidak disimpan |

### Notifikasi dan integrasi

| Pengaturan | Fungsi |
|---|---|
| `notif_*`, `user_notification_*` | Template pesan (expired, reminder, invoice, selamat datang, saldo) dan kanal per jenis pesan |
| `telegram_bot`, `telegram_target_id` | Token bot dan ID tujuan Telegram |
| `alt_wga_server_url`, `alt_wga_device_id`, `alt_wga_username`, `alt_wga_password` | Server WhatsApp langsung (lihat bagian WhatsApp) |
| `wa_url`, `sms_url` | URL gateway WhatsApp dan SMS dengan placeholder `[number]` dan `[text]` |
| `smtp_host`, `smtp_port`, `smtp_user`, `smtp_pass`, `smtp_ssltls`, `mail_from`, `mail_reply_to` | Email (SMTP) |
| `webhook_url`, `webhook_secret` | Webhook keluar, ditandatangani di header `X-Signature` |

Setiap bagian punya tombol uji di Settings > Integrations (SuperAdmin, maksimal 5 tes per menit). Simpan dulu sebelum menguji, karena tes memakai nilai yang tersimpan. Tes tidak terpengaruh `notify_customers`, karena tujuannya operator:

- "Kirim pesan uji Telegram": mengirim pesan singkat ke `telegram_target_id`.
- "Kirim SMS uji": mengirim pesan ke nomor yang Anda ketik lewat `sms_url`.
- "Kirim email uji": mengirim email ke alamat yang Anda ketik lewat SMTP tersimpan.
- "Kirim webhook uji": mengirim event `test` yang ditandatangani ke `webhook_url` dan menampilkan status HTTP.
- "Cek koneksi" (Settings > Payment Gateway, Tripay): memanggil daftar channel pembayaran Tripay untuk memastikan kunci dan merchant code benar.

### Tampilan

`logo`, `logo_dark`, `login_page_*` (logo, favicon, wallpaper, judul), `date_format`, `dec_point`, `thousands_sep`, `language`. Diatur di Settings. Tema (terang/gelap, warna aksen) diatur dari header, bukan dari setting ini.

## QRIS statis

Di Settings > Payment Gateway, bagian QRIS, unggah foto QRIS statis merchant Anda (PNG atau JPG, maks 2 MB). Sistem membaca kode QR-nya, memastikan formatnya QRIS valid, lalu hanya menyimpan teksnya di `qris_payload`; gambarnya tidak disimpan. Nama merchant dan NMID yang aktif ditampilkan di bawah isian, dan "Hapus QRIS" mengosongkannya. Teks QRIS juga bisa ditempel lewat "Opsi lanjutan". Sistem membuat QR yang terkunci nominal untuk setiap invoice dan mengirim tautannya lewat WhatsApp (`[[qris_link]]`, atau ditambahkan di akhir pesan jika template tidak memakainya). Tautan memerlukan `app_url`. Pelanggan tidak perlu login. Sistem tidak memverifikasi pembayaran QRIS: konfirmasi pembayaran tetap dilakukan manual. Recharge yang sudah dibayar lewat saldo, gateway, atau voucher tidak mendapat tautan ini.

## WhatsApp

Ada dua cara mengirim WhatsApp. Yang dipakai ditentukan otomatis:

1. **Server WA langsung** (disarankan): isi di Pengaturan > Integrasi, bagian "WhatsApp (server WA)":
   `alt_wga_server_url` (mis. `http://127.0.0.1:3030`), `alt_wga_device_id` (opsional), `alt_wga_username` dan `alt_wga_password` (basic auth, jika server memakainya). NuxBill mengirim `POST <alt_wga_server_url>/send/message` dengan body `{"phone":"628xxx@s.whatsapp.net","message":"..."}`, sama seperti plugin "Alternative WhatsApp Gateway" di PHPNuxBill. Nomor yang diawali 0 diubah memakai `country_code_phone`.
2. **`wa_url`** (URL template dengan `[number]` dan `[text]`): dipakai hanya jika `alt_wga_server_url` kosong.

Jika `alt_wga_server_url` terisi, `wa_url` diabaikan sepenuhnya. Jika `wa_url` masih berisi alamat plugin PHP lama (`...?_route=plugin/wga_sendMessage&...`), NuxBill mencatat peringatan di log dan tetap mengirim langsung ke server WA. Kosongkan `wa_url` supaya tidak membingungkan.

Tombol "Kirim pesan uji" di halaman yang sama mengirim satu pesan ke nomor yang Anda ketik, memakai isian di form (walau belum disimpan), lalu menampilkan jawaban server dalam bahasa biasa. Perangkat WhatsApp dan login QR tidak diatur di NuxBill: lakukan di halaman server WA sendiri.
