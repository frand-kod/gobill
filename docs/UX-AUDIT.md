# Audit UX: admin UI dan portal pelanggan

Dibuat 2026-10-09 dari pembacaan kode di `main` (read-only; aplikasi tidak dijalankan, tidak ada router atau layanan eksternal yang dihubungi). Sumber: `web/templates/**`, `web/tailwind.css`, `internal/web/*.go`, `lang/indonesia.json`, `docs/UI-PARITY.md`.

## Ringkasan

Fondasinya sudah baik: label terhubung ke input (`for`/`id`), `aria-invalid` + `aria-describedby` untuk error field, `role="alert"`/`role="status"` pada flash, focus ring global (`web/tailwind.css:22`), `aria-label` pada tombol ikon, dark mode, viewport meta, dan tabel dibungkus `overflow-x-auto`. Perlindungan CSRF memakai `http.NewCrossOriginProtection` (`internal/web/web.go:255`).

Masalah terbesar ada di tiga area:

1. **Aksi uang tanpa pengaman klik ganda dan tanpa konfirmasi.** Tidak ada satu pun JS yang menonaktifkan tombol submit (`grep` di `web/static/*.js` kosong). Berlaku di deposit, recharge, bayar pakai saldo, kirim saldo, dan beli untuk teman.
2. **Terjemahan Indonesia bocor.** 24 string yang dipakai template tidak ada di `lang/indonesia.json`, termasuk menu utama portal pelanggan ("Order Package", "Order History", "Request OTP", "Save Changes"). Banyak pesan error dari `internal/billing` juga tampil dalam bahasa Inggris. Error server 500 berupa teks polos `Internal Server Error`.
3. **Tombol aksi baris terlalu kecil dan tersembunyi di mobile.** Kolom aksi (Edit/Hapus/Extend/Deactivate) ada di ujung kanan tabel yang scroll horizontal. Tombol `btn-sm` hanya sekitar 26 px tinggi.

Jumlah temuan: **Tinggi 6, Sedang 11, Rendah 8** (total 25).

---

## Tinggi

### T1. Tidak ada proteksi double-submit di aksi uang
- **Layar:** recharge konfirmasi, deposit, redeem voucher, portal "Pay with Balance", "Send Balance", "Buy for friend", "Buy" (top-up).
- **File:** `web/templates/recharge_confirm.html:25`, `web/templates/form.html:109` (dipakai deposit dan redeem), `web/templates/portal/plans.html:11,13,20`, `web/templates/portal/dashboard.html:8`, `web/templates/portal/friend.html:7`.
- **Masalah:** tombol submit tidak dinonaktifkan setelah klik; tidak ada token idempotensi. Halaman konfirmasi recharge adalah POST biasa (`internal/web/customers.go:326`), jadi klik dua kali di jaringan lambat bisa mengirim dua request `Billing.Recharge`.
- **Dampak:** pelanggan di HP murah dengan sinyal lemah sering mengetuk dua kali; saldo terpotong dua kali atau paket diperpanjang dua kali, dan operator harus menyelesaikan sengketa manual.
- **Perbaikan minimal:** satu handler global di `base.html`: `document.addEventListener('submit', e => { const b = e.target.querySelector('[type=submit]'); if (b) setTimeout(() => b.disabled = true) })` (setTimeout agar nilai `name/value` tombol tetap terkirim). Opsional jangka panjang: token form sekali pakai di sesi untuk endpoint recharge/deposit/transfer.

