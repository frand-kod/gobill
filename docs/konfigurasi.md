# Konfigurasi

Untuk operator. Ada dua lapis: variabel lingkungan `NUXBILL_*` (dibaca sebelum database dibuka, di systemd ada di `/etc/nuxbill/config.env`) dan pengaturan di UI admin (disimpan di tabel `settings`).

## Variabel lingkungan

| Variabel | Bawaan | Fungsi |
|---|---|---|
| `NUXBILL_DB` | `./nuxbill.db` (Docker: `/data/nuxbill.db`) | Lokasi file SQLite. Berlaku juga untuk `nuxbill import` |
| `NUXBILL_HTTP` | `:8080` | Alamat listen HTTP |
| `NUXBILL_HTTPS` | kosong | Isi `1` jika dilayani lewat HTTPS (cookie sesi diberi flag Secure) |
| `NUXBILL_SECRET_KEY` | kosong | Kunci AES-GCM untuk secret. Kosong: dibuat otomatis di `<NUXBILL_DB>.key`. Jika diisi, nilainya menggantikan file `.key` dan harus disimpan aman |
| `NUXBILL_RADIUS` | `:1812` | Alamat listen RADIUS UDP (auth; accounting di port+1). Kosong atau `off` mematikan listener UDP; `/radius.php` tetap jalan |
| `NUXBILL_BACKUP_DIR` | `<folder DB>/backup` | Folder backup harian |

Contoh `/etc/nuxbill/config.env`:

    NUXBILL_DB=/var/lib/nuxbill/nuxbill.db
    NUXBILL_HTTP=:8080
    NUXBILL_RADIUS=:1812
    NUXBILL_SECRET_KEY=<SECRET>

`NUXBILL_TEST_MYSQL_DSN` dan `NUXBILL_TEST_PHP_SQL` hanya dipakai test importer (lihat [pengembangan.md](pengembangan.md)).

Opsi CLI: `nuxbill --version`, dan `nuxbill import ...` ([migrasi-phpnuxbill.md](migrasi-phpnuxbill.md)).

## Admin pertama

Saat database kosong, aplikasi membuat user `admin` (SuperAdmin) dengan password acak 16 karakter yang dicetak sekali di log (level WARN). Catat, lalu ganti.

## Pengaturan penting di UI

Ubah di menu Settings (kunci di bawah muncul di tabel `settings`).

| Pengaturan | Fungsi |
|---|---|
| `timezone` | Zona waktu tampilan (mis. `Asia/Jakarta`). Waktu disimpan UTC. Kosong dianggap UTC, jadi isi saat pertama kali setup |
| `default_plan_device` | Device bawaan paket baru: `MikrotikHotspot`, `MikrotikPppoe`, `Dummy`, `Radius`, atau kosong (ikut tipe paket) |
| `radius_rest_allow` | Daftar IP/CIDR (pisahkan koma) yang boleh memanggil `/radius.php`. Kosong = semua boleh, dengan peringatan di log |
| `trust_proxy` | `yes` agar `X-Forwarded-For` dipercaya. Biarkan `no` tanpa reverse proxy |
| `clock_guard` | `off` mematikan guard jam (hanya jika ada RTC akurat) |
| `log_keep_days` | Hapus log lebih lama dari N hari. `0`/kosong = simpan selamanya |
| `backup_keep` | Jumlah file backup harian yang disimpan (bawaan 7) |
| `reminder_hour` | Jam kirim pengingat harian |
| `daily_summary_enabled`, `daily_summary_time`, `daily_summary_channel`, `daily_summary_wa_to` | Ringkasan harian untuk operator (bukan pelanggan): `yes`/`no` (bawaan `no`), jam `HH:MM` zona waktu server (bawaan `07:00`), saluran `telegram`/`wa`/`both`, nomor WA operator. Telegram memakai `telegram_target_id`. Dikirim sekali sehari (tanggal terakhir di `daily_summary_last`, aman saat restart), ditunda bila jam sistem tidak tepercaya. Tombol "Kirim ringkasan sekarang" di Settings > Notifications untuk uji coba |
| `maintenance_mode` | Mode perawatan: portal pelanggan ditutup |
| `extend_expiry`, `enable_balance` | Perilaku bisnis. Jika belum diisi, keduanya dianggap aktif |
| `disable_registration`, `disable_voucher`, `allow_phone_otp` | Pembatasan portal pelanggan |
| `telegram_bot`, `wa_url`, `sms_url`, SMTP | Kanal notifikasi |
| `tripay_*`, `merchant_code`, `api_key`, `private_key` | Gateway Tripay (lihat [keamanan.md](keamanan.md) soal penyimpanan) |

## WhatsApp

Ada dua cara mengirim WhatsApp. Yang dipakai ditentukan otomatis:

1. **Server WA langsung** (disarankan): isi di Pengaturan > Integrasi, bagian "WhatsApp (server WA)":
   `alt_wga_server_url` (mis. `http://127.0.0.1:3030`), `alt_wga_device_id` (opsional), `alt_wga_username` dan `alt_wga_password` (basic auth, jika server memakainya). NuxBill mengirim `POST <alt_wga_server_url>/send/message` dengan body `{"phone":"628xxx@s.whatsapp.net","message":"..."}`, sama seperti plugin "Alternative WhatsApp Gateway" di PHPNuxBill. Nomor yang diawali 0 diubah memakai `country_code_phone`.
2. **`wa_url`** (URL template dengan `[number]` dan `[text]`): dipakai hanya jika `alt_wga_server_url` kosong.

Jika `alt_wga_server_url` terisi, `wa_url` diabaikan sepenuhnya. Jika `wa_url` masih berisi alamat plugin PHP lama (`...?_route=plugin/wga_sendMessage&...`), NuxBill mencatat peringatan di log dan tetap mengirim langsung ke server WA. Kosongkan `wa_url` supaya tidak membingungkan.

Tombol "Kirim pesan uji" di halaman yang sama mengirim satu pesan ke nomor yang Anda ketik, memakai isian di form (walau belum disimpan), lalu menampilkan jawaban server dalam bahasa biasa. Perangkat WhatsApp dan login QR tidak diatur di NuxBill: lakukan di halaman server WA sendiri.
