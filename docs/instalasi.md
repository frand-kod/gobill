# Instalasi

Untuk operator yang memasang NuxBill di STB Armbian (arm64/armv7), VPS Linux, atau Docker. Binary tunggal, tanpa PHP dan tanpa web server. Setelah terpasang, lanjut ke [mikrotik.md](mikrotik.md) dan [keamanan.md](keamanan.md). Semua variabel konfigurasi ada di [konfigurasi.md](konfigurasi.md).

## STB Armbian

### 1. Pasang Armbian

Tulis image Armbian resmi untuk board Anda ke kartu SD atau eMMC (Balena Etcher atau `dd`), boot dengan jaringan tersambung, login sebagai `root` atau user `sudo`, lalu:

    apt update && apt -y upgrade

### 2. Pasang NuxBill

Ambil `install.sh` dari rilis (atau `deploy/install.sh` di repo), lalu jalankan:

    # dari binary yang sudah diunduh
    sudo sh install.sh ./nuxbill-linux-arm64

    # langsung dari URL rilis ({arch} diganti otomatis: arm64, armv7, amd64)
    sudo sh install.sh https://github.com/frand-kod/gobill/releases/latest/download/nuxbill-linux-{arch}

Script ini:

- mendeteksi arsitektur dan memasang binary ke `/usr/local/bin/nuxbill`;
- membuat user sistem `nuxbill` dan folder `/var/lib/nuxbill`;
- membuat `/etc/nuxbill/config.env` dengan `NUXBILL_SECRET_KEY` acak (hanya jika file belum ada, mode 600);
- mengaktifkan chrony atau systemd-timesyncd jika ada;
- mengaktifkan dan menjalankan service `nuxbill`.

Script aman dijalankan ulang. Hapus service dan binary tanpa menghapus data:

    sudo sh install.sh --uninstall

Ambil password admin pertama (hanya dicetak sekali):

    journalctl -u nuxbill | grep "first admin"

Buka `http://IP-STB:8080`, login sebagai `admin`, lalu ganti password.

### 3. Jam dan NTP

STB umumnya tidak punya RTC. Setelah boot, jam bisa kembali ke 1970 atau ke waktu build image sampai NTP sinkron. Karena itu:

- Service menunggu `time-sync.target` dan `network-online.target` sebelum start.
- Job expiry **menolak berjalan** jika jam lebih awal dari waktu terakhir yang tercatat di DB, atau NTP belum sinkron. Penolakan dicatat di log dan jadi banner di dashboard.
- Tanpa guard ini, jam yang melompat maju bisa meng-expire semua pelanggan sekaligus.
- Jika perangkat punya RTC akurat, matikan guard dengan setting `clock_guard=off`.

Cek sinkronisasi:

    timedatectl status        # "System clock synchronized: yes"
    chronyc tracking          # jika memakai chrony

### 4. Backup ke USB atau NAS

Data ada di `/var/lib/nuxbill`: `nuxbill.db` (SQLite) dan `nuxbill.db.key` (kunci enkripsi). Simpan backup di luar eMMC/SD karena cepat aus dan rentan saat listrik padam.

1. Mount penyimpanan, misalnya `/mnt/usb`, dengan opsi fstab `nofail` agar boot tidak macet bila USB tidak terpasang.
2. Buat drop-in service dengan `sudo systemctl edit nuxbill`, isi:

        [Service]
        ReadWritePaths=/mnt/usb/nuxbill-backup

3. Tambahkan di `/etc/nuxbill/config.env`:

        NUXBILL_BACKUP_DIR=/mnt/usb/nuxbill-backup

4. `sudo systemctl restart nuxbill`

Backup harian dibuat otomatis (`VACUUM INTO`); jumlah file yang disimpan diatur setting `backup_keep` (bawaan 7).

**Backup `nuxbill.db.key` bersama database.** Tanpa file ini, data terenkripsi (password perangkat, gateway) tidak terbaca. Jika memakai `NUXBILL_SECRET_KEY`, simpan nilainya di tempat aman karena menggantikan file `.key`. Salinan manual:

    sudo cp /var/lib/nuxbill/nuxbill.db /var/lib/nuxbill/nuxbill.db.key /mnt/usb/manual-backup/

