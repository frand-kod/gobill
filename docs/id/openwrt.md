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

Diuji dengan OpenWrt 25.12.6 dan CoovaChilli 1.7.

1. Pasang paket. OpenWrt 25 memakai nftables, sedangkan skrip Chilli memanggil `iptables`, jadi pasang juga `iptables-nft`:

   ```
   apk add coova-chilli iptables-nft
   ```

2. Serahkan LAN ke Chilli. LAN tidak punya IP, dan DHCP OpenWrt di LAN dimatikan. Chilli memberi IP ke klien sendiri.

   ```
   uci set network.lan.proto=none
   uci delete network.lan.ipaddr
   uci set dhcp.lan.ignore=1
   uci add_list firewall.@zone[0].device=tun0
   uci commit
   ```

   Zona `lan` harus berisi `tun0`. Tanpa itu, klien yang sudah login tidak bisa keluar ke internet.

3. Isi `/etc/config/chilli`:

   ```
   config chilli
       option disabled      0
       option dhcpif        br-lan
       option net           10.1.0.0/24
       option uamlisten     10.1.0.1
       option uamserver     <URL halaman login>
       option uamsecret     <rahasia UAM>
       option radiusserver1 <IP gobill>
       option radiusserver2 <IP gobill>
       option radiussecret  <secret NAS di gobill>
       option radiusnasid   <nama NAS>
       option coaport       3799
       option uamallowed    <IP gobill>
   ```

   - `coaport` membuat Chilli menerima CoA dan Disconnect. FUP, boost, dan tombol putus sesi butuh ini.
   - Chilli hanya menerima CoA dari `radiusserver1`. Jika gobill mengirim dari IP lain, tambahkan `option coanoipcheck 1`.
   - `uamallowed` membuka halaman portal dan bayar gobill sebelum login.

4. Jalankan `/etc/init.d/network reload`, lalu `/etc/init.d/chilli enable` dan `/etc/init.d/chilli restart`.

Daftarkan IP OpenWrt di **Jaringan > NAS** dengan tipe **WISPr**. Cek sesi dengan `chilli_query list`. Dua kolom terakhir sebelum URL adalah limit up dan down dalam bit per detik.

## Hasil uji lab

- Login, accounting, Disconnect, dan isolir berjalan.
- CoA mengubah kecepatan tanpa memutus sesi.
- Kuota: Chilli memutus sesi setelah `CoovaChilli-Max-Total-Octets` habis.
- Kecepatan: Chilli membatasi dengan membuang paket. Unduhan TCP mendapat sekitar 60 sampai 95 persen dari limit, dan kadang tersendat beberapa detik. Ini perilaku Chilli, bukan gobill.
