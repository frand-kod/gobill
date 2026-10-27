# Keamanan

Dokumen ini berisi pengerasan RADIUS, firewall, dan keamanan aplikasi, termasuk 2FA untuk admin. Bagian RADIUS dikerjakan setelah gobill dan MikroTik sudah berjalan. Lihat [setup MikroTik](mikrotik.md) dulu.

**Untuk:** operator dan pengembang

**Prasyarat:** gobill dan router sudah berjalan. Lihat [instalasi](installation.md) dan [setup MikroTik](mikrotik.md).

## Pengerasan RADIUS

### Message-Authenticator (BlastRADIUS, CVE-2024-3596)

gobill menambahkan Message-Authenticator pada semua balasan dan CoA.

Di MikroTik, aktifkan:

    /radius set [find] require-message-auth=yes-for-request-resp
    /radius print detail

Form NAS di gobill punya pengaturan "Message-Authenticator" dengan tiga mode:

- **Otomatis (disarankan).** Access-Request tanpa Message-Authenticator diterima sampai NAS pernah mengirim satu Message-Authenticator yang valid. Sejak itu, permintaan tanpa atribut ini dibuang dan dicatat. Daftar NAS menampilkan "MA: belum terlihat" atau "MA: sudah terlihat".
- **Selalu wajib.** Permintaan tanpa Message-Authenticator yang valid langsung dibuang. Pakai hanya jika NAS selalu mengirimnya.
- **Tidak wajib (NAS lama).** Permintaan tanpa Message-Authenticator diterima. Pakai untuk firmware lama yang tidak pernah mengirimnya. Atribut yang salah atau ganda tetap ditolak.

Message-Authenticator yang ada tetapi salah, rusak, atau berulang selalu ditolak, di mode mana pun.

RouterOS 6.49.18 ke atas dan 7.15 ke atas mengirim Message-Authenticator pada Access-Request. Versi lebih lama mungkin tidak, jadi pakai "Tidak wajib" untuk versi itu. Agar router memeriksa balasan yang dikirim gobill, jalankan di router:

    /radius set [find] require-message-auth=yes-for-request-resp

Endpoint RADIUS REST `/radius.php` tidak terpengaruh mode ini. FreeRADIUS di depannya melakukan pemeriksaannya sendiri.

### Shared secret

- Gunakan minimal 16 karakter acak, unik per NAS. Buat dengan `openssl rand -base64 24`.
- Jangan pakai ulang password hotspot atau password admin.
- Ganti secret di MikroTik, lalu samakan di form NAS:

        /radius set [find address=IP-GOBILL] secret=<SECRET-BARU>

### Firewall

**MikroTik.** Hanya gobill yang boleh mengirim CoA ke port 3799:

    /ip firewall filter add chain=input protocol=udp dst-port=3799 src-address=192.168.88.10 action=accept
    /ip firewall filter add chain=input protocol=udp dst-port=3799 action=drop

Jika `input` sudah punya rule drop umum, letakkan kedua rule di atasnya dengan `place-before=<nomor>`. Cek nomornya dengan `/ip firewall filter print`.

**Host gobill, firewalld.** Buat satu rule per NAS:

    sudo firewall-cmd --permanent --add-rich-rule='rule family="ipv4" source address="192.168.88.1" port port="1812-1813" protocol="udp" accept'
    sudo firewall-cmd --permanent --add-rich-rule='rule family="ipv4" source address="192.168.1.0/24" port port="8080" protocol="tcp" accept'
    sudo firewall-cmd --reload

Jangan memakai `--add-port=1812/udp` atau `--add-port=8080/tcp`. Keduanya membuka port untuk semua IP.

**Host gobill, nftables.** Input default drop. Pastikan SSH dari LAN admin diizinkan:

    table inet gobill {
        chain input {
            type filter hook input priority 0; policy drop;
            iif lo accept
            ct state established,related accept
            ip saddr 192.168.88.1 udp dport { 1812, 1813 } accept
            ip saddr 192.168.1.0/24 tcp dport 8080 accept
            ip saddr 192.168.1.0/24 tcp dport 22 accept
        }
    }

