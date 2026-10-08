# Instalasi NuxBill di STB (Armbian)

Panduan ini untuk STB ARM (arm64 atau armv7) dengan Armbian. Binary NuxBill tunggal, tanpa PHP dan tanpa web server.

## 1. Pasang Armbian

Tulis image Armbian ke kartu SD atau eMMC memakai Balena Etcher atau `dd`, lalu boot. Login sebagai `root` atau user dengan `sudo`, lalu perbarui sistem:

    apt update && apt -y upgrade

Gunakan image Armbian resmi untuk board Anda. Pastikan STB tersambung ke jaringan saat pertama kali boot.

## 2. Pasang NuxBill

Ambil `install.sh` dari rilis (atau dari repo `deploy/install.sh`), lalu jalankan dengan argumen binary:

    # dari binary yang sudah diunduh
    sudo sh install.sh ./nuxbill-linux-arm64

    # langsung dari URL rilis ({arch} diganti otomatis: arm64, armv7, amd64)
    sudo sh install.sh https://github.com/frand-kod/nuxbill-go/releases/latest/download/nuxbill-linux-{arch}

Script ini:

- mendeteksi arsitektur dan memasang binary ke `/usr/local/bin/nuxbill`;
- membuat user sistem `nuxbill` dan folder `/var/lib/nuxbill`;
- membuat `/etc/nuxbill/config.env` dengan `NUXBILL_SECRET_KEY` acak (hanya jika file belum ada, mode 600);
- mengaktifkan chrony atau systemd-timesyncd jika ada;
- mengaktifkan dan menjalankan service `nuxbill`.

Script bisa dijalankan ulang tanpa merusak data. Untuk menghapus service dan binary tanpa menghapus data:

    sudo sh install.sh --uninstall

Setelah instalasi baru, ambil password admin pertama (hanya dicetak sekali saat start pertama):

    journalctl -u nuxbill | grep "first admin"

Buka `http://IP-STB:8080`, login sebagai `admin`, lalu ganti password.

## 3. Jam dan NTP

STB umumnya tidak punya RTC. Saat boot, jam bisa kembali ke 1970 atau ke waktu build image sampai NTP tersinkron. Karena itu:

- Service `nuxbill` menunggu `time-sync.target` dan `network-online.target` sebelum start.
- Job expiry **menolak berjalan** jika jam sekarang lebih awal dari waktu terakhir yang tercatat di DB, atau jika NTP belum sinkron. Penolakan ini dicatat di log dan muncul sebagai banner di dashboard admin.
- Tanpa guard ini, jam yang melompat maju bisa membuat semua pelanggan expired sekaligus.
- Jika perangkat Anda punya RTC yang akurat, matikan guard lewat setting `clock_guard=off`.

Cek sinkronisasi:

    timedatectl status        # "System clock synchronized: yes"
    chronyc tracking          # jika memakai chrony

## 4. Database dan backup di USB atau NAS

Data ada di `/var/lib/nuxbill`, yaitu `nuxbill.db` (SQLite) dan `nuxbill.db.key` (kunci enkripsi). Backup sebaiknya disimpan di luar eMMC atau SD, karena keduanya cepat aus dan rentan rusak saat listrik padam.

Untuk memindahkan folder backup ke USB atau NAS:

1. Mount penyimpanan, misalnya `/mnt/usb`. Gunakan fstab dengan opsi `nofail` agar boot tidak macet bila USB tidak terpasang.
2. Buat drop-in service:

        sudo systemctl edit nuxbill

   Isi:

        [Service]
        ReadWritePaths=/mnt/usb/nuxbill-backup

3. Tambahkan di `/etc/nuxbill/config.env`:

        NUXBILL_BACKUP_DIR=/mnt/usb/nuxbill-backup

4. Restart: `sudo systemctl restart nuxbill`

**Backup `nuxbill.db.key` bersama database.** Tanpa file ini, data terenkripsi (password perangkat, gateway) tidak bisa dibaca. Jika Anda memakai `NUXBILL_SECRET_KEY` di `config.env`, simpan juga isi variabel itu di tempat aman, karena nilai itu menggantikan file `.key`.

Salin file ke luar perangkat secara berkala, misalnya:

    sudo cp /var/lib/nuxbill/nuxbill.db /var/lib/nuxbill/nuxbill.db.key /mnt/usb/manual-backup/

## 5. Setup MikroTik

Jalankan perintah ini di terminal MikroTik. Ganti `192.168.88.10` dengan IP STB dan `RAHASIA` dengan secret yang sama.

**RADIUS client (hotspot dan PPPoE):**

    /radius add service=hotspot,ppp address=192.168.88.10 secret=RAHASIA authentication-port=1812 accounting-port=1813 timeout=2s
    /radius incoming set accept=yes port=3799

- `/radius incoming` membuka CoA di port 3799. NuxBill memakai ini untuk memutus sesi atau mengubah paket tanpa menunggu reconnect.
- Login voucher di halaman hotspot (kode sebagai username, password kosong atau sama dengan kode) langsung diaktifkan lewat RADIUS. Percobaan voucher gagal dibatasi 10 kali per 15 menit untuk tiap NAS dan MAC.

**Hotspot:**

    /ip hotspot profile set [find] use-radius=yes radius-accounting=yes interim-update=1m

**PPPoE:**

    /ppp aaa set use-radius=yes accounting=yes interim-update=1m

**User API untuk NuxBill** (NuxBill memanggil API RouterOS di port 8728 untuk membuat secret PPPoE dan memutus sesi):

    /user group add name=nuxbill policy=read,write,api,test
    /user add name=nuxbill group=nuxbill password=PASSWORD-KUAT

