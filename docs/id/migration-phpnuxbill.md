# Migrasi dari PHPNuxBill

Dokumen ini untuk operator yang pindah dari PHPNuxBill dengan MySQL ke gobill. Impor dilakukan satu kali, lewat UI atau perintah `gobill import`. Sistem lama tidak diubah.

**Untuk:** operator

**Prasyarat:** backup JSON dari PHPNuxBill, atau akses MySQL ke database lama. gobill sudah terpasang. Lihat [instalasi](installation.md).

## Lewat UI

Cara ini tidak memakai terminal. SuperAdmin membuka **Pengaturan > Aneka ragam > Import PHPNuxBill**, di `/admin/settings/miscellaneous/import`.

1. Unggah file backup JSON, yang wajib, dan `system/uploads/notifications.json`, yang opsional. Klik **Periksa**. Tidak ada data yang berubah. Laporan menampilkan per tabel: jumlah yang dibaca, yang akan diimpor, dan yang dilewati beserta alasannya. Laporan juga menampilkan jumlah data gobill saat ini.
2. Klik **Impor sekarang**. Centang "Saya mengerti data akan ditimpa".
3. Sebelum impor, gobill membuat backup database otomatis. Lihat [pengaman impor](backup-restore.md#pengaman-impor). Jika backup gagal, impor tidak dijalankan.
4. Data lama ditimpa, termasuk admin. Anda lalu diarahkan ke halaman login. Masuk dengan akun admin PHPNuxBill lama dan password yang sama. Hash sha1 lama diganti ke bcrypt saat login pertama.
5. Hapus file backup JSON dari komputer Anda setelah impor selesai, karena isinya password lama.

File yang diunggah maksimal 200 MB. Upload yang belum dikonfirmasi dihapus otomatis setelah 30 menit, atau saat dibatalkan.

## Perintah impor

Cara yang disarankan adalah backup JSON dari PHPNuxBill, tanpa akses MySQL.

1. Di PHPNuxBill, buka **Pengaturan > Database Status**. Centang semua tabel. Minimal tabel berikut harus ada: `tbl_customers`, `tbl_plans`, `tbl_bandwidth`, `tbl_routers`, `tbl_pool`, `tbl_user_recharges`, `tbl_voucher`, `tbl_users`, dan `tbl_appconfig`. Klik **Backup**.
2. Jalankan:

        gobill import --json=/path/phpnuxbill_backup.json --db=./gobill.db [--timezone=Asia/Jakarta] [--dry-run] [--force] [--notifications=/path/phpnuxbill/system/uploads/notifications.json]

File backup berisi password pelanggan dan secret router dalam bentuk lama. Simpan file itu hanya untuk Anda. Hapus setelah impor.

Alternatifnya adalah MySQL langsung. Pilih salah satu: `--json` atau `--mysql-dsn`.

    gobill import --mysql-dsn='<user>:<password>@tcp(127.0.0.1:3306)/phpnuxbill' --db=./gobill.db [--timezone=Asia/Jakarta] [--dry-run] [--force] [--notifications=/path/phpnuxbill/system/uploads/notifications.json]

Catatan:

- Tabel yang tidak ada di file backup, atau tidak ada di database, dilewati. Dilewati dicatat di laporan sebagai "tidak ada atau kosong di sumber, dilewati". Impor tidak gagal karena itu.
- Semua berjalan dalam satu transaksi SQLite. Target harus kosong. Opsi `--force` menghapus isinya.
- `--dry-run` hanya membuat laporan. Laporan berisi baris yang dibaca, diimpor, dan dilewati, beserta alasannya.
- Zona waktu bawaan diambil dari pengaturan lama, atau `Asia/Jakarta`.
- Template pesan, yaitu expired, pengingat 7, 3, dan 1 hari, invoice, selamat datang, dan saldo, disimpan PHPNuxBill di `system/uploads/notifications.json`, bukan di MySQL. Tambahkan `--notifications=<path file itu>` agar template ikut diimpor ke pengaturan `notif_*`. Tanpa opsi ini, atau jika file tidak ada, pesan memakai template bawaan bahasa Inggris, dan laporan impor mencatatnya.
- Hanya template yang punya padanan di gobill yang diimpor. `email_invoice` dilewati.
- Placeholder yang tidak dikenal dikirim sebagai teks kosong, tidak pernah sebagai `[[...]]` mentah.
- `[[payment_link]]` dan `[[invoice_link]]` berisi tautan ke portal. Tautan ini memerlukan pengaturan `app_url`, dan pelanggan harus login dulu.
- Gunakan `GOBILL_SECRET_KEY` yang sama dengan yang dipakai di produksi. Atau biarkan gobill membuat `<db>.key`, lalu backup file itu.

## Yang dikonversi

Yang diimpor: pengaturan, admin, router, bandwidth, IP pool, paket, pelanggan, langganan aktif, transaksi, voucher, log, dan NAS.

- Password admin sha1 ditandai `legacy_sha1`, lalu diganti ke bcrypt saat login pertama.
- Password pelanggan dan secret perangkat dienkripsi ulang, dengan bcrypt untuk password dan AES-GCM untuk secret.
- Waktu kedaluwarsa diubah ke Unix UTC. Uang menjadi INTEGER rupiah.
- Harga yang gagal dikonversi dilewati, lalu dilaporkan.
- Plan `RadiusRest` menjadi plan `Radius`. Lihat [FreeRADIUS lewat REST](freeradius-rest.md).

Belum diimpor: kupon, ODP, dan inbox.

### Atribut pelanggan

Atribut pelanggan di `tbl_customers_fields` diimpor sebagai kolom kustom dengan nama yang sama. Kolom itu tampil di form pelanggan. gobill memakainya seperti PHP:

- `<nama> Bill`, misalnya `Router Bill`: tagihan tambahan. Nilainya ditambahkan ke harga setiap recharge, dari admin, saldo, voucher, gateway, atau perpanjang otomatis. Nilai dengan format `biaya:sisa`, misalnya `50000:3`, adalah cicilan. Sisanya berkurang satu setiap recharge, dan berhenti saat sisa 0.
- `Invoice`: harga tagihan paket Period berikutnya. Nilai ini menggantikan harga paket. Diisi otomatis setelah recharge Period, seperti PHP.
- `Expired Date`: tanggal jatuh tempo paket Period per pelanggan. Nilainya masuk ke `billing_day` pelanggan, bukan ke kolom kustom. Nilai yang bukan angka 1 sampai 31 dilewati dan dilaporkan.

Perbedaan: pembayaran gateway online memakai harga paket ditambah tagihan saat pesanan dibuat. Kupon hanya mendiskon harga paket. Tagihan ditambahkan di atasnya.

### Kolom yang sengaja dibuang

`account_type`, kota, kecamatan, provinsi, kode pos, `price_old`, dan `plan_type`.

## Verifikasi yang sudah dilakukan

Impor sudah diuji dengan dump produksi asli. Semua tanggal kedaluwarsa dan total transaksi cocok dengan sistem lama. Ulangi pembandingan ini pada dump terbaru sebelum cutover.

## WhatsApp setelah cutover

Di sistem lama, `wa_url` menunjuk ke aplikasi PHP sendiri, misalnya `http://<host-php>/?_route=plugin/wga_sendMessage&phone=[[phone]]&message=[[text]]&secret=...`. Plugin "Alternative WhatsApp Gateway" meneruskannya ke server WA. Setelah cutover, aplikasi PHP tidak ada lagi. Karena itu `wa_url` tidak boleh dibiarkan.

`gobill import` ikut membawa `alt_wga_server_url`, `alt_wga_device_id`, `alt_wga_username`, dan `alt_wga_password`. gobill memakainya untuk mengirim langsung ke server WA. Selama `alt_wga_server_url` terisi, `wa_url` diabaikan.

Setelah impor:

1. Buka **Pengaturan > Integrasi**. Cek keempat isian "WhatsApp (server WA)" sudah terisi. Password tampil kosong, dan itu normal.
2. Kosongkan `wa_url` yang masih menunjuk ke plugin PHP, lalu simpan.
3. Pastikan server WA bisa dijangkau dari STB baru. Alamat `127.0.0.1` hanya benar jika server WA berjalan di STB yang sama.
4. Klik "Kirim pesan uji" ke nomor Anda sendiri.

Lihat [integrasi](integrations.md#whatsapp).

## Checklist jalan paralel

Sebelum jalan paralel, set `notify_customers` = `no` di **Pengaturan > Notifikasi**. Dengan begitu pelanggan tidak menerima pesan dari dua sistem sekaligus. Alert operator tetap berjalan. Lihat [integrasi](integrations.md#sakelar-pesan).

1. Uji CoA Disconnect di MikroTik, setelah perbaikan NAS-IP-Address.
2. Uji jalur FreeRADIUS REST. Arahkan `connect_uri` server uji ke gobill di instance uji, lalu jalankan `freeradius -X`.
3. Uji login voucher hotspot lewat RADIUS dan pembatas MAC.
4. Impor dump produksi terbaru ke STB. Bandingkan pelanggan aktif, kedaluwarsa, dan saldo dengan sistem lama.
5. Jalankan paralel selama 1 sampai 3 hari dalam mode baca, yaitu accounting saja. Bandingkan sesi dan kedaluwarsa.
6. Build ARM dan uji di STB: RAM, start tanpa RTC, listrik padam, dan backup ke USB.
7. Bersihkan sisa uji di router. Lihat [setup MikroTik](mikrotik.md#pelajaran-dari-uji-lapangan).

## Satu STB bersama PHPNuxBill, FreeRADIUS, dan server WA

Jika gobill dipasang di STB yang sudah menjalankan PHPNuxBill, FreeRADIUS, dan server WA, misalnya di `192.168.99.2`, tidak perlu memasang ulang apa pun untuk WhatsApp. Server WA tetap dipakai. gobill hanya mengirim ke sana. Yang perlu diperhatikan adalah bentrok port.

1. **Port HTTP.** Bawaan gobill adalah `:8080`. Jika port itu sudah dipakai, set `GOBILL_HTTP` ke port lain di `/etc/gobill/config.env`, misalnya `GOBILL_HTTP=:8090`. Cek dulu dengan `ss -ltn`.
2. **Port RADIUS.** FreeRADIUS sudah memakai UDP 1812 dan 1813. Selama FreeRADIUS tetap menjadi server RADIUS lewat `/radius.php`, set `GOBILL_RADIUS=off`. Jika suatu saat MikroTik diarahkan langsung ke gobill, matikan FreeRADIUS dulu, lalu aktifkan RADIUS gobill.
3. **Allow-list `/radius.php`.** FreeRADIUS di host yang sama memanggil lewat loopback. Loopback diizinkan oleh `radius_rest_allow` yang kosong. Tidak perlu diisi.
4. **Server WA.** `alt_wga_server_url` hasil impor, yaitu `http://127.0.0.1:3030`, langsung benar karena server WA ada di STB yang sama. Kosongkan `wa_url` yang berisi plugin PHP, lalu kirim pesan uji ke nomor Anda sendiri.
5. **Notifikasi selama paralel.** `notify_customers` = `no` sampai cutover. PHPNuxBill tetap mengirim pesan ke pelanggan.

## Cutover dan rollback

### Cutover

1. Hentikan perubahan di sistem lama dalam jendela singkat. Ambil backup JSON terakhir, atau dump MySQL.
2. Impor ke gobill lewat UI atau `gobill import --json=...`. Cek laporannya. Pastikan `notify_customers` masih `no`.
3. Pindahkan RADIUS. Ubah `connect_uri` FreeRADIUS ke gobill, atau ubah `address` dan `secret` pada `/radius` MikroTik ke gobill.
4. Matikan notifikasi di PHPNuxBill, atau hentikan cron-nya. Lalu set `notify_customers` = `yes` di gobill. Urutan ini mencegah pesan ganda.
5. Pantau log RADIUS, sesi aktif, dan halaman Status Sistem selama beberapa jam.

### Rollback

Kembalikan `connect_uri` atau `address` pada `/radius` ke entri lama. Data sistem lama tidak berubah. Namun transaksi yang terjadi di gobill setelah cutover tidak ikut kembali. Catat transaksi itu, dan masukkan manual jika perlu.

## Lihat juga

- [Instalasi](installation.md): pemasangan gobill.
- [Integrasi](integrations.md): WhatsApp, sakelar pesan, dan gateway.
- [Setup MikroTik](mikrotik.md): RADIUS di router.
- [FreeRADIUS lewat REST](freeradius-rest.md): jalur REST.
- [Backup dan restore](backup-restore.md): pengaman impor dan restore.
