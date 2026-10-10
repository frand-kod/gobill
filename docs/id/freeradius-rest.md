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

`radius.php` lama tidak punya autentikasi. Di gobill, pengamannya adalah pengaturan `radius_rest_allow`:

- Isi dengan IP FreeRADIUS. Boleh memakai CIDR, dan dipisah koma.
- Jika kosong, hanya loopback yang diizinkan, yaitu `127.0.0.0/8` dan `::1`. gobill mencatat peringatan saat start.
- Jika FreeRADIUS berjalan di host lain, isi dengan IP-nya.

`X-Forwarded-For` hanya dipercaya jika `trust_proxy` = `yes` dan koneksi langsung datang dari loopback atau dari `trusted_proxies`. Allow-list selalu memakai alamat koneksi asli.

Endpoint ini tidak memakai CSRF karena dipanggil oleh mesin. Allow-list adalah pengamannya.

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
