# Setup MikroTik

Dokumen ini menjelaskan cara menghubungkan router MikroTik ke gobill. Ada dua mode, dan keduanya bisa dipakai bersamaan:

- **Mode API.** gobill mengatur router lewat RouterOS API.
- **Mode RADIUS bawaan.** Router bertanya ke server RADIUS gobill.

Setup ini diuji di RouterOS 6.49.22. Pengerasan ada di [keamanan](security.md).

**Untuk:** operator

**Prasyarat:** gobill sudah berjalan. Ganti `192.168.88.10` dengan IP host gobill, dan `<SECRET>` dengan secret RADIUS yang sama di kedua sisi.

## Mode API

gobill memanggil RouterOS API untuk membuat user hotspot, membuat secret PPPoE, membuat queue, dan memutus sesi. Port-nya TCP 8728, atau 8729 jika memakai TLS.

Buat user khusus di MikroTik:

    /user group add name=gobill policy=read,write,api,test
    /user add name=gobill group=gobill password=<PASSWORD-KUAT>

Di UI gobill, tambah router dengan:

- IP MikroTik;
- user `gobill`;
- password tadi.

Lalu sinkronkan paket ke profil router.

## Mode RADIUS bawaan

### RADIUS client untuk hotspot dan PPPoE

    /radius add service=hotspot,ppp address=192.168.88.10 secret=<SECRET> authentication-port=1812 accounting-port=1813 timeout=2s
    /radius incoming set accept=yes port=3799

`/radius incoming` membuka CoA di port 3799. gobill memakai CoA untuk memutus sesi atau mengubah paket, tanpa menunggu pelanggan reconnect.

### Hotspot

    /ip hotspot profile set [find] use-radius=yes radius-accounting=yes radius-interim-update=1m

### PPPoE

    /ppp aaa set use-radius=yes accounting=yes interim-update=1m

### Daftarkan NAS di gobill

Di menu NAS, isi:

- IP router;
- secret yang sama;
- mode Message-Authenticator. Biarkan **Otomatis (disarankan)**. Jika router lebih lama dari RouterOS 6.49.18 atau 7.15, pilih **Tidak wajib (NAS lama)**.

`address` pada `/radius` harus sama dengan IP host gobill yang bisa dijangkau router. Tanpa baris NAS, paket dari router dibuang dan dicatat di log. Disconnect hanya menjadi peringatan.

Login voucher di halaman hotspot memakai kode sebagai username. Login ini langsung lewat RADIUS. Percobaan yang gagal dibatasi 10 kali per 15 menit per NAS dan MAC, dan 100 kali per 15 menit per NAS.

## Pelajaran dari uji lapangan

1. **Host gobill tidak boleh menjadi klien hotspot.** NAT universal hotspot membuat router menjangkau host itu lewat `to-address`, bukan IP aslinya. Akibatnya RADIUS dan CoA gagal. Solusinya, bypass host itu:

        /ip hotspot ip-binding add mac-address=<MAC-HOST-GOBILL> type=bypassed

   Cara lain: letakkan host di port atau VLAN terpisah.

