# Isolir pelanggan yang paketnya habis

Isolir membuat pelanggan yang paketnya habis tidak langsung diputus. Mereka masuk status isolir (kecepatan 64 kbps) selama beberapa hari. Di paket ini pelanggan mendapat isolasi penuh kecuali daftar izin host. WhatsApp diizinkan secara bawaan agar pelanggan bisa menghubungi Anda, dan Anda bisa menambah aplikasi bank. Halaman web dialihkan ke halaman isolir, sementara aplikasi dan situs lain diblokir. Halaman ini menampilkan nama paket, tombol **Hubungi admin**, tombol logout (bila diatur) dan form voucher. Halaman isolir tidak menawarkan pembayaran: pelanggan membayar lewat admin atau portal.

Isolir hanya bekerja untuk pelanggan yang login lewat RADIUS gobill (paket dengan perangkat **Radius**). Pelanggan yang akunnya disimpan langsung di MikroTik (hotspot atau PPPoE lokal) tetap diputus seperti biasa.

## Pengaturan

Buka **Pengaturan > Isolir**. Tab ini hanya bisa dibuka SuperAdmin. Setiap kolom menampilkan nilai bawaan bila belum pernah diisi. Setiap bagian punya tombol **Kembalikan ke default**.

| Pengaturan | Bawaan | Keterangan |
|---|---|---|
| Isolir Aktif | Tidak | Aktifkan setelah router sudah disetup (lihat di bawah). |
| Nama paket | Otomatis | Nama paket sistem untuk isolir. gobill membuat paket **Isolir** saat Isolir pertama kali diaktifkan, dengan profil bandwidth **Isolir** (kecepatan awal 64k). Paket ini tidak dijual di portal dan tidak bisa diberikan manual. |
| Kecepatan download, Kecepatan upload | `64k` | Kecepatan paket Isolir, misalnya `64k` atau `2M`. Disimpan ke profil bandwidth **Isolir** (kecepatan awal 64k). |
| Lama Isolir (hari) | 30 | Berapa hari isolir berjalan setelah paket habis. Setelah itu pelanggan nonaktif seperti biasa. `0` berarti isolir sampai pelanggan membayar. |
| Judul | Paket internet Anda sudah habis | Judul halaman isolir. |
| Pesan | Internet Anda dibatasi sampai paket diperpanjang. WhatsApp tetap bisa dipakai untuk menghubungi kami. | Teks di bawah judul. |
| Nomor WhatsApp admin | Nomor telepon usaha | Dipakai untuk tombol **Hubungi admin**. |
| Tampilkan form voucher | Ya | Pelanggan bisa memasukkan voucher langsung dari halaman isolir. |
| Nama address-list MikroTik | `isolir` | Nama address-list yang dikirim RADIUS untuk pelanggan yang diisolir. Daftar izinnya adalah nama ini dengan `-allow`. |
| Diizinkan saat diisolir | `*.whatsapp.net`, `*.whatsapp.com`, `whatsapp.com`, `whatsapp.net`, `g.whatsapp.net`, `mmg.whatsapp.net`, `wa.me` | Host yang tetap bisa diakses pelanggan saat diisolir, satu per baris. Wildcard `*.` diperbolehkan. Setelah diubah, tempel lagi perintah router. |
| Alamat hotspot | Kosong | Alamat yang dipakai pelanggan untuk halaman login hotspot, misalnya `http://fourkeys.net` atau `http://192.168.20.1`. Kosong berarti tanpa tombol keluar. |
| Mode tampilan | Bawaan | `HTML sendiri` memakai HTML dari kolom di bawahnya. |

