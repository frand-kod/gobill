# 01 — Audit Kode Lama (PHPNuxBill 2025.3.20)

Audit dilakukan pada 2026-10-08 terhadap branch `master` (commit `d3e05962`).

**Untuk:** pengembangan. Hanya tersedia dalam bahasa Indonesia.

## Ukuran

| Item | Jumlah |
|---|---|
| Baris PHP (tanpa `system/vendor`) | ±58.000 |
| Baris kode inti (tanpa library yang di-vendor: PHPMailer, PEAR2, phpqrcode, Parsedown, Idiorm) | ±15.000–20.000 |
| Controller (`system/controllers/*.php`) | 39 |
| Template Smarty (`ui/**/*.tpl`) | 157 (±18.700 baris) |
| Tabel aplikasi (`install/phpnuxbill.sql`) | 21 |
| Tabel FreeRADIUS (`install/radius.sql`) | 9 |
| Versi migrasi schema (`system/updates.json`) | 60 |
| Bahasa (`system/lan/*.json`) | 5 (english, indonesia, spanish, turkish, arabic) |
| Test otomatis | 0 |

## Arsitektur saat ini

- **Entry point:** `index.php` menyimpan parameter hotspot (`nux-mac`, `nux-ip`, `nux-router`, `nux-key`) ke session, lalu memanggil `system/boot.php` → `App::_run()`.
- **Bootstrap:** `init.php` berisi autoloader custom, konfigurasi ORM, load semua plugin (`include` tanpa sandbox, error ditelan), dan load `tbl_appconfig` ke global `$config`. File ini juga berisi helper global (`_post`, `_get`, `_admin`, `r2`, `_log`, dll.).
- **Routing:** `?_route=controller/action/...`. Controller di-`include`, lalu bercabang dengan `switch ($action)`. Satu controller bisa >1.000 baris (`services.php`, `settings.php`, `plan.php`).
- **Data:** Idiorm (`system/orm.php`, di-vendor) dipakai bersama raw SQL. Tidak ada layer service. Logika bisnis tersebar di controller dan `system/autoload/Package.php`.
- **View:** Smarty 4.5.3, folder `ui/ui`. Tema custom ada di `ui/ui_custom` dan `ui/themes`.
- **State global:** `$config`, `$ui`, `$admin`, `$routes`, dan `$_c` dipakai lintas file.

## Titik integrasi (wajib dipahami sebelum port)

| Integrasi | Lokasi lama | Catatan |
|---|---|---|
| Driver device | `system/devices/*.php`, kontrak di `system/devices/readme.md` | Method: `description`, `add_customer`, `remove_customer`, `change_username`, `add_plan`, `update_plan`, `remove_plan`, `online_customer`, `connect_customer`, `disconnect_customer`. Implementasi: Dummy, MikrotikHotspot, MikrotikPppoe, MikrotikVpn, Radius, RadiusRest |
| MikroTik API | `system/autoload/Mikrotik.php` + PEAR2 `Net/RouterOS` | TCP 8728/8729 |
| FreeRADIUS REST | `radius.php` (`rlm_rest`, header `X-Freeradius-Section`) | authorize/authenticate/accounting, CHAP |
| FreeRADIUS SQL | tabel `radcheck`, `radreply`, `radacct`, dll. | Ditulis oleh driver `Radius.php` |
| Payment gateway | `system/controllers/order.php`, `callback.php`, `paymentgateway.php` | Konvensi nama fungsi: `{gw}_validate_config`, `{gw}_show_config`, `{gw}_save_config`, `{gw}_create_transaction`, `{gw}_get_status`, `{gw}_payment_notification`. Kode gateway ada di repo terpisah |
| Hook plugin | `system/autoload/Hookers.php` | `register_hook`, `run_hook` (±129 titik panggil), `register_menu`. `run_hook` berhenti di hook pertama yang cocok |
| Cron | `system/cron.php` (expiry), `system/cron_reminder.php` (pengingat) | Butuh crontab OS. Ada lock file di `system/cron.php` |
| API | `system/api.php` | Memakai ulang controller dengan dummy `$ui`. Token `md5`. CORS `*` |
| Notifikasi | `system/autoload/Message.php` | Telegram, SMS/WA via URL gateway, email (PHPMailer) |
| Installer & update | `install/`, `update.php`, `system/updates.json` | Download zip dari GitHub |

## Masalah yang ditemukan

### Keamanan

