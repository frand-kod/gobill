# Audit Alur Tugas dan Arsitektur Informasi (UX Flow Audit)

Cakupan: alur kerja operator (RT-RW-net, sering lewat HP) dan pelanggan (Android low-end). Bukan audit per layar (itu sudah di `docs/internal/audit-ux.md`). Read-only, tanpa perubahan kode. Hitungan klik = ketukan dari layar setelah login sampai selesai; menu HP dihitung 1 ketukan (hamburger) + 1 (item).

**Untuk:** pengembangan. Hanya tersedia dalam bahasa Indonesia.

Ringkasan: mesinnya lengkap, tetapi tidak ada "jalan pintas" antar tugas. Dashboard hanya laporan (tanpa tombol aksi), pembuatan pelanggan berakhir di daftar (bukan di tombol Isi Ulang), dan tidak ada panduan urutan setup. Kebanyakan perbaikan cukup berupa link, urutan, default, dan hint.

---

## 1. Tugas harian operator

### 1.1 Isi ulang / perpanjang pelanggan
Alur: cari nama (kolom search atas, `app.html:88`) -> daftar `/admin/customers` -> klik nama -> kartu "Isi Ulang Akun" (`customer.html:42-55`) -> pilih paket + metode -> Recharge -> halaman konfirmasi (`recharge_confirm.html`) -> Recharge.

| Langkah sekarang | Langkah usulan |
|---|---|
| 1 buka menu (HP) + 1 ketik di search + 1 Enter + 1 klik nama + 1 pilih paket + 1 pilih metode + 1 Recharge + 1 Recharge lagi = sekitar 8 ketukan, 3 isian | Hasil search 1 baris -> langsung buka detail pelanggan. Paket default = paket aktif terakhir (sekarang opsi pertama, `customer.html:46`). Metode default Cash. Tombol "Perpanjang paket yang sama" 1 ketukan di baris langganan -> konfirmasi -> selesai (4 ketukan) |
| Harus tahu: beda "Cash" vs "Balance" (tanpa penjelasan) | Label "Tunai (bayar di tempat)" dan "Potong saldo pelanggan (saldo Rp X)" |
| Flash "Recharge Successful" muncul walau perintah ke router gagal (`billing/service.go:317-327`: hanya `slog.Error`) | Bila gagal ke router, tampilkan peringatan kuning "Paket tercatat, tapi belum aktif di router. Tekan Sinkronisasi." |

### 1.2 Tambah pelanggan baru + aktifkan paket
Form `custFields` (`customers.go:27-67`): 16 field dalam 4 kartu (Account, Contact, Service & billing, Welcome Message). Wajib: username, password, nama. Setelah simpan redirect ke daftar pelanggan (`customers.go:494`), bukan ke detail.

| Langkah sekarang | Langkah usulan |
|---|---|
| Add -> isi (min. 3 field, tapi layar menampilkan 16) -> Save -> kembali ke daftar -> cari pelanggan baru -> klik -> pilih paket -> Recharge -> konfirmasi = sekitar 9 ketukan | Setelah Save redirect ke `/admin/customers/{id}` dengan flash "Pelanggan dibuat. Pilih paket untuk mengaktifkan" (1 baris perubahan di `customers.go:494`). Jadi 5 ketukan |
| Harus tahu: "Service Type" (default "Others"), "PPPoE Username/IP", "Router Secret", "Billing Day", "Auto Renewal" | Lipat bagian "Service & billing" dan "Welcome Message" di bawah `<details>` "Pengaturan lanjutan". Default Service Type = jenis paket yang paling banyak dipakai; jika "Others" tidak bisa jaringan, jangan jadikan default |
| Tidak ada pilihan paket di form | Opsional: dropdown "Aktifkan paket sekarang" di form (default kosong) |

### 1.3 Generate + cetak voucher
Menu Services -> Voucher -> Add -> form 7 field (`vouchers.go:112-124`) -> Save -> halaman teks kode (`voucher_view.html`) -> Print. `print_now` default mati (`vouchers.go:129`).

