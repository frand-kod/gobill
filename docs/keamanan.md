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

**Batasi `/radius.php`:** kosong = hanya loopback; isi `radius_rest_allow` dengan IP FreeRADIUS jika di host lain, dan biarkan `trust_proxy=no` tanpa reverse proxy ([freeradius-rest.md](freeradius-rest.md)).

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
- **Rate limit:** pembatas brute-force untuk login, voucher, OTP, dan auth RADIUS per user. Tersimpan di memori, jadi reset saat restart.
- **Sesi:** cookie `HttpOnly`, `SameSite=Lax`, `Secure` secara default (nonaktif hanya dengan `NUXBILL_HTTPS=0`). Sesi dicabut saat password, role, atau status berubah.
- **Role:** dicek di middleware per route. Admin tidak bisa mengangkat SuperAdmin; SuperAdmin terakhir dilindungi.
- **SQL:** semua query lewat `sqlc` dengan parameter terikat.
- **Log:** error notifikasi disaring agar token dan API key tidak bocor.
- **Dump database:** file `docs/*.sql` dan `*.sql.gz` ada di `.gitignore` dan tidak boleh di-commit. Jangan menaruh kredensial nyata di dokumen atau test; pakai placeholder seperti `<SECRET>`.
