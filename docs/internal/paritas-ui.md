# Paritas UI: PHPNuxBill lama vs NuxBill Go

Dokumen ini membandingkan UI lama (`../phpnuxbill/ui/ui`, 157 template) dengan rewrite Go. Hanya dokumentasi, tidak ada kode yang diubah. Dibuat 2026-10-08 dari kode di `main`.

**Untuk:** pengembangan. Hanya tersedia dalam bahasa Indonesia.

Sumber: template `ui/ui/admin/**`, `ui/ui/customer/*.tpl`, `ui/ui/widget/**`; controller `system/controllers/*.php`; sisi baru `internal/web/*.go`, `web/templates/*.html`, `internal/db/migrations/0001-0003`. Fase dan non-goal dari `rencana/audit-legacy.md` dan `progres.md`.

## 1. Legenda dan ringkasan

**Penanda field:** ✅ ada (setara) · ⚠️ beda (cara/nama/tipe berbeda, dijelaskan di Catatan) · ❌ belum ada.

**Status layar:** `Ada` semua field penting ada (beda yang disengaja boleh) · `Sebagian` layar ada tapi ada field/aksi yang hilang · `Belum` tidak ada · `Ditunda` sengaja ditunda sampai ada permintaan nyata · `Non-goal` sengaja tidak di-port.

**Konvensi sisi baru:**
- Form dibuat dari `[]field` (`internal/web/crud.go`) dan dirender `web/templates/form.html`; daftar dari `listPage` + `list.html`. Semua daftar punya satu kotak cari `q` dan paging 20 baris (`perPage`); filter select lewat `listPage.Filters` (voucher, pelanggan, langganan), kolom bisa diurut (`SortKeys`).
- Hak akses: `managers` = SuperAdmin+Admin; `staff` = +Agent+Sales; `all` = semua admin yang login.
- Uang = INTEGER rupiah (`price`, `balance`). Waktu = unix UTC (`expires_at`, `created_at`), tampil lewat `s.ts()`.
- Password router/secret pelanggan/secret NAS = AES-GCM (`*_enc`), tidak pernah dirender balik. Password login = bcrypt (`password_hash`).
- Template layout/partial dilewati (lihat bagian akhir tabel).

### Tabel ringkasan per layar

| # | Template lama | Controller/action | Route baru | Status | Fase |
|---|---|---|---|---|---|
| A1 | `admin/dashboard.tpl` | `dashboard` | `GET /admin` | Ada | F5 |
| A2 | `admin/admin/login.tpl` | `admin/post` | `GET/POST /login`, `POST /logout` | Ada | F0 |
| A3 | `admin/admin/list.tpl` | `settings/users` | `GET /admin/users` | Sebagian | F5 |
| A4 | `admin/admin/add.tpl` | `settings/users-add`, `users-post` | `GET /admin/users/new`, `POST /admin/users` | Sebagian | F5 |
| A5 | `admin/admin/edit.tpl` | `settings/users-edit`, `users-edit-post`, `users-delete` | `GET /admin/users/{id}/edit`, `POST /admin/users/{id}`, `POST /admin/users/{id}/delete` | Sebagian | F5 |
| A6 | `admin/admin/view.tpl` | `settings/users-view` | — (diganti halaman edit) | Non-goal (digabung ke edit) | F5 |
| A7 | `admin/change-password.tpl` | `settings/change-password(-post)` | `GET/POST /admin/password` | Sebagian | F5 |
| A8 | `admin/customers/list.tpl` | `customers/list`, `csv`, `sync`, `delete` | `GET /admin/customers`, `GET /admin/customers/export` | Sebagian | F1 |
| A9 | `admin/customers/add.tpl` | `customers/add`, `add-post` | `GET /admin/customers/new`, `POST /admin/customers` | Sebagian | F1 |
| A10 | `admin/customers/edit.tpl` | `customers/edit`, `edit-post` | `GET /admin/customers/{id}/edit`, `POST /admin/customers/{id}` | Sebagian | F1 |
| A11 | `admin/customers/view.tpl` | `customers/view`, `recharge`, `deactivate`, `login` | `GET /admin/customers/{id}` (`customer.html`) | Sebagian | F1 |
| A12 | `admin/hotspot/list.tpl` | `services/hotspot` | `GET /admin/plans` | Sebagian | F1 |
| A13 | `admin/hotspot/add.tpl` | `services/add`, `add-post` | `GET /admin/plans/new`, `POST /admin/plans` | Sebagian | F1 |
| A14 | `admin/hotspot/edit.tpl` | `services/edit`, `edit-post` | `GET /admin/plans/{id}/edit`, `POST /admin/plans/{id}` | Sebagian | F1 |
| A15 | `admin/pppoe/list.tpl` | `services/pppoe` | `GET /admin/plans` (tipe PPPoE) | Sebagian | F1 |
| A16 | `admin/pppoe/add.tpl` | `services/pppoe-add(-post)` | `/admin/plans/new` | Sebagian | F1 |
| A17 | `admin/pppoe/edit.tpl` | `services/pppoe-edit(-post)` | `/admin/plans/{id}/edit` | Sebagian | F1 |
| A18 | `admin/balance/list.tpl` | `services/balance` | `/admin/plans` (tipe Balance) | Sebagian | F1 |
| A19 | `admin/balance/add.tpl` | `services/balance-add(-post)` | `/admin/plans/new` | Sebagian | F1 |
| A20 | `admin/balance/edit.tpl` | `services/balance-edit(-post)` | `/admin/plans/{id}/edit` | Sebagian | F1 |
| A21 | `admin/vpn/list.tpl` | `services/vpn` | — | Ditunda | Tunda |
| A22 | `admin/vpn/add.tpl` | `services/vpn-add(-post)` | — | Ditunda | Tunda |
| A23 | `admin/vpn/edit.tpl` | `services/vpn-edit(-post)` | — | Ditunda | Tunda |
| A24 | `admin/bandwidth/list.tpl` | `bandwidth/list` | `GET /admin/bandwidth` | Ada | F1 |
| A25 | `admin/bandwidth/add.tpl` | `bandwidth/add`, `add-post` | `/admin/bandwidth/new`, `POST /admin/bandwidth` | Sebagian | F1 |
| A26 | `admin/bandwidth/edit.tpl` | `bandwidth/edit`, `edit-post` | `/admin/bandwidth/{id}/edit` | Sebagian | F1 |
| A27 | `admin/pool/list.tpl` | `pool/list`, `sync` | `GET /admin/pool` | Ada | F1 |
| A28 | `admin/pool/add.tpl` | `pool/add`, `add-post` | `/admin/pool/new` | Ada | F1 |
| A29 | `admin/pool/edit.tpl` | `pool/edit`, `edit-post` | `/admin/pool/{id}/edit` | Ada | F1 |
| A30 | `admin/port/list.tpl` | `pool/port` | — | Ditunda | Tunda (bersama VPN) |
| A31 | `admin/port/add.tpl` | `pool/add-port(-post)` | — | Ditunda | Tunda |
| A32 | `admin/port/edit.tpl` | `pool/edit-port(-post)` | — | Ditunda | Tunda |
| A33 | `admin/routers/list.tpl` | `routers/list` | `GET /admin/routers`, `POST /admin/routers/{id}/test` | Sebagian | F1 / F2 |
| A34 | `admin/routers/add.tpl` | `routers/add`, `add-post` | `/admin/routers/new` | Sebagian | F1 |
| A35 | `admin/routers/edit.tpl` | `routers/edit`, `edit-post` | `/admin/routers/{id}/edit` | Sebagian | F1 |
| A36 | `admin/radius/nas.tpl` | `radius/nas-list` | `GET /admin/nas` | Ada | F3 |
| A37 | `admin/radius/nas-add.tpl` | `radius/nas-add(-post)` | `/admin/nas/new` | Ada | F3 |
| A38 | `admin/radius/nas-edit.tpl` | `radius/nas-edit(-post)` | `/admin/nas/{id}/edit` | Ada | F3 |
| A39 | `admin/voucher/list.tpl` | `rencana/voucher`, `remove-voucher` | `GET /admin/vouchers` | Sebagian | F1 |
| A40 | `admin/voucher/add.tpl` | `rencana/add-voucher`, `voucher-post` | `GET /admin/vouchers/new`, `POST /admin/vouchers` | Sebagian | F1 |
| A41 | `admin/voucher/view.tpl` | `rencana/voucher-view` | `GET /admin/vouchers/view` (`voucher_view.html`) | Ada | F5 |
| A42 | `admin/print/voucher.tpl` | `rencana/print-voucher` | `GET /admin/vouchers/print` (`print.html`) | Sebagian | F1 |
| A43 | `admin/rencana/recharge.tpl` | `rencana/recharge`, `recharge-post` | `POST /admin/customers/{id}/recharge` (form di `customer.html`) | Sebagian | F1 |
| A44 | `admin/rencana/recharge-confirm.tpl` | `rencana/recharge-confirm`, `customers/recharge` | `POST /admin/customers/{id}/recharge/confirm` (`recharge_confirm.html`) | Ada | F1 |
| A45 | `admin/rencana/refill.tpl` | `rencana/refill`, `refill-post` | `GET/POST /admin/vouchers/redeem` | Ada | F1 |
| A46 | `admin/rencana/deposit.tpl` | `rencana/deposit`, `deposit-post` | `GET/POST /admin/deposit` | Ada | F1 |
| A47 | `admin/rencana/active.tpl` | `rencana/list`, `sync`, `csv`, `extend` | `GET /admin/subscriptions`, `GET /admin/subscriptions/export`, `POST .../{id}/extend`, `POST .../{id}/deactivate`, `POST .../{id}/sync` | Ada | F1 |
| A48 | `admin/rencana/edit.tpl` | `rencana/edit`, `edit-post` | `GET /admin/subscriptions/{id}/edit`, `POST /admin/subscriptions/{id}` | Ada | F1 |
| A49 | `admin/rencana/invoice.tpl` | `rencana/view`, `viewx` | `GET /admin/transactions/{id}/invoice` (`invoice.html`) | Sebagian | F5 |
| A50 | `admin/rencana/invoice-print.tpl` | `rencana/print` | `GET /admin/transactions/{id}/invoice` (tombol Print) | Ada | F5 |
| A51 | `admin/reports/activation.tpl` | `reports/activation` | `GET /admin/transactions` | Sebagian | F5 |
| A52 | `admin/reports/list.tpl` | `reports/daily-report`, `by-date` | `GET /admin/reports`, `GET /admin/reports/export` (`report.html`) | Ada | F5 |
| A53 | `admin/reports/period.tpl` | `reports/period-report` | `GET /admin/reports/period` | Ada | F5 |
| A54 | `admin/reports/period-view.tpl` | `reports/period-view` | `GET /admin/reports/period` (hasil di halaman yang sama) | Ada | F5 |
| A55 | `admin/print/by-date.tpl` | `export/print-by-date` | `GET /admin/reports/print?date=` (`report_print.html`) | Ada | F5 |
| A56 | `admin/print/by-period.tpl` | `export/print-by-period` | `GET /admin/reports/print?from=&to=` | Ada | F5 |
| A57 | `admin/logs/system.tpl` | `logs/list`, `list-csv` | `GET /admin/logs` | Sebagian | F1 |
| A58 | `admin/logs/radius.tpl` | `logs/radius`, `radius-csv` | `GET /admin/logs/radius`, `GET /admin/logs/radius/export` | Ada | F3 |
| A58b | — (baru, tanpa padanan lama) | — | `GET /admin/radius/sessions`, `POST /admin/radius/sessions/{id}/disconnect` | Ada | F3 |
| A59 | `admin/logs/message.tpl` | `logs/message`, `message-csv` | `GET /admin/logs/messages`, `GET /admin/logs/messages/export` | Ada | F4 |
| A60 | `admin/coupons/list.tpl` | `coupons` | `/admin/coupons` | Ada | F5 |
| A61 | `admin/coupons/add.tpl` | `coupons/add`, `add-post` | `/admin/coupons` | Ada | F5 |
| A62 | `admin/coupons/edit.tpl` | `coupons/edit`, `edit-post` | `/admin/coupons` | Ada | F5 |
| A63 | `admin/message/single.tpl` | `message/send`, `send-post` | `GET/POST /admin/message/send` | Ada | F5 |
| A64 | `admin/message/bulk.tpl` | `message/send_bulk`, `send_bulk_ajax` | `GET/POST /admin/message/bulk`, `GET /admin/message/bulk/status` | Sebagian | F5 |
| A65 | `admin/odp/list.tpl` | `odp/list` | `GET /admin/odp` | Ada | F5 |
| A66 | `admin/odp/add.tpl` | `odp/add`, `add-post` | `GET /admin/odp/new`, `POST /admin/odp` | Ada | F5 |
| A67 | `admin/odp/edit.tpl` | `odp/edit`, `edit-post` | `GET /admin/odp/{id}/edit`, `POST /admin/odp/{id}` | Ada | F5 |
| A68 | `admin/maps/customers.tpl` | `maps/customer` | `GET /admin/maps/customers` (+ `/data`) | Ada | F5 |
| A69 | `admin/maps/routers.tpl` | `maps/routers` | `GET /admin/maps/routers` (+ `/data`), lingkaran coverage | Ada | F5 |
| A70 | `admin/maps/odps.tpl` | `maps/odp` | `GET /admin/maps/odp` (+ `/data`) | Ada | F5 |
| A71 | `admin/paymentgateway/list.tpl` | `paymentgateway` | `GET /admin/payment-gateway` | Sebagian | F4 |
| A72 | `admin/paymentgateway/audit.tpl` | `paymentgateway/audit` | `GET /admin/payment-gateway/audit` (+ `/export` CSV) | Ada | F4 |
| A73 | `admin/paymentgateway/audit-view.tpl` | `paymentgateway/auditview` | `GET /admin/payment-gateway/audit/{id}` | Ada | F4 |
| A74 | `admin/maintenance.tpl` | `init.php` (saat maintenance mode) | `503` untuk portal/publik | Sebagian | F5 |
| A75 | `admin/404.tpl`, `admin/error.tpl`, `admin/alert.tpl` | `boot.php`, banyak controller | respons teks `http.Error`; flash di `app.html` | Sebagian | F5 |
| A76 | `admin/community.tpl`, `admin/rollback.tpl` | `community`, `community/rollback` | — | Non-goal | — (updater diganti install script) |
| S1 | `admin/settings/app.tpl` | `settings/app(-post)` | `GET/POST /admin/settings` (4 key saja) | Sebagian | F0 dasar / F5 |
| S2 | `admin/settings/localisation.tpl` | `settings/localisation(-post)` | `/admin/settings` | Sebagian | F0 / F5 |
| S3 | `admin/settings/notifications.tpl` | `settings/notifications(-post)` | `GET/POST /admin/settings/notifications` | Sebagian | F4 |
| S4 | `admin/settings/miscellaneous.tpl` | `settings/miscellaneous(-post)` | `GET/POST /admin/settings/miscellaneous` | Sebagian | F4 / F5 |
| S5 | `admin/settings/maintenance-mode.tpl` | `settings/maintenance` | `maintenance_date` di `/admin/settings/miscellaneous` | Sebagian | F5 |
| S6 | `admin/settings/dbstatus.tpl` | `settings/dbstatus`, `dbbackup`, `dbrestore` | `GET /admin/settings/miscellaneous/restore` (restore), `GET /admin/settings/miscellaneous/import` (impor PHPNuxBill), download backup di Miscellaneous | Ada | F5 |
| S7 | `admin/settings/language-add.tpl` | `settings/language`, `lang-post` | — (bahasa = file JSON `internal/i18n`) | Ditunda | Tunda |
| S8 | `admin/settings/customfield.tpl` | `customfield` | `/admin/fields` (CRUD) | Sebagian | F5 |
| S9 | `admin/settings/page.tpl` | `pages/{nama}` | `GET/POST /admin/pages/{slug}`, `GET /pages/{slug}` | Sebagian | F5 |
| S10 | `admin/settings/devices.tpl` | `settings/devices` | — (driver bawaan di kode) | Non-goal | — |
| S11 | `admin/settings/widgets.tpl`, `widgets_add_edit.tpl` | `widgets` | — (widget tetap) | Non-goal | — (F5: widget tetap) |
| S12 | `admin/settings/plugin-manager.tpl` | `pluginmanager` | — | Non-goal | — |
| W1-W14 | `widget/*.tpl` (14 file) | `dashboard` | lihat bagian 4 | Sebagian (8 dari 14) | F5 |
| W15-W22 | `widget/customers/*.tpl` (8 file) | `home` | — | Belum | F4 |
| C1-C27 | `customer/*.tpl` (lihat bagian 5) | `login`, `register`, `forgot`, `home`, `accounts`, `order`, `voucher`, `mail`, `page` | — | Belum | F4 |

