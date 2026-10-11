# Audit alur UX (edisi v0.1.5)

**Ringkasan:** Audit ulang per alur, dicek langsung ke kode `internal/web` dan `web/templates`. Sebagian besar temuan lama sudah beres. Yang tersisa: isi ulang massal, akses Admin ke pengaturan pembayaran, 2FA yang tersembunyi, dan label yang bentrok. Yang belum bisa dipastikan dari kode ditandai "perlu dicek di browser".

**Untuk:** pengembangan dan operator yang menguji alur sebelum rilis.

## Alur operator dan admin

### A1. Login pertama dan setup awal
Siapa: SuperAdmin. Pemicu: instalasi baru.

1. Buka `/login`. Password awal tidak tampil di layar. Ia ada di file `initial-admin-password.txt` di folder data.
2. Masuk, lalu ganti password lewat dropdown profil (2 ketukan). Tidak ada paksaan ganti.
3. Dashboard menampilkan **Mulai cepat**: router atau NAS, bandwidth, paket, lalu pelanggan atau voucher.
4. WA dan QRIS tidak ada di checklist. Keduanya ada di tab **Integrations** dan **Payment Gateway**, yang khusus SuperAdmin.

Titik gagal: Admin tidak bisa mengisi WA atau QRIS. Admin juga tidak tahu dari mana password awal berasal.

### A2. Tambah pelanggan
Siapa: Agent, Sales, Admin, SuperAdmin. Pemicu: pelanggan baru datang.

1. Tombol **Add Customer** di dashboard, atau menu **Customer** lalu **Add** (1-2 ketukan).
2. Form berisi 16 kolom dalam 4 bagian. Tiga kolom wajib: Username, Password, Nama Lengkap.
3. Simpan. Halaman pindah ke detail pelanggan dengan pesan untuk memilih paket. Ini sudah beres.
4. Pilih paket, **Recharge**, lalu **Recharge** lagi di dialog. Total sekitar 6 ketukan.

Titik gagal: kolom PPPoE dan Router Secret tampil tanpa penjelasan untuk operator baru.

### A3. Isi ulang satu pelanggan
Siapa: Agent, Sales, Admin, SuperAdmin. Pemicu: pelanggan bayar tunai atau minta perpanjang.

1. Dari detail pelanggan: cari nama (1-2), pilih paket dan metode di kartu **Recharge Account**, lalu **Recharge** (1). Dialog ringkasan muncul, tap **Recharge** lagi. Sekitar 5 ketukan.
2. Jalur dashboard: picker pelanggan, paket, **Continue**, **Recharge**. Sekitar 6 ketukan.

Titik gagal: saldo kurang membuat tombol nonaktif, dengan tautan ke **Add Balance**. Jika router gagal, pesan kuning meminta **Sync**. Arti "Cash" dan "Balance" tidak dijelaskan.

### A4. Perpanjang banyak pelanggan sekaligus
Siapa: Admin, SuperAdmin. Pemicu: akhir bulan, banyak paket habis bersamaan.

1. Tidak ada isi ulang massal. Daftar pelanggan hanya punya **Send message to selected**. Daftar langganan hanya punya **Extend** per baris, dan itu menambah hari gratis tanpa pembayaran.
2. Untuk 20 pelanggan, operator mengulang A3. Sekitar 100 ketukan.

Titik gagal: tidak ada ringkasan total uang sebelum simpan.

### A5. Pelanggan bilang "internet mati"
Siapa: Admin, Agent. Pemicu: telepon atau WA.

1. Ketik nama di palette, lalu tap hasil (2 ketukan).
2. Strip diagnosa di atas: Akun, Paket, Koneksi, Kuota, Router, dan satu saran.
3. Untuk PPPoE atau hotspot API, chip Koneksi tampil "No data" kecuali **Check Customer Online** aktif.
4. Tombol **Sync** (1).

Titik gagal: hanya satu saran yang tampil, walau ada dua masalah. Chip "No data" tidak menjelaskan cara mengaktifkan pengecekan.

### A6. Cetak voucher
Siapa: Agent, Sales, Admin. Pemicu: pelanggan baru tanpa akun.

