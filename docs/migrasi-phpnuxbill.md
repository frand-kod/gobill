# Migrasi dari PHPNuxBill

Untuk operator yang pindah dari PHPNuxBill (MySQL) ke NuxBill Go. Impor dilakukan satu kali dengan perintah `nuxbill import`; sistem lama tidak diubah.

## Lewat UI

Tanpa terminal: SuperAdmin buka Pengaturan > Miscellaneous > Import PHPNuxBill (`/admin/settings/miscellaneous/import`). Pakai backup JSON seperti di bawah.

1. Unggah file backup JSON (wajib) dan `system/uploads/notifications.json` (opsional), lalu klik **Periksa**. Tidak ada data yang berubah. Laporan menampilkan per tabel: jumlah dibaca, akan diimpor, dan dilewati beserta alasannya, serta jumlah data gobill saat ini.
2. Klik **Impor sekarang**. Karena selalu ada admin, konfirmasi dulu: centang "Saya mengerti data akan ditimpa".
3. Sebelum impor, gobill membuat backup database otomatis di `NUXBILL_BACKUP_DIR` (bawaan folder `backup` di samping database), namanya `nuxbill-YYYYMMDD-HHMMSS-pre-import.db`. Bila backup gagal, impor tidak dijalankan.
4. Data lama ditimpa, termasuk admin. Anda lalu diarahkan ke halaman login: masuk dengan akun admin PHPNuxBill lama dan password yang sama. Password sha1 lama diganti ke bcrypt saat login pertama.
5. Setelah impor selesai, hapus file backup JSON dari komputer Anda, karena isinya password lama.

File yang diunggah maksimal 200 MB. Upload yang belum dikonfirmasi dihapus otomatis setelah 30 menit, atau saat dibatalkan.

## Perintah impor

Cara yang disarankan: backup JSON dari PHPNuxBill, tanpa akses MySQL.

1. Di PHPNuxBill buka Pengaturan > Database Status, centang semua tabel (minimal `tbl_customers`, `tbl_plans`, `tbl_bandwidth`, `tbl_routers`, `tbl_pool`, `tbl_user_recharges`, `tbl_voucher`, `tbl_users`, `tbl_appconfig`), lalu klik Backup.
2. Jalankan:

        nuxbill import --json=/path/phpnuxbill_backup.json --db=./nuxbill.db [--timezone=Asia/Jakarta] [--dry-run] [--force] [--notifications=/path/phpnuxbill/system/uploads/notifications.json]

File backup berisi password pelanggan dan secret router dalam bentuk lama (plain/legacy). Simpan file itu hanya untuk Anda, dan hapus setelah impor.

Alternatif lewat MySQL langsung (pilih salah satu, `--json` atau `--mysql-dsn`):

    nuxbill import --mysql-dsn='<user>:<password>@tcp(127.0.0.1:3306)/phpnuxbill' --db=./nuxbill.db [--timezone=Asia/Jakarta] [--dry-run] [--force] [--notifications=/path/phpnuxbill/system/uploads/notifications.json]

- Tabel yang tidak ada di file backup (atau di database) dilewati dan dicatat di laporan sebagai "tidak ada di sumber, dilewati". Impor tidak gagal karena itu.
- Semua berjalan dalam satu transaksi SQLite. Target harus kosong (`--force` menghapus isinya).
- `--dry-run` hanya membuat laporan: baris dibaca, diimpor, dilewati, beserta alasannya.
- Zona waktu bawaan diambil dari setting lama (atau `Asia/Jakarta`).
- Template pesan (expired, reminder 7/3/1 hari, invoice, selamat datang, saldo) disimpan PHPNuxBill di file `system/uploads/notifications.json`, bukan di MySQL. Tambahkan `--notifications=<path file itu>` agar ikut diimpor ke setting `notif_*`; tanpa opsi ini (atau bila file tidak ada) pesan memakai template bawaan bahasa Inggris, dan laporan impor mencatatnya. Hanya template yang punya padanan di NuxBill Go yang diimpor (`email_invoice` dilewati). Placeholder yang tidak dikenal dikirim sebagai teks kosong, tidak pernah `[[...]]` mentah; `[[payment_link]]` dan `[[invoice_link]]` berisi tautan ke portal (butuh setting `app_url`; pelanggan login dulu).
- Pakai `NUXBILL_SECRET_KEY` yang sama dengan yang akan dipakai produksi (atau biarkan membuat `<db>.key`, lalu backup).

## Yang dikonversi

Setting, admin, router, bandwidth, IP pool, paket, pelanggan, langganan aktif, transaksi, voucher, log, dan NAS.