**Dilewati (layout/partial, bukan layar):** `admin/header.tpl`, `admin/footer.tpl`, `admin/autoload/plan.tpl`, `admin/autoload/pool.tpl`, `admin/autoload/server.tpl` (partial opsi AJAX; di UI baru diganti `show`/Alpine di `plans.go` dan dropdown langsung), `customer/header.tpl`, `customer/footer.tpl`, `customer/header-public.tpl`, `customer/footer-public.tpl`, `widget/card_html.tpl` (pembungkus HTML bebas).

**Template mati / tidak terjangkau:**
- `customer/order.tpl`: dipanggil di `order.php:18` tapi filenya tidak ada di repo lama (referensi mati).
- `customer/login-custom-moon.tpl` dan `customer/reg-login-custom-moon.tpl`: hanya dipakai bila `login_Page_template` dipilih (tema opsional, dinamis).
- `admin/settings/devices.tpl`: hanya daftar driver (tidak ada input).
- `admin/admin/view.tpl`: hanya tampilan baca dengan tombol Edit/Cancel.
- `admin/community.tpl`/`rollback.tpl`: hanya tautan komunitas dan tombol update GitHub.
- `admin/autoload/*.tpl`: hanya dipanggil lewat AJAX dari form paket/voucher.

**Hitungan baris tabel ringkasan (92 baris, dihitung ulang 2026-10-10):** Ada 37 · Sebagian 41 · Non-goal 5 · Ditunda 7 · Belum 2.

**Hitungan baris field bertanda** (bagian 2, 3, 5; satu baris = satu field): Admin ✅ 101 · ⚠️ 54 · ❌ 24; Settings ✅ 58 · ⚠️ 28 · ❌ 27; Portal pelanggan ✅ 30 · ⚠️ 4 · ❌ 9. Total ✅ 189 · ⚠️ 86 · ❌ 60. (termasuk baris 2FA v0.1.4). Baris VPN/Port (Ditunda) ditulis sebagai prosa dan tidak dihitung.

---

## 2. Bagian Admin

Format tiap layar: tabel field lalu **Kolom daftar** dan **Aksi**. Kolom tabel: `Field lama` = atribut `name` input; `Validasi PHP` dari template (`required`, `maxlength`) dan controller; `Field baru` = nama kolom/input di sisi Go.

### A1. Dashboard `admin/dashboard.tpl`

Berisi tata letak widget yang disusun dari `$config['dashboard_Admin']` (lihat bagian 4). Tidak ada form.

| Elemen lama | Elemen baru | St | Catatan |
|---|---|---|---|
| Widget `top_widget` (4 kotak) | `dashboard.html` (4 kotak: `tile-today`, `tile-month`, `tile-subs`, `tile-customers`) | ⚠️ | Isi beda sedikit, lihat W1 |
| Susunan widget dari config (`dashboard_cr`) | Susunan tetap | ⚠️ | Disengaja: "widget tetap, tidak dinamis" (01-audit) |
| Banner peringatan jam (tidak ada) | `.Warn` clock guard | ✅ | Fitur baru |

### A2. Login admin `admin/admin/login.tpl`

| Field lama | Label | Tipe | Wajib/validasi | Field baru | St | Catatan |
|---|---|---|---|---|---|---|
| `username` | Username | text | required | `username` (`login.html`) | ✅ | |
| `password` | Password | password | required | `password` | ✅ | Hash lama `sha1` tanpa salt diganti bcrypt; pembatas brute-force |
| (baru, v0.1.4) | Kode 2FA | 6 digit TOTP | opsional per admin | `/login/2fa` (`login_2fa.html`) | ✅ | Setelah kata sandi benar; kode pemulihan 8 kali pakai. Diatur di `/admin/2fa`. Lihat [keamanan.md](../id/security.md) |
| (cookie "remember") | — | — | cookie `sha1(id.time.db_pass)` | session `scs` (sqlite3store) | ⚠️ | Disengaja: cookie bertanda tangan password DB dianggap lemah (01-audit keamanan 5) |
| `csrf_token` | — | hidden | `csrf_enabled` setting | `http.NewCrossOriginProtection` | ⚠️ | CSRF selalu aktif, tidak bisa dimatikan |

Aksi: Login, Logout (`POST /logout`). Tidak ada link "lupa password" admin di lama.

### A3-A6. Admin user (`settings/users*`)

Baru: `internal/web/admins.go`, `/admin/users` (SuperAdmin, Admin, Agent; Report/Sales = 403). Kolom baru di `admins`: `email`, `phone`, `city`, `root_id` (Agent pemilik Sales), `session_version`.

**Aturan peran (lebih ketat dari lama):** SuperAdmin kelola semua; Admin hanya Report/Agent/Sales (lama: Admin bisa menetapkan peran apa saja); Agent hanya Sales miliknya (`root_id` dipaksa = Agent, peran dipaksa Sales; lama: peran dari form); hapus hanya SuperAdmin/Admin. Tak ada yang bisa menghapus diri, mengubah peran/status/password sendiri lewat form ini (password sendiri lewat A7). SuperAdmin aktif terakhir tidak bisa dihapus, diturunkan, atau dinonaktifkan (dijaga di SQL, atomik). Ganti password orang lain, ganti peran/status, atau hapus = sesi orang itu mati (`admins.session_version` dibandingkan dengan `sv` di sesi pada tiap request). Semua perubahan masuk activity log (`users.create|update|delete|password`).

**A4 `admin/admin/add.tpl`**

| Field lama | Label | Tipe | Wajib/validasi | Field baru | St | Catatan |
|---|---|---|---|---|---|---|
| `fullname` | Full Name | text | 3-45 karakter | `admins.fullname` | ✅ | |
| `phone` | Phone | number | — | `admins.phone` | ⚠️ | text, bukan number |
| `email` | Email | text | — | `admins.email` | ✅ | divalidasi bila diisi |
| `city`, `subdistrict`, `ward` | Location | text | — | `admins.city` | ⚠️ | hanya `city`; subdistrict/ward dibuang |
| `user_type` | User Type | select | SuperAdmin/Admin/Report/Agent/Sales | `admins.role` | ✅ | pilihan dibatasi per peran pelaku |
| `root` | Agent | select | admin induk bila Sales | `admins.root_id` | ✅ | harus Agent yang ada; Agent pelaku otomatis |
| `username` | Username | text | 3-45, unik | `admins.username` | ✅ | |
| `password` | Password | password | min 6 | `admins.password_hash` | ✅ | wajib saat buat, 6-72 byte, plus `cpassword`; bcrypt |
| `send_notif` | Send Notification | select | -/sms/wa | — | ❌ | Bergantung notifikasi F4; password tidak dikirim lewat pesan |

**A5 `admin/admin/edit.tpl`:** field A4; password kosong = tidak diubah (`cpassword` wajib sama bila diisi); `status` (Active/Inactive) ada untuk admin lain. Belum: `photo` (face detect = Tunda). Diri sendiri: hanya nama/email/telepon/kota.

Kolom daftar `admin/list.tpl`: Username, Full Name, Phone, Email, Type, Location, Agent, Last Login, Manage, ID. Baru: Username, Full Name, User Type, Status, Last Login; cari `q` (username/nama) dan paging. Belum: kolom Phone/Email/Location/Agent, halaman View (A6).

### A7. Ganti password `admin/change-password.tpl`

| Field lama | Label | Tipe | Wajib/validasi | Field baru | St | Catatan |
|---|---|---|---|---|---|---|
| `password` | Current Password | password | cocok dengan hash | `current` | ✅ | `POST /admin/password`; salah = 422 dan dihitung di throttle login; sukses = `session_version` naik (sesi lain mati) dan token sesi diputar (`RenewToken`) |
| `npass` | New Password | password | min 6 (settings.php) | `password` | ✅ | 6-72 byte |
| `cnpass` | Confirm New Password | password | harus sama | `cpassword` | ✅ | |

### A8-A11. Pelanggan

**A9 form tambah `admin/customers/add.tpl` / A10 edit `admin/customers/edit.tpl`** (baru: `custFields` di `internal/web/customers.go`)