1. **SQL injection di `radius.php`.** Contoh: `whereRaw("BINARY username = '$username' AND status = 'Active'")`, dengan `$username` berasal langsung dari request RADIUS.
2. **±63 pemakaian raw SQL** (`whereRaw`, `raw_query`, `raw_execute`) di controller, autoload, dan `radius.php`. Setiap pemakaian perlu diperiksa.
3. **Password admin memakai `sha1` tanpa salt** (`system/autoload/Password.php`, `_crypt`/`_verify`).
4. **Password pelanggan disimpan plaintext.** Nilai aslinya memang dibutuhkan untuk CHAP dan PPPoE, tetapi kolomnya tidak dienkripsi.
5. **Cookie login** ditandatangani dengan `sha1(id.time.db_pass)`, jadi password DB dipakai sebagai secret.
6. **API** memakai `Access-Control-Allow-Origin: *`. Token API menurut komentar kode tidak bisa di-revoke.
7. **Plugin** di-`include` begitu saja ke proses yang sama, dan exception-nya ditelan diam-diam.

### Integritas data

1. `tbl_plans.price` bertipe `varchar(40)`. Uang tidak disimpan sebagai angka.
2. Expiry dipecah menjadi dua kolom (`expiration` date + `time` time) tanpa timezone eksplisit. Cron membandingkannya dengan `date()` PHP, padahal jam MySQL bisa berbeda (cron mencetak keduanya untuk debugging).
3. Banyak kolom `enum` dan string yang menyimpan status (`status = 'on'`), tanpa foreign key.
4. Migrasi berupa daftar `ALTER TABLE` di JSON tanpa rollback.

### Maintainability

1. Tidak ada test.
2. Logika masa aktif (Mins/Hrs/Days/Months/**Period**) diduplikasi di beberapa tempat di `system/autoload/Package.php` (sekitar baris 84, 146, 205, 292, 319).
3. Controller sangat panjang, berisi HTML inline dan aset CDN.
4. Library penting di-vendor manual, sehingga tidak ter-update otomatis.
5. `Dockerfile` masih memakai `php:7.4-apache`, padahal README mensyaratkan PHP 8.2.

## Checklist fitur (parity)

Kolom **Fase** merujuk ke [roadmap.md](roadmap.md). `Tunda` = tidak dikerjakan sampai ada permintaan nyata.

| Controller lama | Fitur | Fase |
|---|---|---|
| `login`, `logout`, `admin`, `forgot` | Login admin & pelanggan, role (SuperAdmin, Admin, Report, Agent, Sales), reset password | F0 / F4 |
| `settings` | Pengaturan aplikasi, user admin, bahasa, notifikasi, backup | F0 (dasar), F5 (lengkap) |
| `customers`, `customfield` | CRUD pelanggan, custom field, import/export CSV | F1 / F5 |
| `plan`, `services` | Paket Hotspot/PPPoE/Balance, prepaid/postpaid, aktivasi & perpanjangan | F1 |
| `bandwidth`, `pool` | Profil bandwidth, IP pool | F1 |
| `routers` | Data router, tes koneksi | F1 / F2 |
| `voucher` | Generate, cetak, aktivasi voucher | F1 |
| `coupons` | Kupon diskon | F5 |
| `accounts`, `home` | Dashboard dan akun pelanggan | F4 |
| `order`, `callback`, `paymentgateway` | Order paket mandiri, payment gateway | F4 |
| `register` | Registrasi mandiri (OTP SMS/WA) | F4 |
| `message`, `mail` | Kirim pesan massal, inbox pelanggan | F4 / F5 |
| `reports`, `export` | Laporan transaksi, export (PDF lama → CSV + print HTML) | F5 |
| `dashboard`, `widgets` | Dashboard admin dengan widget | F5 (widget tetap, tidak dinamis) |
| `maps`, `odp` | Peta pelanggan, data ODP | F5 |
| `logs` | Log aktivitas | F1 |
| `radius` (+ `radius.php`) | Manajemen NAS, auth RADIUS | F3 |
| `search_user` | Pencarian user | F1 |
| `pages`, `page`, `community` | Halaman statis (pengumuman, TOS) | F5 |
| `pluginmanager`, `plugin` | Plugin manager runtime | Tidak di-port (non-goal) |
| Face detection (`accounts`, `customers`, `settings`) | Deteksi wajah untuk foto | Tunda |
| `update.php`, `install/` | Updater & installer web | Diganti install script + migrasi otomatis saat start |

## Lihat juga

- [README](README.md)
- [roadmap](roadmap.md)
- [paritas-ui](../paritas-ui.md)
