# Paritas perilaku bisnis: PHPNuxBill vs gobill

Audit baca-saja (tanpa mengubah kode), dibuat 2026-10-10 dari `main`. Pertanyaannya: apa yang terjadi saat admin, pelanggan, cron, atau RADIUS memicu aksi X, dan di mana hasilnya beda dari PHP. Beda UI/label tidak dibahas (lihat `UI-PARITY.md`). Beda yang sudah tercatat di PROGRESS "disengaja" tidak diulang.

Sumber PHP: `system/controllers/*.php`, `system/cron.php`, `cron_reminder.php`, `radius.php`, `autoload/Package.php`, `Message.php`, `widgets/top_widget.php`, `devices/*`. Sumber gobill: `internal/billing`, `internal/web`, `internal/radius`, `internal/device`, `internal/notify`, `internal/importer`. Semua baris dicek di kedua kode.

Dampak: **Tinggi** = uang, akses, data hilang, atau operator tidak bisa mengerjakan tugas harian. **Sedang** = beda yang terasa, ada jalan memutar. **Rendah** = kosmetik atau jarang.

Sudah diperiksa dan **sama** (tidak ada temuan): hitung expiry Days/Hrs/Mins/Months/Period dan `extend_expiry`, ganti paket = hitung dari sekarang, auto-renewal dari saldo, urutan cron expiry (router dulu, status kemudian), jadwal reminder H-7/3/1, hapus pelanggan (transaksi dipertahankan), transfer saldo, perpanjang mandiri sebulan sekali, redeem voucher, keputusan RADIUS (status Active, login lewat pppoe_username, expired, shared_users, rate, kuota data, CHAP), peran admin untuk pelanggan/plan/router/laporan.

## Pelanggan

| ID | Dampak | PHP | gobill | Beda | Saran |
|---|---|---|---|---|---|
| P1 | Tinggi | `Package.php:44-48`: `rechargeUser` memanggil `_alert` (lalu `die`) bila `status != Active`; berlaku untuk recharge admin, voucher, bayar saldo, gateway | `billing/service.go:197` `recharge()`, `web/customers.go:321` `custRecharge`, `web/portal.go:513` `pBuyBalance`, `web/payment.go:92` `pPay`, `vouchers.go` `vchRedeem`: tidak ada cek status | Pelanggan Suspended/Inactive/Limited bisa diisi ulang (uang masuk atau saldo terpotong), tetapi RADIUS menolaknya (`GetCustomerForRadius` hanya `status='Active'`). Tidak ada peringatan. Portal hanya menolak Banned/Disabled | Tolak di handler web bila `status != Active` (cron auto-renew dikecualikan; di PHP CLI `_alert` juga lolos) |
| P2 | Sedang | `customers.php:638-650,788-815`: edit username memanggil `change_username` di router dan memperbarui `tbl_user_recharges.username`; ubah pppoe_username/ip/password juga di-sync | `web/customers.go:380` `custSave`: username read-only saat edit; `device.ChangeUsername` ada tetapi tidak dipanggil; hanya perubahan password yang memanggil `SyncCustomer` | Operator yang memakai nomor HP sebagai username tidak bisa memperbaiki salah ketik. Ubah `pppoe_username`/`pppoe_ip` tidak didorong ke router | Izinkan edit username (Admin) lalu `ChangeUsername` + `SyncCustomer`; sync juga saat `pppoe_*` berubah |
| P3 | Sedang | `Package.php:75-81`: plan nonaktif tetap bisa direcharge SuperAdmin/Admin | `web/customers.go:321` dan `billing.Preview` menolak plan `enabled != 1` untuk semua peran | Trik "sembunyikan paket lama dari portal tapi pelanggan lama tetap diperpanjang admin" tidak jalan; admin harus mengaktifkan paket sementara | Izinkan SuperAdmin/Admin merecharge plan nonaktif |
| P4 | Rendah | `customers.php:440,441`: hapus pelanggan dengan `plan_expired = 0`, user dihapus dari router | `billing/extras.go:46` `DeleteCustomer` -> `prepare()` memuat `ExpiredPlan`; `device/mikrotik_hotspot.go:59` `RemoveCustomer` memindahkan user ke profil expired | Pada plan Mikrotik langsung yang punya "Expired Plan", pelanggan yang dihapus tertinggal di router dengan profil expired. Tidak berlaku untuk plan `Radius` | Kosongkan `dp.ExpiredPlan` saat hapus pelanggan |
| P5 | Rendah | `home.php:211-226`: pelanggan menonaktifkan paketnya sendiri (+ Telegram) | tidak ada | Pelanggan tidak bisa berhenti sendiri; admin tetap bisa | Abaikan kecuali diminta |
| P6 | Rendah | `customers.php:515`: nomor disimpan dengan `Lang::phoneFormat` (kode negara), SMS memakainya | `notify/notify.go` `SMS()` mengirim nomor apa adanya; hanya `WhatsApp()` memanggil `phoneFormat` | Pelanggan baru dengan nomor `08...` tidak diberi `62` di gateway SMS | Panggil `phoneFormat` juga di `SMS()` |

