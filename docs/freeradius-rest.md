# FreeRADIUS lewat REST

Untuk operator yang sudah punya FreeRADIUS (biasanya dari PHPNuxBill) dan ingin tetap memakainya. NuxBill menyediakan endpoint kompatibel `radius.php` untuk modul `rlm_rest`.

## Cara pakai

Di `/etc/freeradius/3.0/mods-enabled/rest`, ubah `connect_uri` ke NuxBill:

    connect_uri = "https://<nuxbill>/radius.php"

(`/radius/rest` juga tersedia dengan fungsi sama.) Bagian `authorize`, `authenticate`, `accounting`, dan `post-auth`, serta konfigurasi MikroTik, tidak perlu diubah. Plan `RadiusRest` dari PHPNuxBill diimpor sebagai plan `Radius`.

## Kompatibilitas respons

Format sama dengan `radius.php` lama: JSON `control:` / `reply:`, status 204 untuk authenticate sukses, 401 untuk ditolak. Keputusan auth memakai logika yang sama dengan server UDP bawaan.

Login voucher (kode sebagai username, dengan password sama dengan kode atau kosong) juga didukung di endpoint ini, dengan pembatas yang sama seperti server bawaan.

## Allow-list

`radius.php` lama tidak punya autentikasi. Di NuxBill, isi setting `radius_rest_allow` dengan IP FreeRADIUS (pisahkan koma, boleh CIDR). Kosong = hanya loopback (127.0.0.0/8, ::1), dan NuxBill mencatat peringatan saat start; isi dengan IP FreeRADIUS jika berjalan di host lain. `X-Forwarded-For` hanya dipercaya jika `trust_proxy=yes` dan koneksi langsung datang dari loopback atau dari `trusted_proxies`; allow-list selalu memakai alamat koneksi asli. Endpoint ini bebas CSRF karena dipanggil mesin; allow-list adalah pengamannya.

## Disconnect

Plan `Radius` tidak memanggil API router. Putus paksa (plan habis atau admin menekan Disconnect) dikirim sebagai CoA Disconnect-Request langsung ke MikroTik. Agar berfungsi, daftarkan MikroTik di menu NAS dan aktifkan `/radius incoming` (port 3799).

## Kapan memilih server bawaan

| | FreeRADIUS + REST | Server bawaan |
|---|---|---|
| Perubahan di MikroTik | Tidak ada | Alamat dan secret `/radius` diganti |
| Komponen | NuxBill + FreeRADIUS | Satu proses |
| Modul lain (EAP, dll.) | Bisa | Tidak ada |
| Metode auth | Sesuai FreeRADIUS | PAP, CHAP, MS-CHAPv2 |
| Latensi | Satu hop HTTP per login | Langsung |
| Login voucher | Ya | Ya |

Pilih bawaan untuk instalasi baru. Pilih FreeRADIUS saat migrasi bertahap, atau jika butuh modul FreeRADIUS lain. Jika FreeRADIUS sudah memakai UDP 1812 di host yang sama, set `NUXBILL_RADIUS=off`. Pengerasan FreeRADIUS ada di [keamanan.md](keamanan.md#freeradius-di-jalur-rest).