| Field lama | Label | Tipe | Wajib/validasi PHP | Field baru | St | Catatan |
|---|---|---|---|---|---|---|
| `username` | Username | text | required, 3-54 karakter, unik (edit: tidak boleh bentrok dengan pelanggan/pppoe lain) | `username` | ⚠️ | Baru: required + unik, tanpa batas panjang. Edit: username read-only (tidak ada input) |
| `fullname` | Full Name | text | required (add), 2-25 karakter | `fullname` | ⚠️ | Baru: required saja, tanpa batas panjang |
| `email` | Email | email | — | `email` | ✅ | Validasi format `Invalid email address` |
| `phonenumber` | Phone Number | text | — | `phone` | ⚠️ | Nama beda. Lama menormalkan kode negara (`country_code_phone`); baru belum |
| `password` | Password | password | required (add), 8-35 (sejak v0.1.4; sebelumnya 3-35) | `password` -> `password_hash` | ⚠️ | Disengaja: lama plaintext (01-audit keamanan 4); baru bcrypt untuk login portal. Edit: kosong = tidak diubah |
| `address` | Home Address | textarea | — | `address` | ✅ | |
| `service_type` | Service Type | select Hotspot/PPPoE/VPN/Others | — | `service_type` | ⚠️ | Opsi `VPN` tidak ada (VPN Ditunda) |
| `account_type` | Account Type | select Personal/Business | — | — | ❌ | Tidak ada kolom `account_type` |
| `coordinates` | Coordinates | text (peta) | — | — | ✅ | Kolom `coordinates` (lat,lng), migrasi 0005; picker peta di form |
| `status` (edit saja) | Status | select | — | `status` | ✅ | Add juga punya `status` di baru. Opsi sama (Active/Banned/Disabled/Inactive/Limited/Suspended) |
| `pppoe_username` | Usernames | text | harus unik | `pppoe_username` | ✅ | |
| `pppoe_password` | Password | password | — | `secret` -> `secret_enc` | ⚠️ | Disengaja: plaintext diganti AES-GCM; label "Router Secret"; kosong = tidak diubah |
| `pppoe_ip` | Remote IP | text | — | `pppoe_ip` | ✅ | |
| `city` | City | text | — | — | ❌ | Tidak ada kolom (alamat satu field) |
| `district` | District | text | — | — | ❌ | idem |
| `state` | State | text | — | — | ❌ | idem |
| `zip` | Zip | text | — | — | ❌ | idem |
| `photo` (edit saja) | Photo | file | face detection | — | ❌ | Foto Tunda (face detection Tunda); tidak ada kolom |
| `custom_field_name[]`, `custom_field_value[]`, `custom_fields[...]` | Custom fields | text | — | `cf_<id>` (tabel `customer_field_values`) | ✅ | Form dan detail pelanggan; nilai divalidasi (wajib, angka, tanggal, pilihan) |
| `send_welcome_message` | Send welcome message | checkbox | — | `send_welcome_message` + `notify_sms`, `notify_wa`, `notify_email` | ✅ | Hanya form tambah. Mengirim `welcome_message` lewat `internal/notify` di kanal yang dicentang; kata sandi tidak pernah ikut (`[[Password]]` = `********`) |
| `sms`, `wa`, `mail` | Notification via | checkbox | — | — | ❌ | `internal/notify` ada, belum disambung ke UI |
| `id` (edit) | — | hidden | — | `{id}` di URL | ✅ | |
| — | Billing Day | number 1-31 | — | `billing_day` | ✅ | Field baru; override `plans.billing_day` |
| — | Auto Renewal | checkbox | — | `auto_renewal` | ✅ | Field baru; di lama ada di widget `account_info` pelanggan (`Disable auto renewal?`) |

**Kolom daftar A8 `customers/list.tpl`:** checkbox (`customer_ids[]`), Username, Photo, Account Type, Full Name, Balance, Contact, Package, Service Type, PPPOE, Status, Created On, Manage.
- Baru ada: Username (link ke view), Full Name, Balance, Package (langganan aktif), Service Type, PPPoE Username, Status.
- Belum: Photo, Account Type, Contact, Created On, checkbox.

**Aksi daftar:**
- Filter lama: `order` (username/fullname/lastname/created_at/balance/status), `orderby` (asc/desc), `filter` (status), `search`. Baru: `q`, `service_type`, `status`, urut `sort`/`dir` (username, fullname, balance, status), tombol Export CSV (`/admin/customers/export`, mengikuti filter, semua halaman).
- Per baris lama: View, Edit, Sync, Recharge, Delete. Baru: View (link username), Edit, Delete. Belum: Sync, Recharge langsung dari daftar (ada di view).
- Massal lama: Delete Selected, Send Message (modal: email/inbox/sms/wa). Belum.
- Export: `customers/csv` (tombol CSV). Belum (F5). Import CSV (F5) belum.

**A11 `customers/view.tpl` (baru: `customer.html`)**

| Elemen lama | Elemen baru | St | Catatan |
|---|---|---|---|
| Info akun (username, nama, kontak, alamat, tipe, status, balance) | `<dl>` Full Name, Phone, Email, Address, Service Type, PPPoE Username, PPPoE IP, Router Secret (Yes/No), Balance, Billing Day, Auto Renewal, Created | ✅ | Secret hanya ditampilkan Yes/No |
| Tabel paket aktif (Plan Name, Gateway, Routers, Type, Plan Price, Created On, Expires On, Date Done, Method) | Tabel "Service Plan": Plan Name, Type, Expires, Status | ⚠️ | Hilang: Created On, Method, Price, Routers; `Expires On` tanggal+jam diganti `expires_at` (disengaja, satu kolom UTC) |
| Tabel riwayat transaksi (Invoice, Username, Plan Name, Plan Price, Type, Created On, Expires On, Method) | Tabel "Transactions": Invoice, Date, Plan Name, Method, Plan Price | ⚠️ | Hilang: Type, Expires On |
| Tombol Recharge per paket | Form "Recharge Account" (`plan`, `method` Cash/Balance) | ⚠️ | Di lama per-paket dan memakai halaman confirm (A44) |
| Tombol Deactivate paket | Tombol Deactivate (`POST /admin/customers/{id}/deactivate`, SuperAdmin/Admin) | ✅ | Kedaluwarsakan semua langganan aktif + hapus dari router (`DeactivateCustomer`); satu tombol untuk semua paket, bukan per paket |
| Tombol Sync | Tombol Sync (`POST /admin/customers/{id}/sync`, SuperAdmin/Admin) | ✅ | Kirim ulang semua langganan aktif ke router (`SyncCustomer`) |
| Tombol Send Message | Link `/admin/message/send?customer=ID` (form terisi) | ✅ | |
| Tombol Login as Customer | Tombol `POST /admin/customers/{id}/login` (SuperAdmin/Admin) | ✅ | Sesi pelanggan di browser yang sama tanpa kata sandi; log `customer.impersonate`; portal menampilkan banner "Anda masuk sebagai admin" + tombol Back to admin (`POST /portal/impersonate/end`) yang hanya mengakhiri sesi pelanggan, sesi admin utuh |
| Tombol Edit, Back | Edit, link ke daftar | ✅ | |
| Link Redeem Voucher | `/admin/vouchers/redeem?customer=` | ✅ | Tambahan baru |

### A12-A14. Paket Hotspot (`hotspot/*`) dan A15-A17 PPPoE (`pppoe/*`)

Satu form baru `planFields` (`internal/web/plans.go`) melayani tipe Hotspot/PPPoE/Balance lewat select `type` (Alpine `show`). Tabel `plans`. Tabel di bawah menggabungkan hotspot (H), pppoe (P). Field PPPoE memakai nama `name_plan` bukan `name`.

| Field lama | Label | Tipe | Wajib/validasi PHP | Field baru | St | Catatan |
|---|---|---|---|---|---|---|
| `enabled` | Status | radio 1/0 | — | `enabled` | ✅ | Checkbox |
| `prepaid` | Type | radio yes/no | — | `billing` prepaid/postpaid | ⚠️ | Nama dan nilai beda; label "Plan Type" |
| `plan_type` | Package Type | radio Personal/Business | — | — | ❌ | Tidak ada kolom (kategori) |
| `radius` (P saja) | Radius | checkbox | — | `device` | ⚠️ | RADIUS built-in; tidak ada flag, pilih `device` = Radius (router tidak disentuh, server RADIUS yang autentikasi) |
| `device` | Device | select (driver di `system/devices`) | — | `device` | ⚠️ | Opsi: kosong/MikrotikHotspot/MikrotikPppoe/Dummy/Radius (Disconnect-Request ke NAS saat expired). RadiusRest tidak ada (RADIUS built-in) |
| `name` (H) / `name_plan` (P) | Package Name | text maxlength 40 | required, unik (`Name Plan Already Exist`) | `name` | ✅ | |
| `typebp` (H) | Package Type | radio Unlimited/Limited | — | `limited` | ⚠️ | checkbox 0/1 |
| `limit_type` (H) | Limit Type | radio Time_Limit/Data_Limit/Both_Limit | — | `limit_type` | ✅ | |
| `time_limit` (H) | Time Limit | text | — | `time_limit` | ✅ | validasi > 0 |
| `time_unit` (H) | Time Limit unit | select | Mins/Hrs | `time_unit` | ✅ | |
| `data_limit` (H) | Data Limit | text | — | `data_limit` | ✅ | |
| `data_unit` (H) | Data Limit unit | select | MB/GB | `data_unit` | ✅ | |
| `id_bw` | Bandwidth Name | select | required | `bandwidth_id` | ⚠️ | Nama beda; wajib kecuali Balance |
| `price` | Package Price | number | required, numerik | `price` | ✅ | INTEGER rupiah (lama `varchar(40)`) |
| `price_old` (edit) | Price Before Discount | number | — | — | ❌ | Tidak ada kolom |
| `sharedusers` (H) | Shared Users | text | — | `shared_users` | ✅ | |
| `validity` | Package Validity | text | required, numerik | `validity` | ✅ | > 0 |
| `validity_unit` | Validity unit | select | Mins/Hrs/Days/Months/Period | `validity_unit` | ✅ | |
| `expired_date` | Expired Date | number maxlength 2 | hanya Period | `billing_day` | ⚠️ | Nama beda; 1-31 wajib bila postpaid |
| `routers` | Router Name | select (add) / text (edit) | required | `router_id` | ⚠️ | FK ke `routers`, bukan nama; opsional bila `device` = Radius (NULL, router tidak disentuh) |
| `pool_name` (P) | IP Pool | select | required (P) | `pool_id` | ⚠️ | FK ke `pools`; opsional di baru |
| `plan_expired` (edit) | Expired Internet Package | select | — | `expired_plan_id` | ⚠️ | Nama beda; FK |
| `on_login` (edit) | On Login script | textarea | — | `on_login` | ✅ | Bagian Network & device |
| `on_logout` (edit) | On Logout script | textarea | — | `on_logout` | ✅ | idem |
| `id` (edit) | — | hidden | — | `{id}` di URL | ✅ | |

**Kolom daftar H:** Internet Package (Name), Limit, Expired, Name, Type, Bandwidth, Category, Price, Validity, Time, Data, Location (router), Device, Date, ID, Manage. **P:** Internet Plan, Expired, Name, Type, Bandwidth, Price, Validity, IP Pool, Date, Location, Device, ID, Manage.
- Baru (satu daftar): Plan Name, Type, Plan Price, Plan Validity, Router, Status.
- Belum: Bandwidth, Category, Time/Data limit, IP Pool, Device, Date.

**Aksi:**
- Filter lama: `name`, `type1` (prepaid/postpaid), `type2` (Personal/Business), `bandwidth`, `type3` (limit), `valid` (satuan), `router`, `device`, `status`. Baru: `q` saja.
- Tombol `sync` (`services/sync/hotspot|pppoe`): baru otomatis saat simpan/hapus lewat `syncPlan` (ke router), tanpa tombol manual.
- Edit/Delete per baris: ada. Tambah: ada.

### A18-A20. Paket Balance (`balance/*`)

| Field lama | Label | Tipe | Wajib/validasi PHP | Field baru | St | Catatan |
|---|---|---|---|---|---|---|
| `enabled` (add) | Status | radio 1/0 | — | `enabled` | ✅ | |
| `name` | Package Name | text maxlength 40 | required, unik | `name` | ✅ | `type=Balance` |
| `price` | Package Price | number | required, numerik | `price` | ✅ | |
| `price_old` (edit) | Price Before Discount | number | required | — | ❌ | Tidak ada kolom |
| `id` (edit) | — | hidden | — | `{id}` di URL | ✅ | |

Kolom daftar lama: Package Name, Package Price, Manage. Baru: bagian dari daftar plans (filter tipe belum ada). Aksi: "New Service Package", Edit, cari `name`.

### A21-A23. Paket VPN (`vpn/*`) dan A30-A32 Port (`port/*`)

Ditunda (progres: "port dan vpn (VPN ditunda)"; tidak ada driver `MikrotikVpn` di `plans.device` CHECK; tidak ada tabel port). Form VPN identik dengan PPPoE (`name_plan`, `id_bw`, `price`, `price_old`, `validity`, `validity_unit`, `expired_date`, `routers`, `pool_name`, `plan_expired`, `on_login`, `on_logout`, `enabled`, `prepaid`, `plan_type`, `device`). Form Port: `name`, `public_ip`, `port_range` (add) / `range_port` (edit), `routers`. Semua ❌ (Ditunda), tidak dihitung sebagai target saat ini.

### A24-A26. Bandwidth (`bandwidth/*`)