### 5. Port firewall

| Arah | Port | Protokol | Keterangan |
|---|---|---|---|
| MikroTik ke STB | 1812 | UDP | Autentikasi RADIUS |
| MikroTik ke STB | 1813 | UDP | Accounting RADIUS |
| STB ke MikroTik | 3799 | UDP | CoA (putus sesi, ubah paket) |
| STB ke MikroTik | 8728 | TCP | API RouterOS (8729 jika TLS) |
| LAN admin ke STB | 8080 | TCP | UI admin. Jangan buka ke internet |

Contoh aturan firewall lengkap ada di [keamanan.md](keamanan.md#firewall).

### 6. Upgrade

Data dan config tidak berubah:

    sudo sh install.sh ./nuxbill-linux-arm64

Atau manual:

    sudo systemctl stop nuxbill
    sudo install -m 0755 ./nuxbill-linux-arm64 /usr/local/bin/nuxbill
    sudo systemctl start nuxbill

Cek versi: `/usr/local/bin/nuxbill --version`. Backup database dan `.key` dulu.

### 7. Pemecahan masalah

    journalctl -u nuxbill -n 100 --no-pager
    journalctl -u nuxbill -f
    systemctl status nuxbill

- **Log berisi `clock`, expiry ditolak**: jam belum sinkron. Cek `timedatectl status` dan pastikan internet bisa diakses.
- **`first admin created`**: password admin pertama, hanya muncul saat DB baru dibuat.
- **`NUXBILL_RADIUS ... want host:port`**: format salah. Pakai `:1812`, atau `off` untuk mematikan.
- **`permission denied` saat tulis DB**: `sudo chown -R nuxbill:nuxbill /var/lib/nuxbill`.
- **Service terus restart**: lihat log; pastikan `/etc/nuxbill/config.env` ada dan berisi `NUXBILL_SECRET_KEY`.
- **Pelanggan tidak bisa login hotspot**: `journalctl -u nuxbill | grep -i radius`. Cek secret cocok dan port 1812 tidak diblokir.
- **Port 8080/1812 sudah dipakai**: `ss -ulnp | grep 1812` atau `ss -tlnp | grep 8080`. Ubah `NUXBILL_HTTP`/`NUXBILL_RADIUS`. Jika FreeRADIUS memakai 1812 di host yang sama, isi `NUXBILL_RADIUS=off`; `/radius.php` tetap jalan.

## VPS

Langkahnya sama dengan STB (`install.sh`, binary `amd64`). Perbedaan penting:

- Jam VPS sudah sinkron, jadi clock guard jarang berpengaruh.
- Jangan kirim UDP RADIUS/CoA lewat internet terbuka. Pakai WireGuard atau RadSec, lihat [keamanan.md](keamanan.md#link-jarak-jauh-vps).
- Pasang reverse proxy (HTTPS) di depan `:8080`, lalu pastikan `NUXBILL_HTTPS` tidak diset `0` (default aktif) dan set setting `trust_proxy=yes`.

## Docker

    docker build --build-arg VERSION=dev -t nuxbill .
    docker run -d --name nuxbill -p 8080:8080 -p 1812:1812/udp -p 1813:1813/udp \
      -v nuxbill-data:/data nuxbill

Data SQLite dan `nuxbill.db.key` ada di volume `/data`. Tambahkan `-e NUXBILL_SECRET_KEY=<SECRET>` untuk mengelola kunci sendiri. Password admin pertama:

    docker logs nuxbill | grep "first admin"

## Tanpa installer

    NUXBILL_DB=./nuxbill.db ./nuxbill     # atau: go run ./cmd/nuxbill

Build manual untuk STB (lihat juga [pengembangan.md](pengembangan.md)):

    CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o nuxbill ./cmd/nuxbill
    CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -trimpath -ldflags="-s -w" -o nuxbill ./cmd/nuxbill