1. **Make vouchers** di dashboard (1), atau menu **Voucher** lalu **Add** (2).
2. Pilih paket (1), isi jumlah (1). **Print Now** sudah tercentang. Simpan (1).
3. Format dan panjang kode ada di **Advanced options**, yang terlipat.

Sekitar 4 ketukan. Titik gagal: dropdown paket kosong tanpa tautan ke **Service Plan**. Halaman cetak di HP perlu dicek di browser.

### A7. Siapa habis hari ini dan minggu ini, lalu ingatkan
Siapa: Admin, Agent, Sales. Pemicu: rutinitas pagi.

1. Kartu **Expiring Soon (next 7 days)** ada di dashboard, di bawah kartu jaringan.
2. Daftar berisi 20 baris terdekat, tanpa pemisah "hari ini".
3. Ikon WA (1) membuka chat dengan teks siap kirim. Ikon ini hanya muncul jika nomor valid.
4. Ikon dompet (1) membuka detail pelanggan untuk isi ulang.

Titik gagal: pelanggan dengan nomor salah tidak punya ikon WA, dan layar tidak menjelaskan kenapa.

### A8. Siapa online dan putuskan
Siapa: SuperAdmin, Admin. Pemicu: pelanggan minta diputus.

1. Menu **Online Sessions** (1), langsung di sidebar.
2. Cari (1 isian), lalu **Disconnect** (1) dan konfirmasi (1).

Titik gagal: hanya sesi RADIUS yang tampil. Pelanggan PPPoE dan hotspot tidak muncul, dan layar tidak bilang begitu. Agent dan Sales tidak melihat menu ini.

### A9. Isi saldo pelanggan
Siapa: Agent, Sales, Admin, SuperAdmin. Pemicu: pelanggan bayar tunai untuk saldo.

1. Tombol **Add balance** di detail pelanggan (1). Nama pelanggan sudah terisi.
2. Atau menu **Add balance** (1), lalu cari pelanggan (1-2).
3. Pilih paket saldo atau isi jumlah (1), lalu simpan (1).

Titik gagal: satu aksi punya tiga nama. Menu memakai "Tambah saldo", judul halaman "Isi Saldo", dan tombol dialog "Add Balance".

### A10. Ubah paket dan harga
Siapa: SuperAdmin, Admin. Pemicu: harga naik. **Plan setup**, **Service Plan**, pilih baris, ubah harga, simpan. Sekitar 4 ketukan. Titik gagal: form tidak bilang apakah harga baru berlaku untuk langganan aktif. Perlu dicek di browser.

### A11. Pembayaran Tripay sudah lunas
Siapa: pelanggan bayar, Admin cek. Pemicu: pelanggan bilang sudah bayar tapi paket belum aktif.

1. Pelanggan bayar lewat portal. Callback `/callback/tripay` mengisi paket otomatis.
2. Admin: grup **Admin**, **Payment Gateway**, **Payment Audit**, lalu filter status.

Titik gagal: halaman Payment Gateway hanya berisi satu baris "Tripay: Enabled". Tidak ada tombol cek ulang untuk admin.

### A12. Pembayaran QRIS manual di luar sistem
Siapa: Admin. Pemicu: pelanggan bayar langsung ke QRIS.

1. Admin mencatat isi ulang dengan metode Cash (A3).
2. Tidak ada status "menunggu bayar" dan tidak ada tombol "tandai lunas". Kode tidak punya fitur itu.
3. Link QRIS bernominal ikut terkirim di pesan invoice (`internal/billing/recharge.go`, `internal/notify/notify.go`). Perlu dicek di browser: pesan ini datang sebelum atau sesudah paket aktif.

Titik gagal: pelanggan bisa menerima link bayar setelah paket sudah aktif. Admin tidak bisa menandai pembayaran yang belum tercatat.

### A13. Cari apa saja
Siapa: semua peran. Pemicu: cari pelanggan atau menu.

1. Tekan `/` atau Ctrl+K, atau klik kolom cari di header (1). Ketik. Hasil dikelompokkan dan sudah difilter menurut peran.
2. Tap hasil (1).