### T2. Bayar pakai saldo di portal langsung memotong saldo tanpa konfirmasi, harga vs saldo tidak terlihat
- **Layar:** `/portal/plans`.
- **File:** `web/templates/portal/plans.html:4,11`, `internal/web/portal.go:514-552`.
- **Masalah:** tombol "Pay with Balance" langsung mengeksekusi pembelian. `$bal` hanya boolean, jadi saldo pelanggan tidak ditampilkan di halaman ini dan tombol tetap aktif walau saldo kurang (baru ketahuan setelah submit). Tidak ada ringkasan "saldo sekarang, harga, sisa".
- **Dampak:** salah ketuk di kartu paket yang berdempetan langsung membeli paket. Uang pelanggan kecil adalah uang yang sensitif.
- **Perbaikan:** tampilkan `{{money .Customer.Balance}}` di atas daftar paket; tambahkan `onsubmit="return confirm(...)"` berisi nama paket dan harga, atau gunakan halaman konfirmasi seperti `recharge_confirm.html` (admin sudah punya polanya). Beri `disabled` + hint jika saldo < harga.

### T3. Kirim saldo dan "Buy for friend" tanpa konfirmasi tujuan
- **File:** `web/templates/portal/dashboard.html:5-9`, `web/templates/portal/friend.html:4-8`, `internal/web/extras.go:175-205`.
- **Masalah:** username penerima diketik bebas; tidak ada langkah "Kirim Rp X ke <nama>?". Saldo ditransfer langsung. Tidak ada `autocomplete="off"`, jadi keyboard HP menyarankan username lain.
- **Dampak:** salah ketik username yang kebetulan valid = saldo hilang ke orang lain, tidak bisa ditarik kembali.
- **Perbaikan:** `confirm()` dengan nilai input (`'Kirim '+tb.value+' ke '+tu.value+'?'`) atau halaman konfirmasi yang menampilkan nama lengkap penerima (samarkan sebagian). Tambahkan `inputmode="numeric"` ke input jumlah.

### T4. Aksi destruktif di daftar: Deactivate/Extend tanpa konfirmasi, konfirmasi lain tidak menyebut nama
- **Layar:** `/admin/subscriptions`, `/admin/coupons`, `/admin/routers`, semua daftar CRUD.
- **File:** `web/templates/list.html:41` (aksi baris), `:43` (hapus), `internal/web/subscriptions.go:65`, `web/templates/customer.html:123`.
- **Masalah:** form aksi baris (`Extend`, `Deactivate`, `Sync`, `Block/Unblock`, `Test connection`) tidak punya `onsubmit confirm`. "Deactivate" langsung memutus pelanggan dan menghapus user di router. "Extend" memakai input hari berisi default 7 yang mudah ketap tanpa sengaja. Konfirmasi hapus hanya "Hapus?" (`list.html:43`), tanpa nama baris. Konfirmasi Deactivate di halaman pelanggan hanya "Menonaktifkan paket?" tanpa username.
- **Dampak:** salah tap di tabel padat di HP; operator tidak tahu baris mana yang akan dihapus/dinonaktifkan.
- **Perbaikan:** tambah field `Confirm bool` pada `rowAction` (`internal/web/crud.go:96`) dan render `onsubmit="return confirm(...)"` di `list.html:41`, set true untuk `deactivate`. Pada konfirmasi hapus/deactivate sertakan nama baris (`{{index $r.Cells 0}}`). Ikon aksi di `list.html:41` selalu `circle-check` termasuk untuk "Deactivate"; ganti ikon per aksi (`x` untuk deactivate).