Simpan di `/etc/nftables.conf`. Periksa dengan `sudo nft list ruleset`, lalu terapkan dengan `sudo nft -f /etc/nftables.conf`. Jika FreeRADIUS berada di host lain, tambahkan `ip saddr IP-FREERADIUS tcp dport 8080 accept`.

**Batasi `/radius.php`.** Nilai `radius_rest_allow` kosong berarti hanya loopback. Isi dengan IP FreeRADIUS jika berjalan di host lain. `trust_proxy` tidak memengaruhi allow-list ini. Selama kosong, request yang membawa header proxy (`X-Forwarded-For`, `X-Real-IP`, `Forwarded`, `CF-Connecting-IP`) ditolak, karena di belakang Nginx atau cloudflared di host yang sama semua klien internet datang dari loopback. Lihat [FreeRADIUS lewat REST](freeradius-rest.md).

### Link jarak jauh (VPS)

Jangan kirim UDP RADIUS atau CoA lewat internet terbuka.

**WireGuard bawaan (disarankan, RouterOS 7).** gobill membawa server WireGuard userspace di dalam binary: tanpa root, tanpa `/dev/net/tun`, tanpa paket di host, dan jalan juga di Docker. Router yang membuka koneksi ke gobill, jadi router di belakang NAT atau CGNAT bisa dijangkau. RADIUS (1812 dan 1813) dilayani di dalam tunnel, sedangkan CoA dan API RouterOS dikirim lewat tunnel.

Fitur ini mati secara default.

1. Buka **Pengaturan → WireGuard** (hanya SuperAdmin). Pilih aktif, isi port UDP (bawaan 51820), endpoint publik (domain atau IP publik yang langsung menuju server ini, tanpa port), dan subnet tunnel (bawaan `10.99.0.0/24`). Kartu Status menampilkan hasil pemeriksaan alamat publik dan UDP.
2. Buka halaman router (**Router → Ubah**), bagian **Tunnel WireGuard**, lalu klik **Provision**. gobill membuat keypair untuk router (kunci privat disimpan terenkripsi lewat `internal/secret`, tidak pernah teks biasa di database), mengambil satu IP dari subnet, dan membuat baris NAS dengan secret RADIUS sendiri untuk router itu.
3. Tempel blok perintah RouterOS yang tampil ke terminal router. Blok ini hanya muncul sekali karena memuat kunci privat dan secret. Isinya antarmuka dan peer WireGuard, klien NTP, `/radius` dengan `src-address` alamat tunnel, dan rule firewall untuk CoA dan API dari alamat tunnel gobill saja.
4. Ubah **Host** router menjadi alamat tunnel yang tertera supaya API RouterOS lewat tunnel. Klik **Cek tunnel** untuk menguji login API dan CoA. Status per tahap menampilkan handshake dan paket RADIUS terakhir; "handshake tidak pernah terjadi" dibedakan dari "API gagal".

Tombol di halaman router: **Provision ulang** (selalu membuat kunci baru dan kunci lama langsung tidak berlaku), **Reset sesi peer**, dan **Cabut** (peer dan baris NAS-nya langsung dihapus). Menghapus router mencabut peernya juga. Provisioning satu baris lewat token (`/tool fetch`) belum ada.

Kunci keamanan yang berlaku:

- `GOBILL_WG=off` di environment adalah kill-switch dan mengalahkan pengaturan di UI. Build dengan tag `nowg` (`go build -tags nowg ./cmd/gobill`) membuang fitur ini dari binary. Rilis resmi hanya satu varian, lengkap dengan WireGuard.
- Tiap peer hanya punya satu `AllowedIPs` (alamat tunnel-nya), tidak ada routing antar peer, dan di sisi tunnel gobill hanya mendengarkan 1812 dan 1813. HTTP admin tidak dibuka ke tunnel.
- RADIUS secret dibuat per router. Router yang dicuri atau konfigurasinya bocor cukup dicabut.
- Setelah restart, gobill memulai handshake sendiri ke alamat terakhir tiap router (alamat itu disimpan), sehingga tunnel tidak putus sekitar 2 menit menunggu router mencoba lagi.