- Password admin sha1 ditandai `legacy_sha1` dan diganti ke bcrypt saat login pertama.
- Password pelanggan dan secret perangkat dienkripsi ulang (bcrypt / AES-GCM).
- Waktu expiry diubah ke Unix UTC; uang menjadi INTEGER rupiah. Harga yang gagal dikonversi dilewati dan dilaporkan.
- Plan `RadiusRest` menjadi plan `Radius` ([freeradius-rest.md](freeradius-rest.md)).
- Belum diimpor: kupon, ODP, dan inbox.
- Atribut pelanggan (`tbl_customers_fields`) diimpor sebagai kolom kustom dengan nama yang sama (tampil di form pelanggan). NuxBill Go ikut memakainya seperti PHP:
  - `<nama> Bill` (mis. `Router Bill`): tagihan tambahan, ditambahkan ke harga setiap recharge (admin, saldo, voucher, gateway, auto-renew). Nilai `biaya:sisa` (mis. `50000:3`) adalah cicilan: berkurang satu setiap recharge, berhenti saat sisa 0.
  - `Invoice`: harga tagihan paket Period berikutnya (menggantikan harga paket), diisi otomatis setelah recharge Period seperti PHP.
  - `Expired Date`: tanggal jatuh tempo paket Period per pelanggan; masuk ke `billing_day` pelanggan (bukan kolom kustom). Nilai yang bukan angka 1-31 dilewati dan dilaporkan.
  - Beda: pembayaran gateway online memakai harga paket + tagihan saat pesanan dibuat; kupon hanya mendiskon harga paket, tagihan ditambahkan di atasnya.
- Kolom lama yang sengaja dibuang: `account_type`, kota/kecamatan/provinsi/kode pos, `price_old`, `plan_type`.

## Verifikasi yang sudah dilakukan

Import diuji dengan dump produksi asli: semua tanggal expiry dan total transaksi cocok dengan sistem lama. Ulangi pembandingan itu pada dump terbaru Anda sebelum cutover.

## WhatsApp setelah cutover

Di sistem lama, `wa_url` menunjuk ke aplikasi PHP sendiri (`http://<host-php>/?_route=plugin/wga_sendMessage&phone=[[phone]]&message=[[text]]&secret=...`) dan plugin "Alternative WhatsApp Gateway" meneruskannya ke server WA. Setelah cutover aplikasi PHP tidak ada lagi, jadi `wa_url` itu tidak boleh dibiarkan.

`nuxbill import` ikut membawa `alt_wga_server_url`, `alt_wga_device_id`, `alt_wga_username`, `alt_wga_password`. NuxBill memakainya untuk mengirim langsung ke server WA, dan mengabaikan `wa_url` selama `alt_wga_server_url` terisi.

Setelah import:
1. Buka Pengaturan > Integrasi, cek keempat isian "WhatsApp (server WA)" sudah terisi (password tampil kosong, itu normal).
2. Kosongkan `wa_url` yang masih menunjuk ke plugin PHP, lalu simpan.
3. Pastikan server WA bisa dijangkau dari STB baru (alamat `127.0.0.1` hanya benar jika server WA berjalan di STB yang sama).
4. Klik "Kirim pesan uji" ke nomor sendiri.

## Checklist jalan paralel

1. Uji CoA Disconnect di MikroTik (setelah perbaikan NAS-IP-Address).
2. Uji jalur FreeRADIUS REST: arahkan `connect_uri` server uji ke NuxBill di instance uji, jalankan `freeradius -X`.
3. Uji login voucher hotspot lewat RADIUS dan pembatas MAC.
4. Import dump produksi terbaru ke STB, bandingkan pelanggan aktif, expiry, dan saldo dengan sistem lama.
5. Jalankan paralel 1-3 hari dalam mode baca (accounting saja), bandingkan sesi dan expiry.
6. Build ARM dan uji di STB: RAM, startup tanpa RTC, listrik padam, backup ke USB.
7. Bersihkan sisa uji di router: `split-user-domain=no`, hapus entry `/radius` uji, profil dan user uji ([mikrotik.md](mikrotik.md#pelajaran-dari-uji-lapangan)).

## Cutover dan rollback

**Cutover:**
1. Hentikan perubahan di sistem lama (jendela singkat), ambil dump MySQL terakhir.
2. `nuxbill import` ke database baru, cek laporan, lalu start NuxBill.
3. Pindahkan RADIUS: ubah `connect_uri` FreeRADIUS ke NuxBill, atau ubah `address` dan `secret` pada `/radius` MikroTik ke NuxBill.
4. Pantau log RADIUS dan sesi aktif beberapa jam.

**Rollback:** kembalikan `connect_uri` atau `/radius address` ke entri lama. Data sistem lama tidak berubah, tetapi transaksi yang terjadi di NuxBill setelah cutover tidak ikut kembali. Catat dan masukkan manual bila perlu.
