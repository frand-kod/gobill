# Setup MikroTik

Untuk operator yang menghubungkan router MikroTik ke NuxBill. Ada dua mode: **mode API** (NuxBill mengatur router lewat RouterOS API) dan **mode RADIUS bawaan** (router bertanya ke server RADIUS NuxBill). Keduanya bisa dipakai bersamaan. Diuji di RouterOS 6.49.22. Hardening ada di [keamanan.md](keamanan.md).

Ganti `192.168.88.10` dengan IP host NuxBill dan `<SECRET>` dengan secret RADIUS yang sama di kedua sisi.

## Mode API

NuxBill memanggil RouterOS API (TCP 8728, atau 8729 untuk TLS) untuk membuat user hotspot/secret PPPoE, queue, dan memutus sesi.

    /user group add name=nuxbill policy=read,write,api,test
    /user add name=nuxbill group=nuxbill password=<PASSWORD-KUAT>

Di UI NuxBill, tambah router: IP MikroTik, user `nuxbill`, dan passwordnya. Lalu sinkronkan paket ke profil router.

## Mode RADIUS bawaan

**RADIUS client (hotspot dan PPPoE):**

    /radius add service=hotspot,ppp address=192.168.88.10 secret=<SECRET> authentication-port=1812 accounting-port=1813 timeout=2s
    /radius incoming set accept=yes port=3799

`/radius incoming` membuka CoA di port 3799, dipakai NuxBill untuk memutus sesi atau mengubah paket tanpa menunggu reconnect.

**Hotspot:**

    /ip hotspot profile set [find] use-radius=yes radius-accounting=yes interim-update=1m

**PPPoE:**

    /ppp aaa set use-radius=yes accounting=yes interim-update=1m

**Daftarkan NAS di NuxBill** (menu NAS): IP router, secret yang sama, dan opsi "require Message-Authenticator" bila router mendukung. `address` pada `/radius` harus sama dengan IP host NuxBill yang dijangkau router. Tanpa baris NAS, paket dari router dibuang dan dicatat di log, dan disconnect hanya jadi peringatan.

Login voucher di halaman hotspot (kode sebagai username) diaktifkan langsung lewat RADIUS. Percobaan voucher gagal dibatasi 10 kali per 15 menit per NAS dan MAC, dan 100 kali per 15 menit per NAS.

## Pelajaran dari uji lapangan

1. **Host NuxBill tidak boleh menjadi klien hotspot.** NAT universal hotspot membuat router menjangkau host itu lewat `to-address`, bukan IP aslinya, sehingga RADIUS dan CoA gagal. Bypass:

        /ip hotspot ip-binding add mac-address=<MAC-HOST-NUXBILL> type=bypassed

   atau letakkan host di port/VLAN terpisah.

2. **RouterOS 6.49 dan `require-message-auth`.** NuxBill menambahkan Message-Authenticator pada semua balasan, dan nilai `yes-for-request-resp` diterima router:

        /radius set [find] require-message-auth=yes-for-request-resp

3. **NAS-IP-Address vs IP sumber untuk CoA.** Router mencocokkan CoA dengan NAS-IP-Address miliknya. CoA yang membawa identitas salah ditolak (NAK). NuxBill kini mengirim NAS-IP-Address yang dilaporkan NAS itu sendiri dan men-decode Error-Cause di log. Pastikan IP NAS di NuxBill sama dengan yang dipakai router sebagai sumber paket RADIUS; jika router punya beberapa IP, set `src-address` pada `/radius`.

4. **Uji aman di router produksi.** Jangan ubah profil yang dipakai pelanggan. Buat entry `/radius` terpisah dan profil uji yang memakai `domain=` dengan `split-user-domain=yes`:

        /radius add service=hotspot address=192.168.88.10 secret=<SECRET> domain=uji.local comment=nuxbill-test

   Hanya login `user@uji.local` yang diarahkan ke NuxBill. Pelanggan lain tetap lewat server lama. Setelah selesai, kembalikan `split-user-domain=no` dan hapus entry `/radius`, profil, dan user uji.