Titik gagal: tidak ada hasil yang disorot otomatis. Enter tanpa panah pindah ke halaman cari biasa.

### A14. Pengaturan
Siapa: SuperAdmin penuh, Admin sebagian. Pemicu: ganti nama usaha, tarif, atau kanal kirim.

1. Menu **Settings** (1). Enam tab: General, Localisation, Notifications, Integrations, Payment Gateway, Miscellaneous.

Titik gagal: Admin tidak melihat Integrations dan Payment Gateway. Batas sesi dan backup ada di Miscellaneous.

### A15. Backup, restore, dan import
Siapa: backup untuk Admin, restore dan import untuk SuperAdmin. Pemicu: sebelum upgrade atau pindah dari PHPNuxBill.

1. **Settings > Miscellaneous**: link **Database backup**, lalu restore dan import di bagian System, dengan pratinjau dan konfirmasi.

Titik gagal: backup lama hanya terlihat di halaman Status.

### A16. Pemantauan dan alert
Siapa: Admin. Pemicu: alert dari Telegram atau WA.

1. **Admin > System Status** (2 ketukan).
2. Halaman memuat ulang seluruh layar setiap 30 detik.
3. Alert dikirim ke kanal di **Operator alert channel**. Aplikasi tidak punya daftar alert, selain Activity Log di dashboard.

Titik gagal: reload penuh bisa menutup dropdown atau form yang sedang diisi.

### A17. Ringkasan harian dan logout
Siapa: semua peran. Pemicu: laporan harian, atau meninggalkan layar.

1. Ringkasan: **Settings > Notifications**, tombol **Send summary now**. Tidak ada waktu kirim terakhir di layar. Perlu dicek di browser.
2. Logout di dropdown profil (2). Sesi admin habis setelah tidak aktif, default 120 menit, dicek saat permintaan berikutnya.

Titik gagal: tidak ada peringatan sebelum sesi habis. Operator mengisi form panjang, klik simpan, lalu diminta login. Isian hilang.

### A18. Mengaktifkan 2FA
Siapa: semua admin. Pemicu: ingin keamanan tambahan.

1. Halaman `/admin/2fa` tidak punya tautan di menu, dropdown profil, atau halaman ganti password. Operator harus mengetik URL.
2. **Enable 2FA**, pindai kode, masukkan kode konfirmasi, lalu simpan kode pemulihan.

Titik gagal: fitur keamanan tidak ditemukan.

## Alur pelanggan

### P1. Daftar akun
Pemicu: pelanggan baru membuka portal.

1. `/portal/login`, lalu **Register** (1).
2. Isi Username, Nama, Email, Alamat, Password, dan Konfirmasi. Nomor HP dan OTP muncul jika aktif.
3. Kirim, lalu login.

Titik gagal: kolom Nama, Email, dan Alamat tampil walau tidak wajib. Tidak ada tanda bintang. Username bentrok baru ketahuan setelah kirim.

### P2. Login portal dan hotspot
1. `/portal/login`: isi Username dan Password (2), lalu **Sign in** (1). Ada tombol Show dan peringatan Caps Lock.
2. Login hotspot ditampilkan oleh MikroTik, bukan oleh gobill. Perlu dicek di browser.
3. Jika registrasi mati, halaman login memuat form aktivasi voucher.

Titik gagal: pelanggan bingung antara username portal dan username PPPoE.

### P3. Lupa password atau username
1. **Lupa Kata Sandi** (1), isi username (1), kirim OTP (1), isi kode (1), password baru dua kali (2).
2. **Lupa Username** memakai email atau HP. Pesan balasan tidak membuka akun terdaftar. Ini sudah benar.

Titik gagal: pelanggan tidak tahu nomor mana yang dipakai untuk OTP.

### P4. Beli paket
1. **Buy / Extend** di dashboard (1), atau **Order Package** di menu (1).
2. Setiap kartu paket punya kupon opsional. Lalu **Pay with Balance** (1) dan konfirmasi (1), atau pilih kanal (1) lalu **Pay Now** (1).
3. Tripay membuka halaman bayar. QRIS dari WA membuka halaman bayar tanpa tombol "saya sudah bayar".