| Langkah sekarang | Langkah usulan |
|---|---|
| Sekitar 7 ketukan: menu, Voucher, Add, pilih paket, isi jumlah, (Format, Prefix, Length dibiarkan), Save, Print | Dari dashboard tombol "Buat voucher". Sembunyikan Format/Prefix/Length di `<details>` "Atur bentuk kode". Centang "Cetak sekarang" default aktif (`vouchers.go:129` set `print_now: "1"`). Jadi 4 ketukan |
| Jargon: "Voucher Format", "Length Code", "Prefix" | Hint: "Kode acak 8 huruf besar tanpa 0/O/1/I sudah cukup" |
| Jika belum ada paket, dropdown kosong tanpa penjelasan (`planOptions` enabledOnly) | Empty state: "Belum ada paket aktif. Buat paket dulu" + link |

### 1.4 Siapa yang habis hari ini / minggu ini + ingatkan
Dashboard "Segera Berakhir" (`dashboard.html:45-58`, query `widgets.sql:1-8`): 20 baris terdekat sejak kemarin, tanpa batas "hari ini/minggu ini", tanpa nomor HP, tanpa tombol kirim pesan. Daftar Subscriptions punya filter status dan urut kolom expires, tidak ada filter tanggal (`subscriptions.go:60-64`). Pengingat otomatis ada (Reminder job), tetapi tidak terlihat dari menu.

| Langkah sekarang | Langkah usulan |
|---|---|
| Dashboard -> baca tabel -> klik nama -> "Send Message" -> pilih kanal -> isi pesan -> kirim = 6+ ketukan per orang | Pada tabel dashboard tambah dua tombol per baris: "WA" (link `wa.me/<hp>?text=...`, tanpa gateway) dan "Isi ulang" (ke detail). Judul "Segera Berakhir (7 hari ke depan)" dengan tautan "Lihat semua" ke `/admin/subscriptions?status=active&sort=expires` |
| Pilih pelanggan di `message/send` lewat dropdown 500 nama (`messages.go:73`, tidak bisa dicari) | Tautan dari baris dashboard sudah pakai `?customer=`; cukup pakai itu |
| Tombol massal "Send message to selected" ada di daftar pelanggan, tetapi tidak ada cara menyaring "akan habis" | Tambah filter "Habis dalam 3 / 7 hari" di daftar Subscriptions + bulk "Kirim pesan" |

### 1.5 Siapa online + putuskan
"Online Sessions" ada di grup menu **RADIUS** (`app.html:49-55`), hanya manager, dan hanya menampilkan sesi RADIUS (`radius_sessions.html`). Pelanggan hotspot/PPPoE via API tidak muncul. Tombol Disconnect ada dengan konfirmasi (`radius_sessions.html:24`).

| Langkah sekarang | Langkah usulan |
|---|---|
| Menu -> RADIUS -> Online Sessions -> cari -> Disconnect -> konfirmasi = 5 ketukan, tapi hanya jika perangkat RADIUS | Pindahkan "Online Sessions" ke bawah Pelanggan dengan nama "Sedang Online". Beri teks kosong "Daftar ini hanya untuk pelanggan RADIUS; yang lain lihat di detail pelanggan" |
| Kolom NAS, MAC, Stale, Upload/Download | Sembunyikan MAC/NAS di HP; "Stale" -> "Tidak aktif lagi" dengan hint |

### 1.6 Top-up / deposit saldo
Menu Services -> "Refill Balance" -> ketik username persis (`subscriptions.go:218-226`) -> pilih paket Balance atau isi nominal -> Save.

| Langkah sekarang | Langkah usulan |
|---|---|
| 5 ketukan + mengetik username dari ingatan; salah ketik = error "Customer not found" | Tombol "Isi Saldo" di halaman detail pelanggan (sudah ada dukungan `?customer=` di `depositForm`, tetapi tidak ada tombol yang menuju ke sana; `grep deposit web/templates` hanya ketemu menu). Jadi 3 ketukan tanpa mengetik |
| Dua cara input sekaligus (paket Balance ATAU nominal) membingungkan | Utamakan "Nominal"; paket Balance di `<details>` |
| Menu bernama "Refill Balance", "Recharge Account / Redeem Voucher" (`app.html:20-21`) | Ubah: "Isi Ulang Paket" vs "Tambah Saldo" vs "Tukar Voucher" sebagai tiga item terpisah, dengan ikon berbeda (sekarang dua-duanya ikon wallet) |

### 1.7 Cari pelanggan cepat
Kolom search selalu ada di header dan mencari pelanggan (`app.html:88`): bagus. Kekurangan: mencari hanya di Customer (bukan kode voucher / invoice), hasil selalu daftar penuh (tidak langsung ke detail), dan kolom "Package" di daftar tidak menampilkan masa berlaku, sehingga operator harus membuka detail hanya untuk melihat "habis kapan".