Mengubah port, endpoint, atau subnet memutus semua router yang sudah di-provision. Pengaturan menampilkan berapa router yang terdampak dan meminta konfirmasi. Setelah disimpan, router itu ditandai "perlu provision ulang" dan blok perintahnya harus ditempel lagi. Mematikan WireGuard (atau `GOBILL_WG=off` padahal ada router yang memakainya) juga memutus mereka; gobill mencatat jumlahnya di log saat start.

Risiko yang perlu diketahui:

- **gobill mati berarti semua login baru gagal.** Sesi yang sedang berjalan tetap hidup, tetapi reconnect dan voucher baru ditolak sampai gobill hidup lagi. Jika itu tidak bisa diterima, biarkan secret dan user lokal di router tetap aktif sebagai cadangan. Kontrol isolir menjadi lebih lemah, dan keputusannya ada pada admin. Peringatan "NAS diam" di Pengaturan → Notifikasi memberi tahu jika router yang biasanya aktif berhenti mengirim request RADIUS.
- **Jam router mundur setelah reboot.** WireGuard menolak handshake yang timestamp-nya lebih lama dari handshake terakhir. MikroTik tanpa RTC (seri hAP dan sejenisnya) kembali ke jam awal setelah mati listrik. Blok perintah memasang klien NTP. Jika tunnel tetap tidak tersambung, klik **Reset sesi peer**.
- **UDP gobill tidak bisa menguji dirinya dari dalam.** Kartu Status hanya bisa memastikan UDP terbuka setelah satu router menyelesaikan handshake. Cloudflare Tunnel (cloudflared) dan record Cloudflare yang diproksi tidak meneruskan UDP: pakai record DNS-only yang menunjuk ke IP publik server. Server tanpa IP publik sama sekali (CGNAT) tidak bisa dipakai. Jika operator memblokir 51820, ganti port (misalnya 443/udp).
- Kunci WireGuard server ada di pengaturan (`wg_private`, terenkripsi) dan ikut di `gobill backup`. Kunci enkripsi `gobill.db.key` tetap harus disimpan terpisah, lihat [backup dan restore](backup-restore.md).
- Biaya: binary sekitar 4,6 MB lebih besar (23,9 MB menjadi 28,5 MB di amd64, +19%). RAM idle sekitar 3,5 MB lebih tinggi saat WireGuard mati, dan sekitar 3 MB lagi saat tunnel menyala (200 peer: sekitar 5 MB lagi dari keadaan mati). Angka ini diukur tanpa trafik.
- RouterOS 6 tidak punya WireGuard. Gunakan L2TP/IPsec atau SSTP, atau upgrade RouterOS.

**WireGuard manual (alternatif).** Jika WireGuard bawaan dimatikan atau gobill memakai tunnel di host. Buat kunci dengan `wg genkey | tee privat.key | wg pubkey`. Lalu di MikroTik:

    /interface wireguard add name=wg-gobill listen-port=13231 private-key="<KUNCI-PRIVAT-MIKROTIK>"
    /ip address add address=10.10.10.2/24 interface=wg-gobill
    /interface wireguard peers add interface=wg-gobill public-key="<KUNCI-PUBLIK-VPS>" endpoint-address=<IP-VPS> endpoint-port=51820 allowed-address=10.10.10.1/32 persistent-keepalive=25s

gobill di VPS memakai `10.10.10.1`. Daftarkan alamat itu sebagai alamat RADIUS atau NAS, dan pakai sebagai `src-address` untuk rule CoA dan untuk `/radius` di router.

**RadSec.** Di RouterOS 7:

    /radius add service=hotspot,ppp address=<IP-VPS> protocol=radsec certificate=<nama-sertifikat>