### T5. Terjemahan Indonesia bocor ke menu dan pesan pelanggan
- **File:** `lang/indonesia.json` (kunci hilang), dipakai di `web/templates/portal/layout.html:11,12`, `portal/profile.html:15,17,24,26,35`, `portal/register.html:19`, `portal/login.html:24,26`, `portal/dashboard.html:4`, `dashboard.html:7,11`, `portal/inbox.html:18`.
- **Masalah:** 24 kunci tidak ada: Order Package, Order History, Voucher Activation, Request OTP, Save Changes, Home Address, Change Phone Number, Change Email, New Number, New Email, Login / Activate Voucher, Enter voucher code here, Voucher code, Package Price, Plan Name, New, Income Today, Income This Month, Total Monthly Sales, Send Via, Subscription Status, Bulk Message, Bulk Message Status, dan satu hint di form. Pesan error billing juga tidak diterjemahkan: "Cannot send to yourself", "Minimum Transfer", "Failed, balance is not available", "You already extend for this month", "Plan Not Found or Not Active", "account is not active" (`internal/billing/portal.go:16-24`), "voucher not valid or already used", "insufficient balance" (huruf kecil, tidak cocok dengan kunci "Insufficient balance") (`internal/billing/service.go:21-22`), serta "Recharge failed", "Invalid payment method", "Voucher redeemed" (`internal/web/customers.go:296,345,353`, `vouchers.go:317`).
- **Dampak:** pelanggan melihat menu setengah Inggris di halaman utama. Tampilan tidak profesional dan membingungkan.
- **Perbaikan:** tambahkan 24+ kunci tersebut ke `lang/indonesia.json`. Tambahkan test kecil di `internal/i18n` atau `internal/web/ui_test.go` yang mengekstrak semua `{{T "..."}}` dan literal `Label:` lalu memastikan ada di `indonesia.json` (skrip ekstraksi yang saya pakai: regex `\{\{T[T]? "([^"]+)"`).

### T6. Error server berupa teks polos, router test menahan request tanpa indikator
- **File:** `internal/web/crud.go:200-203` (`fail`), `internal/web/handlers.go:57-63`, `internal/web/routers.go:164-181`.
- **Masalah:** semua kegagalan 500 menampilkan teks putih polos "Internal Server Error" tanpa layout, tombol kembali, atau bahasa Indonesia; di tengah alur recharge pun begitu (`customers.go:290,329` untuk billing nil, atau error DB). `routerTest` memanggil `Billing.Ping` sinkron dari sebuah POST dari tombol kecil, tanpa spinner, dan menampilkan `err.Error()` mentah dari Go/RouterOS ke layar.
- **Dampak:** operator tidak tahu apakah uang sudah terpotong atau belum setelah 500; router mati membuat halaman "hang" lalu pesan teknis.
- **Perbaikan:** render halaman error ber-layout (judul "Terjadi kesalahan", tombol Kembali, ID request) dari `fail`, bukan `http.Error`. Setelah T1 diterapkan, tombol otomatis menampilkan state nonaktif; tambahkan teks "Menghubungi router..." lewat `x-data` di tombol Test. Terjemahkan `Connection failed` dengan penjelasan manusiawi (timeout / kredensial salah).

---

## Sedang

### S1. Tabel CRUD di HP: kolom aksi di ujung kanan, tombol kecil
- **File:** `web/templates/list.html:27-47`, `web/tailwind.css:44,57-59`.
- **Masalah:** daftar pelanggan punya 7 kolom + kolom aksi (`custList`, `customers.go:100`). Di layar 360 px tabel scroll horizontal; Edit/Hapus ada di luar layar. `.btn-sm` = `px-2 py-1 text-xs` sekitar 26 px tinggi, di bawah 44 px yang dianjurkan; Edit dan Hapus berdempetan (`gap-1`).
- **Dampak:** salah tap Hapus (didukung T4) dan operator sering tidak menemukan tombol edit.
- **Perbaikan:** di bawah `sm`, sembunyikan kolom yang kurang penting (`hidden sm:table-cell` untuk "PPPoE Username", "Service Type"); jadikan sel pertama link yang sudah ada (sudah, `list.html:36`) dan taruh aksi di halaman detail. Naikkan `.btn-sm` ke `py-2` pada `max-sm`. Beri jarak `gap-2` antara Edit dan Hapus.

### S2. Tabel portal pelanggan tidak memakai gaya `.table`
- **File:** `web/templates/portal/dashboard.html:12-15`, `inbox.html:16`, `activation.html:3-6`, `orders.html:3-6`.
- **Masalah:** pakai `class="w-full text-sm"` + `p-2` manual: tanpa garis pemisah baris, header tidak dibedakan, sel "No data" tanpa padding dan tanpa tengah. Tabel 7 kolom `activation.html` hampir tidak terbaca di HP (sempit, scroll horizontal tanpa petunjuk).
- **Dampak:** pelanggan mobile sulit membaca riwayat; tampilan beda dengan admin.
- **Perbaikan:** ganti ke `class="table"` (sudah ada, `tailwind.css:56`). Untuk HP, render kartu per baris (`sm:hidden`) atau kurangi kolom (hapus Type/Method di `activation.html`).

