# Keamanan

Untuk operator dan developer. Bagian RADIUS dikerjakan setelah NuxBill dan MikroTik sudah bekerja ([mikrotik.md](mikrotik.md)). Bagian aplikasi menjelaskan apa yang sudah dilakukan kode.

## Pengerasan RADIUS

### Message-Authenticator (BlastRADIUS, CVE-2024-3596)

NuxBill menambahkan Message-Authenticator pada semua balasan dan CoA. Di MikroTik:

    /radius set [find] require-message-auth=yes-for-request-resp
    /radius print detail

Di form NAS NuxBill, aktifkan "require Message-Authenticator". Perangkat yang tidak mengirimnya akan ditolak, jadi perbarui firmware NAS lebih dulu.

### Shared secret

- Minimal 16 karakter acak, unik per NAS. Buat dengan `openssl rand -base64 24`.
- Jangan pakai ulang password hotspot atau admin.
- Ganti di MikroTik lalu samakan di form NAS:

        /radius set [find address=IP-NUXBILL] secret=<SECRET-BARU>

### Firewall

**MikroTik.** Hanya NuxBill yang boleh mengirim CoA ke 3799:

    /ip firewall filter add chain=input protocol=udp dst-port=3799 src-address=192.168.88.10 action=accept
    /ip firewall filter add chain=input protocol=udp dst-port=3799 action=drop

Jika `input` sudah punya rule drop umum, taruh keduanya di atasnya dengan `place-before=<nomor>` (cek `/ip firewall filter print`).

**Host NuxBill (firewalld).** Satu rule per NAS:

    sudo firewall-cmd --permanent --add-rich-rule='rule family="ipv4" source address="192.168.88.1" port port="1812-1813" protocol="udp" accept'
    sudo firewall-cmd --permanent --add-rich-rule='rule family="ipv4" source address="192.168.1.0/24" port port="8080" protocol="tcp" accept'
    sudo firewall-cmd --reload

Jangan memakai `--add-port=1812/udp` atau `--add-port=8080/tcp` (terbuka untuk semua IP).

**Host NuxBill (nftables).** Input default drop; pastikan SSH dari LAN admin diizinkan:

    table inet nuxbill {
        chain input {
            type filter hook input priority 0; policy drop;
            iif lo accept
            ct state established,related accept
            ip saddr 192.168.88.1 udp dport { 1812, 1813 } accept
            ip saddr 192.168.1.0/24 tcp dport 8080 accept
            ip saddr 192.168.1.0/24 tcp dport 22 accept
        }
    }

Simpan di `/etc/nftables.conf`, periksa `sudo nft list ruleset`, terapkan `sudo nft -f /etc/nftables.conf`. FreeRADIUS di host lain: tambahkan `ip saddr IP-FREERADIUS tcp dport 8080 accept`.

**Batasi `/radius.php`:** kosong = hanya loopback (bawaan sejak v0.1.4); isi `radius_rest_allow` dengan IP FreeRADIUS jika di host lain. `trust_proxy` tidak memengaruhi allow-list ini ([freeradius-rest.md](freeradius-rest.md)).

### Link jarak jauh (VPS)

Jangan kirim UDP RADIUS/CoA lewat internet terbuka.

**WireGuard (disarankan, RouterOS 7).** Buat kunci dengan `wg genkey | tee privat.key | wg pubkey`, lalu di MikroTik:

    /interface wireguard add name=wg-nuxbill listen-port=13231 private-key="<KUNCI-PRIVAT-MIKROTIK>"
    /ip address add address=10.10.10.2/24 interface=wg-nuxbill
    /interface wireguard peers add interface=wg-nuxbill public-key="<KUNCI-PUBLIK-VPS>" endpoint-address=<IP-VPS> endpoint-port=51820 allowed-address=10.10.10.1/32 persistent-keepalive=25s

NuxBill di VPS memakai `10.10.10.1`. Daftarkan sebagai alamat RADIUS/NAS dan pakai sebagai `src-address` rule CoA. RouterOS 6 tidak punya WireGuard: pakai L2TP/IPsec atau upgrade.