gobill belum melayani RadSec secara native. Pasang radsecproxy di VPS yang meneruskan ke `127.0.0.1:1812`, atau pakai WireGuard saja.

### Login hotspot

`http-pap` mengirim password dalam teks biasa lewat HTTP. Pilih salah satu cara berikut.

Pakai CHAP:

    /ip hotspot profile set [find] login-by=http-chap

Pakai HTTPS. Perangkat harus mempercayai sertifikatnya, jika tidak captive portal menampilkan peringatan:

    /certificate import file-name=hotspot.pem passphrase=""
    /ip hotspot profile set [find] login-by=https,http-chap ssl-certificate=<nama-sertifikat>

### PPPoE dengan MS-CHAPv2

MS-CHAPv2 bisa dibongkar offline karena DES-nya lemah. Di link yang tidak tepercaya, misalnya kabel bersama atau WiFi terbuka, jalankan PPPoE di dalam tunnel WireGuard. Di jaringan akses lokal yang terkontrol, risikonya bisa diterima.

### FreeRADIUS di jalur REST

Jaga FreeRADIUS tetap terbaru. Ini penting untuk CVE-2019-11234, CVE-2019-11235 (EAP-pwd), CVE-2022-41860, dan CVE-2022-41861 (EAP-SIM dan AKA):

    sudo apt update && sudo apt -y upgrade freeradius
    freeradius -v

Jika EAP tidak dipakai, nonaktifkan EAP:

