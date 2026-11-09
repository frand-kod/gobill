# FreeRADIUS lewat REST

Dokumen ini untuk operator yang sudah memakai FreeRADIUS, biasanya dari PHPNuxBill, dan ingin tetap memakainya. gobill menyediakan endpoint `radius.php` yang kompatibel untuk modul `rlm_rest`.

**Untuk:** operator

**Prasyarat:** FreeRADIUS sudah berjalan dengan modul `rest`. gobill sudah terpasang dan bisa dijangkau dari FreeRADIUS.

## Cara pakai

Di `/etc/freeradius/3.0/mods-enabled/rest`, ubah `connect_uri` ke gobill:

    connect_uri = "https://<gobill>/radius.php"

Endpoint `/radius/rest` juga tersedia dan fungsinya sama.

Bagian `authorize`, `authenticate`, `accounting`, dan `post-auth` tidak perlu diubah. Konfigurasi MikroTik juga tidak perlu diubah. Plan `RadiusRest` dari PHPNuxBill diimpor sebagai plan `Radius`.

## Kompatibilitas respons

Format respons sama dengan `radius.php` lama:

- JSON dengan bagian `control:` dan `reply:`.
- Status 204 untuk authenticate yang berhasil.
- Status 401 untuk permintaan yang ditolak.

Keputusan auth memakai logika yang sama dengan server UDP bawaan.

Login voucher juga didukung di endpoint ini. Kode voucher dipakai sebagai username. Password-nya sama dengan kode, atau kosong. Pembatasnya sama dengan server bawaan.

## Allow-list

`radius.php` lama tidak punya autentikasi. gobill menutup endpoint ini sampai Anda membukanya. Request hanya diterima jika salah satu syarat ini terpenuhi:

- Alamat koneksinya ada di pengaturan `radius_rest_allow` (IP atau CIDR, dipisah koma). Untuk FreeRADIUS di host yang sama, isi `127.0.0.1, ::1`.
- Request membawa header `X-Gobill-Secret` berisi nilai pengaturan `radius_rest_secret`. Perbandingannya constant-time. Request dengan secret yang benar diterima dari alamat mana saja.

Jika kedua pengaturan kosong, semua request ditolak dengan 403 dan gobill mencatat peringatan saat start. Ini berubah: sebelumnya `radius_rest_allow` kosong berarti hanya loopback. Setelah upgrade, isi salah satu pengaturan di **Pengaturan > System & Security > FreeRADIUS**, atau login lewat FreeRADIUS berhenti bekerja.

Untuk mengirim secret dari FreeRADIUS 3, tambahkan header pada panggilan `rest`, misalnya di `sites-enabled/default`: `update control { REST-HTTP-Header += "X-Gobill-Secret: <secret>" }` sebelum `rest` di `authorize`, `post-auth`, dan `accounting`. Cocokkan sintaksnya dengan dokumentasi `rlm_rest` versi FreeRADIUS Anda. Jika header tidak bisa diatur, pakai `radius_rest_allow`.

`X-Forwarded-For` hanya dipercaya jika `trust_proxy` = `yes` dan koneksi langsung datang dari loopback atau dari `trusted_proxies`. Allow-list selalu memakai alamat koneksi asli. Request yang membawa `X-Forwarded-For`, `X-Real-IP`, `Forwarded` atau `CF-Connecting-IP` tidak pernah lolos allow-list. Alasannya, reverse proxy di host yang sama membuat semua klien internet terlihat sebagai 127.0.0.1. Jika FreeRADIUS lewat proxy, pakai `radius_rest_secret`.

Endpoint ini tidak memakai CSRF karena dipanggil oleh mesin. Allow-list atau secret adalah pengamannya.

## Disconnect

Plan `Radius` tidak memanggil API router. Putus paksa, misalnya saat paket habis atau admin menekan Disconnect, dikirim sebagai CoA Disconnect-Request langsung ke MikroTik.

Agar ini berfungsi:

1. Daftarkan MikroTik di menu NAS.
2. Aktifkan `/radius incoming` dengan port 3799. Lihat [setup MikroTik](mikrotik.md).

## Kapan memilih server bawaan

| | FreeRADIUS dan REST | Server bawaan |
|---|---|---|
| Perubahan di MikroTik | Tidak ada | Alamat dan secret `/radius` diganti |
| Komponen | gobill dan FreeRADIUS | Satu proses |
| Modul lain, misalnya EAP | Bisa | Tidak ada |
| Metode auth | Sesuai FreeRADIUS | PAP, CHAP, dan MS-CHAPv2 |
| Latensi | Satu hop HTTP per login | Langsung |
| Login voucher | Ya | Ya |

Pilih server bawaan untuk instalasi baru. Pilih FreeRADIUS saat migrasi bertahap, atau jika butuh modul FreeRADIUS lain.

Jika FreeRADIUS sudah memakai UDP 1812 di host yang sama, set `GOBILL_RADIUS=off`. Pengerasan FreeRADIUS ada di [keamanan](security.md#freeradius-di-jalur-rest).

## Lihat juga

- [Setup MikroTik](mikrotik.md): mode API dan RADIUS bawaan.
- [Keamanan](security.md): pengerasan RADIUS dan FreeRADIUS.
- [Konfigurasi](configuration.md#jaringan-dan-proxy): `radius_rest_allow` dan proxy.
- [Migrasi dari PHPNuxBill](migration-phpnuxbill.md): pindah bertahap dari sistem lama.