| Field lama | Label | Tipe | Wajib/validasi PHP | Field baru | St | Catatan |
|---|---|---|---|---|---|---|
| `name` | Bandwidth Name | text | 1-255, unik | `name` | ✅ | |
| `rate_down` | Rate Download | text | numerik | `rate_down` | ✅ | > 0 |
| `rate_down_unit` | unit | select | Kbps/Mbps | `rate_down_unit` | ✅ | |
| `rate_up` | Rate Upload | text | numerik | `rate_up` | ✅ | > 0 |
| `rate_up_unit` | unit | select | Kbps/Mbps | `rate_up_unit` | ✅ | |
| `burst[]` (5 input: Burst Limit, Burst Threshold, Burst Time, Priority, Limit At) | Burst | text/number | digabung jadi string | `burst` | ⚠️ | Satu text bebas "MikroTik burst limit"; lima input terpisah hilang. Perlu diputuskan: pecah lagi atau tetap string |
| `id` (edit) | — | hidden | — | `{id}` di URL | ✅ | |

Kolom daftar lama: Bandwidth Name, Rate, Burst, Manage. Baru: Name, Download, Upload, Burst (ada). Aksi: cari `name` (baru `q`), New Bandwidth, Edit, Delete (ada).

### A27-A29. IP Pool (`pool/*`)

| Field lama | Label | Tipe | Wajib/validasi PHP | Field baru | St | Catatan |
|---|---|---|---|---|---|---|
| `name` | Name Pool | text | 3-30, unik | `name` | ✅ | unik per router (`UNIQUE (router_id, name)`) |
| `local_ip` | Local IP | text | — | `local_ip` | ✅ | |
| `ip_address` | Range IP | text | required | `range_ip` | ⚠️ | Nama beda |
| `routers` | Routers | select (add) / text (edit) | required | `router_id` | ⚠️ | FK, wajib |

Kolom lama: Name Pool, Local IP, Range IP, Routers, Manage, ID. Baru: Pool Name, Local IP, IP Range, Router (sama). Aksi: Sync (lama manual; baru otomatis lewat `syncPool` saat simpan), New Pool, Edit, Delete, cari.

### A33-A35. Router (`routers/*`)

| Field lama | Label | Tipe | Wajib/validasi PHP | Field baru | St | Catatan |
|---|---|---|---|---|---|---|
| `name` | Router Name / Location | text maxlength 32 | 1-30 (add), 5-30 (edit), unik, nama `Radius` reserved | `name` | ⚠️ | Unik; tanpa batas panjang; `Radius` reserved belum dicek |
| `ip_address` | IP Address | text | required, unik | `host` + `port` | ⚠️ | Dipecah jadi host dan `port` (default 8728, 1-65535). Unik host belum |
| `username` | Username | text | required | `username` | ✅ | |
| `password` | Router Secret | text (add) / password (edit) | required | `password` -> `password_enc` | ⚠️ | Disengaja: AES-GCM, tidak dirender balik; kosong saat edit = tetap |
| `description` | Description | textarea | — | `description` | ⚠️ | Input satu baris |
| `coordinates` (edit) | Coordinates | text (peta) | — | — | ✅ | Kolom `coordinates` (lat,lng), migrasi 0005 |
| `coverage` (edit) | Coverage | number | — | — | ✅ | Kolom `coverage` (meter), migrasi 0005 |
| `testIt` (add) | Test Connection | checkbox (default checked, `yes`) | tes koneksi sebelum simpan | `POST /admin/routers/{id}/test` | ⚠️ | Tes jadi tombol baris terpisah setelah simpan, bukan checkbox di form add |
| (list "Status" Enabled/Disabled) | — | — | — | `enabled` | ✅ | Checkbox di baru |
| `id` (edit) | — | hidden | — | `{id}` di URL | ✅ | |