### S3. Status paket di portal tidak diterjemahkan, tombol Extend tanpa konfirmasi
- **File:** `web/templates/portal/dashboard.html:14`.
- **Masalah:** `<span class="badge {{badge .Status}}">{{.Status}}</span>` tanpa `T` (admin memakai `{{T $c}}`, `list.html:34`); pelanggan melihat "on"/"off"/"expired" mentah. Tombol "Extend" memakai `btn btn-ghost` standar di dalam sel, langsung mengeksekusi perpanjangan (berbayar atau berhutang) tanpa konfirmasi.
- **Perbaikan:** `{{T .Status}}` dan `onsubmit="return confirm(...)"` pada form Extend, dengan sebutan tanggal baru jika tersedia.

### S4. Halaman pembayaran gateway tidak menyegarkan diri
- **File:** `web/templates/portal/payment.html:13-17`.
- **Masalah:** status `pending` harus dicek manual lewat tombol "Check for Payment". Tidak ada auto-refresh atau petunjuk "Setelah membayar, kembali ke sini". Tidak ada teks yang menjelaskan masa berlaku atau langkah selanjutnya; kolom "Expires On" dalam format tanggal mentah.
- **Dampak:** pelanggan membayar di app e-wallet, kembali, melihat "pending", mengira gagal dan membayar lagi.
- **Perbaikan:** `<meta http-equiv="refresh" content="20">` saat `pending` (satu baris di template), dan teks hint "Sudah membayar? Tunggu beberapa detik lalu ketuk Cek Pembayaran". Tombol "Pay Now" gunakan `target="_self"` (sudah) dan tampilkan sisa waktu.

### S5. Layar baru memakai teks teknis/Inggris di nav dan form
- **File:** `web/templates/app.html:20`, `:21`, `:76`; `web/templates/portal/layout.html:9`.
- **Masalah:** menu "Recharge Account / Redeem Voucher" digabung dalam satu label panjang yang terpotong di sidebar sempit; "Refill Balance" (deposit) dan "Recharge Account" memakai ikon `wallet` yang sama sehingga operator sulit membedakan. Terjemahannya: "Isi Ulang" untuk Recharge, dan Refill Balance tidak jelas. Dua grup menu terpisah memakai ikon sama (`ticket` untuk Voucher dan Coupons, `scroll-text` untuk Logs dan Pages, `map` untuk Customer Map dan ODP Map, `receipt` untuk Transactions dan Payment Gateway).
- **Perbaikan:** pisah jadi dua menu: "Redeem Voucher" (`ticket`) dan "Isi Saldo" (`wallet`), pakai ikon berbeda untuk Payment Gateway (`credit-card`) dan Pages (`file-text`) jika tersedia di `web/static/icons`.

### S6. Form: `novalidate` + `required` tidak dipakai, error tidak mendapat fokus
- **File:** `web/templates/form.html:70,80,97,102`.
- **Masalah:** form memakai `novalidate`, jadi `required` tidak memunculkan peringatan browser; semua validasi baru muncul setelah round-trip (status 422). Setelah error, halaman dimuat ulang dari atas; di form panjang (pelanggan, pengaturan) field yang salah ada di bawah layar dan tidak ada ringkasan error di atas. Input angka tidak memakai `inputmode="numeric"` (hanya `min="0"`), jadi keyboard HP penuh huruf.
- **Dampak:** operator mengira tombol Simpan "tidak bekerja".
- **Perbaikan:** saat ada `.Error` pada field mana pun, tampilkan `alert-error` ringkas di atas ("Periksa N kolom yang ditandai") dan beri `autofocus` pada field error pertama. Tambahkan `inputmode="numeric"` untuk `type=number`.