2. **RouterOS 6.49.18+ / 7.15+ dan `require-message-auth`.** gobill menambahkan Message-Authenticator pada semua balasan. Pada versi ini, atur di router agar balasan itu diperiksa:

        /radius set [find] require-message-auth=yes-for-request-resp

   Versi sebelum 6.49.18 dan 7.15 mungkin tidak mengirim Message-Authenticator pada permintaan. Untuk versi itu, atur NAS ke **Tidak wajib (NAS lama)** di gobill (lihat [keamanan](security.md#pengerasan-radius)).

3. **NAS-IP-Address dan IP sumber untuk CoA.** Router mencocokkan CoA dengan NAS-IP-Address miliknya. CoA dengan identitas yang salah ditolak (NAK). gobill mengirim NAS-IP-Address yang dilaporkan NAS itu sendiri, dan mendecode Error-Cause di log. Pastikan IP NAS di gobill sama dengan IP yang dipakai router sebagai sumber paket RADIUS. Jika router punya beberapa IP, set `src-address` pada `/radius`.

4. **Uji aman di router produksi.** Jangan ubah profil yang dipakai pelanggan. Buat entry `/radius` terpisah dan profil uji dengan `domain=` dan `split-user-domain=yes`:

        /radius add service=hotspot address=192.168.88.10 secret=<SECRET> domain=uji.local comment=gobill-test

   Hanya login `user@uji.local` yang diarahkan ke gobill. Pelanggan lain tetap lewat server lama.

   Setelah selesai:

   1. Kembalikan `split-user-domain=no`.
   2. Hapus entry `/radius` uji.
   3. Hapus profil dan user uji.

## Isolir

Isolir mengalihkan trafik HTTP pelanggan isolir ke gobill, sehingga mereka melihat halaman isolir dan bukan situs lain. **Pengaturan > Isolir**, bagian **Setup router MikroTik**, menampilkan perintah dengan alamat gobill yang sudah terisi. Bagian ini menjelaskan aturan di balik perintah tersebut.

Aturan berikut berlaku dalam urutan ini di router. Aturan `accept` harus berada sebelum aturan `drop` di chain forward:

1. **NAT.** Alihkan HTTP port 80 dari address-list isolir ke gobill, kecuali trafik ke router itu sendiri.
2. **Accept.** Izinkan klien isolir mencapai gobill.
3. **Drop opsional.** Blokir semua trafik lain dari klien isolir. Ini isolasi penuh.
4. **Walled garden.** Izinkan halaman isolir terbuka sebelum login hotspot.
5. **Bypass hotspot.** Jauhkan trafik gobill sendiri dari hotspot.

    /ip firewall nat add chain=dstnat src-address-list=isolir dst-address-type=!local protocol=tcp dst-port=80 action=dst-nat to-addresses=192.168.99.2 to-ports=8090
    /ip firewall filter add chain=forward src-address-list=isolir dst-address=192.168.99.2 action=accept
    /ip firewall filter add chain=forward src-address-list=isolir action=drop
    /ip hotspot walled-garden ip add dst-address=192.168.99.2 dst-port=8090 action=accept
    /ip hotspot ip-binding add mac-address=<MAC-HOST-GOBILL> type=bypassed

Alamat dan port adalah yang didengar gobill. Keduanya diatur oleh `GOBILL_HTTP`, lihat [konfigurasi](configuration.md). Contohnya, `GOBILL_HTTP=192.168.99.2:8090` menghasilkan `to-addresses=192.168.99.2 to-ports=8090`. Jika `GOBILL_HTTP` kosong atau `0.0.0.0`, gobill memakai IPv4 LAN pertama dari host-nya. Isi `GOBILL_HTTP` dengan IP LAN tetap bila host punya lebih dari satu alamat.

Kondisi `dst-address-type=!local` membuat trafik ke router itu sendiri tidak ikut dialihkan. Dengan begitu halaman login dan logout hotspot di router tetap bisa dibuka, misalnya `http://192.168.20.1/logout`. Tanpa kondisi ini, pelanggan yang diisolir tidak bisa keluar (logout) atau berganti akun.

Isi `<MAC-HOST-GOBILL>` dengan MAC address kartu LAN gobill. Tanpa bypass, hotspot bisa menangkap trafik gobill sendiri.

Hanya HTTP port 80 yang dialihkan ke halaman isolir. HTTPS dan aplikasi tetap bisa dipakai dengan kecepatan isolir. Tambahkan aturan drop opsional (langkah 3) hanya untuk isolasi penuh. Aturan ini juga memblokir WhatsApp dan aplikasi bank, jadi ubah pesan isolir bawaan juga.

### Tes halaman isolir

gobill melayani halaman isolir untuk klien isolir di `/` dan di semua path lain, dengan header `Host` apa pun. Tes dari klien LAN dengan curl:

    curl -i -H "Host: example.com" http://192.168.99.2:8090/

Jangan tes dengan `/tool fetch` di RouterOS. Perintah itu mengirim header `Host` kedua dan mendapat `400 Bad Request`.

## Lihat juga

- [Keamanan](security.md#pengerasan-radius): pengerasan RADIUS, firewall, dan CoA.
- [FreeRADIUS lewat REST](freeradius-rest.md): alternatif jika memakai FreeRADIUS.
- [Migrasi dari PHPNuxBill](migration-phpnuxbill.md): pindah dari sistem lama.