## Paket & recharge

| ID | Dampak | PHP | gobill | Beda | Saran |
|---|---|---|---|---|---|
| R1 | Tinggi | `plan.php:1036-1090` `extend`: bila sudah expired, mulai dari **sekarang** + N hari, status `on` + `add_customer`; tanpa cek peran | `billing/admin.go:130` `ExtendSubscription` = `expires_at lama + N hari` via `EditSubscription`; rute hanya `managers` (`web.go:227`) | Pelanggan yang expired lebih lama dari N hari tetap expired setelah "Extend", sementara layar menampilkan "Data Updated Successfully". Tugas harian "kasih tempo 3 hari" gagal untuk yang lama expired. Agent/Sales juga tidak bisa extend | Bila `expires_at < now`, pakai `now + N hari`; beri Agent/Sales akses bila perlu |
| R2 | Sedang | `plan.php:80,148,196,240`: metode bayar dari setting `payment_usings` (Cash, Transfer, QRIS, ...), `using=zero` = "Recharge Zero" (harga 0), method `"<using> - <nama admin>"` | `web/customers.go:321` hanya `Cash`/`Balance`, method `Admin - Cash`; `payment_usings` tidak dibaca | Kasir tidak bisa membedakan tunai vs transfer di laporan dan tidak bisa recharge gratis (kompensasi). Pengelompokan method baru beda dari riwayat impor (`Recharge - <admin>`) | Dropdown dari `payment_usings`, opsi `Zero`, simpan `"<metode> - <admin>"` |
| R3 | Sedang | `Package.php:293-300` (sudah punya baris recharge, walau `off`: harga `plan.price`), `:405-411` (nol hanya pembelian pertama) | `billing/service.go:251,567`: `Period && !found`, `found` hanya langganan **aktif** | Beli ulang paket Period setelah expired (auto-renew dan Tripay ikut) dicatat Rp 0, sementara saldo dipotong penuh (`RechargeWithBalance` memakai `plan.Price`). Hanya paket Period | Tentukan "pertama" dari riwayat langganan apa pun |
| R4 | Sedang | `devices/RadiusRest.php:31-41`: `remove_customer` (saat expired) nol-kan `acctInput/OutputOctets` untuk plan Data/Both limit; extend tidak menyentuh usage | `radius/radius.go:277` `SumRadiusUsage(started_at >= sub.StartedAt)`; `ExtendSubscription`, `EditSubscription`, `ExtendExpired` (portal) tidak mengubah `started_at` | Pelanggan plan berkuota data yang kuotanya habis lalu diperpanjang admin atau mandiri tetap kena "You have exceeded your data limit" (usage periode lama ikut dihitung). Recharge normal aman (`started_at = now`) | Saat reaktivasi expired -> aktif set `started_at = now` |
| R5 | Sedang | `Package.php:240,356` Telegram "System Error. When activate Package. You need to sync manually"; `:335,458` Telegram `#recharge`/`#buy`; `customers.php:268` Telegram deactivate | `billing/service.go:317` `apply()` hanya `slog.Error` bila router gagal; Telegram admin hanya untuk transfer, extend, registrasi, router, renewal gagal | Operator tidak tahu bila paket sudah dibayar tetapi gagal diaktifkan di router (plan Mikrotik langsung). Tidak ada notif admin per pembelian/deactivate | Kirim Telegram saat `activate` gagal (paling penting); opsional `#buy`, `#deactivate` |
| R6 | Rendah | `plan.php:37`, `services.php:19`, `pool.php:71`, `customers.php:274`: sync massal (semua pelanggan aktif, semua profil, semua pool) | tidak ada (hanya sync per pelanggan/langganan dan saat plan/pool diubah) | Setelah router diganti/reset tidak ada tombol "sync semua". Tidak berlaku untuk plan `Radius` | Tombol sync massal (loop `SyncSubscription`/`AddPlan`) |
| R7 | Sedang | `plan.php:126,155,216`, `order.php:238`: pajak (`enable_tax`, `tax_rate`) menambah harga dan potongan saldo | tidak ada (`UI-PARITY`: Ditunda); setting ikut terimpor tetapi diabaikan | Bila operator mengaktifkan PPN, pelanggan ditagih/dipotong lebih kecil dari PHP tanpa peringatan | Importer memperingatkan bila `enable_tax=yes`, atau implementasikan di `recharge()` |
| R8 | Rendah | `Package.php:198`, `cron.php:102`: `extend_expiry` dan `enable_balance` harus `== 'yes'` | `billing/service.go:243,371`: `!= "no"` (kosong dianggap aktif) | Hanya beda bila key tidak ada di DB (instalasi baru): auto-renew bisa menyala padahal PHP mati | Samakan dengan `== "yes"` |

