# Instalasi

Dokumen ini menjelaskan cara memasang gobill di STB Armbian, VPS Linux, atau Docker. gobill berupa satu binary, tanpa PHP dan tanpa web server.

**Untuk:** operator

**Prasyarat:** akses `root` atau `sudo` ke perangkat, jaringan tersambung, dan untuk STB, image Armbian resmi untuk board Anda.

## STB Armbian

### 1. Pasang Armbian

1. Tulis image Armbian resmi ke kartu SD atau eMMC, misalnya dengan Balena Etcher atau `dd`.
2. Boot perangkat dengan jaringan tersambung.
3. Login sebagai `root` atau user `sudo`, lalu jalankan:

        apt update && apt -y upgrade

### 2. Pasang gobill

Ambil `install.sh` dari rilis, atau pakai `deploy/install.sh` dari repo. Lalu jalankan salah satu perintah berikut.

Dari binary yang sudah diunduh:

    sudo sh install.sh ./gobill-linux-arm64

Langsung dari URL rilis. `{arch}` diganti otomatis menjadi `arm64`, `armv7`, atau `amd64`:

    sudo sh install.sh https://github.com/frand-kod/gobill/releases/latest/download/gobill-linux-{arch}

Script ini:

- mendeteksi arsitektur dan memasang binary ke `/usr/local/bin/gobill`;
- membuat user sistem `gobill` dan folder `/var/lib/gobill`;
- membuat `/etc/gobill/config.env` dengan `GOBILL_SECRET_KEY` acak, hanya jika file belum ada, dengan mode 600;
- mengaktifkan chrony atau systemd-timesyncd jika ada;
- mengaktifkan dan menjalankan service `gobill`.

Script aman dijalankan ulang. Untuk menghapus service dan binary tanpa menghapus data:

    sudo sh install.sh --uninstall

Password admin pertama ada di file `initial-admin-password.txt` di folder database. File ini memakai mode 0600 dan password-nya tidak ditulis ke log. Untuk instalasi default:

    sudo cat /var/lib/gobill/initial-admin-password.txt

Lalu:

1. Buka `http://IP-STB:8080`.
2. Login sebagai `admin`.
3. Ganti password.
4. Hapus file password awal:

        sudo rm /var/lib/gobill/initial-admin-password.txt

### 3. Jam dan NTP

STB umumnya tidak punya RTC. Setelah boot, jam bisa kembali ke 1970 atau ke waktu build image, sampai NTP sinkron. Karena itu gobill memakai guard jam.

- Service menunggu `time-sync.target` dan `network-online.target` sebelum start.
- Job expiry menolak berjalan jika jam lebih awal dari waktu terakhir yang tercatat di database, atau jika NTP belum sinkron. Penolakan dicatat di log dan tampil sebagai banner di dashboard.
- Tanpa guard ini, jam yang melompat maju bisa membuat semua pelanggan kedaluwarsa sekaligus.
- Jika perangkat punya RTC yang akurat, matikan guard dengan pengaturan `clock_guard` = `off`. Lihat [konfigurasi](configuration.md).

Cek sinkronisasi:

    timedatectl status
    chronyc tracking

Baris pertama `timedatectl status` harus berisi `System clock synchronized: yes`. Perintah `chronyc tracking` hanya dipakai jika memakai chrony.

### 4. Backup ke USB atau NAS

Data ada di `/var/lib/gobill`: `gobill.db` dan `gobill.db.key`. Simpan backup di luar eMMC atau SD card, karena keduanya cepat aus dan rentan saat listrik padam. Langkah lengkapnya ada di [backup dan restore](backup-restore.md).

### 5. Port firewall

| Arah | Port | Protokol | Keterangan |
|---|---|---|---|
| MikroTik ke STB | 1812 | UDP | Autentikasi RADIUS |
| MikroTik ke STB | 1813 | UDP | Accounting RADIUS |
| STB ke MikroTik | 3799 | UDP | CoA: memutus sesi dan mengubah paket |
| STB ke MikroTik | 8728 | TCP | API RouterOS. Gunakan 8729 jika memakai TLS |
| LAN admin ke STB | 8080 | TCP | UI admin. Jangan buka ke internet |