| Langkah sekarang | Langkah usulan |
|---|---|
| Ketik + Enter + klik nama = 3 | Jika hasil tepat 1, redirect langsung ke detail. Tambah kolom "Berlaku s/d" di daftar pelanggan (data sudah ada di `subscriptions`) |

### 1.8 "Internet pelanggan tidak jalan" - ke mana operator melihat
Tidak ada halaman diagnosis. Jejaknya tersebar: detail pelanggan (status, "Connection" hanya jika setting `check_customer_online` = yes, `customers.go:249`, default tidak aktif), Subscriptions (status/expired), Routers (Online/Offline, `routers.go:56`), Logs, tombol Sync/Deactivate.

| Langkah sekarang | Langkah usulan |
|---|---|
| Operator harus menebak: cek langganan, cek router, cek Sync | Di detail pelanggan tambah kartu "Cek cepat" 4 baris dengan tanda centang/silang: Status akun, Paket aktif & masa berlaku, Router paket (Online/Offline + link), Sedang terhubung. Di bawahnya tombol "Sinkronkan ke router" yang diberi hint "Pakai ini jika pelanggan sudah bayar tapi tetap tidak bisa internet" |
| Tombol "Sync" tanpa penjelasan, di antara "Deactivate" dan "Login as Customer" (`customer.html:9-12`) | Label "Sinkronkan ke router"; satukan aksi berbahaya (Deactivate) ke menu "Lainnya" |

---

## 2. Navigasi dan arsitektur informasi

Urutan menu sekarang (`app.html:10-79`): Dashboard, Customer, Send Message | Services (Service Plan, Voucher, Recharge/Redeem, Refill Balance, Coupons, Subscriptions) | Maps (4 item) | Network (Routers, Pool, Bandwidth) | RADIUS (NAS, Online Sessions) | Reports (Daily, Period, Transactions, Payment Gateway, Logs) | Admin Users | Pages, Settings.

Temuan:
- Frekuensi pemakaian tidak tercermin. Tugas harian (isi ulang, voucher, saldo, siapa habis) terkubur di "Services" bersama Plan dan Coupons (jarang). "Maps" (4 item, jarang) berada di atas "Network/Reports".
- Dua item berbeda memakai label ganda "Recharge Account / Redeem Voucher" (`app.html:20`) dan ikon sama dengan Refill Balance.
- Item konfigurasi sekali-pasang (Routers, Pool, Bandwidth, NAS) berada di tengah menu yang sama dengan item harian. "Logs" dan "Payment Gateway" ditaruh di grup "Reports". "Pages" ditaruh sejajar Settings tanpa kejelasan (`app.html:76`).
- Semua grup terbuka secara default (`x-data="{o:true}"`), sehingga drawer HP panjang (sekitar 25 item untuk admin).
- Jargon tanpa hint: NAS, Pool, Bandwidth, RADIUS, Device (kolom plan, `plans.go:68` hanya hint "Empty = by plan type"), Router Secret, Burst, "On Login/On Logout" (skrip MikroTik), Shared Users, ODP, "Coverage". Di `indonesia.json` NAS/Pool/Bandwidth/RADIUS/Burst tidak diterjemahkan (sama dengan Inggris), Router Secret = "Secret Router". Hint ada hanya di sebagian field (`nas.go:29`, `pools.go:26`), tetapi tidak di judul menu / daftar.
- Item berbahaya dicampur: tombol "Deactivate" merah sejajar "Sync" di header detail pelanggan; "Login as Customer"; aksi massal "Delete selected" sejajar aksi aman (`list.html:20`).

Dashboard (`dashboard.html`, `handlers.go:129-157`): 4 tile (pendapatan hari ini/bulan, aktif/habis, jumlah pelanggan), dua grafik, tabel Expiring, pie, Voucher Stock, Job Monitor, Activity Log. **Tidak ada satu pun tombol aksi** (Tambah Pelanggan, Isi Ulang, Buat Voucher, Isi Saldo). Tile "Active / Expired" bukan link (`dashboard.html:14`). Dua tile pendapatan menuju halaman yang sama. Grafik dan Job Monitor memenuhi layar HP sebelum "Segera Berakhir" terlihat. Pesan Warn (clock) muncul bagus di atas.