## Voucher & kupon

| ID | Dampak | PHP | gobill | Beda | Saran |
|---|---|---|---|---|---|
| V1 | Rendah | `plan.php:551-567` `remove-voucher` (hapus voucher terpakai > 3 bulan) | tidak ada | Tidak ada pembersihan massal | Tombol atau job opsional |
| V2 | Rendah | `order.php:418` kupon aktif hanya bila `enable_coupons` | `web/templates/portal/plans.html:12,14` kolom kupon selalu tampil; setting tidak dibaca | Operator yang mematikan kupon masih menerima kode kupon dari portal | Gate di `pBuyBalance`/`pPay` |

## Saldo

| ID | Dampak | PHP | gobill | Beda | Saran |
|---|---|---|---|---|---|
| S1 | Tinggi | `widgets/top_widget.php:10-26`: pendapatan hari/bulan **mengecualikan** `method = 'Customer - Balance'` (beli paket dari saldo); periode mulai dari `reset_day` (`dashboard.php:29-35`) | `web/handlers.go:163-168` `SumTransactionsBetween` menjumlah semua baris; `reset_day` tidak dipakai; transfer saldo dan `SendPlan` menulis baris `transactions` (`billing/portal.go:76`, `extras.go:156`) yang di PHP tidak ada | Pendapatan dashboard menghitung ganda: top-up Rp 100.000 lalu beli paket Rp 50.000 dari saldo = Rp 150.000. Transfer antar pelanggan menambah 2x nilai transfer; laporan harian/periode ikut menjumlah baris itu. (Transfer di PHP hanya ke `tbl_payment_gateway`.) | Kecualikan `method = 'Customer - Balance'` dan `'Balance - Gift from%'` di `SumTransactionsBetween` dan laporan; pakai `reset_day`; lebih baik jangan tulis transfer ke `transactions` |
| S2 | Rendah | `plan.php:1004` deposit nominal negatif diizinkan (koreksi saldo) | `billing/admin.go:33` `amount <= 0` ditolak; `web/subscriptions.go:263` `posInt` | Tidak ada cara mengurangi saldo yang salah diisi | Izinkan negatif dengan catatan wajib (Admin) |
| S3 | Rendah | `customers.php:197`, `plan.php:134,224`: bayar dengan saldo oleh admin hanya bila `enable_balance == 'yes'` | `web/customers.go:321` metode `Balance` tidak cek `enable_balance` | Admin masih bisa memotong saldo saat fitur saldo dimatikan | Cek setting |