**RadSec.** RouterOS 7: `/radius add service=hotspot,ppp address=<IP-VPS> protocol=radsec certificate=<nama-sertifikat>`. NuxBill belum melayani RadSec secara native; pasang radsecproxy di VPS yang meneruskan ke `127.0.0.1:1812`, atau pakai WireGuard saja.

### Login hotspot

`http-pap` mengirim password teks biasa lewat HTTP. Pilih:

    /ip hotspot profile set [find] login-by=http-chap

atau HTTPS (perlu sertifikat yang dipercaya perangkat, kalau tidak captive portal menampilkan peringatan):

    /certificate import file-name=hotspot.pem passphrase=""
    /ip hotspot profile set [find] login-by=https,http-chap ssl-certificate=<nama-sertifikat>

### PPPoE (MS-CHAPv2)

MS-CHAPv2 bisa dibongkar offline (DES lemah). Di link tidak tepercaya (kabel bersama, WiFi terbuka), jalankan PPPoE di dalam tunnel WireGuard. Di jaringan akses lokal yang terkontrol, risikonya bisa diterima.

### FreeRADIUS di jalur REST

Jaga tetap terbaru (CVE-2019-11234/11235 EAP-pwd, CVE-2022-41860/41861 EAP-SIM/AKA):

    sudo apt update && sudo apt -y upgrade freeradius
    freeradius -v

