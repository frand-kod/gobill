# Konfigurasi

Dokumen ini menjelaskan dua lapis konfigurasi NuxBill: variabel lingkungan `NUXBILL_*` dan pengaturan di UI. Variabel lingkungan dibaca sebelum database dibuka. Di systemd, variabel ini ada di `/etc/nuxbill/config.env`. Pengaturan disimpan di tabel `settings` dan diubah lewat menu **Pengaturan**.

**Untuk:** operator

**Prasyarat:** aplikasi sudah terpasang. Lihat [instalasi](installation.md).

## Variabel lingkungan

Daftar ini berisi semua variabel yang dibaca kode di `cmd/nuxbill`.

| Variabel | Bawaan | Fungsi |
|---|---|---|
| `NUXBILL_DB` | `./nuxbill.db`. Di Docker: `/data/nuxbill.db` | Lokasi file SQLite. Juga dipakai oleh `nuxbill import` |
| `NUXBILL_HTTP` | `:8080` | Alamat listen HTTP |
| `NUXBILL_HTTPS` | aktif | Cookie sesi selalu memakai flag `Secure`. Isi `0` hanya jika UI diakses lewat HTTP biasa, misalnya IP LAN. Browser tidak mengirim cookie `Secure` lewat `http://`, kecuali `localhost`. Saat bernilai `0`, start mencatat peringatan. |
| `NUXBILL_SECRET_KEY` | kosong | Kunci AES-GCM untuk secret router, pelanggan, dan NAS. Jika kosong, kunci dibuat otomatis di `<NUXBILL_DB>.key`. Jika diisi, nilainya menggantikan file `.key` dan harus disimpan dengan aman. Juga dipakai oleh `nuxbill import`. |
| `NUXBILL_RADIUS` | `:1812` | Alamat listen RADIUS UDP untuk auth. Accounting memakai port berikutnya. Kosong atau `off` mematikan listener UDP. `/radius.php` tetap berjalan. |
| `NUXBILL_BACKUP_DIR` | folder `backup` di samping database | Folder backup harian dan backup otomatis sebelum impor, restore, atau pemulihan. Lihat [backup dan restore](backup-restore.md). |
| `NUXBILL_BACKUP_MIRROR` | kosong, mati | Salinan kedua backup harian di folder lain, misalnya USB, NAS, atau rclone. Folder harus sudah ada. Lihat [backup dan restore](backup-restore.md#mirror). |
| `NUXBILL_TEST_MYSQL_DSN`, `NUXBILL_TEST_PHP_SQL` | kosong | Hanya untuk test importer. Tidak dibaca saat aplikasi berjalan. Lihat [panduan pengembangan](../internal/pengembangan.md). |

Contoh `/etc/nuxbill/config.env`:

    NUXBILL_DB=/var/lib/nuxbill/nuxbill.db
    NUXBILL_HTTP=:8080
    NUXBILL_RADIUS=:1812
    NUXBILL_SECRET_KEY=<SECRET>

Opsi CLI:

- `nuxbill --version` menampilkan versi.
- `nuxbill import ...` memindahkan data dari PHPNuxBill. Lihat [migrasi](migration-phpnuxbill.md).

## Admin pertama

Saat database kosong, tanpa admin, NuxBill membuat user `admin` dengan peran SuperAdmin. Password-nya acak dan 16 karakter. Password tidak dicetak ke log. Password ditulis ke `initial-admin-password.txt` di folder database dengan mode 0600.

Login, ganti password, lalu hapus file itu. Jika file sudah ada dan database masih kosong, start berhenti dengan pesan. Hapus file lama dulu.

## Pengaturan di UI

Ubah pengaturan di menu **Pengaturan**. Kunci di bawah tersimpan di tabel `settings`. Jika tidak disebut, nilai bawaannya kosong atau `no`.

### Umum dan lokalisasi

| Pengaturan | Fungsi |
|---|---|
| `company_name` | Nama usaha. Tampil di invoice, pesan, dan halaman login. |
| `app_url` | Alamat publik aplikasi, misalnya `https://billing.example.com`. Terisi otomatis dari alamat yang dipakai admin pertama kali login, kecuali alamat LAN atau localhost. Dipakai untuk tautan di WhatsApp, misalnya QRIS dan invoice. |
| `timezone` | Zona waktu tampilan, misalnya `Asia/Jakarta`. Waktu disimpan dalam UTC. Jika kosong, dianggap UTC, jadi isi saat pertama kali setup. |
| `country_code_phone` | Kode negara untuk nomor yang diawali `0`, misalnya `62`. |
| `default_plan_device` | Device bawaan untuk paket baru: `MikrotikHotspot`, `MikrotikPppoe`, `Dummy`, `Radius`, atau kosong. Kosong berarti mengikuti tipe paket. |
| `maintenance_mode`, `maintenance_mode_logout`, `maintenance_date` | Mode perawatan. Portal pelanggan ditutup. `maintenance_mode_logout` juga mengeluarkan pelanggan yang sedang login. |
| `reminder_hour` | Jam kirim pengingat harian, 0 sampai 23. Bawaan 7. |
| `clock_guard` | `off` mematikan guard jam. Pakai hanya jika ada RTC yang akurat. Bawaan `on`. Lihat [instalasi](installation.md#3-jam-dan-ntp). |
| `log_keep_days` | Lama penyimpanan log, sesi RADIUS tertutup, pesan inbox yang sudah dibaca, dan pembayaran belum lunas, dalam hari. Bawaan 90 jika belum pernah diisi. `0` berarti simpan selamanya. Pembayaran lunas tidak pernah dihapus. |
| `backup_keep` | Jumlah file backup harian yang disimpan, di folder utama dan di mirror. Bawaan 7. |

### Admin dan sesi

| Pengaturan | Fungsi |
|---|---|
| `session_timeout_duration` | Batas diam sesi admin dan portal, dalam menit. Bawaan 120. |
| `single_session` | `yes` agar satu admin hanya punya satu sesi aktif. |

Peran admin dan 2FA diatur per akun di menu **Pengguna Admin** dan halaman `/admin/2fa`, bukan di **Pengaturan**. Lihat [keamanan](security.md#verifikasi-dua-langkah-2fa-admin).

### Jaringan dan proxy

| Pengaturan | Fungsi |
|---|---|
| `radius_rest_allow` | Daftar IP atau CIDR, dipisah koma, yang boleh memanggil `/radius.php`. Kosong berarti hanya loopback, yaitu `127.0.0.0/8` dan `::1`. Isi dengan IP FreeRADIUS jika berjalan di host lain. Saat start, NuxBill mencatat peringatan jika kosong. |
| `trust_proxy` | `yes` agar `X-Forwarded-For` dipercaya. Header hanya dipercaya jika koneksi langsung datang dari loopback atau dari IP atau CIDR di `trusted_proxies`. Bawaan `no`. NuxBill memakai entri paling kanan sebagai IP klien, jadi proxy harus menulis IP asli di sana. |
| `trusted_proxies` | IP atau CIDR reverse proxy, dipisah koma, misalnya `172.16.0.0/16, 10.0.0.2`. Loopback selalu dipercaya. Allow-list `/radius.php` tetap memakai alamat koneksi asli, bukan hasil `X-Forwarded-For`. |
| `router_check` | `no` mematikan pengecekan router. Bawaannya aktif. Router yang mati memicu alert. Lihat [monitoring](monitoring.md). |

Contoh Nginx di depan aplikasi pada host yang sama:

- Set `trust_proxy` = `yes`.
- Di Nginx, tulis `proxy_set_header X-Forwarded-For $remote_addr;`.

Jika Cloudflare ada di depan Nginx, atur `set_real_ip_from` dengan rentang IP Cloudflare, dan `real_ip_header CF-Connecting-IP`.

### Bisnis

| Pengaturan | Fungsi |
|---|---|
| `extend_expiry` | Perpanjang paket yang masih aktif dengan menambah dari tanggal kedaluwarsa. Bawaan aktif jika belum diisi. |
| `admin_extend` | Siapa yang boleh memakai tombol Perpanjang di daftar langganan. Bawaannya `staff`, yaitu Admin, Agent, dan Sales. `managers` berarti SuperAdmin dan Admin. `super` berarti hanya SuperAdmin. `off` menyembunyikan tombol. |
| `enable_balance` | Sistem saldo: isi saldo, transfer saldo, dan perpanjang otomatis dari saldo. Bawaan aktif jika belum diisi. |
| `start_on_first_login` | `yes` agar masa aktif paket RADIUS mulai saat login pertama, bukan saat recharge. Bawaan `no`. Lihat bagian "Start on first login" di bawah. |
| `disable_registration`, `disable_voucher`, `allow_phone_otp`, `registration_username`, `phone_otp_type` | Pembatasan dan metode di portal pelanggan. `phone_otp_type` memilih `sms` atau `wa`. |
| `voucher_format` | Format kode voucher bawaan: `up`, `low`, `rand`, atau `numbers`. |
| `hs_auth_method` | Metode auth hotspot yang ditampilkan di **Aneka ragam**: `pap` atau `chap`. Pengaturan ini tidak mengubah server RADIUS bawaan. Server bawaan menerima PAP, CHAP, dan MS-CHAPv2. |
| `check_customer_online` | `yes` menampilkan di halaman pelanggan apakah pelanggan sedang terhubung lewat device paket. |

### Start on first login (`start_on_first_login`)

Jika `yes`, recharge paket RADIUS, device `Radius`, yang baru atau setelah paket habis tidak langsung memulai masa aktif. Langganan ditandai menunggu. Login RADIUS pertama pelanggan setelah itu memulai masa aktif dari saat login, dengan durasi penuh paket.

Selama menunggu:

- pelanggan tetap bisa login;
- job expiry tidak menganggap langganan kedaluwarsa;
- tanggal kedaluwarsa tampil sebagai "Mulai saat login pertama" di admin, portal, dan pesan WhatsApp recharge.

Aturan lainnya:

- Hanya paket RADIUS yang terpengaruh. Paket MikroTik, hotspot atau PPPoE dengan router, selalu mulai saat recharge.
- Recharge ulang paket yang belum dipakai menunggu lagi dari awal.
- Perpanjang paket yang sedang berjalan tetap mengikuti `extend_expiry`.

### Sakelar pesan dan OTP

Sakelar `notify_customers`, `notify_otp`, dan `expired_notify_minutes_before` dijelaskan di [integrasi](integrations.md#sakelar-pesan). Pesan operator tidak terpengaruh oleh sakelar ini.

### Tampilan

`logo`, `logo_dark`, `login_page_*`, `date_format`, `dec_point`, `thousands_sep`, dan `language` diatur di **Pengaturan**. Tema terang atau gelap dan warna aksen diatur dari header, bukan dari pengaturan ini.

## Lihat juga

- [Instalasi](installation.md): pemasangan dan variabel awal.
- [Integrasi](integrations.md): WhatsApp, Telegram, email, webhook, Tripay, dan QRIS.
- [Monitoring](monitoring.md): alert operator, ringkasan harian, dan `/metrics`.
- [Keamanan](security.md): secret, proxy, dan 2FA admin.
- [Backup dan restore](backup-restore.md): backup, mirror, dan pemulihan.