Usulan susunan menu (urut frekuensi), tanpa fitur baru:
1. Dashboard
2. Pelanggan (Customer) / Sedang Online
3. Isi Ulang / Tukar Voucher (satu halaman, 2 tab) / Isi Saldo
4. Voucher
5. Langganan (Subscriptions)
6. Laporan (Daily, Period, Transactions)
7. Kirim Pesan
8. grup tertutup default "Pengaturan Jaringan": Routers, Bandwidth, Pool, NAS, Maps, ODP, Service Plan
9. grup tertutup "Admin": Admin Users, Payment Gateway, Logs, Pages, Settings

---

## 3. First-run / setup

- Setelah install satu-satunya petunjuk ada di README (password admin di log, README.md:55-56). Di UI tidak ada checklist, wizard, atau kartu "Mulai di sini". Dashboard baru kosong menampilkan "No data" semua.
- Urutan sebenarnya: Router (atau NAS) -> Bandwidth -> Pool (PPPoE) -> Plan -> Pelanggan -> Isi ulang. Tidak ada yang memberi tahu. Menu "Network" memang sebelum "Services" secara logika, tetapi di menu justru Services duluan (`app.html:15` vs `:40`).
- Plan sebelum router: `planFields` (`plans.go:34-58`) memakai `.none()` untuk router, pool, expired plan, sehingga plan bisa disimpan tanpa router; dropdown Bandwidth wajib tetapi kosong jika belum ada bandwidth, tanpa tautan "Buat bandwidth dulu". Akibatnya paket "berhasil" dibuat, lalu isi ulang berjalan (DB tercatat) namun gagal diam-diam di router (hanya log, `service.go:326`).
- Empty state hanya "No data" (`list.html:36`, `customer.html`, `dashboard.html`): tidak ada kalimat "Belum ada X. Buat sekarang" dan tidak ada tombol.
- Router baru: tombol "Test connection" ada di baris (`routers.go:50`), tetapi tidak disorot setelah Save; tidak ada hint "Aktifkan API (port 8728) di MikroTik; buat user khusus". Form Router meminta Host/Port/Username/Password tanpa bantuan (`routers.go:21-27`). Port tidak punya default terlihat.

Usulan minimal: (a) kartu "Mulai cepat" di dashboard yang tampil selama ada hitungan 0, berisi 5 baris bercentang otomatis: "1. Tambah router" -> "2. Atur kecepatan (Bandwidth)" -> "3. Buat paket" -> "4. Tambah pelanggan / buat voucher" -> "5. Tes isi ulang" (hanya query COUNT, tanpa tabel baru); (b) empty state per daftar dengan teks + tombol Add; (c) di form Paket, jika bandwidth kosong tampilkan hint dengan link `/admin/bandwidth/new`; (d) setelah Save router redirect ke daftar dengan flash "Tekan Uji koneksi".

---

## 4. Portal pelanggan

Menu atas (`portal/layout.html:11-17`): Dashboard, Order Package, Order History, Activation History, Inbox, Profile, Logout (7 tautan, `flex-wrap`). Dashboard (`portal/dashboard.html`): saldo, tombol "Voucher Activation" (hanya jika diaktifkan, 1 ketukan), form kirim saldo, pengumuman, tabel langganan dengan "Expires On" dan tombol Extend.

| Tugas | Ketukan dari beranda | Catatan |
|---|---|---|
| Lihat masa berlaku | 0 | Ada di tabel, tapi tabel lebar (4 kolom) dan tanpa kalimat "Paket habis 3 hari lagi"; judul "Active Subscriptions" |
| Beli / perpanjang | 1 (menu Order Package), lalu 2-3 (kupon opsional, pilih kanal, Pay) | Tidak ada tombol "Perpanjang" besar di beranda; kupon dan dropdown kanal tampil di tiap kartu paket (`plans.html:13-17`) |
| Tukar voucher | 1 jika `Voucher` aktif | Tombol kecil ghost di beranda; lebih baik kartu dengan kolom kode langsung di beranda |
| Hubungi operator | Tidak ada | Tidak ada nomor / WhatsApp operator di layout atau beranda, padahal setting `phone` ada (`settings.go:117`) |