**Keluar / pakai akun lain.** Bila Alamat hotspot diisi, halaman isolir menampilkan tombol **Keluar / pakai akun lain** yang mengarah ke `<Alamat hotspot>/logout`. Logout hotspot menghapus cookie login di perangkat pelanggan, sehingga pelanggan bisa login dengan akun lain. Router tidak boleh mengalihkan alamat itu, lihat aturan `dst-address-type=!local` di [Setup MikroTik](mikrotik.md#isolir).

## Perilaku

1. Paket pelanggan habis. Pesan pemberitahuan habis tetap dikirim seperti biasa.
2. Jika isolir aktif, paket itu perangkatnya **Radius**, pelanggan berstatus Aktif, dan paket itu tidak punya **Paket kedaluwarsa** sendiri (pengaturan paket `expired_plan_id`), pelanggan masuk status isolir selama **Lama Isolir**. Isolir bukan paket: tidak ada langganan baru, dan isolir tidak muncul di daftar paket atau langganan. Jika paket punya paket kedaluwarsa sendiri, pengaturan itu yang dipakai.
3. Pelanggan yang membuka internet dialihkan ke halaman `/isolir`. Halaman ini mengenali pelanggan dari alamat IP klien pada sesi RADIUS yang masih terbuka.
4. Pembelian paket (dari admin, portal, pembayaran gateway, atau saldo) dan voucher yang berhasil mengakhiri isolir. Top-up saldo saja tidak mengakhiri isolir: saldo baru dipakai saat paket dibeli. Sesi pelanggan di router diputus, lalu pelanggan login ulang dan mendapat paket normal.
5. Jika masa isolir habis, pelanggan nonaktif seperti biasa.

Isolir tidak diperpanjang otomatis. Pelanggan yang saldonya cukup tetap diperpanjang otomatis dari paket asalnya, dan isolir tidak dimulai.

Mematikan **Isolir Aktif** di pengaturan langsung mengakhiri semua isolir yang sedang berjalan. Sesi pelanggan di router diputus, dan pelanggan login ulang untuk mendapat paket normal. Akhir isolir ini tidak mengirim pemberitahuan habis paket.

## Daftar isolasi

Menu **Isolasi** di bagian admin (bawah Langganan) menampilkan setiap isolasi: pelanggan, tanggal mulai, tanggal berakhir, tanggal selesai, alasan selesai, dan status. Status **berjalan** berarti isolasi masih aktif. **Lewat masa** berarti sudah lewat tanggal berakhir, tetapi job expiry belum menutupnya. **Selesai** berarti sudah ditutup, dengan alasan `expired` (masa habis), `paid` (sudah bayar), atau `disabled` (Isolir dimatikan). Filter status dan kotak cari berlaku juga untuk **Ekspor CSV**. Di halaman pelanggan, isolasi yang sedang berjalan ditampilkan di kartu Paket Layanan.

## Setup router MikroTik

Tab **Isolir** menampilkan aturan RouterOS di bagian **Setup router** sebagai satu blok yang bisa disalin, sudah terisi alamat dan port LAN gobill, nama address-list, dan daftar izin. Tempel seluruh blok sekaligus, sesuai urutan. Aturan harus berada di atas aturan accept umum di chain forward. Bypass hotspot dan tes curl ada sebagai item terpisah di bawahnya. Detail, batasan RouterOS 6, dan cara menambah aplikasi bank ada di [Setup MikroTik](mikrotik.md#isolir).

## HTML sendiri

Mode **HTML sendiri** menampilkan HTML yang Anda tulis, dengan variabel yang diganti sebelum halaman dikirim:

| Variabel | Isi |
|---|---|
| `[[company_name]]` | Nama usaha |
| `[[logo_url]]` | Alamat logo |
| `[[title]]`, `[[message]]` | Judul dan pesan isolir |
| `[[username]]`, `[[name]]` | Username dan nama pelanggan |
| `[[plan_name]]`, `[[expired_at]]`, `[[amount]]` | Paket terakhir, waktu habis, dan harga |
| `[[logout_url]]` | Alamat logout hotspot (`<Alamat hotspot>/logout`), kosong bila Alamat hotspot belum diisi |
| `[[wa_admin_link]]` | Link chat admin |
| `[[voucher_form]]` | Form voucher siap pakai |
| `[[promos]]` | Blok promo |
| `[[known]]` | `yes` bila pelanggan dikenali, `no` bila tidak |
| `[[notice]]` | Pesan hasil voucher |

Ketuk variabel di bawah kolom HTML untuk menyisipkannya di posisi kursor. **Kembali ke bawaan** menghapus HTML sendiri. **Kembalikan ke default** di bagian **Mode tampilan** hanya mengembalikan mode, HTML tidak dihapus.

Nilai selain `voucher_form` dan `promos` di-escape HTML. Variabel yang tidak dikenal atau kosong menjadi kosong. HTML berjalan dalam sandbox tanpa akses ke cookie gobill, jadi skrip di dalamnya tidak bisa membaca sesi admin. HTML kosong akan memakai halaman bawaan.

Gunakan **Buka pratinjau** untuk melihat halaman dengan data contoh. Pratinjau memakai HTML yang tersimpan, jadi simpan dulu perubahan HTML.

## Batasan

- Isolir hanya untuk pelanggan yang login lewat RADIUS gobill.
- Alamat IP untuk mengenali pelanggan diambil dari sesi RADIUS yang masih terbuka (`framed_ip`). Pastikan NAS mengirim accounting dengan benar.
- Voucher di halaman isolir dibatasi 10 percobaan salah per IP selama 15 menit.
