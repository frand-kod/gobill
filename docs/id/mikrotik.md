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

    /ip hotspot profile set [find] use-radius=yes radius-accounting=yes interim-update=1m

### PPPoE

    /ppp aaa set use-radius=yes accounting=yes interim-update=1m

### Daftarkan NAS di gobill

Di menu NAS, isi:

- IP router;
- secret yang sama;
- opsi "require Message-Authenticator", jika router mendukungnya.

`address` pada `/radius` harus sama dengan IP host gobill yang bisa dijangkau router. Tanpa baris NAS, paket dari router dibuang dan dicatat di log. Disconnect hanya menjadi peringatan.

Login voucher di halaman hotspot memakai kode sebagai username. Login ini langsung lewat RADIUS. Percobaan yang gagal dibatasi 10 kali per 15 menit per NAS dan MAC, dan 100 kali per 15 menit per NAS.

## Pelajaran dari uji lapangan

1. **Host gobill tidak boleh menjadi klien hotspot.** NAT universal hotspot membuat router menjangkau host itu lewat `to-address`, bukan IP aslinya. Akibatnya RADIUS dan CoA gagal. Solusinya, bypass host itu:

        /ip hotspot ip-binding add mac-address=<MAC-HOST-GOBILL> type=bypassed

   Cara lain: letakkan host di port atau VLAN terpisah.

2. **RouterOS 6.49 dan `require-message-auth`.** gobill menambahkan Message-Authenticator pada semua balasan. Router menerima nilai `yes-for-request-resp`:

        /radius set [find] require-message-auth=yes-for-request-resp

3. **NAS-IP-Address dan IP sumber untuk CoA.** Router mencocokkan CoA dengan NAS-IP-Address miliknya. CoA dengan identitas yang salah ditolak (NAK). gobill sekarang mengirim NAS-IP-Address yang dilaporkan NAS itu sendiri, dan mendecode Error-Cause di log. Pastikan IP NAS di gobill sama dengan IP yang dipakai router sebagai sumber paket RADIUS. Jika router punya beberapa IP, set `src-address` pada `/radius`.

4. **Uji aman di router produksi.** Jangan ubah profil yang dipakai pelanggan. Buat entry `/radius` terpisah dan profil uji dengan `domain=` dan `split-user-domain=yes`:

        /radius add service=hotspot address=192.168.88.10 secret=<SECRET> domain=uji.local comment=gobill-test

   Hanya login `user@uji.local` yang diarahkan ke gobill. Pelanggan lain tetap lewat server lama.

   Setelah selesai:

   1. Kembalikan `split-user-domain=no`.
   2. Hapus entry `/radius` uji.
   3. Hapus profil dan user uji.

## Lihat juga

- [Keamanan](security.md#pengerasan-radius): pengerasan RADIUS, firewall, dan CoA.
- [FreeRADIUS lewat REST](freeradius-rest.md): alternatif jika memakai FreeRADIUS.
- [Migrasi dari PHPNuxBill](migration-phpnuxbill.md): pindah dari sistem lama.