Contoh aturan firewall lengkap ada di [keamanan](security.md#firewall).

## VPS

Langkahnya sama seperti di STB, dengan binary `amd64`. Ada tiga perbedaan penting.

- Jam VPS sudah sinkron, jadi guard jam jarang berpengaruh.
- Jangan kirim UDP RADIUS atau CoA lewat internet terbuka. Gunakan WireGuard atau RadSec. Lihat [keamanan](security.md#link-jarak-jauh-vps).
- Pasang reverse proxy dengan HTTPS di depan `:8080`. Biarkan `GOBILL_HTTPS` tidak diset agar cookie tetap `Secure`. Set pengaturan `trust_proxy` = `yes`. Jika proxy tidak berjalan di host yang sama, isi `trusted_proxies` dengan IP proxy. Lihat [konfigurasi](configuration.md#jaringan-dan-proxy).

## Docker

    docker build --build-arg VERSION=dev -t gobill .
    docker run -d --name gobill -p 8080:8080 -p 1812:1812/udp -p 1813:1813/udp \
      -v gobill-data:/data gobill

Database SQLite dan `gobill.db.key` ada di volume `/data`. Untuk mengelola kunci sendiri, tambahkan `-e GOBILL_SECRET_KEY=<SECRET>`.

Password admin pertama ada di `/data/initial-admin-password.txt`:

    docker exec gobill cat /data/initial-admin-password.txt

Setelah login dan mengganti password, hapus file itu:

    docker exec gobill rm /data/initial-admin-password.txt

## Tanpa installer

Jalankan binary langsung:

    GOBILL_DB=./gobill.db ./gobill

Atau dengan `go run ./cmd/gobill` dari root repo.

Build manual untuk STB:

    CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o gobill ./cmd/gobill
    CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -trimpath -ldflags="-s -w" -o gobill ./cmd/gobill

Untuk pengembangan, lihat [panduan pengembangan](../internal/pengembangan.md).

## Pemecahan masalah

Lihat log service:

    journalctl -u gobill -n 100 --no-pager
    journalctl -u gobill -f
    systemctl status gobill

| Gejala | Penyebab dan solusi |
|---|---|
| Log berisi `clock`, dan expiry ditolak | Jam belum sinkron. Cek `timedatectl status` dan pastikan internet bisa diakses. |
| Log berisi `first admin created` | Admin pertama dibuat karena database baru. Password ada di `initial-admin-password.txt` di folder database. |
| Log berisi `GOBILL_RADIUS ... want host:port` | Format salah. Pakai `:1812`, atau `off` untuk mematikan listener. |
| `permission denied` saat menulis database | Jalankan `sudo chown -R gobill:gobill /var/lib/gobill`. |
| Service terus restart | Lihat log. Pastikan `/etc/gobill/config.env` ada dan berisi `GOBILL_SECRET_KEY`. |
| Pelanggan tidak bisa login hotspot | Jalankan `journalctl -u gobill \| grep -i radius`. Pastikan secret cocok dan port 1812 tidak diblokir. |
| Port 8080 atau 1812 sudah dipakai | Cek dengan `ss -ulnp \| grep 1812` atau `ss -tlnp \| grep 8080`. Ubah `GOBILL_HTTP` atau `GOBILL_RADIUS`. Jika FreeRADIUS memakai 1812 di host yang sama, set `GOBILL_RADIUS=off`. `/radius.php` tetap berjalan. |

## Lihat juga

- [Upgrade](upgrade.md): mengganti binary ke versi baru.
- [Konfigurasi](configuration.md): semua variabel `GOBILL_*` dan pengaturan.
- [Setup MikroTik](mikrotik.md): menghubungkan router.
- [Keamanan](security.md): pengerasan RADIUS, firewall, dan 2FA admin.
- [Monitoring](monitoring.md): memantau dari luar dan alert operator.
- [Backup dan restore](backup-restore.md): backup, mirror, dan pemulihan.
- [Migrasi dari PHPNuxBill](migration-phpnuxbill.md): memindahkan data dari sistem lama.