### S7. Deposit dan redeem: username diketik bebas, hasil tidak menyebut jumlah
- **File:** `internal/web/subscriptions.go:236-283`, `internal/web/vouchers.go:287-321`.
- **Masalah:** pelanggan dicari dari teks username, tanpa autocomplete/`datalist`. Flash sukses deposit hanya "Refill Balance: <username>" (`subscriptions.go:277`), tanpa jumlah dan saldo baru. Field jumlah (`type=number`) tanpa prefix "Rp" dan tanpa pemisah ribuan.
- **Dampak:** admin tidak bisa memastikan jumlah yang masuk; salah ketik nol tidak terlihat.
- **Perbaikan:** flash menjadi "Saldo +Rp 50.000, sisa Rp 120.000: <username>". Sertakan halaman konfirmasi seperti recharge atau minimal `confirm()` dengan jumlah. Parameter `?customer=` sudah ada, tautkan dari halaman pelanggan agar mengurangi ketik manual (tombol "Isi Saldo" di `customer.html:153-165`, saat ini hanya link Redeem Voucher).

### S8. Halaman pelanggan: tombol berbahaya sejajar dengan aksi biasa, tidak bisa dibungkus di HP
- **File:** `web/templates/customer.html:119-128`.
- **Masalah:** hingga 6 tombol sejajar dalam satu `flex gap-2` tanpa `flex-wrap`, jadi di HP meluap ke samping atau memampatkan teks. "Deactivate" (merah) dan "Login as Customer" bersebelahan. "Sync" tidak punya konfirmasi atau umpan balik progres.
- **Perbaikan:** tambah `flex-wrap` pada container (`:119`); pindahkan Deactivate ke akhir atau ke bagian "Zona berbahaya" terpisah. Tampilkan hasil Sync di flash dengan nama router.

### S9. Navigasi portal wrap menjadi 3-4 baris di HP
- **File:** `web/templates/portal/layout.html:8-16`.
- **Masalah:** tujuh tombol `btn-ghost` berbingkai dalam `flex-wrap` memakan sekitar 150-200 px tinggi layar sebelum konten di ponsel 360 px. Tombol Logout menempel `ms-auto` sehingga terlempar ke baris sembarang.
- **Perbaikan:** untuk `< sm`, jadikan nav satu baris yang bisa scroll horizontal (`overflow-x-auto flex-nowrap`) atau gunakan menu hamburger Alpine seperti di `app.html:87`. Pindahkan "Order History" dan "Activation History" ke dalam satu halaman bertab.

### S10. Kontras teks putih pada ubin amber dan label "Active / Expired"
- **File:** `web/templates/dashboard.html:14-15`.
- **Masalah:** `text-white` pada `bg-amber-500` memiliki rasio kontras sekitar 2,1:1 (di bawah 4,5:1 WCAG AA). Ubin sky-600/emerald-600/rose-600 lulus, amber tidak. Ubin "Active / Expired" juga tidak bisa diklik, berbeda dari tiga lainnya (tidak ada link ke `/admin/subscriptions`).
- **Perbaikan:** `bg-amber-600 text-white` atau teks `text-amber-950`; bungkus ubin dengan `<a href="/admin/subscriptions">`.

### S11. Sidebar drawer: tidak ada tombol tutup, Esc, atau fokus terkunci
- **File:** `web/templates/app.html:4-6,87`.
- **Masalah:** drawer ditutup hanya dengan mengetuk overlay (`@click="drawer=false"`). Tidak ada tombol X, tidak ada `@keydown.escape`, tidak ada `role="dialog"` atau trap fokus; tautan di dalam drawer tidak menutupnya. Tombol hamburger dengan `aria-label="Menu"` tidak diterjemahkan dan tanpa `aria-expanded`.
- **Perbaikan:** tambah `@keydown.escape.window="drawer=false"` pada root `x-data` dan `:aria-expanded="drawer"` pada tombol; tombol X di header drawer.

---

## Rendah