Titik gagal: saldo kurang membuat tombol nonaktif dengan hint, tetapi tidak ada tautan ke isi saldo.

### P5. Tukar voucher
1. Tombol **Voucher Activation** di dashboard (1), hanya jika diaktifkan. Tombolnya kecil dan berwarna ghost.
2. Isi kode (1), lalu kirim (1).

Titik gagal: pesan "Voucher Not Valid" tidak membedakan salah ketik, sudah terpakai, atau kedaluwarsa. Perlu dicek di browser.

### P6. Cek sisa waktu dan kuota
Dashboard langsung menampilkan **Active Plan** dengan hitung mundur, dan **Usage** dengan meter. Tidak ada klik. Titik gagal: tidak ada kalimat seperti "Paket habis 3 hari lagi".

### P7. Pengingat dan pemberitahuan habis
1. Pelanggan menerima WA, SMS, atau email. Inbox di portal menampilkan badge belum dibaca.

Titik gagal: jika `app_url` kosong, tautan bayar di pengingat ikut kosong. Admin tidak diberi peringatan.

### P8. Ganti password dan profil
1. **Profile** (1), atau **Change Password** di dashboard (1), yang langsung ke `#password`.
2. Isi password lama, baru, dan konfirmasi (3 isian), lalu simpan (1).

Titik gagal: aturan minimal 8 karakter baru muncul setelah kirim.

### P9. Kirim saldo ke pelanggan lain
1. Kartu **Send Balance** ada di bawah kartu-kartu lain di dashboard. Perlu gulir.
2. Ketik username (1 isian), isi jumlah (1), lalu **Send Balance** (1) dan konfirmasi (1).

Titik gagal: nama penerima tidak dicek sebelum kirim. Salah ketik berarti saldo masuk ke orang lain.

## Lintas alur

- **Navigasi dan mobile:** sidebar, drawer, dan palette sudah baik. Grup **Plan setup** tampil untuk Sales dengan satu item.
- **Empty state dan pesan error:** checklist dan pesan router sudah baik. Halaman lain hanya "No data". Beberapa pesan masih Inggris.
- **Istilah:** "Expires" jadi "Kedaluwarsa". Saldo punya tiga nama. "Payment Gateway" dipakai di menu dan di Pengaturan.
- **Aksesibilitas:** dialog dan fokus sudah ditangani. Tidak ada tautan lewati ke konten.
- **Kesan kecepatan:** daftar live lancar. Status memuat ulang seluruh halaman.

## Sudah beres dari audit lama

Isi ulang dengan pesan router, tambah pelanggan ke detail, voucher, dashboard aksi cepat, diagnosa, picker pelanggan, konfirmasi uang, dan kontak operator di portal.

Masih terbuka: menu portal membungkus, Online Sessions untuk manajer saja, dan kolom masa berlaku di daftar pelanggan.

## Temuan

