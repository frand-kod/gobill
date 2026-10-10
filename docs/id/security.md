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

Di form NAS gobill, aktifkan opsi "require Message-Authenticator". Perangkat yang tidak mengirim atribut ini akan ditolak. Karena itu, perbarui firmware NAS lebih dulu.

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

**Batasi `/radius.php`.** Nilai `radius_rest_allow` kosong berarti hanya loopback. Ini bawaan sejak v0.1.4. Isi dengan IP FreeRADIUS jika berjalan di host lain. `trust_proxy` tidak memengaruhi allow-list ini. Lihat [FreeRADIUS lewat REST](freeradius-rest.md).

### Link jarak jauh (VPS)

Jangan kirim UDP RADIUS atau CoA lewat internet terbuka.

**WireGuard (disarankan, RouterOS 7).** Buat kunci dengan `wg genkey | tee privat.key | wg pubkey`. Lalu di MikroTik:

    /interface wireguard add name=wg-gobill listen-port=13231 private-key="<KUNCI-PRIVAT-MIKROTIK>"
    /ip address add address=10.10.10.2/24 interface=wg-gobill
    /interface wireguard peers add interface=wg-gobill public-key="<KUNCI-PUBLIK-VPS>" endpoint-address=<IP-VPS> endpoint-port=51820 allowed-address=10.10.10.1/32 persistent-keepalive=25s

gobill di VPS memakai `10.10.10.1`. Daftarkan alamat itu sebagai alamat RADIUS atau NAS, dan pakai sebagai `src-address` untuk rule CoA. RouterOS 6 tidak punya WireGuard. Gunakan L2TP/IPsec, atau upgrade RouterOS.

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
- **Secret terenkripsi.** Secret router, pelanggan PPPoE dan hotspot, dan NAS memakai AES-GCM. Kuncinya berasal dari `GOBILL_SECRET_KEY` atau file `.key`. Simpan backup kunci bersama database. Lihat [backup dan restore](backup-restore.md#backup-kunci-bersama-database).
- **Catatan.** Secret integrasi, yaitu SMTP, Telegram, Tripay, dan token metrics, tersimpan dalam teks biasa di tabel `settings`. Ini sama seperti aplikasi lama. Lindungi file database dan backup-nya.
- **CSRF.** Dilindungi lewat `http.CrossOriginProtection` dari stdlib. Pengecualiannya hanya callback Tripay, yang diverifikasi tanda tangannya, dan `/radius.php`, yang memakai allow-list.
- **Pembatas percobaan.** Pembatas brute-force tersimpan di memori dan di-reset saat restart.
  - Login admin dan portal: 10 kegagalan dalam 15 menit, dihitung per IP dan per username. Login 2FA memakai pembatas yang sama.
  - Voucher RADIUS: 10 kegagalan dalam 15 menit per NAS dan MAC, dan 100 per NAS.
  - Auth RADIUS per username: pembatas terpisah untuk password yang salah.
  - Kirim OTP: per IP 5 kali dalam 15 menit. Per nomor, jeda 60 detik dan maksimal 5 kali per jam. Jika `notify_otp` = `no`, OTP tidak dikirim sama sekali.
  - Brute force yang terdeteksi juga memicu alert operator. Lihat [monitoring](monitoring.md).
- **Lupa kata sandi.** Balasannya sama untuk username yang ada maupun tidak. Jadi tidak bisa dipakai untuk menebak akun.
- **Kata sandi pelanggan.** Minimal 8 karakter, maksimal 35. Berlaku untuk registrasi, ganti, reset, dan edit oleh admin.
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

1. Login, buka **Ganti Kata Sandi**, lalu **Kelola 2FA**. Anda juga bisa langsung membuka `/admin/2fa`.
2. Klik **Aktifkan 2FA**.
3. Pindai kode QR dengan aplikasi authenticator, atau ketik kunci yang tertulis di bawahnya.
4. Masukkan kode 6 digit dari aplikasi. 2FA baru aktif setelah kode ini benar.
5. Simpan 8 **kode pemulihan** yang muncul. Kode hanya ditampilkan sekali.

### Kode pemulihan

- Bentuknya `XXXX-XXXX`. Setiap kode hanya bisa dipakai sekali dan disimpan sebagai hash bcrypt.
- Dipakai di layar kedua login jika aplikasi authenticator tidak bisa diakses. Ketik dengan atau tanpa tanda strip.
- Setelah dipakai, kode itu mati. Jika habis, nonaktifkan lalu aktifkan lagi 2FA untuk mendapat 8 kode baru.

### Menonaktifkan

Di `/admin/2fa`, isi password saat ini dan satu kode 6 digit dari aplikasi. Kode pemulihan yang tersisa ikut dihapus.

### Reset oleh SuperAdmin

Jika admin kehilangan aplikasi dan kode pemulihan:

1. SuperAdmin membuka **Pengguna Admin**.
2. SuperAdmin memilih akun tersebut, lalu klik **Reset 2FA**.

Admin itu bisa masuk dengan password saja. 2FA harus diaktifkan lagi. Tindakan ini tercatat di log aktivitas sebagai `users.2fa.reset`. Reset tidak meminta password SuperAdmin. Karena itu, jaga akun SuperAdmin sendiri dengan 2FA.

### Batasan

- Percobaan kode salah dihitung bersama pembatas login: 10 kali gagal dalam 15 menit per IP dan per nama pengguna.
- Kode yang sudah dipakai tidak bisa dipakai ulang. Riwayat kode yang sudah dipakai disimpan di memori. Setelah restart, kode yang sama bisa diterima lagi selama sisa langkah waktu, maksimal sekitar 90 detik.
- Login pelanggan dan API tidak memakai 2FA admin. Sesi admin hanya dibuka lewat halaman login.

## Lihat juga

- [Setup MikroTik](mikrotik.md): konfigurasi RADIUS di router.
- [FreeRADIUS lewat REST](freeradius-rest.md): jalur REST dan allow-list.
- [Konfigurasi](configuration.md#jaringan-dan-proxy): pengaturan proxy dan allow-list.
- [Monitoring](monitoring.md): alert brute force dan endpoint `/metrics`.
- [Backup dan restore](backup-restore.md): backup kunci dan database.