Bahasa: banyak istilah teknis atau Inggris campur: "Order Package", "Activation History", "Extend", "Bandwidth: X", "Pay with Balance", "Buy for friend", "Custom Balance", "Input Desired Amount" (beberapa kunci memang memakai pola garis bawah di katalog; terjemahan Indonesia pun kaku, mis. "Memperpanjang" untuk tombol "Extend"). Pelanggan dengan membaca minim lebih terbantu ikon + kata satu-dua: "Beli Paket", "Riwayat", "Pesan", "Bantuan". Tombol "Extend" tanpa penjelasan efeknya (`portal/dashboard.html:14`, hanya bila admin mengaktifkan).

---

## 5. Pesan error dalam alur

Yang baik: halaman 500 memakai bahasa Indonesia dengan referensi (`crud.go:299-307`); stok saldo tidak cukup di portal memberi hint dan menonaktifkan tombol (`plans.html:12-13`); konfirmasi saldo akhir ada di `recharge_confirm.html`.

Yang kurang:
- Router tidak terjangkau saat isi ulang: tidak ada pesan sama sekali ke operator (log saja, `service.go:325-327`). Pelanggan dan operator mengira berhasil.
- Router test gagal: pesan = "Connection failed: nama: <error Go mentah>" (`routers.go:177`), mis. "dial tcp ... i/o timeout"; tidak ada langkah berikutnya (cek IP, port API, firewall).
- "Invalid plan" (`customers.go:~284, 323`) dipakai untuk paket dinonaktifkan, tidak ada, atau router mati; tidak menyebut penyebab ("Paket ini sedang dimatikan. Aktifkan di Service Plan").
- "Insufficient balance" (`recharge_confirm.html:15`): tidak memberi tahu langkah berikut. Tambahkan tombol "Tambah saldo" yang membawa ke `/admin/deposit?customer=` atau ganti metode ke Cash.
- "Action failed" (`subscriptions.go:~241`) dan "Recharge failed" tanpa sebab / saran.
- "Disconnect failed: <err.Error()>" (`radius.go:94`) membocorkan error mentah.
- Portal: "Voucher Not Valid" (`portal_extra.go:~75`) tidak membedakan salah ketik, sudah dipakai, atau kadaluarsa; "Invalid Username or Password" tanpa tautan lupa sandi di pesan (ada tautan di bawah, ok).
- Banyak pesan portal memakai teks error billing apa adanya yang berbahasa Inggris dan kaku ("Target has active plan, different with current plant.", `extras.go:13`).

---

## Temuan menurut dampak

### Tinggi (9)
1. **Setelah Add pelanggan redirect ke daftar** (`customers.go:494`) -> ke detail pelanggan. 1 baris, menghemat 3-4 ketukan di tugas paling sering.
2. **Dashboard tanpa aksi cepat** (`dashboard.html:3-4`). Tambah baris 4 tombol besar: Tambah Pelanggan, Isi Ulang (cari), Buat Voucher, Tambah Saldo.
3. **Kegagalan router saat isi ulang diam-diam** (`billing/service.go:325-327`, `customers.go:326`). Kembalikan flag "device gagal" dari `apply` dan tampilkan flash kuning + saran Sync.
4. **Tidak ada panduan setup / empty state** (`list.html:36`, `dashboard.html`). Kartu "Mulai cepat" + teks kosong dengan tombol Add.
5. **Plan bisa dibuat tanpa bandwidth/router; dropdown kosong tanpa tautan** (`plans.go:34-58`). Hint dengan link ke form bandwidth/router.
6. **"Expiring" tidak bisa ditindaklanjuti** (`dashboard.html:45-58`, `widgets.sql`). Tambah nomor HP + tombol WA/Isi ulang, ubah judul "7 hari ke depan", link ke Subscriptions terurut expires.
7. **Tombol Isi Saldo tidak ada di detail pelanggan** (`customer.html:6-18`; menu `app.html:21`). Tambah link `/admin/deposit?customer=<username>`; utamakan nominal.
8. **Portal tanpa kontak operator** (`portal/layout.html`, `portal/dashboard.html`). Tampilkan `phone`/WhatsApp dari setting di footer dan di pesan error.
9. **Pesan gagal teknis tanpa langkah berikutnya** (`routers.go:177`, `radius.go:94`, `customers.go` "Invalid plan"/"Recharge failed"). Ganti dengan kalimat penyebab + tindakan.