| ID | Alur | Masalah | Dampak | Usaha | Usulan |
|---|---|---|---|---|---|
| T01 | A4 | Tidak ada isi ulang massal | Tinggi | M | Aksi "Isi ulang terpilih" dengan ringkasan total |
| T02 | A1 | Checklist tidak memuat WA, QRIS, Tripay | Tinggi | S | Tambah langkah dengan tautan |
| T03 | A1 | Admin tidak bisa lihat atau atur WA dan QRIS | Tinggi | S | Status read-only untuk Admin |
| T04 | A1, login | Tidak ada jalur reset password admin | Tinggi | M | Perintah CLI reset, dan teks di layar login |
| T05 | A18 | 2FA tanpa tautan di UI | Tinggi | S | Item "Keamanan akun" di dropdown profil |
| T06 | A7 | Expiring tanpa pemisah "hari ini" | Sedang | S | Pisahkan "Hari ini" dan "2-7 hari" |
| T07 | A5 | Koneksi "No data" untuk non-RADIUS, hanya satu saran | Sedang | S | Tampilkan semua saran, dan cara aktifkan pengecekan |
| T08 | A2, A9 | "Expires" jadi "Kedaluwarsa", saldo punya tiga nama | Sedang | S | "Berakhir" untuk tanggal, satu nama "Tambah saldo" |
| T09 | A10 | Label "Deactivate" jadi "Menonaktifkan paket" | Sedang | S | Cek efeknya di browser, lalu perjelas label |
| T10 | A2, A3 | Daftar pelanggan tanpa kolom masa berlaku | Sedang | S | Kolom "Berakhir" |
| T11 | A12 | QRIS manual tanpa status menunggu atau tandai lunas | Sedang | M | Dokumentasikan alur, lalu tambah status |
| T12 | A11 | Payment Gateway ganda, tanpa cek ulang | Sedang | S | Satu nama, dan tombol cek status |
| T13 | A16 | Status memuat ulang seluruh layar tiap 30 detik | Sedang | S | Ambil `status.json`, perbarui angka saja |
| T14 | A8 | Online Sessions hanya RADIUS dan hanya manajer | Sedang | S | Teks kosong yang menjelaskan cakupan |
| T15 | A17 | Sesi habis tanpa peringatan, isian hilang | Sedang | M | Peringatan beberapa menit sebelum habis |
| T16 | A9, pesan | Dropdown pelanggan hanya 500 terbaru, tidak bisa dicari | Sedang | S | Pakai picker pelanggan yang sudah ada |
| T17 | P4 | Saldo kurang tanpa tautan ke isi saldo | Sedang | S | Tautan dari kartu paket |
| T18 | Lintas | Menu portal membungkus jadi beberapa baris | Sedang | S | Tab bawah atau menu ringkas |
| T19 | P9 | Penerima saldo tidak dicek sebelum kirim | Sedang | M | Tampilkan nama penerima sebelum kirim |
| T20 | P2 | Login hotspot di luar gobill, belum diuji | Sedang | M | Uji di MikroTik, lalu tulis panduannya |
| T21 | P7 | Tautan pengingat kosong jika `app_url` kosong | Sedang | S | Peringatan di Pengaturan dan saat simpan |
| T22 | A15 | Backup lama hanya terlihat di Status | Sedang | S | Banner di dashboard jika lebih dari 36 jam |
| T23 | P1 | Kolom wajib dan opsional tampil sama, tanpa tanda | Rendah | S | Sembunyikan sesuai pengaturan wajib |
| T24 | P8, A16 | Teks hardcoded: "min ago", "Backup", "account is not active" | Rendah | S | Pindahkan ke terjemahan |
| T25 | A1 | Tombol "Sembunyikan" checklist tidak berfungsi | Rendah | S | Hapus tombol, atau tambah handler |
| T26 | A13 | Enter tanpa pilihan pindah ke halaman biasa | Rendah | S | Sorot hasil pertama otomatis |
| T27 | A6 | Dropdown paket kosong tanpa tautan | Rendah | S | Tautan ke Service Plan |
| T28 | Lintas | Tidak ada tautan lewati ke konten | Rendah | S | Tambah skip link di `base.html` |

Jumlah: Tinggi 5, Sedang 17, Rendah 6. Total 28.

## Top 10 berikutnya

1. T05: tautan 2FA di dropdown profil (Tinggi, S).
2. T02: WA, QRIS, dan Tripay masuk checklist (Tinggi, S).
3. T03: Admin bisa melihat status WA dan QRIS (Tinggi, S).
4. T01: isi ulang massal dengan ringkasan total (Tinggi, M).
5. T04: reset password admin lewat CLI, dengan teks di login (Tinggi, M).
6. T06: pisahkan "Hari ini" di kartu Expiring (Sedang, S).
7. T07: diagnosa non-RADIUS dan semua saran (Sedang, S).
8. T08: rapikan istilah "Expires" dan saldo (Sedang, S).
9. T10: kolom masa berlaku di daftar pelanggan (Sedang, S).
10. T17: tautan isi saldo dari kartu paket portal (Sedang, S).

## Lihat juga

- [Audit UX](audit-ux.md): audit per layar.
- [Paritas UI](paritas-ui.md): kesesuaian dengan PHPNuxBill.
- [Panduan menulis docs](panduan-menulis-docs.md): aturan gaya dokumen ini.