## Portal pelanggan

| ID | Dampak | PHP | gobill | Beda | Saran |
|---|---|---|---|---|---|
| T1 | Rendah | `login.php:37`: hanya `Banned` ditolak masuk portal | `web/portal.go:39,103`: `Banned` dan `Disabled` ditolak | Pelanggan Disabled tidak bisa melihat riwayat/tagihan (PHP tetap memblokir pembelian lewat P1) | Boleh dibiarkan |
| T2 | Rendah | `order.php:138-153,572`: order `status=1` ditampilkan dan bisa dilanjutkan; riwayat berisi order batal/gagal | `web/portal.go` `pOrders` hanya `transactions`; banyak `payment_requests` pending boleh | Pelanggan tidak melihat order gateway yang batal/kedaluwarsa | Opsional |
| T3 | Rendah | `tbl_payment_gateway` tidak punya FK | `db/migrations/0009_payments.sql:8` `ON DELETE CASCADE` | Hapus pelanggan yang punya order Tripay pending: callback berikutnya "unknown reference" dibalas sukses, uang tidak tercatat | Tolak hapus bila ada payment pending |

## Cron / expiry / reminder / notifikasi

| ID | Dampak | PHP | gobill | Beda | Saran |
|---|---|---|---|---|---|
| C1 | Sedang | `Message.php:199-307` (`sendPackageNotification`: `[[price]]`, `[[bills]]`, `[[payment_link]]`), `:309-` (`sendInvoice`: `[[invoice_link]]`, `[[password]]`, `[[note]]`); `cron_reminder.php` mengirim harga | `notify/notify.go:300,322` (`Expired`, `Reminder`) hanya mengisi `name, username, package/plan, expired_date` | Template WA operator yang memuat `[[price]]` atau `[[payment_link]]` mengirim teks mentah "[[price]]" ke pelanggan (reminder dan expired) | Isi `price` (dan `invoice_link`) di `sendReminders`/`expireOne`/`notifyRecharge` |
| C2 | Sedang | Template disimpan di file `uploads/notifications.json` (`init.php:96`) | `importer/importer.go:170` hanya `tbl_appconfig`; template bawaan gobill berbahasa Inggris (`notify.go:28-36`) | Setelah cutover semua pesan expired/reminder/invoice kembali ke bahasa Inggris bawaan | `nuxbill import --notifications=<json>` -> `settings.notif_*` |
| C3 | Rendah | `cron.php:138-153`: `frrest_interim_update` menutup sesi Start yang basi | `radius/radius.go:34` `staleAfter = 600` tetap; setting tidak dibaca (Non-goal) | Bila interim NAS > 10 menit, sesi dianggap basi dan batas `shared_users` tidak berlaku | Baca `frrest_interim_update` bila diisi |
| C4 | Rendah | `cron.php:155`: `router_check` harus aktif; email + Telegram tiap cron | `billing/routermonitor.go:16` default aktif; Telegram hanya saat status berubah; tanpa email | Beda ringan (gobill lebih tenang) | Tidak perlu |

## RADIUS

| ID | Dampak | PHP | gobill | Beda | Saran |
|---|---|---|---|---|---|
| D1 | Sedang | `radius.php:425-455`: Time_Limit memberi `Max-All-Session` + `Expire-After` (ditegakkan FreeRADIUS) | `radius/radius.go:262-270`: jalur UDP bawaan hanya `Session-Timeout = min(sisa, limit)` per sesi, tidak ada penjumlahan `session_time`. Jalur REST (`web/radiusrest.go`) mengirim `Max-All-Session` seperti PHP | Dengan RADIUS bawaan, paket "2 jam" memberi 2 jam **per login** selama masa berlaku, bukan total. Dengan FreeRADIUS+REST sama seperti PHP | Akumulasikan `session_time` sejak `started_at` untuk Time/Both limit |
| D2 | Rendah | `radius.php:262`: pesan "Internet Plan Expired.." | `radius/radius.go:233`: "No active plan" | Pesan di halaman login hotspot berubah | Samakan teks |