1. Periksa dulu baris yang akan dikomentari:

        grep -n "eap" /etc/freeradius/3.0/sites-enabled/*

2. Nonaktifkan modul dan baris EAP:

        sudo rm /etc/freeradius/3.0/mods-enabled/eap
        sudo sed -i 's/^\(\s*\)eap$/\1#eap/' /etc/freeradius/3.0/sites-enabled/*
        sudo freeradius -CX | tail -n 2

Perintah `freeradius -CX` harus berakhir dengan pesan OK.

Jika `connect_uri` memakai `https`, pastikan `check_cert = yes` di blok `tls` pada `mods-enabled/rest`. Untuk sertifikat self-signed, tambahkan CA di `ca_file`.

## Keamanan aplikasi

- **Password.** Password admin dan pelanggan memakai bcrypt. Hash sha1 lama ditandai `legacy_sha1` dan diganti saat login pertama.
- **Secret terenkripsi.** Secret router, pelanggan PPPoE dan hotspot, dan NAS, serta pengaturan rahasia (token Telegram, password SMTP, secret webhook, password GOWA, kunci Tripay dan Duitku, token metrics), memakai AES-GCM. Pengaturan dari versi lama dienkripsi saat start berikutnya. Kuncinya berasal dari `GOBILL_SECRET_KEY` atau file `.key`. Simpan backup kunci bersama database. Lihat [backup dan restore](backup-restore.md#backup-kunci-bersama-database).
- **Catatan.** Secret integrasi, yaitu SMTP, Telegram, Tripay, dan token metrics, tersimpan dalam teks biasa di tabel `settings`. Ini sama seperti aplikasi lama. Lindungi file database dan backup-nya.
- **CSRF.** Dilindungi lewat `http.CrossOriginProtection` dari stdlib. Pengecualiannya hanya callback Tripay, yang diverifikasi tanda tangannya, dan `/radius.php`, yang memakai allow-list.
- **Header keamanan.** Setiap respons mengirim `X-Frame-Options: DENY` untuk mencegah clickjacking, `X-Content-Type-Options: nosniff`, dan `Referrer-Policy: same-origin`, sehingga link pembayaran QRIS beserta tokennya tidak pernah terkirim ke situs lain.
- **Pembatas percobaan.** Pembatas brute-force tersimpan di memori dan di-reset saat restart.
  - Login admin dan portal: 10 kegagalan dalam 15 menit mengunci IP itu. Satu username hanya diperlambat (tambah 0,25 detik per kegagalan, maksimal 3 detik), tidak dikunci, sehingga orang lain tidak bisa mengunci akun admin. Login 2FA memakai pembatas yang sama.
  - Voucher RADIUS: 10 kegagalan dalam 15 menit per NAS dan MAC, dan 100 per NAS.
  - Auth RADIUS per username: pembatas terpisah untuk password yang salah.
  - Kirim OTP: per IP 5 kali dalam 15 menit. Per nomor, jeda 60 detik dan maksimal 5 kali per jam. Jika `notify_otp` = `no`, OTP tidak dikirim sama sekali.
  - Brute force yang terdeteksi juga memicu alert operator. Lihat [monitoring](monitoring.md).
- **Lupa kata sandi.** Balasannya sama untuk username yang ada maupun tidak. Jadi tidak bisa dipakai untuk menebak akun.
- **Kata sandi pelanggan.** Minimal `customer_password_min` karakter (default 4, boleh 4 sampai 64). Berlaku untuk registrasi, ganti, reset, dan edit oleh admin. Kata sandi yang sama dengan username, dan kata sandi umum seperti `1234` atau `password`, selalu ditolak.
- **Kata sandi admin pertama.** Dibuat acak dan ditulis ke `initial-admin-password.txt` di folder database, dengan mode 0600. Tidak ditulis ke log. Hapus file itu setelah login.
- **Proxy.** `X-Forwarded-For` dibaca hanya jika koneksi langsung datang dari loopback, atau dari `trusted_proxies` dan `trust_proxy` = `yes`. Pembatas login memakai alamat klien itu. Allow-list `/radius.php` selalu memakai alamat koneksi asli, jadi header dari luar tidak bisa memalsukan IP.
- **Cetak voucher.** Hanya untuk peran staf: SuperAdmin, Admin, Agent, dan Sales.
- **Endpoint publik.** `/health` hanya memuat status database dan disk. `/metrics` memakai bearer token dan mengembalikan 404 jika token dimatikan.
- **Sesi.** Cookie memakai `HttpOnly`, `SameSite=Lax`, dan `Secure` secara bawaan. `Secure` hanya nonaktif dengan `GOBILL_HTTPS=0`. Sesi dicabut saat password, role, atau status berubah.
- **Batas diam sesi.** Diatur oleh `session_timeout_duration`, dalam menit, bawaan 120. Pengaturan `single_session` = `yes` membatasi satu sesi admin aktif.
- **Role.** Dicek di middleware untuk tiap route. Admin tidak bisa mengangkat SuperAdmin. SuperAdmin terakhir dilindungi.
- **SQL.** Semua query lewat `sqlc` dengan parameter terikat.
- **Log.** Error notifikasi disaring, sehingga token dan API key tidak bocor.
- **Dump database.** File `docs/*.sql` dan `*.sql.gz` ada di `.gitignore` dan tidak boleh di-commit. Jangan menaruh kredensial nyata di dokumen atau test. Pakai placeholder seperti `<SECRET>`.

## Verifikasi dua langkah (2FA) admin

2FA bersifat opsional untuk setiap akun admin. Pelanggan tidak memakai 2FA. Setelah password benar, login meminta kode 6 digit dari aplikasi authenticator, misalnya Google Authenticator atau Authy. Kode berlaku 30 detik dan diterima dengan toleransi satu langkah, yaitu ±30 detik.

Disarankan untuk setiap SuperAdmin. Akun SuperAdmin bisa mengubah semua pengaturan dan pengguna.

### Mengaktifkan

1. Login, klik nama Anda di pojok kanan atas, lalu **Verifikasi dua langkah (2FA)**. Anda juga bisa langsung membuka `/admin/2fa`.
2. Klik **Aktifkan 2FA**.
3. Pindai kode QR dengan aplikasi authenticator, atau ketik kunci yang tertulis di bawahnya.
4. Masukkan kode 6 digit dari aplikasi. 2FA baru aktif setelah kode ini benar.
5. Simpan 8 **kode pemulihan** yang muncul. Kode hanya ditampilkan sekali.

### Kode pemulihan

- Bentuknya `XXXX-XXXX`. Setiap kode hanya bisa dipakai sekali dan disimpan sebagai hash bcrypt.
- Dipakai di layar kedua login jika aplikasi authenticator tidak bisa diakses. Ketik dengan atau tanpa tanda strip.
- Setelah dipakai, kode itu mati. Jika habis, nonaktifkan lalu aktifkan lagi 2FA untuk mendapat 8 kode baru.

### Menonaktifkan

Di `/admin/2fa`, isi password saat ini dan satu kode 6 digit dari aplikasi, lalu klik **Nonaktifkan 2FA**. Kode pemulihan yang tersisa ikut dihapus.

### Reset oleh SuperAdmin

Jika admin kehilangan aplikasi dan kode pemulihan:

1. SuperAdmin membuka **Pengguna Admin**.
2. SuperAdmin memilih akun tersebut, lalu klik **Reset 2FA**.

Admin itu bisa masuk dengan password saja. 2FA harus diaktifkan lagi. Tindakan ini tercatat di log aktivitas sebagai `users.2fa.reset`. Reset tidak meminta password SuperAdmin. Karena itu, jaga akun SuperAdmin sendiri dengan 2FA.

### Batasan

- Percobaan kode salah dihitung bersama pembatas login: 10 kali gagal dalam 15 menit mengunci IP, dan nama pengguna diperlambat.
- Kode yang sudah dipakai tidak bisa dipakai ulang. Riwayat kode yang sudah dipakai disimpan di memori. Setelah restart, kode yang sama bisa diterima lagi selama sisa langkah waktu, maksimal sekitar 90 detik.
- Login pelanggan dan API tidak memakai 2FA admin. Sesi admin hanya dibuka lewat halaman login.

## Lupa password admin

Ada dua jalur. Keduanya membuat password acak baru yang hanya ditampilkan sekali. Semua sesi admin itu ikut berakhir, jadi admin harus masuk ulang.

### Dari layar: SuperAdmin

1. SuperAdmin membuka **Pengguna Admin**, memilih akun, lalu klik **Reset kata sandi**. Tombol ini tidak muncul untuk akun sendiri. Untuk akun sendiri, pakai **Ganti Password**.
2. Konfirmasi. Salin password yang muncul. Setelah halaman ditutup, password itu tidak bisa dilihat lagi. Jika hilang, ulangi reset.
3. Berikan password itu ke admin. Admin harus masuk ulang.

Tindakan ini tercatat di log aktivitas sebagai `users.password.reset` dengan nama akun. Password tidak pernah masuk log.

### Dari server: perintah CLI

Pakai jalur ini jika tidak ada SuperAdmin yang bisa masuk. Jalankan di server, dengan `GOBILL_DB` yang sama dengan service. Service tidak perlu dihentikan.

    GOBILL_DB=/var/lib/gobill/gobill.db gobill reset-admin-password admin

Password baru dicetak ke layar sekali. Jangan simpan output-nya di file log.

Tambahkan `--disable-2fa` jika aplikasi authenticator juga hilang. 2FA akan dimatikan dan kode pemulihan dihapus, sehingga admin bisa masuk dengan password saja. Di Docker, jalankan dengan `docker exec -it <nama-container> gobill reset-admin-password admin` dan pastikan `GOBILL_DB` menunjuk ke `/data/gobill.db`.

## Lihat juga

- [Setup MikroTik](mikrotik.md): konfigurasi RADIUS di router.
- [FreeRADIUS lewat REST](freeradius-rest.md): jalur REST dan allow-list.
- [Konfigurasi](configuration.md#jaringan-dan-proxy): pengaturan proxy dan allow-list.
- [Monitoring](monitoring.md): alert brute force dan endpoint `/metrics`.
- [Backup dan restore](backup-restore.md): backup kunci dan database.