Jika EAP tidak dipakai, nonaktifkan:

    sudo rm /etc/freeradius/3.0/mods-enabled/eap
    sudo sed -i 's/^\(\s*\)eap$/\1#eap/' /etc/freeradius/3.0/sites-enabled/*
    sudo freeradius -CX | tail -n 2

(Jalankan dulu `grep -n "eap" /etc/freeradius/3.0/sites-enabled/*` untuk memeriksa baris yang akan dikomentari; `-CX` harus berakhir OK.) Jika `connect_uri` memakai `https`, pastikan `check_cert = yes` di blok `tls` pada `mods-enabled/rest`; untuk sertifikat self-signed tambahkan CA di `ca_file`.

## Keamanan aplikasi

- **Password:** admin dan pelanggan memakai bcrypt. Hash sha1 lama ditandai `legacy_sha1` dan diganti saat login pertama.
- **Secret terenkripsi:** secret router, pelanggan (PPPoE/hotspot), dan NAS memakai AES-GCM. Kunci dari `NUXBILL_SECRET_KEY` atau file `.key`; backup kunci bersama database.
- **Catatan:** secret integrasi (SMTP, Telegram, Tripay) tersimpan plaintext di tabel `settings`, sama seperti aplikasi lama. Lindungi file database dan backup-nya.
- **CSRF:** lewat `http.CrossOriginProtection` stdlib. Pengecualian hanya callback Tripay (diverifikasi signature) dan `/radius.php` (allow-list).
- **Rate limit:** pembatas brute-force, tersimpan di memori (reset saat restart):
  - Login admin dan portal: 10 kegagalan dalam 15 menit, dihitung per IP dan per username. Login 2FA memakai pembatas yang sama.
  - Voucher RADIUS: 10 kegagalan dalam 15 menit per NAS dan MAC, dan 100 per NAS.
  - Auth RADIUS per username: pembatas terpisah untuk password yang salah.
  - Kirim OTP: per IP 5 kali dalam 15 menit; per nomor jeda 60 detik dan maksimal 5 kali per jam. Di `notify_otp` = `no`, OTP tidak dikirim sama sekali.
  - Brute force terdeteksi juga memicu alert operator (lihat [monitoring.md](monitoring.md)).
- **Lupa kata sandi:** balasan sama untuk username yang ada maupun tidak, jadi tidak bisa dipakai untuk menebak akun.
- **Kata sandi pelanggan:** minimal 8 karakter, maksimal 35. Berlaku untuk registrasi, ganti, reset, dan edit oleh admin.
- **Kata sandi admin pertama:** dibuat acak (16 karakter) dan ditulis ke `initial-admin-password.txt` di folder database (mode 0600), tidak ke log. Hapus file itu setelah login.
- **Proxy:** `X-Forwarded-For` dibaca hanya jika koneksi langsung dari loopback atau dari `trusted_proxies` dan `trust_proxy=yes`. Pembatas login memakai alamat klien itu. Allow-list `/radius.php` selalu memakai alamat koneksi asli, jadi header dari klien luar tidak bisa memalsukan IP.
- **Cetak voucher:** hanya peran staf (SuperAdmin, Admin, Agent, Sales).
- **Endpoint publik:** `/health` hanya memuat status database dan disk. `/metrics` butuh bearer token dan 404 jika token dimatikan.
- **Sesi:** cookie `HttpOnly`, `SameSite=Lax`, `Secure` secara default (nonaktif hanya dengan `NUXBILL_HTTPS=0`). Sesi dicabut saat password, role, atau status berubah.
- **Batas diam sesi:** `session_timeout_duration` (menit, bawaan 120). `single_session=yes` membatasi satu sesi admin aktif.
- **Role:** dicek di middleware per route. Admin tidak bisa mengangkat SuperAdmin; SuperAdmin terakhir dilindungi.
- **SQL:** semua query lewat `sqlc` dengan parameter terikat.
- **Log:** error notifikasi disaring agar token dan API key tidak bocor.
- **Dump database:** file `docs/*.sql` dan `*.sql.gz` ada di `.gitignore` dan tidak boleh di-commit. Jangan menaruh kredensial nyata di dokumen atau test; pakai placeholder seperti `<SECRET>`.

## Verifikasi dua langkah (2FA) admin

Opsional untuk setiap akun admin (bukan pelanggan). Setelah kata sandi benar, login meminta kode 6 digit dari aplikasi authenticator (Google Authenticator, Authy, dll.). Kode berlaku 30 detik dan diterima dengan toleransi satu langkah (±30 detik).

**Disarankan untuk setiap SuperAdmin.** Akun SuperAdmin bisa mengubah semua pengaturan dan pengguna.

### Mengaktifkan

1. Masuk, buka **Ganti Kata Sandi** lalu **Kelola 2FA** (atau langsung `/admin/2fa`).
2. Klik **Aktifkan 2FA**. Pindai kode QR dengan aplikasi authenticator, atau ketik kunci yang tertulis di bawahnya.
3. Masukkan kode 6 digit dari aplikasi. 2FA baru aktif setelah kode ini benar.
4. Simpan 8 **kode pemulihan** yang muncul. Kode hanya ditampilkan sekali.

### Kode pemulihan

- Berbentuk `XXXX-XXXX`, masing-masing hanya bisa dipakai sekali, dan disimpan sebagai hash bcrypt.
- Dipakai di layar kedua login jika aplikasi authenticator tidak bisa diakses. Ketik dengan atau tanpa tanda strip.
- Setelah dipakai, kode itu mati. Jika habis, nonaktifkan lalu aktifkan lagi 2FA untuk mendapat 8 kode baru.

### Menonaktifkan

Di `/admin/2fa` isi kata sandi saat ini dan satu kode 6 digit dari aplikasi. Kode pemulihan yang tersisa ikut dihapus.

### Reset oleh SuperAdmin

Jika admin kehilangan aplikasi dan kode pemulihan, SuperAdmin membuka **Pengguna Admin**, memilih akun tersebut, lalu klik **Reset 2FA**. Admin itu bisa masuk dengan kata sandi saja, dan 2FA harus diaktifkan lagi. Tindakan ini tercatat di log aktivitas (`users.2fa.reset`). Reset tidak meminta kata sandi SuperAdmin, jadi jaga akun SuperAdmin sendiri dengan 2FA.

### Batasan

- Percobaan kode salah dihitung bersama pembatas login (10 kali gagal dalam 15 menit per IP dan per nama pengguna). Kode yang sudah dipakai tidak bisa dipakai ulang.
- Riwayat kode yang sudah dipakai disimpan di memori. Setelah aplikasi di-restart, kode yang sama bisa diterima lagi selama sisa langkah waktu (maksimal sekitar 90 detik).
- Login sebagai pelanggan dan API tidak memakai 2FA admin. Sesi admin hanya dibuka lewat halaman login.
