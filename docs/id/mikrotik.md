# Setup MikroTik

   **Router di belakang NAT atau CGNAT tidak bisa menerima CoA.** gobill mengirim CoA ke IP sumber paket RADIUS di port 3799. Jika router keluar lewat NAT, misalnya modem ISP atau CGNAT, paket itu tidak sampai. Login dan accounting tetap jalan, tetapi Disconnect dan isolir paksa gagal. Sesi baru berakhir saat pelanggan reconnect. Solusinya, sambungkan router dan gobill lewat tunnel, lihat [link jarak jauh](security.md#link-jarak-jauh-vps).
Dokumen ini menjelaskan cara menghubungkan router MikroTik ke gobill. Ada dua mode, dan keduanya bisa dipakai bersamaan:

- **Mode API.** gobill mengatur router lewat RouterOS API.
- **Mode RADIUS bawaan.** Router bertanya ke server RADIUS gobill.

Setup ini diuji di RouterOS 6.49.22. Pengerasan ada di [keamanan](security.md).

**Untuk:** operator

**Prasyarat:** gobill sudah berjalan. Ganti `192.168.88.10` dengan IP host gobill, dan `<SECRET>` dengan secret RADIUS yang sama di kedua sisi.

## Mode API

gobill memanggil RouterOS API untuk membuat user hotspot, membuat secret PPPoE, membuat queue, dan memutus sesi. Port-nya TCP 8728, atau 8729 jika memakai TLS.

Pada port 8729 gobill mem-pin sertifikat router (trust on first use). Koneksi TLS pertama menyimpan sidik jari SHA-256 sertifikat router; koneksi berikutnya ditolak, sebelum sandi dikirim, jika sidik jarinya berbeda. Pesan error menyebut sidik jari yang diharapkan dan yang diterima. Setelah Anda sengaja mengganti sertifikat di router, buka **Router > Ubah**, centang **Reset pin sertifikat**, lalu simpan; koneksi berikutnya mem-pin sertifikat baru.

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

`/radius incoming` membuka CoA di port 3799. gobill memakai CoA untuk memutus sesi atau mengubah paket, tanpa menunggu pelanggan reconnect. Saat paket Radius pelanggan berubah (isi ulang, ganti paket, atau admin mengedit paket/bandwidth), gobill mengirim CoA-Request berisi `Mikrotik-Rate-Limit` dan `Session-Timeout` baru ke sesi yang online. Jika NAS menolak (NAK) atau tidak menjawab, gobill memutus sesi dengan Disconnect-Request dan pelanggan mendapat paket baru saat login ulang.

Atur interval interim di router (contoh di bawah memakai `1m`). gobill menghitung batas sesi basi per NAS dari jarak Interim-Update yang teramati (sekitar 2x interval + 1 menit). `GOBILL_STALE_AFTER` tetap menjadi batas bawah. Tanpa interim, sesi dianggap basi setelah `GOBILL_STALE_AFTER`.

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

## Access point di belakang router

Access point (AP) di belakang MikroTik wajib memakai mode **bridge** atau **access point**. Di mode ini AP hanya memancarkan Wi-Fi. Router tetap memberi IP, menjalankan hotspot, dan melihat setiap perangkat satu per satu.

Nama mode ini berbeda di tiap merek. Cari pengaturan yang biasanya disebut **Access Point mode** atau **Bridge mode**.

Jika AP berjalan dalam mode router, AP melakukan NAT dan DHCP sendiri. Router hanya melihat satu klien, yaitu AP itu. Akibatnya:

- Semua perangkat di belakang AP terlihat sebagai satu klien.
- Satu login membuka akses untuk semua perangkat di belakang AP itu.
- `shared_users` tidak berarti, karena gobill hanya melihat satu sesi untuk seluruh AP.

Cek setelah mengubah mode:

1. Sambungkan dua perangkat ke AP yang sama.
2. Login dengan perangkat pertama.
3. Perangkat kedua harus diminta login sendiri. Jika tidak diminta, AP masih dalam mode router.

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

Pelanggan yang diisolir mendapat isolasi penuh kecuali daftar izin. Permintaan web mereka dialihkan ke halaman isolir, sehingga pengecekan captive-portal di ponsel membuka halaman itu. Semua aplikasi dan situs lain diblokir. Host di **Diizinkan saat diisolir** tetap bisa diakses: WhatsApp secara bawaan, supaya pelanggan bisa menghubungi Anda, dan aplikasi bank bila Anda menambahkannya. **Pengaturan > Isolir**, bagian **Setup router**, menampilkan aturan sebagai satu blok yang bisa disalin, dengan alamat gobill, nama address-list, dan daftar izin yang sudah terisi.

Tempel seluruh blok sekaligus, sesuai urutan. Aturan harus berada di atas aturan `accept` umum di chain forward, jika tidak klien isolir tidak terputus. Bentuk blok ini (nama list `isolir`, daftar izin `isolir-allow`):

    /ip firewall nat add chain=dstnat src-address-list=isolir dst-address-type=!local protocol=tcp dst-port=80 action=dst-nat to-addresses=192.168.99.2 to-ports=8090
    /ip firewall filter add chain=forward src-address-list=isolir dst-address=192.168.99.2 action=accept
    /ip firewall address-list add list=isolir-allow address=wa.me comment="gobill isolir allow"
    /ip firewall filter add chain=forward src-address-list=isolir protocol=tcp dst-port=443 tls-host=*.whatsapp.net action=add-dst-to-address-list address-list=isolir-allow address-list-timeout=1d comment="isolir: learn *.whatsapp.net"
    /ip firewall filter add chain=forward src-address-list=isolir protocol=tcp dst-port=443 tls-host=wa.me action=add-dst-to-address-list address-list=isolir-allow address-list-timeout=1d comment="isolir: learn wa.me"
    /ip firewall filter add chain=forward src-address-list=isolir protocol=tcp dst-port=443 tcp-flags=syn action=accept comment="isolir: TLS handshake"
    /ip firewall filter add chain=forward src-address-list=isolir dst-address-list=isolir-allow action=accept comment="isolir: allowed apps"
    /ip firewall filter add chain=forward src-address-list=isolir action=drop comment="isolir: block the rest"

Aturan ini bekerja sebagai berikut, berurutan:

1. **NAT.** HTTP port 80 dari klien isolir diarahkan ke gobill. `dst-address-type=!local` membiarkan trafik ke router itu sendiri, sehingga halaman login dan logout hotspot tetap bisa dibuka, misalnya `http://192.168.20.1/logout`.
2. **Accept gobill.** Klien isolir boleh mencapai gobill.
3. **Daftar izin.** Setiap host persis (tanpa `*.`) ditambahkan ke address-list `isolir-allow`. Setiap entri, wildcard atau bukan, mendapat aturan `tls-host`: saat klien isolir membuka koneksi TLS dengan SNI yang cocok, IP tujuan ditambahkan ke `isolir-allow` selama satu hari.
4. **Handshake TLS.** Koneksi HTTPS baru (SYN) diterima, sehingga handshake berjalan dan aturan di atas bisa mempelajari IP-nya.
5. **Aplikasi yang diizinkan.** Trafik ke alamat di `isolir-allow` diterima.
6. **Blokir sisanya.** Semua trafik lain dari klien isolir dibuang.

Nama list diambil dari pengaturan **Nama address-list MikroTik** (bawaan `isolir`). Daftar izin adalah nama itu ditambah `-allow`. Daftar izin diatur di **Diizinkan saat diisolir**, satu host per baris. Wildcard seperti `*.whatsapp.net` diperbolehkan. Setelah diubah, tempel lagi bloknya.

### Batasan RouterOS 6

- Entri DNS di address-list hanya menyelesaikan nama persis di RouterOS 6. Karena itu host persis langsung dimasukkan ke list, dan host wildcard dipelajari.
- Host wildcard dipelajari saat aplikasi membuka koneksi TLS ke host itu. IP yang dipelajari berlaku satu hari. Aplikasi yang terhubung ke host CDN baru lolos setelah aturan mempelajari host itu.
- Karena itu media WhatsApp dari host CDN yang berubah kadang bisa gagal. Pesan dan panggilan ke admin tetap berfungsi.

### Menambah aplikasi bank

Cari host yang dihubungi aplikasi, misalnya dari daftar koneksi router atau catatan dukungan aplikasi. Tambahkan di baris sendiri di **Diizinkan saat diisolir**, misalnya `*.bank.example` untuk semua hostnya atau `m.bank.example` untuk satu host. Simpan, lalu tempel lagi bloknya. Uji dari klien yang diisolir bahwa aplikasinya berfungsi, lalu pastikan situs lain tetap diblokir.

Dua item lain berada di luar chain forward:

    /ip hotspot walled-garden ip add dst-address=192.168.99.2 dst-port=8090 action=accept
    /ip hotspot ip-binding add mac-address=<MAC-HOST-GOBILL> type=bypassed

Walled garden membuat halaman isolir bisa dibuka sebelum login hotspot. Alamat dan port adalah yang didengar gobill, diatur oleh `GOBILL_HTTP` (lihat [konfigurasi](configuration.md)). Misalnya, `GOBILL_HTTP=192.168.99.2:8090` menghasilkan `to-addresses=192.168.99.2 to-ports=8090`. Jika `GOBILL_HTTP` kosong atau `0.0.0.0`, gobill memakai IPv4 LAN pertama dari host-nya. Isi `GOBILL_HTTP` dengan IP LAN tetap bila host punya lebih dari satu alamat.

Isi `<MAC-HOST-GOBILL>` dengan MAC address kartu LAN gobill. Tanpa bypass, hotspot bisa menangkap trafik gobill sendiri.

### Tes halaman isolir

gobill melayani halaman isolir untuk klien isolir di `/` dan di semua path lain, dengan header `Host` apa pun. Tes dari klien LAN dengan curl:

    curl -i -H "Host: example.com" http://192.168.99.2:8090/

Jangan tes dengan `/tool fetch` di RouterOS. Perintah itu mengirim header `Host` kedua dan mendapat `400 Bad Request`.

## Lihat juga

- [Keamanan](security.md#pengerasan-radius): pengerasan RADIUS, firewall, dan CoA.
- [FreeRADIUS lewat REST](freeradius-rest.md): alternatif jika memakai FreeRADIUS.
- [Migrasi dari PHPNuxBill](migration-phpnuxbill.md): pindah dari sistem lama.
