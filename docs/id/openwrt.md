# NAS OpenWrt dan WISPr

**Untuk:** operator yang memakai OpenWrt atau controller AP sebagai NAS.

**Prasyarat:** gobill dengan RADIUS bawaan, dan NAS yang sudah terdaftar di **Jaringan > NAS**.

## Pilih tipe NAS

Setiap NAS punya **Tipe NAS**. Tipe ini menentukan atribut kecepatan dan kuota yang dikirim gobill.

- **MikroTik.** Ini default. gobill mengirim `Mikrotik-Rate-Limit`, `Mikrotik-Total-Limit`, dan address list isolir.
- **WISPr.** Untuk CoovaChilli di OpenWrt dan controller yang memahami WISPr. gobill mengirim `WISPr-Bandwidth-Max-Up` dan `-Down` dalam bit per detik. Kuota dikirim sebagai `CoovaChilli-Max-Total-Octets` dan `-Gigawords`.
- **Standar.** gobill hanya mengirim atribut RFC: login, `Session-Timeout`, `Idle-Timeout`, dan Disconnect. Atur kecepatan di NAS sendiri. Pakai tipe ini untuk NAS yang belum dikenal.

Login, masa aktif, dan putus sesi berjalan di semua tipe.

## Batasan

- Isolir hanya jalan di MikroTik. Di WISPr dan Standar, pelanggan yang diisolir ditolak dengan pesan "Account isolated, please pay to reconnect".
- WISPr tidak punya burst. Burst paket diabaikan.
- Halaman **Tes login** dan mode FreeRADIUS REST tetap menampilkan atribut MikroTik.
- PPPoE di OpenWrt (`pppd` + `radius.so`) tidak membaca atribut kecepatan. Pakai tipe Standar.
- openNDS tidak memakai RADIUS secara langsung.

## CoovaChilli di OpenWrt

Pasang paket `coova-chilli`. Isi `/etc/config/chilli`:

```
option radiusserver1 <IP gobill>
option radiusserver2 <IP gobill>
option radiussecret  <secret NAS di gobill>
option radiusnasid   <nama NAS>
option coaport       3799
option uamallowed    <IP gobill>
```

- `coaport` membuat Chilli menerima CoA dan Disconnect. FUP, boost, dan tombol putus sesi butuh ini.
- Chilli hanya menerima CoA dari `radiusserver1`. Jika gobill mengirim dari IP lain, tambahkan `option coanoipcheck`.
- `uamallowed` membuka halaman portal dan bayar gobill sebelum login.

Daftarkan IP OpenWrt di **Jaringan > NAS** dengan tipe **WISPr**.
