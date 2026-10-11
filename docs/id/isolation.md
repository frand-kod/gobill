# Isolir pelanggan yang paketnya habis

Isolir membuat pelanggan yang paketnya habis tidak langsung diputus. Mereka masuk ke paket **Isolir** (64 kbps) selama beberapa hari. Di paket ini internet tetap bisa dipakai pelan untuk membuka WhatsApp dan aplikasi bank. Saat membuka situs lain, pelanggan diarahkan ke halaman isolir yang berisi tagihan, QRIS, tombol chat admin dan form voucher.

Isolir hanya bekerja untuk pelanggan yang login lewat RADIUS gobill (paket dengan perangkat **Radius**). Pelanggan yang akunnya disimpan langsung di MikroTik (hotspot atau PPPoE lokal) tetap diputus seperti biasa.

## Pengaturan

Buka **Pengaturan → Isolir**. Tab ini hanya bisa dibuka SuperAdmin. Setiap kolom menampilkan nilai bawaan bila belum pernah diisi. Setiap bagian punya tombol **Kembalikan ke default**.

| Pengaturan | Bawaan | Keterangan |
|---|---|---|
| Isolir Aktif | Tidak | Aktifkan setelah router sudah disetup (lihat di bawah). |
| Paket Isolir | Otomatis | Saat isolir diaktifkan pertama kali, gobill membuat paket **Isolir** (bandwidth **Isolir 64k**, 64 kbps/64 kbps). Paket ini tidak dijual di portal. Admin boleh memilih paket lain. |
| Lama Isolir (hari) | 30 | Berapa hari isolir berjalan setelah paket habis. Setelah itu pelanggan nonaktif seperti biasa. `0` berarti isolir sampai pelanggan membayar. |
| Judul | Paket internet Anda sudah habis | Judul halaman isolir. |
| Pesan | Silakan bayar untuk mengaktifkan kembali. Internet tetap bisa dipakai pelan untuk membuka WhatsApp dan aplikasi bank. | Teks di bawah judul. |
| Nomor WhatsApp admin | Nomor telepon usaha | Dipakai untuk tombol "Sudah bayar? Chat admin". |
| Tampilkan QRIS | Ya | QRIS dengan nominal terkunci ke harga paket terakhir. Perlu QRIS statis di Pembayaran. |
| Tampilkan form voucher | Ya | Pelanggan bisa memasukkan voucher langsung dari halaman isolir. |
| Nama address-list MikroTik | `isolir` | Nama address-list yang dikirim RADIUS untuk pelanggan yang diisolir. |
| Mode tampilan | Bawaan | `HTML sendiri` memakai HTML dari kolom di bawahnya. |

## Perilaku

1. Paket pelanggan habis. Pesan pemberitahuan habis tetap dikirim seperti biasa.
2. Jika isolir aktif dan paket itu tidak punya **Paket kedaluwarsa** sendiri (pengaturan paket `expired_plan_id`), pelanggan pindah ke paket Isolir selama **Lama Isolir**. Jika paket punya paket kedaluwarsa sendiri, pengaturan itu yang dipakai.
3. Pelanggan yang membuka internet dialihkan ke halaman `/isolir`. Halaman ini mengenali pelanggan dari alamat IP klien pada sesi RADIUS yang masih terbuka.
4. Pembayaran atau voucher yang diterima mengakhiri isolir. Sesi pelanggan di router diputus, lalu pelanggan login ulang dan mendapat paket normal.
5. Jika masa isolir habis, pelanggan nonaktif seperti biasa.

Isolir tidak memperpanjang otomatis paket Isolir. Pelanggan yang saldonya cukup tetap diperpanjang otomatis dari paket asalnya, dan isolir tidak dimulai.

## Setup router MikroTik

Tab Isolir menampilkan perintah yang bisa disalin. Alamat gobill diambil dari **App URL**, atau dari alamat yang dipakai untuk membuka halaman admin bila App URL kosong. Ganti IP bila alamat LAN gobill berbeda.

Contoh (gobill di `192.168.88.2` port `8080`):

```
/ip firewall nat add chain=dstnat src-address-list=isolir protocol=tcp dst-port=80 action=dst-nat to-addresses=192.168.88.2 to-ports=8080
```

Aturan NAT ini mengalihkan HTTP klien isolir ke halaman gobill.

```
/ip firewall filter add chain=forward src-address-list=isolir dst-address=192.168.88.2 action=accept
```

Aturan filter ini membolehkan klien isolir mencapai gobill. Letakkan di atas aturan drop untuk klien isolir. Pada RouterOS 7 atau 6, gunakan `place-before` bila urutannya perlu diatur.

```
/ip hotspot walled-garden add dst-host=billing.example.com action=allow
```

Walled garden membuat halaman isolir bisa dibuka sebelum login hotspot. Jika gobill memakai IP, gunakan `/ip hotspot walled-garden ip add dst-address=<ip> action=accept`.

Catatan: HTTPS tetap bisa dipakai, tetapi lambat karena dibatasi 64 kbps. Hanya HTTP port 80 yang dialihkan ke halaman isolir. Address-list dikirim RADIUS sebagai `Mikrotik-Address-List`, jadi hotspot harus memakai RADIUS gobill untuk login.

## HTML sendiri

Mode **HTML sendiri** menampilkan HTML yang Anda tulis, dengan variabel yang diganti sebelum halaman dikirim:

| Variabel | Isi |
|---|---|
| `[[company_name]]` | Nama usaha |
| `[[logo_url]]` | Alamat logo |
| `[[title]]`, `[[message]]` | Judul dan pesan isolir |
| `[[username]]`, `[[name]]` | Username dan nama pelanggan |
| `[[plan_name]]`, `[[expired_at]]`, `[[amount]]` | Paket terakhir, waktu habis, dan harga |
| `[[qris_image]]` | Gambar QRIS (data URI), untuk `src` gambar |
| `[[wa_admin_link]]` | Link chat admin |
| `[[voucher_form]]` | Form voucher siap pakai |
| `[[promos]]` | Blok promo |
| `[[known]]` | `yes` bila pelanggan dikenali, `no` bila tidak |
| `[[notice]]` | Pesan hasil voucher |

Nilai selain `voucher_form` dan `promos` di-escape HTML. Variabel yang tidak dikenal atau kosong menjadi kosong. HTML berjalan dalam sandbox tanpa akses ke cookie gobill, jadi skrip di dalamnya tidak bisa membaca sesi admin. HTML kosong akan memakai halaman bawaan.

Gunakan **Pratinjau** untuk melihat halaman dengan data contoh. Pratinjau memakai HTML yang tersimpan, jadi simpan dulu perubahan HTML.

## Batasan

- Isolir hanya untuk pelanggan yang login lewat RADIUS gobill.
- Alamat IP untuk mengenali pelanggan diambil dari sesi RADIUS yang masih terbuka (`framed_ip`). Pastikan NAS mengirim accounting dengan benar.
- Voucher di halaman isolir dibatasi 10 percobaan salah per IP selama 15 menit.