- **R1. Faktur dan cetak voucher tanpa gaya layar kecil yang konsisten.** `web/templates/invoice.html:7-14` dan `print.html:7-14` memakai CSS inline sendiri, tanpa dark mode (OK untuk cetak), tapi grid voucher tetap 3 kolom di HP (`print.html:9`) sehingga pratinjau mengecil; QR `alt=""` (`print.html:26`) sebaiknya `alt="QR {{.Code}}"`. Faktur tidak menyertakan tombol kembali (`invoice.html:18`).
- **R2. Tidak ada tombol tampil/sembunyikan password.** `login.html:17`, `portal/login.html:15`, `portal/register.html:15-16`, `portal/profile.html:32-34`. Mengetik password di HP salah lebih sering; tambah toggle kecil Alpine.
- **R3. Placeholder pencarian tercampur.** `app.html:90` menghasilkan "Cari: Pelanggan" yang selalu hanya mencari pelanggan; di halaman lain (voucher, transaksi) pengguna mengira itu pencarian halaman tersebut. Beri label "Cari pelanggan..." dan hilangkan di halaman yang punya pencarian sendiri.
- **R4. Pagination hanya Previous/Next, tanpa total.** `list.html:56-60`, `radius_sessions.html:30-35`. Tidak ada jumlah hasil atau nomor halaman; filter `q` tidak diteruskan ke `radius_sessions` dengan benar bila berisi `&` (`radius_sessions.html:32` tanpa `urlquery`).
- **R5. Header tabel huruf kapital semua.** `tailwind.css:57` (`uppercase tracking-wide text-xs`) pada teks Indonesia panjang ("TIPE LAYANAN", "USERNAME PPPOE") sulit dibaca; kontras `text-slate-500` pada `bg-slate-50` sekitar 4,6:1 (batas). Hapus `uppercase`.
- **R6. Pesan "No data" tidak memberi tindakan.** `list.html:50`, `customer.html:174`: tanpa petunjuk "Belum ada pelanggan. Tambah pelanggan pertama." dan tombol Add. Untuk instalasi baru, daftar kosong Router/Paket/Pelanggan adalah titik buntu pertama operator.
- **R7. Emoji dan Unicode panah sebagai penanda urut.** `list.html:29` memakai `&uarr;/&darr;` tanpa `aria-sort`. Tambahkan `aria-sort="ascending"` pada `<th>`.
- **R8. Chart.js dimuat di semua pemakaian dashboard.** Canvas sudah punya `role="img"` dan `aria-label` (baik), tetapi tidak ada tabel data cadangan; pada HP lama chart bisa lambat. Pertimbangkan menampilkan angka ringkas di atas chart.

---

## Top 10 quick wins (urut prioritas)

1. **Nonaktifkan tombol submit setelah klik** (satu listener global di `base.html`; T1). Perbaikan terkecil dengan dampak uang terbesar.
2. **Lengkapi `lang/indonesia.json`** dengan 24 kunci yang hilang plus pesan error billing (T5), dan tambah test yang mendeteksi kunci hilang.
3. **Tambah konfirmasi + saldo terlihat di "Pay with Balance"** (`portal/plans.html`; T2).
4. **Konfirmasi dengan nama/jumlah** untuk Kirim Saldo dan Buy for Friend (T3).
5. **Field `Confirm` pada `rowAction`**: konfirmasi untuk Deactivate/Extend di daftar, dan sertakan nama baris pada konfirmasi hapus (T4).
6. **Halaman error ber-layout** menggantikan `http.Error` di `fail()` (T6).
7. **`flex-wrap` pada tombol halaman pelanggan** dan jarak lebih besar antar Edit/Hapus di HP (S8, S1).
8. **Pakai kelas `.table` di semua tabel portal** dan sembunyikan kolom sekunder di HP (S2).
9. **Flash deposit menyebut jumlah dan saldo baru**, plus tautan "Isi Saldo" dari halaman pelanggan (S7).
10. **Auto-refresh + hint di halaman pembayaran pending** (S4) dan perbaiki kontras ubin amber (S10).