Kolom lama: Router Name, IP Address, Username, Description, Online Status, Last Seen, Status, Manage, ID. Baru: Name, Host, Username, Enabled.
- Belum: Description. Online Status dan Last Seen ada (kolom Status dan Last Seen; job `router_check` tiap 5 menit lewat `billing.Service.Ping`, peringatan di dashboard, alert Telegram sekali per perubahan status). Kunci `router_check` = no mematikan job; kosong = aktif.
- Aksi: "Cek Now" (link ke miscellaneous#router_check) -> baru tombol baris "Test connection" (`POST /admin/routers/{id}/test`). Tambah, Edit, Delete, cari ada.

### A36-A38. NAS RADIUS (`radius/nas*`)

| Field lama | Label | Tipe | Wajib/validasi PHP | Field baru | St | Catatan |
|---|---|---|---|---|---|---|
| `shortname` | Router Name | text maxlength 32 | required, 3-30 | `name` | ⚠️ | Nama beda; unik |
| `nasname` | IP Address | text maxlength 128 | required, unik (`NAS IP Exists`) | `ip` | ⚠️ | Divalidasi IP/CIDR, unik |
| `secret` | Secret | password maxlength 60 | required | `secret` -> `secret_enc` | ⚠️ | Disengaja: AES-GCM; kosong saat edit = tetap |
| `ports` | Ports | text | — | — | ❌ | Tidak perlu: tabel NAS FreeRADIUS lama; RADIUS built-in tidak memakai |
| `type` | Type | text | required | — | ❌ | idem (NAS-type `other`) |
| `server` | Server | text | — | — | ❌ | idem |
| `community` | Community | text | — | — | ❌ | idem (SNMP) |
| `description` | Description | textarea | — | `description` | ✅ | |
| `routers` | Routers | select | — | — | ❌ | Tidak ada relasi NAS-router di schema |

Kolom lama: Name, IP, Type, Port, Server, Community, Routers, Manage, ID. Baru: Name, IP / CIDR, Description. Aksi: New NAS, Edit, Delete, cari `name` (baru tanpa cari: `Searchable` tidak diset).

### A39-A42. Voucher

**A40 `voucher/add.tpl` (baru: `vchFields` di `vouchers.go`)**

| Field lama | Label | Tipe | Wajib/validasi PHP | Field baru | St | Catatan |
|---|---|---|---|---|---|---|
| `type` | Type | radio Hotspot/PPPOE | required | — | ⚠️ | Tipe mengikuti paket yang dipilih |
| `server` | Routers | select | required | — | ⚠️ | Router mengikuti paket |
| `plan` | Service Plan | select | required | `plan` | ✅ | |
| `numbervoucher` | Number of Vouchers | text | numerik | `numbervoucher` | ✅ | 1..500 (`maxVouchers`) |
| `voucher_format` | Voucher Format | select numbers/up/low/rand | required | `voucher_format` | ✅ | |
| `prefix` | Voucher Prefix | text | — | `prefix` | ✅ | |
| `lengthcode` | Length Code | text | numerik; numbers > 6 | `lengthcode` | ✅ | 4-32, numbers 6-32. Kode `crypto/rand` tanpa karakter mirip (disengaja, bukan md5) |
| `print_now` | Print Now | checkbox | — | `print_now` | ✅ | |
| `voucher_per_page` | Vouchers Per Page | text | — | — | ❌ | Print baru pakai `limit` query (grid 3 kolom) |

**Kolom daftar A39:** ID, Type, Routers, Plan Name, Code Voucher, Status Voucher, Customer, Create Date, Used Date, Generated By, Manage, checkbox. Baru: Code Voucher, Plan Name, Status, Created, Used. Belum: Type, Routers, Customer (`used_by`), Generated By (`generated_by` ada di schema).

**Aksi:** filter lama `search`, `router`, `plan`, `status`, `customer`; baru: `q` + `status`. Tombol lama: Vouchers (add), Print, "Delete > 3 Months" (`remove-voucher`), View per baris, Delete Selected. Baru: Add, Redeem Voucher, Print, Delete per baris. Belum: View, bulk delete, hapus > 3 bulan.

**A41 `voucher/view.tpl`** (setelah generate: textarea `content`, `id`, tombol Print, WhatsApp, NuxPrint, Finish): ada. Setelah generate masuk ke `GET /admin/vouchers/view` (textarea kode, Print, Finish); `print_now` langsung ke print. Belum: WhatsApp, NuxPrint.

**A42 `print/voucher.tpl`**

| Field lama | Label | Tipe | Wajib | Field baru | St | Catatan |
|---|---|---|---|---|---|---|
| `from_id` | Start ID | text | — | — | ❌ | |
| `limit` | Limit | text | — | `limit` (query) | ✅ | |
| `vpl` | Vouchers per page | text | — | — | ❌ | Grid CSS tetap 3 kolom |
| `pagebreak` | Page break | text | — | — | ❌ | `break-inside: avoid` CSS |
| `planid` | Plan | select | — | `plan` (query) | ✅ | |
| `selected_datetime` | Date | select | — | — | ❌ | |

QR code di voucher cetak baru (`print.html`, `.QR`) = fitur tambahan.

### A43-A50. Recharge, aktivasi, deposit, langganan, invoice

**A43 `rencana/recharge.tpl` (+ A44 `recharge-confirm.tpl`)**

| Field lama | Label | Tipe | Wajib/validasi PHP | Field baru | St | Catatan |
|---|---|---|---|---|---|---|
| `id_customer` | Select Account | select | required | `{id}` di URL (dari halaman view) | ⚠️ | Pelanggan dipilih dari halaman view, tidak ada pemilih |
| `server` | Routers | select (AJAX) | required | — | ⚠️ | Router mengikuti paket |
| `plan` | Service Plan | select (AJAX) | required | `plan` | ✅ | hanya paket `enabled` |
| `using` | Using | select (`payment_usings`, mis. cash/transfer; `balance`; `zero`) | required | `method` Cash/Balance | ⚠️ | Hanya dua metode. `zero` dan metode kustom belum |
| `stoken` | — | hidden | token anti-ganda | — | ⚠️ | Diganti CSRF stdlib |
| (halaman konfirmasi: ringkasan paket, harga, kupon) | — | — | — | `recharge/confirm` | ✅ | A44: pelanggan, paket, harga, kedaluwarsa baru (`billing.NewExpiry`), saldo sesudah; tidak menulis data. Pajak belum |

**A45 `rencana/refill.tpl`** (baru: `GET/POST /admin/vouchers/redeem`)

| Field lama | Label | Tipe | Wajib | Field baru | St | Catatan |
|---|---|---|---|---|---|---|
| `id_customer` | Select Account | select | required | `customer` | ⚠️ | Baru: input username (teks), prefill `?customer=` |
| `code` | Code Voucher | text | required | `code` | ✅ | |

**A46 `rencana/deposit.tpl`** (baru: `GET/POST /admin/deposit`, `billing.Service.Deposit`; staff)

| Field lama | Label | Tipe | Wajib | Field baru | St | Catatan |
|---|---|---|---|---|---|---|
| `id_customer` | Select Account | select | required | `customer` | ⚠️ | Input username (teks), prefill `?customer=`, seperti redeem voucher |
| `id_plan` | Balance Package | select | required | `plan` | ✅ | Paket tipe Balance yang aktif; kosong = pakai jumlah |
| `amount` | Balance Amount | number | — | `amount` | ✅ | Dipakai bila paket kosong |
| `note` | Note | textarea | — | `transactions.note` | ✅ | |
| `stoken` | — | hidden | — | — | ⚠️ | Diganti CSRF stdlib |

Menulis transaksi (tipe Balance, metode `Admin - Deposit`), menaikkan saldo, activity log, dan mengirim notifikasi recharge. Recharge paket Balance (A43, voucher) kini juga mengirim notifikasi.

**A48 `rencana/edit.tpl`** (baru: `subscriptions.go`, `billing.Service.EditSubscription`; managers)

| Field lama | Label | Tipe | Wajib | Field baru | St | Catatan |
|---|---|---|---|---|---|---|
| `username` | Select Account | text (readonly) | — | judul form | ⚠️ | Tampil di judul, bukan field |
| `id_plan` | Service Plan | select | required | `plan` | ✅ | Paket non-Balance; ganti paket = hapus profil lama dari router lalu tambah yang baru |
| `expiration` + `time` | Expires On | date + time | required | `expires_at` | ⚠️ | Satu input `datetime-local` (zona waktu billing), disimpan UTC; status mengikuti (lewat = expired) |
| `id` | — | hidden | — | `{id}` di URL | ⚠️ | Activity log `subscription.update` |

**A47 `rencana/active.tpl`** (baru: `GET /admin/subscriptions`) — Kolom: Username, Plan Name, Type, Created On, Expires On, Method, Location (router), Status; filter `q`, `status`, `type`, `router`; paging. Aksi: Edit (A48), Extend N hari (`POST .../extend`, input `days`), Deactivate (kedaluwarsa sekarang + `RemoveCustomer`, idempoten, `POST .../deactivate`). Sync (`POST .../sync`), CSV (`GET /admin/subscriptions/export`), dan filter `plan` sudah ada. Belum: Delete.

**A49 `rencana/invoice.tpl` / A50 `invoice-print.tpl`:** tampilan invoice (textarea `content`, tombol Finish, Download, WhatsApp, Resend, Print HTML, Print Text, NuxPrint). Sekarang: invoice cetak (perusahaan, alamat, pelanggan, paket, periode, harga, metode, footer `note`), ditautkan dari daftar transaksi dan detail pelanggan. Belum: Resend, WhatsApp, NuxPrint, versi teks.

### A51-A56. Laporan dan transaksi

**A51 `reports/activation.tpl` -> `GET /admin/transactions`**

| Field lama | Label | Tipe | Field baru | St | Catatan |
|---|---|---|---|---|---|
| `q` | Search | text | `q` | ✅ | |

Kolom lama: Invoice, Username, Plan Name, Plan Price, Type, Created On, Expires On, Method. Baru: Invoice, Date, Username, Plan Name, Type, Method, Plan Price. Belum: Expires On (ada `period_end` di schema). Aksi: tidak ada filter tanggal.

**A52 `reports/list.tpl` (harian/by-date)**

| Field lama | Label | Tipe | Field baru | St | Catatan |
|---|---|---|---|---|---|
| `sd`, `ts` | Start Date, Start time | date, time | `date` / `from` | ⚠️ | Tanggal saja, batas hari = tengah malam zona app |
| `ed`, `te` | End Date, End Time | date, time | `to` | ⚠️ | Inklusif, tanpa jam |
| `tps[]` | Type | multi-select | `type` | ⚠️ | Satu nilai |
| `plns[]` | Internet Plans | multi-select | `plan` | ⚠️ | Satu nilai |
| `mts[]` | Methods | multi-select | `method` | ⚠️ | Teks, cocok persis |
| `rts[]` | Routers | multi-select | `router` | ⚠️ | Satu nilai; total per tipe dan metode di atas tabel |

Kolom: Username, Type, Plan Name, Plan Price, Created On, Expires On, Method, Routers, Total. Aksi: Show chart, Export (CSV; PDF diganti CSV + print HTML, 01-audit).

**A53/A54 `reports/period*.tpl`:** `from`, `to` (date) + filter yang sama dengan A52 -> ✅. Aksi: "Period Reports", "Export for Print", "Export to PDF" (PDF diganti print HTML, disengaja). Kolom sama dengan A52 tanpa Total.

**A55/A56 `print/by-date.tpl`, `by-period.tpl`:** tabel cetak (Username, Plan Name, Type, Plan Price, Created On, Expires On, Method, Routers) + "Click Here to Print". Belum.

### A57-A59. Log

**A57 `logs/system.tpl` -> `GET /admin/logs`**

| Field lama | Label | Tipe | Field baru | St | Catatan |
|---|---|---|---|---|---|
| `q` | Search | text | `q` | ✅ | |
| `keep` | Keep logs (hari) | text | — | `keep` (`POST /admin/logs/clean/activity`) | ✅ | Hanya SuperAdmin/Admin. Auto-bersih harian lewat setting `log_keep_days` (0 = simpan selamanya; tanpa input di Settings) |

Kolom lama (tanpa `<th>`): ID, Date, Type, IP, Description. Baru: Date, Actor, Action, Description, IP (Actor/Action menggantikan Type/User ID). Aksi: CSV (`logs/list-csv`) belum; Clean up ada.

**A58 `logs/radius.tpl`:** `GET /admin/logs/radius` memuat semua sesi `radius_sessions` (terbuka dan selesai): User, NAS, IP, MAC, Start, Stop, Duration, Upload, Download; cari `q` (username/NAS), `from`/`to` (tanggal mulai), paging, CSV (`/export`, dengan penjaga formula). Clean Logs (`keep`, `POST /admin/logs/clean/radius`) hanya menghapus sesi TERTUTUP yang lebih lama dari N hari; sesi terbuka tidak pernah dihapus. Sesi terbuka juga di A58b `/admin/radius/sessions`; kartu "RADIUS usage" di detail pelanggan. **A59 `logs/message.tpl`:** `GET /admin/logs/messages` (tabel `message_logs`, diisi `internal/notify` lewat hook `Log` di tiap percobaan kirim Telegram/SMS/WA/Email): Date, Type, Recipient, Subject, Status, Message (galat bila gagal); cari, tanggal, paging, CSV. Clean Logs (`keep`, `POST /admin/logs/clean/messages`) ada.

### A60-A62. Kupon

**A61 `coupons/add.tpl` / A62 edit**

| Field lama | Label | Tipe | Wajib/validasi PHP | Field baru | St | Catatan |
|---|---|---|---|---|---|---|
| `code` | Coupon Code | text maxlength 50 (+ tombol Random) | required, unik | `code` (kosong = acak crypto/rand) | ✅ | `coupons` (migrasi 0004); nilai bulat rupiah/persen |
| `type` | Type | select fixed/percent | required | ada | ✅ | `coupons` (migrasi 0004); nilai bulat rupiah/persen |
| `value` | Discount Value | number | required | ada | ✅ | `coupons` (migrasi 0004); nilai bulat rupiah/persen |
| `description` | Description | textarea | required | ada | ✅ | `coupons` (migrasi 0004); nilai bulat rupiah/persen |
| `max_usage` | Max Usage | number | required, >= 0 | ada | ✅ | `coupons` (migrasi 0004); nilai bulat rupiah/persen |
| `min_order_amount` | Minimum Order Amount | number | required | ada | ✅ | `coupons` (migrasi 0004); nilai bulat rupiah/persen |
| `max_discount_amount` | Max Discount Amount | number | required | ada | ✅ | `coupons` (migrasi 0004); nilai bulat rupiah/persen |
| `start_date` | Start Date | date | required | ada | ✅ | `coupons` (migrasi 0004); nilai bulat rupiah/persen |
| `end_date` | End Date | date | required | ada | ✅ | `coupons` (migrasi 0004); nilai bulat rupiah/persen |
| `status` (diperlukan controller, diubah lewat Block/Unblock) | Status | — | required | ada | ✅ | `coupons` (migrasi 0004); nilai bulat rupiah/persen |

Daftar: Code, Type, Value, Max Usage, Used, Min Order, Start/End Date, Status; aksi cari, Add, Edit, Block/Unblock, Delete (tanpa bulk delete, tanpa kolom Created/Updated). Peran: SuperAdmin/Admin/Sales.

### A63-A64. Pesan

**A63 `message/single.tpl`**

| Field lama | Label | Tipe | Wajib | Field baru | St | Catatan |
|---|---|---|---|---|---|---|
| `id_customer` | Customer | select | required | `customer_id` | ✅ | pilihan 500 pelanggan terbaru; belum ada pencarian |
| `via` | Send Via | select (sms/wa/inbox/email/all) | required | `channel` | ✅ | sms/wa/email/inbox; `all` belum. Gateway belum diset = error, bukan diam |
| `message` | Message | textarea | required (`All field is required`) | `message` | ✅ | placeholder `[[name]]`, `[[user_name]]`, `[[phone]]`, `[[company_name]]`; `[[payment_link]]` belum |

**A64 `message/bulk.tpl`**

| Field lama | Label | Tipe | Wajib | Field baru | St | Catatan |
|---|---|---|---|---|---|---|
| `router` | Router | select | — | `router` | ✅ | semua router atau satu router |
| `service` | Service Type | select | — | `service` | ✅ | all/PPPoE/Hotspot/VPN |
| `group` | Group | select all/new/expired/active | — | `status` | ✅ | diganti status langganan all/active/expired; `new` belum |
| `via` | Send Via | select sms/wa/both | — | `channel` | ✅ | sms/wa/email/inbox; `both` belum |
| `batch` | Message per time | select | — | — | ❌ | tidak dipakai; kirim satu per satu dengan jeda `message_delay` (detik, default 1) |
| `message` | Message | textarea | required | `message` | ✅ | placeholder sama dengan A63 |
| `test` | Test mode | checkbox | — | — | ❌ | belum |
| `page` | — | hidden | — | — | ❌ | tidak perlu; kirim di goroutine latar |

Kolom hasil: Customer, Phone, Status, Message, Router, Service Type. Baru: halaman status `/admin/message/bulk/status` (Customer, Phone, Status; progres dalam memori, reset saat restart). Belum: kolom Message dan Router/Service Type per baris, Test mode, Start AJAX (diganti POST biasa).

### A65-A67. ODP dan A68-A70 Peta

**A66/A67 `odp/add|edit.tpl`:** `name` (5-15 karakter, unik), `port_amount` (number max 16), `attenuation` (text), `address` (textarea), `coordinates` (text), `coverage` (number, meter), `id` (edit). Semua ❌ (tidak ada tabel `odp`). Kolom daftar: Name, Port Amount, Attenuation, Address, Coverage, Coordinates, Action, ID. Aksi: cari `name`, New odp, Edit, Delete.

**A68-A70 `maps/*.tpl`:** `_route` (hidden) + `search` (customers) atau `name` (routers/odps) -> peta Leaflet. Semua ❌; membutuhkan kolom `coordinates` di customers/routers (juga ❌) dan tabel ODP.

### A71-A73. Payment gateway

| Elemen lama | Elemen baru | St | Catatan |
|---|---|---|---|
| `pgs[]` (checkbox gateway aktif) + tombol Save Changes, link Audit per gateway | — | ⚠️ | Daftar hanya Tripay + status Enabled/Disabled (diatur di Settings > Payment Gateway, `payment_gateway`), bukan checkbox `pgs[]` |
| `audit.tpl`: `q`; kolom TRX ID, PG ID, Username, Plan Name, Routers, Price, Payment Link, Channel, Created, Expired, Paid, Invoice, Status; tombol open, back | `/admin/payment-gateway/audit` | ✅ | Tabel `payment_requests` (0009): ref, gateway_ref, pay_url, status, expires_at. Filter `q`, `status`, tanggal, CSV. Tanpa kolom Routers/Invoice |
| `audit-view.tpl`: detail satu transaksi PG, tombol open | `/admin/payment-gateway/audit/{id}` | ✅ | Daftar bidang baca-saja |

### A74-A76. Halaman sistem

- **A74 `maintenance.tpl`:** halaman statis dengan tanggal berakhir (`maintenance_date`). Belum.
- **A75 `404/error/alert`:** baru memakai `http.Error` teks polos dan flash di `app.html` (`.Flash`); halaman 404 dan error yang rapi belum (progres "Admin, belum ada").
- **A76 `community/rollback`:** Non-goal.

---

## 3. Bagian Settings

Baru: sub-halaman `GET/POST /admin/settings/{tab}` (`internal/web/settings.go`): app, localisation, notifications, integrations, miscellaneous. Key di tabel `settings (key, value)`. Integrations hanya SuperAdmin; sisanya SuperAdmin/Admin. Payment ditunda (Tripay di akhir). Tabel di bawah dikelompokkan per sub-halaman lama. Semua key lama disimpan di `tbl_appconfig` (`setting`, `value`); di baru `settings.key`.

Konvensi kolom: `Key lama` = atribut `name`; `Fase` = kapan dibutuhkan.

### S1. `settings/app` (satu halaman panjang, kartu per grup)

**Umum**

| Key lama | Label | Tipe | Wajib | Key baru | St | Catatan / Fase |
|---|---|---|---|---|---|---|
| `CompanyName` | Application Name / Company Name | text | required | `company_name` | ⚠️ | Nama key beda |
| `logo` | Company Logo | file | — | `logo` | ⚠️ | F5. Simpan dan validasi ada (PNG/JPG/WebP/ICO, maks 2 MB, nama file acak di `<db>/uploads`). Belum: input file belum di `form.html` (perlu enctype), logo belum tampil di sidebar |
| `CompanyFooter` | Company Footer | text | — | `company_footer` | ✅ | F5 |
| `address` | Address | textarea | — | `address` | ✅ | F5 (dipakai invoice) |
| `phone` | Phone Number | text | — | `phone` | ✅ | F5 |
| `note` | Invoice Footer | textarea | — | `note` | ✅ | F5 (invoice) |
| `printer_cols` | Print Max Char | number | required | — | ❌ | Ditunda (printer thermal NuxPrint) |
| `theme` | Theme | select | — | toggle terang/gelap di header | ⚠️ | Disengaja: tema AdminLTE diganti Tailwind + mode gelap per pengguna |
| `payment_usings` | Recharge Using | text | — | — | ❌ | F1: daftar metode bayar admin (baru hanya Cash/Balance) |
| `reset_day` | Income reset date | number min 1 | — | `reset_day` | ⚠️ | F5. Opsional, 1-28; dashboard tetap memakai bulan kalender |
| `dashboard_cr` | Dashboard Structure | text | — | — | ❌ | Non-goal (widget tetap) |
| `url_canonical` | Pretty URL | select | — | — | ❌ | Non-goal (route tetap `/admin/...`) |

**Login page**

| Key lama | Label | Tipe | Key baru | St | Catatan / Fase |
|---|---|---|---|---|---|
| `login_page_type` | Choose Template | select | — | ❌ | F4 (portal) |
| `login_Page_template` | Select Login Page | select | — | ❌ | Non-goal (tema "moon" dinamis) |
| `login_page_head` | Page Heading / Company Name | text | `login_page_head` | ⚠️ | F4. Tersimpan; belum tampil di halaman login |
| `login_page_description` | Page Description | textarea | `login_page_description` | ⚠️ | F4. Tersimpan; belum tampil di halaman login |
| `login_page_favicon` | Favicon | file | `login_page_favicon` | ⚠️ | F4. Simpan dan validasi ada; belum dipasang di `base.html` |
| `login_page_logo` | Login Page Logo | file | `login_page_logo` | ⚠️ | F4. Simpan dan validasi ada; belum tampil di login |
| `login_page_wallpaper` | Login Page Wallpaper | file | `login_page_wallpaper` | ⚠️ | F4. Simpan dan validasi ada; belum tampil di login |

**Registrasi**

| Key lama | Label | Tipe | Key baru | St | Catatan / Fase |
|---|---|---|---|---|---|
| `disable_registration` | Allow Registration | select | `disable_registration` | ⚠️ | F4. Settings saja (Miscellaneous > Registration); portal belum membaca |
| `registration_username` | Registration Username | select | `registration_username` | ⚠️ | F4. Settings saja; portal belum membaca |
| `photo_register` | Photo Required | select | — | ❌ | Ditunda (foto/face detection) |
| `sms_otp_registration` | SMS OTP Registration | select | `sms_otp_registration` | ⚠️ | F4. Settings saja; portal belum membaca |
| `phone_otp_type` | OTP Method | select | `phone_otp_type` | ⚠️ | F4. Settings saja, sms/wa; portal belum membaca |
| `reg_nofify_admin` | Notify Admin | select | `reg_nofify_admin` | ⚠️ | F4. Settings saja; belum ada pengirim |
| `man_fields_email` | Mandatory field: Email | checkbox `yes` | `man_fields_email` | ✅ | F4 (field wajib di registrasi) |
| `man_fields_fname` | Mandatory field: Full Name | checkbox `yes` | `man_fields_fname` | ✅ | F4 |
| `man_fields_address` | Mandatory field: Address | checkbox `yes` | `man_fields_address` | ✅ | F4 |

**Keamanan**

| Key lama | Label | Tipe | Key baru | St | Catatan / Fase |
|---|---|---|---|---|---|
| `session_timeout_duration` | Timeout Duration | number min 1 | `session_timeout_duration` | ⚠️ | F5. Settings saja (menit, 1 atau lebih); middleware auth dipasang router agent |
| `single_session` | Single Admin Session | select | `single_session` | ⚠️ | F5. Settings saja; middleware auth dipasang router agent |
| `csrf_enabled` | Enable CSRF Validation | select | — | ⚠️ | Disengaja hilang: CSRF selalu aktif (`CrossOriginProtection`) |
| `enable_session_timeout` | Enable Session Timeout | checkbox `1` | `enable_session_timeout` | ✅ | F5 (bersama `session_timeout_duration`) |

**Voucher, RADIUS, extend, balance**

| Key lama | Label | Tipe | Key baru | St | Catatan / Fase |
|---|---|---|---|---|---|
| `disable_voucher` | Disable Voucher | select | — | ✅ | Input di Miscellaneous > Portal (`yes`/`no`); portal membaca |
| `voucher_format` | Voucher Format (default) | select | `voucher_format` | ⚠️ | F5. Tersimpan dan divalidasi; form generate belum membaca nilai default ini |
| `voucher_redirect` | Redirect URL after Activation | text | — | ✅ | Input di Miscellaneous > Portal; http/https atau kosong, portal membaca |
| `radius_enable` | Enable Radius | select | — | ⚠️ | Disengaja: RADIUS built-in, aktif jika server dijalankan dan NAS ada |
| `extend_expired` | Allow Extend | select | — | ✅ | Input di Miscellaneous > Extend (`1`/`0`); portal membaca |
| `extend_days` | Extend Days | number | — | ✅ | Input di Miscellaneous > Extend; bilangan bulat 0 atau lebih |
| `extend_confirmation` | Confirmation Message | textarea | — | ✅ | Input di Miscellaneous > Extend |
| `enable_balance` | Enable System (saldo) | select | `enable_balance` | ✅ | Dibaca `internal/billing/service.go:290` (default aktif), tapi tidak ada input di UI. Keputusan default menunggu pengguna |
| `allow_balance_transfer` | Allow Transfer | select | — | ✅ | Input di Miscellaneous > Balance; portal membaca |
| `minimum_transfer` | Minimum Balance Transfer | number | — | ✅ | Input di Miscellaneous > Balance; bilangan bulat 0 atau lebih |
| `allow_balance_custom` | Allow Balance Custom Amount | select | — | ✅ | Input di Miscellaneous > Balance; portal `/portal/topup` (hanya dengan Tripay) |

**Notifikasi kanal**

| Key lama | Label | Tipe | Key baru | St | Catatan / Fase |
|---|---|---|---|---|---|
| `telegram_bot` | Telegram Bot Token | password | `telegram_bot` | ✅ | F4; `internal/notify` ada, halaman setting belum |
| `telegram_target_id` | Telegram User/Channel/Group ID | text | `telegram_target_id` | ✅ | F4 |
| `sms_url` | SMS Server URL | text | `sms_url` | ✅ | F4 |
| `mikrotik_sms_command` | Mikrotik SMS Command | text | — | ❌ | F4 |
| `wa_url` | WhatsApp Server URL | text | `wa_url` | ✅ | F4 |
| `smtp_host` | SMTP Host | text | `smtp_host` | ✅ | F4 |
| `smtp_port` | SMTP Port | number | `smtp_port` | ✅ | F4 |
| `smtp_user` | SMTP Username | text | `smtp_user` | ✅ | F4 |
| `smtp_pass` | SMTP Password | password | `smtp_pass` | ✅ | F4 (simpan terenkripsi `internal/secret`) |
| `smtp_ssltls` | SMTP Security | select | `smtp_ssltls` | ✅ | F4 |
| `mail_from` | Mail From | text | `mail_from` | ✅ | F4 |
| `mail_reply_to` | Mail Reply To | text | `mail_reply_to` | ⚠️ | F4. Tersimpan (Integrations > Email); SMTP belum memakai |
| `user_notification_expired` | Expired Notification | select | `user_notification_expired` | ✅ | F4 |
| `user_notification_payment` | Payment Notification | select | `user_notification_payment` | ✅ | F4 |
| `user_notification_reminder` | Reminder Notification | select | `user_notification_reminder` | ✅ | F4 |
| (baru) | Webhook keluar HMAC | text, password | `webhook_url`, `webhook_secret` | ✅ | Baru di Settings > Integrations; tanda tangan X-Signature |
| (baru) | Tripay key/merchant/mode/channel | select, text, password | `payment_gateway`, `tripay_api_key`, `tripay_private_key`, `tripay_merchant_code`, `tripay_mode`, `tripay_channel` | ✅ | Settings > Payment Gateway (hanya SuperAdmin); rahasia tidak ditampilkan lagi; URL callback `/callback/tripay` ditampilkan |

**Lain-lain**

| Key lama | Label | Tipe | Key baru | St | Catatan / Fase |
|---|---|---|---|---|---|
| `tawkto`, `tawkto_api_key` | Tawk.to | text | — | ❌ | Ditunda |
| `api_key` | Access Token | password | — | ❌ | Ditunda (API `system/api.php`, token tidak bisa di-revoke) |
| `http_proxy`, `http_proxyauth` | Proxy Server / Login | text, password | — | ❌ | Ditunda |
| `enable_tax` | Enable Tax System | select | — | ❌ | Ditunda (belum ada kolom pajak di `transactions`) |
| `tax_rate` | Tax Rate | select | — | ❌ | Ditunda |
| `custom_tax_rate` | Custom Tax Rate | text | — | ❌ | Ditunda |
| `github_username`, `github_token` | Github | text, password | — | ❌ | Non-goal (updater/plugin) |

### S2. `settings/localisation`

| Key lama | Label | Tipe | Wajib | Key baru | St | Catatan / Fase |
|---|---|---|---|---|---|---|
| `tzone` | Timezone | select | required | `timezone` | ⚠️ | Divalidasi `time.LoadLocation`; saat ini hanya dibaca saat start (progres item 2) |
| `date_format` | Date Format | select | required | `date_format` | ✅ | F5 |
| `lan` | Default Language | select | required | `language` | ⚠️ | Nama beda; opsi dari katalog JSON |
| `dec_point` | Decimal Point | text | required | `dec_point` | ⚠️ | F5. Tersimpan; `money` belum memakai (rupiah tanpa desimal) |
| `thousands_sep` | Thousands Separator | text | required | `thousands_sep` | ✅ | F5. Dibaca helper `money` (default "."), berlaku setelah simpan |
| `currency_code` | Currency Code | text | required | `currency_code` | ✅ | Default "Rp" |
| `country_code_phone` | Country Code Phone | text | — | `country_code_phone` | ✅ | F4 (normalisasi nomor) |
| `radius_plan` | Radius Package (label menu) | text | — | — | ❌ | Non-goal |
| `hotspot_plan` | Hotspot Package (label) | text | — | — | ❌ | Non-goal |
| `pppoe_plan` | PPPOE Package (label) | text | — | — | ❌ | Non-goal |
| `vpn_plan` | VPN Package (label) | text | — | — | ❌ | Ditunda |

### S3. `settings/notifications` (template pesan)

| Key lama | Label | Tipe | Key baru | St | Catatan / Fase |
|---|---|---|---|---|---|
| `expired` | Expired Notification Message | textarea | `notif_expired` | ✅ | F4 |
| `reminder_7_day` | Reminder 7 days | textarea | `notif_reminder_7_day` | ✅ | F4 (job reminder H-7) |
| `reminder_3_day` | Reminder 3 days | textarea | `notif_reminder_3_day` | ✅ | F4 |
| `reminder_1_day` | Reminder 1 day | textarea | `notif_reminder_1_day` | ✅ | F4 |
| `invoice_paid` | Invoice Notification Payment | textarea | `notif_invoice_paid` | ✅ | F4 |
| `notification_reminder_7day` | Send 7-day reminder | select yes/no | `notification_reminder_7day` | ✅ | Baru: `no` mematikan pengingat 7 hari (dibaca `internal/notify`) |
| `notification_reminder_3day` | Send 3-day reminder | select yes/no | `notification_reminder_3day` | ✅ | Baru: `no` mematikan pengingat 3 hari |
| `notification_reminder_1day` | Send 1-day reminder | select yes/no | `notification_reminder_1day` | ✅ | Baru: `no` mematikan pengingat 1 hari |
| `reminder_hour` | Reminder Hour | number 0-23 | `reminder_hour` | ✅ | Baru: jam kirim pengingat, default 7 |
| `invoice_balance` | Balance Notification Payment | textarea | `notif_invoice_balance` | ✅ | F4. Template `notif_*` di Settings > Notifications |
| `welcome_message` | Welcome Message | textarea | `notif_welcome_message` | ✅ | F4. Template `notif_*` |
| `balance_send` | Send Balance | textarea | `notif_balance_send` | ✅ | F4. Template `notif_*` |
| `balance_received` | Received Balance | textarea | `notif_balance_received` | ✅ | F4. Template `notif_*` |
| `email_invoice` | PDF Invoice Template | textarea | — | ❌ | F5 (PDF diganti HTML cetak) |

### S4. `settings/miscellaneous`

| Key lama | Label | Tipe | Key baru | St | Catatan / Fase |
|---|---|---|---|---|---|
| `new_version_notify` | New Version Notification | select | — | ❌ | Non-goal (updater) |
| `router_check` | Router Check | select | `router_check` | ✅ | Dibaca job router (`no` mematikan, kosong = aktif); input di Miscellaneous > System |
| `allow_phone_otp` | Phone OTP Required | select | — | ✅ | Input di Miscellaneous > OTP; portal membaca (`yes`) |
| `phone_otp_type` | OTP Method | select | `phone_otp_type` | ⚠️ | F4. Settings saja, sms/wa; portal belum membaca |
| `allow_email_otp` | Email OTP Required | select | — | ✅ | Input di Miscellaneous > OTP; portal membaca (`yes`) |
| `show_bandwidth_plan` | Show Bandwidth Plan | select | — | ✅ | Input di Miscellaneous > Portal; portal membaca (`yes`) |
| `hs_auth_method` | Hotspot Auth Method | select | `hs_auth_method` | ⚠️ | Input di Miscellaneous > Hotspot (PAP/CHAP, nilai `pap`/`chap`). Belum ada pembaca: RADIUS built-in menerima PAP, CHAP dan MS-CHAPv2 tanpa setting ini |
| `frrest_interim_update` | Radius Rest Interim-Update | number | — | ❌ | Non-goal (FreeRADIUS REST diganti RADIUS built-in) |
| `check_customer_online` | Check if Customer Online | select | `check_customer_online` | ✅ | F2. Input di Miscellaneous > System; dibaca halaman pelanggan |
| `extend_expiry` | Extend Package Expiry | select | `extend_expiry` | ✅ | Dibaca `internal/billing/service.go:203` (default aktif), tidak ada input. Keputusan default menunggu pengguna |
| `clock_guard` | Clock Guard | select on/off | `clock_guard` | ✅ | Baru di UI; dibaca `internal/job/clock.go` (`off` mematikan) |

### S5-S12. Sub-halaman lain

| Sub-halaman lama | Field/aksi | Key baru | St | Catatan / Fase |
|---|---|---|---|---|
| `settings/maintenance-mode` | `maintenance_date` (date) + Save | `maintenance_date` (Miscellaneous > System) | ✅ | F5. Input ada; sub-halaman maintenance terpisah belum |
| `settings/maintenance-mode` | `maintenance_mode` (checkbox `1`, aktifkan), `maintenance_mode_logout` (checkbox `1`, paksa logout pelanggan), tombol `save` | `maintenance_mode`, `maintenance_mode_logout` (Miscellaneous > System) | ✅ | F5. Kedua checkbox ada di Miscellaneous > System; tombol save memakai form settings |
| `settings/dbstatus` | Download Backup Database, Restore Database, impor | — | ✅ | F5. Backup harian (`VACUUM INTO`) dan tautan download di Miscellaneous (SuperAdmin). Restore dari UI (v0.1.4): validasi file, backup `-pre-restore`, lalu restart. Impor PHPNuxBill dari JSON juga ada di UI (v0.1.4). Tombol `json` (unduh JSON) tetap non-goal |
| `settings/language-add` | satu input per kunci bahasa (`{$lang@key}`) | — | ❌ | Ditunda; bahasa = file JSON di `internal/i18n` |
| `settings/customfield` | `order[]`, `name[]`, `placeholder[]`, `type[]`, `value[]` (opsi), `register[]`, `required[]` | `sort_order`, `name`, `type`, `options`, `required` | ⚠️ | `placeholder`, `value` (default), `register` belum; urutan lewat angka, bukan drag |
| `settings/page` | `html` (editor) + `template_name` (Save as template) + `template_save` (checkbox `yes`, simpan sebagai template) | `body` (textarea) | ⚠️ | Teks biasa, bukan HTML; template simpan/reset belum |
| `settings/devices` | tanpa input | — | ❌ | Non-goal |
| `settings/widgets` | `orders[]`, `id[]`, `dashboard`, tombol Add/Edit | — | ❌ | Non-goal |
| `settings/widgets_add_edit` | `widget`, `title`, `orders`, `position`, `tipeUser`, `enabled`, `content` | — | ❌ | Non-goal |
| `settings/plugin-manager` | `zip_plugin` (file), `gh_url`, Install/Delete | — | ❌ | Non-goal (01-audit: plugin tidak di-port) |

---

## 4. Bagian Dashboard widget

Baru: `dashboard.html` + `dashboardData` (`handlers.go`), 4 kotak + 2 grafik Chart.js (`dashboard.js`), tanpa penyusunan dinamis.

### Widget admin (`widget/*.tpl`)

| # | Template | Isi di lama | Ada di baru? | Catatan |
|---|---|---|---|---|
| W1 | `top_widget.tpl` | 4 kotak: Income Today, Income This Month, Active/Expired, Customers | Ada (⚠️ sebagian) | Baru: `tile-today`, `tile-month`, `tile-subs` ("Active / Expired" digabung satu kotak), `tile-customers`. Pendapatan bulan kalender (lama: sejak `reset_day`) |
| W2 | `graph_monthly_registered_customers.tpl` | Grafik pendaftaran pelanggan per bulan | Ada | `Registered Members {Year}` |
| W3 | `graph_monthly_sales.tpl` | Grafik penjualan per bulan | Ada | `Total Monthly Sales {Year}` |
| W4 | `graph_customers_insight.tpl` | Grafik pie "All Users Insights" (status pelanggan) | Ada (⚠️) | Pie aktif vs expired saja; "Inactive" belum |
| W5 | `customer_expired.tpl` | Tabel pelanggan expired hari ini (Username, Full Name, Internet Package, Created/Expired, Phone, Email, Location) | Sebagian | Baru: 20 langganan terdekat/baru expired (Username, Full Name, Paket, Expires, Status) dengan link; tanpa Phone/Email/Location |
| W6 | `voucher_stocks.tpl` | Stok voucher per paket (Package Name, jumlah unused/used + total) | Ada (⚠️) | Unused dan Used per paket; kolom Total belum |
| W7 | `activity_log.tpl` | 5 log terakhir (`tbl_logs`) | Ada (⚠️) | 10 log terakhir (lama 5) |
| W8 | `cron_monitor.tpl` | "Cron Job last ran on" | Sebagian | Baru: "Job Monitor" menampilkan `expiry_last_run` dan `reminder_last_run`; clock guard banner (W14) |
| W9 | `mikrotik_cron_monitor.tpl` | "Routers Offline" | Belum | F5 / F2 monitor router |
| W10 | `default_info_row.tpl` | Total Customer Balance | Belum | F5 |
| W11 | `info_payment_gateway.tpl` | Info gateway aktif | Belum | F4 |
| W12 | `card_html.tpl` | Kartu HTML bebas | Non-goal | Widget dinamis tidak di-port |
| W13 | `html_php.php` / `html_php_card.php` (widget non-tpl) | HTML/PHP bebas | Non-goal | idem |
| W14 | Banner clock guard | — | Ada (baru) | Tambahan baru |

### Widget pelanggan (`widget/customers/*.tpl`)

| # | Template | Isi | Ada? | Catatan |
|---|---|---|---|---|
| W15 | `account_info.tpl` | Info akun, saldo, Service Type, toggle auto renewal, tagihan tambahan | Belum | F4; `customers.auto_renewal` ada |
| W16 | `active_internet_plan.tpl` | Paket aktif, expiry, IP/MAC, login status, Connect, Extend, Deactivate | Belum | F4 |
| W17 | `announcement.tpl` | Pengumuman (halaman statis) | Sebagian | F4/F5; ditampilkan di dashboard portal dari halaman `announcement` |
| W18 | `balance_transfer.tpl` | Form `friend` + `balance` transfer | Belum | F4 |
| W19 | `button_order_internet_plan.tpl` | Tombol Order Package | Belum | F4 |
| W20 | `recharge_a_friend.tpl` | Form username teman | Belum | F4 |
| W21 | `unpaid_order.tpl` | Order belum bayar: Pay Now/Cancel | Belum | F4 |
| W22 | `voucher_activation.tpl` | Form kode voucher + Order Voucher | Belum | F4 |

---

## 5. Bagian Portal pelanggan

Portal dasar ada di `internal/web/portal.go` (`/portal/*`): login, register, dashboard, profil, ganti password, riwayat order, order dari saldo. Sudah juga: lupa password, inbox, aktivasi voucher (`/portal/voucher`), kirim saldo antar pelanggan (`POST /portal/transfer`; `allow_balance_transfer`, `minimum_transfer`, notifikasi `balance_send`/`balance_received`, atomik satu tx), perpanjang paket kedaluwarsa (`POST /portal/extend/{id}`; `extend_expired`, `extend_days`, sekali per bulan kalender), registrasi menghormati `disable_registration=noreg`, `registration_username` (phone/email), `sms_otp_registration`, `reg_nofify_admin` (Telegram) dan mengirim `welcome_message`, daftar paket menampilkan bandwidth bila `show_bandwidth_plan=yes`. Sudah juga: gateway Tripay, kirim paket ke teman (`/portal/plans/{id}/friend`), riwayat aktivasi (`/portal/activation`), lupa username, top-up saldo kustom.

### C1. Login `customer/login.tpl`, `login-noreg.tpl`, `login-custom-moon.tpl`

| Field lama | Label | Tipe | Wajib | Field baru | St | Catatan |
|---|---|---|---|---|---|---|
| `username` | Phone Number / Email / Username | text | required | ✅ | `/portal/login` | |
| `password` | Password | password | required | ✅ | `/portal/login` | |
| `voucher` (noreg) | Voucher code (login+aktivasi) | text | required | `vouchers.code` | ❌ | |
| `voucher_only` (noreg) | Voucher code | text | required | — | ❌ | |
| Aksi: Login, Register, Forgot Password, tema moon | | | | | ❌ | Tema moon Non-goal |

### C2. Registrasi `register.tpl`, `register-otp.tpl`, `register-rotp.tpl`, `reg-login-custom-moon.tpl`

| Field lama | Label | Tipe | Wajib | Field baru | St | Catatan |
|---|---|---|---|---|---|---|
| `username` | Phone/Email/Username | text | required | ✅ | `/portal/register`; OTP hanya bila `sms_otp_registration=yes` dan gateway WA/SMS ada | |
| `fullname` | Full Name | text | required | ✅ | `/portal/register`; OTP hanya bila `sms_otp_registration=yes` dan gateway WA/SMS ada | |
| `email` | Email | text | required | ✅ | `/portal/register`; OTP hanya bila `sms_otp_registration=yes` dan gateway WA/SMS ada | |
| `address` | Home Address | text | required | ✅ | `/portal/register`; OTP hanya bila `sms_otp_registration=yes` dan gateway WA/SMS ada | |
| `password`, `cpassword` | Password / Confirm | password | required | ✅ | `/portal/register`; OTP hanya bila `sms_otp_registration=yes` dan gateway WA/SMS ada | |
| `photo` | Photo | file | required | — | ❌ | Ditunda |
| `phone_number` (rotp) | Phone Number | text `[0-9]*` | required | ✅ | `/portal/register`; OTP hanya bila `sms_otp_registration=yes` dan gateway WA/SMS ada | Request OTP |
| `otp_code` (otp) | SMS Verification Code | text | required | ✅ | `/portal/register`; OTP hanya bila `sms_otp_registration=yes` dan gateway WA/SMS ada | |
| Aksi: Register, Cancel, Request OTP | | | | | ❌ | |

### C3. Lupa password `forgot.tpl`

| Field lama | Label | Tipe | Wajib | Field baru | St | Catatan |
|---|---|---|---|---|---|---|
| `username` | Phone/Usernames | text | required | `username` (`POST /portal/forgot`) | ✅ | Kode dikirim ke nomor HP lewat gateway WA/SMS (`notify`); tanpa gateway: "contact admin" |
| `find` | Email or Phone number | text | required | — | ❌ | Lupa username belum di-port |
| `otp_code` | OTP | text | required | `otp_code` (`POST /portal/forgot/verify`) | ✅ | 6 digit crypto/rand, hash bcrypt di session, 10 menit, 5 percobaan |
| (baru) | Password baru + konfirmasi | password | required | `npass`, `cnpass` (`POST /portal/forgot/reset`) | ✅ | bcrypt; setelah berhasil ke login |
| Aksi: Validate, Back, Cancel | | | | `GET /portal/forgot?cancel=1` | ✅ | |
| Aksi: Forgot Usernames (`forgot&step=6`) | | | | `GET/POST /portal/forgot/username` | ✅ | Email/telepon -> username dikirim via notify ke kontak itu; jawaban selalu sama (tanpa enumerasi akun), pembatas `otpAllow` |

### C4. Dashboard `dashboard.tpl` + widget W15-W22 (lihat bagian 4). Semua ❌.

### C5. Profil `profile.tpl`, `phone-update.tpl`, `email-update.tpl`, `custom_field.tpl`

| Field lama | Label | Tipe | Wajib | Field baru | St | Catatan |
|---|---|---|---|---|---|---|
| `photo` | Photo | file | — | — | ❌ | Ditunda |
| `username` | Usernames | text (readonly) | — | ✅ | `/portal/profile` | |
| `fullname` | Full Name | text | — | ✅ | `/portal/profile` | |
| `address` | Home Address | textarea | — | ✅ | `/portal/profile` | |
| `phonenumber` | Phone Number | text (readonly) | — | ✅ | Readonly bila `allow_phone_otp=yes`, selain itu bisa diedit langsung (seperti lama) | |
| `email` | Email Address | text (readonly) | — | ✅ | Readonly bila `allow_email_otp=yes` | |
| `phone` / `email` (update) | New Number / New Email | number / text | required | `value` (`POST /portal/contact/{phone\|email}/otp`) | ✅ | Request OTP; telepon via gateway SMS/WA (`phone_otp_type`), email via SMTP; unik antar pelanggan |
| `otp` | OTP | number | required | `otp` (`POST /portal/contact/{kind}/verify`) | ✅ | Hash bcrypt di session, 10 menit, 5 percobaan |
| field kustom `{$field['name']}` | select / file / tipe dinamis | — | required bila diset | — | ❌ | Bergantung customfield F5 |
| Aksi: Save Changes, Cancel, Change (phone/email), Request OTP, Update | | | | | ❌ | |

### C6. Ganti password `change-password.tpl`

| Field lama | Label | Tipe | Wajib | Field baru | St | Catatan |
|---|---|---|---|---|---|---|
| `password` | Current Password | password | required | ✅ | `POST /portal/password` | |
| `npass` | New Password | password | required | ✅ | `POST /portal/password` | |
| `cnpass` | Confirm New Password | password | required | ✅ | `POST /portal/password` | |

### C7. Inbox `inbox.tpl`
`q` (cari), daftar pesan, Previous/Next, Back, Delete, Share (WhatsApp). Tabel `messages` tidak ada. ❌ (F4/F5).

### C8. Aktivasi voucher `activation.tpl`, `activation-list.tpl`, `invoice-customer.tpl`

| Field lama | Label | Tipe | Wajib | Field baru | St | Catatan |
|---|---|---|---|---|---|---|
| `code` | Voucher code | text | required | `code` (`POST /portal/voucher`) | ✅ | `RedeemVoucher` sekali pakai; `disable_voucher=yes` memblokir (403); `voucher_redirect` hanya URL http/https |
| `activation-list`: kolom Invoice, Package Name, Package Price, Type, Created On, Expires On, Method | | | | `GET /portal/activation` | ✅ | Dari transaksi pelanggan, tanpa baris Balance (kirim/terima saldo) |
| `invoice-customer`: `id`, Finish, Download, WhatsApp | | | | | ⚠️ | `GET /portal/orders/{id}/invoice` (hanya transaksi sendiri, selain itu 404); tanpa Download/WhatsApp |

### C9. Order paket `orderPlan.tpl`, `orderBalance.tpl`, `orderHistory.tpl`, `orderView.tpl`, `selectGateway.tpl`, `sendPlan.tpl`

| Field lama | Label | Tipe | Wajib | Field baru | St | Catatan |
|---|---|---|---|---|---|---|
| tombol Buy / Buy for friend per paket (`order/gateway/...`, `order/send/...`, `stoken`) | | link | | `/portal/plans`, `GET /portal/plans/{id}/friend` | ✅ | Daftar paket gabungan; Buy for friend tampil bila saldo aktif |
| `custom` (hidden), `amount` | Jumlah saldo kustom | number | — | `amount`, `channel` (`POST /portal/topup`) | ✅ | Hanya bila `allow_balance_custom=yes` DAN `payment_gateway=tripay` (di kode lama hanya dipakai di jalur gateway). `payment_requests.plan_id=0` = top-up; saldo bertambah saat lunas (migrasi 0010) |
| `coupon` | Coupon Code | text maxlength 50 | required (Apply) | `coupon` | ⚠️ | Di form bayar-saldo `/portal/plans` (tanpa gateway, tanpa pembatas percobaan 5x, tanpa gating `enable_coupons`); kupon tidak berlaku untuk paket Balance |
| `gateway` | Payment Gateway | select (`channel`) | required | `channel` | ✅ | `selectGateway` digabung ke `/portal/plans`: pilih kanal Tripay + kupon per paket, `POST /portal/plans/{id}/pay`; hanya bila `payment_gateway=tripay`. Kupon dipotong dari jumlah, dipakai saat lunas |
| `username` (`sendPlan`) | Friend username | text | required | `username` (`POST /portal/plans/{id}/friend`) | ✅ | `SendPlan`: satu tx debit saldo + recharge teman + baris transaksi pengirim. Aturan lama: saldo tidak `no`, pengirim Active, teman ada, bukan diri sendiri, teman tak punya paket aktif lain, saldo cukup. Keduanya dinotifikasi |
| `orderHistory`: kolom Package Name, Payment Method, Routers, Type, Package Price, Created on, Expires on, Date, Status | | | | ⚠️ | `/portal/orders` dari transaksi; tanpa kolom Routers/Status | |
| `orderView`: Pay Now, Check for Payment (`/check`), Cancel (`/cancel`) | `GET /portal/payments/{id}`, `POST .../check` | | | ⚠️ | Pay Now + Check ada; Cancel belum (pesanan kedaluwarsa otomatis oleh job) |

### C10. Halaman statis dan galat: `pages.tpl` (ditampilkan `page/{nama}`) ✅ `GET /pages/{slug}` (teks biasa, di-escape, baris baru dijaga); `404.tpl`, `error.tpl` ❌.

---

### Lampiran: nama input tombol/submit/meta (bukan field data)

| Nama | Template | Jenis | Keterangan |
|---|---|---|---|
| `export` (value `csv`) | `admin/customers/list.tpl` | Aksi | Tombol export CSV pelanggan (A8); belum ada (F5) |
| `general` | `admin/settings/app.tpl` | Aksi | Tombol submit Save per kartu S1; baru satu tombol Save di `/admin/settings` |
| `save` | `paymentgateway/list`, `settings/widgets`, `maintenance-mode`, `miscellaneous` | Aksi | Tombol submit Save; pada baru satu tombol submit standar `form.html` |
| `nux` | `admin/rencana/invoice-print.tpl` | Aksi | Tombol NuxPrint (printer Android); Ditunda |
| `add_coupon` | `customer/selectGateway.tpl` | Aksi | Tombol Apply Coupon (C9); belum, bergantung kupon F5 |
| `pay` | `customer/selectGateway.tpl` | Aksi | Tombol Pay Now (C9); belum (F4) |
| `send` | `customer/sendPlan.tpl` | Aksi | Tombol kirim paket ke teman (C9); belum (F4) |
| `viewport` | `admin/header.tpl`, `maintenance.tpl`, `error.tpl`, `alert.tpl` | Meta | Ignored: tag `<meta>`, bukan input. Baru: `base.html` punya viewport sendiri |

---

## 6. Daftar selisih (gap list) per fase

Satu baris per item. Urutan prioritas dalam fase: atas = lebih dulu. Rujukan layar: A# / S# / W# / C#.

### F1 Billing inti (paritas operasional admin)

1. Top-up saldo oleh admin (A46 `rencana/deposit`: `id_customer`, `id_plan`, `amount`, `note`); simpan ke `transactions.note`, tipe Balance. (SELESAI di F1)
2. Layar langganan aktif (A47): daftar `subscriptions` dengan filter router/rencana/status, kolom Username/Plan/Type/Created/Expires/Method/Location. (SELESAI di F1)
3. Edit langganan (A48): ubah `plan_id` dan `expires_at` dengan satu input datetime (bukan date+time terpisah). (SELESAI di F1)
4. Perpanjang (Extend) dan nonaktifkan (Deactivate) langganan dari A11/A47.
5. Sinkron ke router per pelanggan dan per paket secara manual (tombol Sync A8/A11/A12).
6. Daftar pelanggan: kolom Balance, Package, PPPOE, Created On, Photo; filter status + urut (`order`, `orderby`, `filter`); aksi baris Recharge.
7. Hapus massal pelanggan (`customer_ids[]`) dan kirim pesan massal (butuh F4).
8. Validasi panjang PHP yang hilang: username 3-54, fullname 2-25, password 3-35, router name 1-30, pool name 3-30, NAS 3-30 (opsional, putuskan).
9. Field paket: `price_old` (harga coret), `plan_type` Personal/Business, (`on_login`/`on_logout` sudah ada di form).
10. Field pelanggan: `account_type`, `coordinates`, `city`, `district`, `state`, `zip`, `photo` (butuh migrasi 0004 + keputusan: pakai custom field F5 atau kolom).
11. Metode bayar admin kustom (`payment_usings`) selain Cash/Balance; opsi `zero`; halaman konfirmasi recharge (A44) bila perlu.
12. Filter daftar paket (tipe, billing, router, status, bandwidth) dan filter tipe di daftar Balance.
13. Daftar voucher: kolom Type, Routers, Customer (`used_by`), Generated By; aksi View, hapus massal, hapus > 3 bulan.
14. Bandwidth: putuskan apakah `burst[]` (5 input) dipecah kembali dari string `burst`.
15. Router: kolom Description; unik `host`; nama `Radius` reserved.
16. Pool: opsi pilih Pool kosong di PPPoE (lama wajib) sudah opsional; konfirmasi perilaku.

### F2 Driver MikroTik

17. Router: kolom Online Status dan Last Seen (A33) lewat `router_check` + job; tombol test sudah ada.
18. Router: `coordinates`, `coverage` (bersama maps F5).
19. Pemeriksaan pelanggan online (`check_customer_online`) di daftar/ view pelanggan.

### F3 RADIUS

20. Log RADIUS (A58): UI di atas `radius_sessions` (`q`, kolom sesi, bersihkan `keep`, CSV).
21. Setting `hs_auth_method` (pilih PAP/CHAP) bila diperlukan; saat ini semua didukung.
22. Hubungkan NAS ke router (`routers` pada A37) bila dibutuhkan; kolom NAS `type/ports/server/community` sengaja dibuang.

### F4 Portal pelanggan dan pembayaran

23. Portal: login (C1), logout, dashboard (C4 + W15-W22), profil (C5), ganti password (C6), inbox (C7).
24. Registrasi + OTP (C2) dan lupa password (C3); setting registrasi/OTP (S1 Registrasi, S4).
25. Aktivasi voucher pelanggan (C8) memakai `internal/billing`.
26. Order paket/saldo, `selectGateway`, `orderView`, callback Tripay dan aktivasi otomatis (C9); simpan `reference` Tripay di `transactions`.
27. Halaman admin payment gateway (A71-A73): konfigurasi Tripay, audit transaksi PG (butuh tabel `payment_transactions`).
28. Halaman setting notifikasi (S3), kanal (Telegram, SMS, WA, SMTP, webhook; S1) dan `user_notification_*`.
29. Kirim pesan tunggal ke pelanggan (A63) dan log pesan (A59, butuh tabel).
30. Sambungkan notifikasi ke recharge/expired/reminder (progres "akan dikerjakan" 1), checkbox `send_welcome_message` + `sms/wa/mail` di form pelanggan (A9).
31. Extend mandiri (`extend_expired`, `extend_days`, `extend_confirmation`), transfer saldo (`allow_balance_transfer`, `minimum_transfer`), `allow_balance_custom`.
32. Login as Customer dari admin (A11) setelah portal ada.
33. `country_code_phone` di Localisation (S2).

### F5 Pelengkap

34. Admin user CRUD (A3-A6): form dengan `fullname`, `username`, `password`, `role`, `status`, dan relasi Agent (`root`) -> migrasi kolom; hapus pengguna; daftar dengan Last Login.
35. Ganti password admin (A7).
36. Laporan: harian dengan filter lengkap (A52), periode (A53/A54), cetak (A55/A56), export CSV; filter tanggal/tipe/metode di `/admin/transactions`.
37. Invoice cetak (A49/A50) + kolom `address`, `note`, logo di settings (S1 Umum).
38. Kupon (A60-A62): migrasi tabel `coupons`, CRUD, block/unblock, pakai di order (C9).
39. Peta + ODP (A65-A70): tabel `odp`, kolom `coordinates`/`coverage` di customers/routers.
40. Pesan massal (A64) dengan filter router/service/group.
41. Custom field (S8) + isi di form pelanggan (A9/A10) dan portal (C5).
42. Halaman statis (S9, C10), halaman 404/error rapi (A75); UI setting maintenance mode (S5, tanggal `maintenance_date`); middleware 503 dan `maintenance_mode_logout` sudah ada.
43. Backup/restore (S6): backup harian `VACUUM INTO` sudah ada (`NUXBILL_BACKUP_DIR`, `backup_keep`); unduh dan restore dari UI belum.
44. Setting umum sisanya: `date_format`, `dec_point`, `thousands_sep`, `reset_day`, logo/alamat/telepon/footer, `session_timeout_duration`, `single_session`, `voucher_format` default.
45. Widget dashboard sisa: W9 monitor router, W10 total saldo.
46. Setting timezone diterapkan tanpa restart (progres item 2).
47. CSV export dan import pelanggan (A8), CSV log (A57), bersihkan log (`keep`) (A57).
48. Log sistem: kolom Type/User ID setara; sudah ada `actor_type`, `actor_id`.

### F6 Migrasi data dan rilis

49. `nuxbill import`: petakan field lama ke baru, termasuk `expiration`+`time` -> `expires_at`, `price varchar` -> INTEGER, `pppoe_password`/`password` plaintext -> `secret_enc` + bcrypt `password_hash`, `tbl_appconfig` -> `settings`.
50. Pemetaan kolom yang belum punya padanan (lihat butir 9, 10, 18, 39) harus diputuskan sebelum import agar data tidak hilang.

### Ditunda / Non-goal (tidak masuk gap aktif)

- Ditunda: VPN (A21-A23), Port (A30-A32), foto + face detection, tambah bahasa lewat UI (S7), pajak (`enable_tax`), proxy, API token, tawk.to, printer thermal (`printer_cols`, NuxPrint).
- Non-goal: plugin manager (S12), widget dinamis (S11, W12-W13), devices (S10), community/rollback (A76), updater `new_version_notify`, `github_*`, `frrest_interim_update`, `login_Page_template` (tema moon), `url_canonical`, `dashboard_cr`.

## Lihat juga

- [progres](progres.md)
- [paritas-bisnis](paritas-bisnis.md)
- [audit-ux](audit-ux.md)
- [audit-legacy](rencana/audit-legacy.md)