### Sedang (9)
10. Isi ulang butuh 2 konfirmasi + paket default bukan paket sekarang (`customer.html:46`, `recharge_confirm.html`). Default ke paket aktif; tombol "Perpanjang paket yang sama".
11. Urutan / pengelompokan menu (`app.html:15-79`). Pindahkan Plan, Maps, Network, NAS ke grup tertutup default; tugas harian di atas.
12. "Online Sessions" tersembunyi di grup RADIUS dan hanya RADIUS (`app.html:49-55`, `radius_sessions.html`). Pindah ke samping Pelanggan; teks kosong menjelaskan cakupan.
13. Form voucher menampilkan Format/Prefix/Length (`vouchers.go:112-124`); `print_now` mati (`:129`). `<details>` + print_now default 1.
14. Form pelanggan 16 field (`customers.go:27-67`), default Service Type "Others" (`:147`). Lipat bagian lanjutan.
15. Daftar pelanggan tanpa kolom masa berlaku (`customers.go:110`) dan search tidak langsung ke detail (`app.html:88`). Tambah kolom, redirect jika 1 hasil.
16. Tidak ada kartu diagnosis di detail pelanggan (`customer.html`); `check_customer_online` default mati (`customers.go:249`). Kartu "Cek cepat" 4 baris.
17. Label menu ganda dan ikon sama (`app.html:20-21`). Pisahkan tiga item, ikon berbeda.
18. Portal: beranda tanpa tombol Beli/Perpanjang besar dan tanpa kalimat "habis N hari lagi" (`portal/dashboard.html`, `plans.html:13-17`). Tombol "Perpanjang" + kupon di `<details>`.

### Rendah (6)
19. Jargon tanpa hint di daftar/menu (NAS, Pool, Bandwidth, RADIUS, Device, Burst, Shared Users, On Login; `indonesia.json`). Tambah `title`/hint satu kalimat atau ganti label: "Bandwidth" -> "Kecepatan (Bandwidth)", "Pool" -> "Rentang IP (Pool)", "NAS" -> "Router RADIUS (NAS)".
20. Aksi berbahaya sejajar aksi aman (`customer.html:9-12`, `list.html:20`). Pindah ke menu "Lainnya" atau pisah warna/jarak.
21. Tile "Active/Expired" bukan link (`dashboard.html:14`). Link ke Subscriptions?status=expired.
22. Dropdown 500 pelanggan di Send Message tidak bisa dicari (`messages.go:73`). Cukup `<input list>` atau abaikan karena entry lewat `?customer=`.
23. Job Monitor/Activity Log di dashboard HP menambah panjang halaman (`dashboard.html:68-77`). Pindah ke bawah / ringkas.
24. Beberapa teks portal Inggris kaku ("Extend", "Buy for friend", error billing `extras.go:13`). Terjemah dan sederhanakan.

---

## Top-10 perubahan: dampak untuk awam / usaha

1. Redirect setelah Add pelanggan ke halaman detail (+ flash "Pilih paket untuk mengaktifkan") - 1 baris.
2. Empat tombol aksi cepat di dashboard (Tambah Pelanggan, Isi Ulang, Buat Voucher, Tambah Saldo) - template saja.
3. Tombol "Isi Saldo" (dan "Isi Ulang") di detail pelanggan, deposit pakai `?customer=` yang sudah ada - template saja.
4. Peringatan di UI bila perintah router gagal saat isi ulang + hint "Tekan Sinkronisasi" - kecil di `apply`/handler.
5. Tombol WA + nomor HP + "Isi ulang" di tabel "Segera Berakhir"; judul "7 hari ke depan" - template + sedikit query.
6. Empty state "Belum ada X, buat sekarang" + kartu "Mulai cepat" setup berurutan - template, COUNT.
7. Default `print_now` aktif dan sembunyikan Format/Prefix/Length voucher di `<details>` - kecil.
8. Kontak operator (telepon/WA dari setting) di layout portal + tombol besar "Beli / Perpanjang" di beranda portal - template.
9. Atur ulang menu: harian di atas, jaringan/konfigurasi/maps di grup tertutup; pisah "Isi ulang / Tukar voucher / Tambah saldo"; pindahkan "Online Sessions" - template saja.
10. Tulis ulang pesan error teknis (router test, Disconnect, "Invalid plan", "Insufficient balance" + tombol Tambah saldo) - string + 2 link.

Jumlah temuan: Tinggi 9, Sedang 9, Rendah 6 (total 24).

## Lihat juga

- [audit-ux](audit-ux.md)
- [paritas-ui](paritas-ui.md)