Daftarkan router di UI NuxBill dengan IP MikroTik, user `nuxbill`, dan secret RADIUS yang sama. Pastikan IP STB sama dengan yang didaftarkan sebagai RADIUS client.

## 6. Port firewall

| Arah | Port | Protokol | Keterangan |
|---|---|---|---|
| MikroTik ke STB | 1812 | UDP | Autentikasi RADIUS |
| MikroTik ke STB | 1813 | UDP | Accounting RADIUS |
| STB ke MikroTik | 3799 | UDP | CoA (putus sesi, ubah paket) |
| STB ke MikroTik | 8728 | TCP | API RouterOS (8729 jika TLS) |
| LAN admin ke STB | 8080 | TCP | UI admin. Jangan buka ke internet |

Di STB, jika memakai `ufw` atau `nftables`, izinkan 8080/tcp dari LAN, serta 1812 dan 1813/udp dari IP MikroTik saja.

## 7. Upgrade

Ganti binary lalu restart. Data dan config tidak berubah:

    sudo sh install.sh ./nuxbill-linux-arm64

Atau manual:

    sudo systemctl stop nuxbill
    sudo install -m 0755 ./nuxbill-linux-arm64 /usr/local/bin/nuxbill
    sudo systemctl start nuxbill

Cek versi: `/usr/local/bin/nuxbill --version`. Sebelum upgrade, backup database dan `.key` seperti di bagian 4.

## 8. Pemecahan masalah

Lihat log service:

    journalctl -u nuxbill -n 100 --no-pager
    journalctl -u nuxbill -f          # ikuti log langsung

Pesan yang sering muncul:

- **Log berisi `clock`, expiry ditolak**: jam belum sinkron. Cek `timedatectl status`, lalu pastikan chrony atau timesyncd aktif dan internet bisa diakses.
- **`first admin created`**: password admin pertama. Hanya muncul saat DB baru dibuat.
- **`NUXBILL_RADIUS ... want host:port`**: format `NUXBILL_RADIUS` salah. Gunakan `:1812`.
- **`permission denied` saat tulis DB**: folder data tidak bisa ditulis user `nuxbill`. Jalankan `sudo chown -R nuxbill:nuxbill /var/lib/nuxbill`.
- **Service terus restart**: `systemctl status nuxbill`, lalu lihat log. Pastikan `/etc/nuxbill/config.env` ada dan berisi `NUXBILL_SECRET_KEY`.
- **Pelanggan tidak bisa login di hotspot**: cek `journalctl -u nuxbill | grep -i radius`. Pastikan secret cocok dan port 1812 tidak diblokir.
- **Port 8080 atau 1812 sudah dipakai**: `ss -ulnp | grep 1812` atau `ss -tlnp | grep 8080`. Ubah `NUXBILL_HTTP` atau `NUXBILL_RADIUS` di config.env, lalu restart.

Cek apakah service berjalan:

    systemctl status nuxbill
    systemctl is-enabled nuxbill

## Migrasi dari PHPNuxBill + FreeRADIUS REST

Plan `RadiusRest` dari PHPNuxBill diimpor sebagai plan `Radius`. Ada dua cara melanjutkan.

**Opsi A: tetap pakai FreeRADIUS.** Di `mods-enabled/rest` ubah `connect_uri` menjadi `https://<nuxbill>/radius.php` (atau `/radius/rest`). Konfigurasi FreeRADIUS lain, MikroTik, dan section `authorize`/`authenticate`/`accounting`/`post-auth` tidak diubah. Format respons sama dengan `radius.php` lama (JSON `control:`/`reply:`, 204 untuk authenticate sukses, 401 untuk ditolak).

- Plus: tidak ada perubahan di MikroTik dan FreeRADIUS, mudah dikembalikan.
- Plus: FreeRADIUS tetap bisa dipakai untuk modul lain (misalnya EAP).
- Minus: satu komponen tambahan yang harus dirawat, dan setiap login menambah satu hop HTTP.

**Opsi B: tanpa FreeRADIUS.** Arahkan RADIUS MikroTik langsung ke NuxBill (UDP 1812/1813, server built-in; mendukung PAP, CHAP, dan MS-CHAPv2).

- Plus: satu proses, tanpa FreeRADIUS, tanpa PHP.
- Plus: MS-CHAPv2 dan pembatasan sesi bersama langsung dari satu sumber data.
- Minus: alamat dan secret RADIUS di MikroTik harus diubah, dan tidak ada modul FreeRADIUS lain.

Catatan penting:

- Amankan endpoint: isi pengaturan `radius_rest_allow` dengan IP FreeRADIUS (pisahkan dengan koma, boleh CIDR). Kosong berarti semua boleh dan NuxBill mencatat peringatan saat start. Header `X-Forwarded-For` hanya dipercaya jika `trust_proxy` = `yes`. `radius.php` lama tidak punya autentikasi sama sekali.
- Disconnect: plan `Radius` tidak memanggil API router. Putus paksa (saat plan habis atau admin menekan Disconnect) dikirim sebagai CoA/Disconnect-Request langsung ke MikroTik. PHPNuxBill lama tidak melakukannya (`disconnect_customer` kosong). Agar berfungsi, tambahkan MikroTik di menu NAS (IP plus secret) dan aktifkan RADIUS incoming (port 3799) di MikroTik. Tanpa baris NAS, disconnect hanya dicatat sebagai peringatan; Access-Request berikutnya tetap ditolak.
- Voucher yang login lewat username tanpa password (mode voucher RADIUS di `radius.php` lama) belum didukung di endpoint ini.
