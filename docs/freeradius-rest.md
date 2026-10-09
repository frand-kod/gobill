# FreeRADIUS lewat REST

Untuk operator yang sudah punya FreeRADIUS (biasanya dari PHPNuxBill) dan ingin tetap memakainya. NuxBill menyediakan endpoint kompatibel `radius.php` untuk modul `rlm_rest`.

## Cara pakai

Di `/etc/freeradius/3.0/mods-enabled/rest`, ubah `connect_uri` ke NuxBill:

    connect_uri = "https://<nuxbill>/radius.php"

(`/radius/rest` juga tersedia dengan fungsi sama.) Bagian `authorize`, `authenticate`, `accounting`, dan `post-auth`, serta konfigurasi MikroTik, tidak perlu diubah. Plan `RadiusRest` dari PHPNuxBill diimpor sebagai plan `Radius`.

## Kompatibilitas respons

Format sama dengan `radius.php` lama: JSON `control:` / `reply:`, status 204 untuk authenticate sukses, 401 untuk ditolak. Keputusan auth memakai logika yang sama dengan server UDP bawaan.

Perbedaan: voucher yang login lewat username tanpa password (mode voucher RADIUS di `radius.php` lama) belum didukung di endpoint ini; gunakan server bawaan untuk itu.

## Allow-list

`radius.php` lama tidak punya autentikasi. Di NuxBill, isi setting `radius_rest_allow` dengan IP FreeRADIUS (pisahkan koma, boleh CIDR). Kosong = semua boleh, dan NuxBill mencatat peringatan saat start. `X-Forwarded-For` hanya dipercaya jika `trust_proxy=yes`. Endpoint ini bebas CSRF karena dipanggil mesin; allow-list adalah pengamannya.

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
| Login voucher tanpa password | Belum | Ya |

Pilih bawaan untuk instalasi baru. Pilih FreeRADIUS saat migrasi bertahap, atau jika butuh modul FreeRADIUS lain. Jika FreeRADIUS sudah memakai UDP 1812 di host yang sama, set `NUXBILL_RADIUS=off`. Pengerasan FreeRADIUS ada di [keamanan.md](keamanan.md#freeradius-di-jalur-rest).