## Router

Tidak ada temuan baru di luar R5 (alert aktivasi gagal), R6 (sync massal), dan P4 (hapus pelanggan). Urutan aksi router (setelah commit DB) adalah beda yang disengaja.

## Laporan

| ID | Dampak | PHP | gobill | Beda | Saran |
|---|---|---|---|---|---|
| L1 | Rendah | `reports.php:115`: filter method memakai prefix gateway (`"$mt - %"`) | `db/reports.sql.go` `method = ?` (cocok penuh, mis. `Admin - Cash`) | Filter "Cash" tidak menemukan apa pun; harus mengetik string penuh | `LIKE ? || ' - %'` |

Pendapatan dashboard dan baris transfer di laporan: lihat S1.

## Admin / Setting / Migrasi

| ID | Dampak | PHP | gobill | Beda | Saran |
|---|---|---|---|---|---|
| IM1 | Tinggi | `User::getBills/getAttribute`: atribut `Bill` (tagihan tambahan, cicilan `biaya:sisa`), `Invoice`, `Expired Date` di `tbl_customers_fields` dipakai `rechargeUser`, `sendInvoice`, `cron.php:103-106` | `importer/importer.go:124` tidak mengimpor `tbl_customers_fields`; `docs/migrasi-phpnuxbill.md` hanya menyebut kupon, ODP, inbox sebagai belum diimpor; `recharge()` tidak membaca `Bill` | Tagihan tambahan per pelanggan hilang diam-diam: pelanggan ditagih lebih murah, tanggal tagih khusus hilang. Tidak ada di dokumen migrasi | Minimal: laporan impor menyebut jumlah baris fields yang dilewati dan dokumentasikan; ideal: impor ke `customer_fields` dan baca `Bill` di `recharge()` |
| IM2 | Rendah | `settings.php`: banyak key berefek (`payment_usings`, `enable_coupons`, `reset_day`, `enable_tax`, `frrest_interim_update`) | tidak dibaca (R2, R7, V2, S1, C3) | Setting terimpor tetapi tanpa efek dan tanpa peringatan | Importer: daftar "setting tidak didukung" di laporan |

## Daftar prioritas

1. **R1** Extend admin tidak bekerja untuk pelanggan yang sudah lama expired (tugas harian).
2. **P1** Recharge pelanggan non-Active diterima (uang masuk, akses tetap ditolak RADIUS).
3. **S1** Pendapatan dashboard menghitung ganda saldo/transfer; `reset_day` diabaikan.
4. **IM1** Atribut pelanggan (`Bill`, `Invoice`, `Expired Date`) hilang saat impor dan tidak didokumentasikan.
5. **R2** Metode bayar kustom (`payment_usings`) dan Recharge Zero tidak ada.
6. **R4** Kuota data tidak di-reset saat reaktivasi (extend admin, edit langganan, perpanjang mandiri).
7. **C1/C2** `[[price]]`/`[[payment_link]]` tidak terisi; template notifikasi Indonesia tidak ikut impor.
8. **R5** Tidak ada Telegram saat aktivasi router gagal setelah bayar.
9. **D1** Time_Limit tidak kumulatif di RADIUS bawaan.
10. **P2/P3/R3/R7** Edit username, recharge plan nonaktif oleh Admin, harga Period setelah expired, pajak.

Hitungan temuan: **Tinggi 4** (R1, P1, S1, IM1), **Sedang 10** (P2, P3, R2, R3, R4, R5, R7, C1, C2, D1), **Rendah 17** (P4, P5, P6, R6, R8, V1, V2, S2, S3, T1, T2, T3, C3, C4, D2, L1, IM2).
