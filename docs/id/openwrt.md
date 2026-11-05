# NAS OpenWrt dan WISPr

**Untuk:** operator yang memakai OpenWrt atau controller AP sebagai NAS.

**Prasyarat:** gobill dengan RADIUS bawaan, dan NAS yang sudah terdaftar di **Jaringan > NAS**.

## Pilih tipe NAS

Setiap NAS punya **Tipe NAS**. Tipe ini menentukan atribut kecepatan dan kuota yang dikirim gobill.

- **MikroTik.** Ini default. gobill mengirim `Mikrotik-Rate-Limit`, `Mikrotik-Total-Limit`, dan address list isolir.
- **WISPr.** Untuk uspot dan CoovaChilli di OpenWrt, dan controller yang memahami WISPr. gobill mengirim `WISPr-Bandwidth-Max-Up` dan `-Down` dalam bit per detik. Kuota dikirim sebagai `CoovaChilli-Max-Total-Octets` dan `-Gigawords`.
- **Standar.** gobill hanya mengirim atribut RFC: login, `Session-Timeout`, `Idle-Timeout`, dan Disconnect. Atur kecepatan di NAS sendiri. Pakai tipe ini untuk NAS yang belum dikenal.

Login, masa aktif, dan putus sesi berjalan di semua tipe.

## Batasan

- Isolir hanya jalan di MikroTik. Di WISPr dan Standar, pelanggan yang diisolir ditolak dengan pesan "Account isolated, please pay to reconnect".
- WISPr tidak punya burst. Burst paket diabaikan.
- Halaman **Tes login** dan mode FreeRADIUS REST tetap menampilkan atribut MikroTik.
- PPPoE di OpenWrt (`pppd` + `radius.so`) tidak membaca atribut kecepatan. Pakai tipe Standar.
- openNDS tidak memakai RADIUS secara langsung.

## uspot di OpenWrt (disarankan)

uspot adalah portal hotspot resmi OpenWrt yang memakai nftables. Kecepatan dibatasi oleh paket `ratelimit` dengan HTB di kernel, sehingga hasilnya akurat. Diuji dengan OpenWrt 25.12.6 dan uspot 2026.06.30.

1. Pasang paket:

   ```
   apk add uspot uspot-www ratelimit
   ```

2. Pasang dictionary RADIUS. Paket OpenWrt tidak membawanya, dan tanpa file ini uspot gagal login. Ambil `etc/dictionary` dari radcli 1.3.0, lalu `dictionary.WISPr` dan `dictionary.chillispot` dari folder `files/etc/radcli` di repo uspot. Salin ketiganya ke `/etc/radcli/`, lalu tambahkan baris ini di akhir `/etc/radcli/dictionary`:

   ```
   $INCLUDE /etc/radcli/dictionary.WISPr
   $INCLUDE /etc/radcli/dictionary.chillispot
   ```

3. Buat interface `captive`, zona firewall, DHCP, dan instance uhttpd seperti di README uspot.

4. Isi `/etc/config/uspot`:

   ```
   config uspot 'captive'
       option auth_mode    'radius'
       option interface    'captive'
       option setname      'uspot'
       option auth_server  '<IP gobill>'
       option auth_secret  '<secret NAS di gobill>'
       option acct_server  '<IP gobill>'
       option acct_secret  '<secret NAS di gobill>'
       option counters     '1'
       option das_secret   '<secret NAS di gobill>'
       option nasid        '<nama NAS>'
       option nasmac       '<MAC LAN router>'
   ```

   - `counters` wajib untuk accounting dan kuota.
   - `das_secret` menyalakan Disconnect di port 3799. Buka port ini di zona wan hanya untuk IP gobill.

Daftarkan IP OpenWrt di **Jaringan > NAS** dengan tipe **WISPr**. Biarkan Message-Authenticator di **Auto**: uspot tidak mengirim atribut ini.

Batasan uspot:

- CoA tidak bisa mengubah kecepatan. Saat FUP, boost, atau edit paket berlaku, gobill memutus sesi. Pelanggan login lagi dan mendapat kecepatan baru.
- Jangan pakai satu entri NAS untuk uspot dan perangkat lain yang mengirim Message-Authenticator. Dalam mode Auto, gobill lalu mewajibkannya, dan login dari uspot ditolak.

## CoovaChilli di OpenWrt (alternatif)

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

| | uspot | CoovaChilli |
|---|---|---|
| Login, accounting, isolir | jalan | jalan |
| Kuota | jalan | jalan |
| Disconnect | jalan | jalan |
| CoA ubah kecepatan | tidak, gobill memutus sesi | jalan |
| Akurasi kecepatan TCP | sekitar 96 persen dari limit | 60 sampai 95 persen, kadang tersendat |

Chilli membatasi kecepatan dengan membuang paket di userspace. uspot memakai HTB di kernel.
