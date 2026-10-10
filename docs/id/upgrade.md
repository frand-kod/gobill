# Upgrade

Dokumen ini menjelaskan cara mengganti binary gobill ke versi baru tanpa mengubah data dan konfigurasi. Bagian akhir berisi catatan untuk setiap versi yang butuh tindakan khusus, dan cara rollback.

**Untuk:** operator

**Prasyarat:** backup `gobill.db` dan `gobill.db.key` sudah dibuat. Lihat [backup dan restore](backup-restore.md).

## Langkah upgrade

1. Buat backup database dan file kunci. Simpan di luar perangkat.
2. Pasang binary baru. Dengan `install.sh`:

        sudo sh install.sh ./gobill-linux-arm64

   Atau secara manual:

        sudo systemctl stop gobill
        sudo install -m 0755 ./gobill-linux-arm64 /usr/local/bin/gobill
        sudo systemctl start gobill

3. Cek versi:

        /usr/local/bin/gobill --version

4. Buka halaman **Status Sistem** dan pastikan aplikasi berjalan normal. Lihat [monitoring](monitoring.md).

Data dan konfigurasi tidak berubah. Migrasi database berjalan otomatis saat start.

## Upgrade dari v0.1.3 ke v0.1.4

Migrasi 0011 sampai 0014 berjalan otomatis saat start. Migrasi ini menambah kolom `start_on_first_login`, indeks, snapshot pelanggan pada pembayaran, dan 2FA admin.

Sebelum upgrade, periksa hal berikut.

- Jika FreeRADIUS berjalan di host lain, isi `radius_rest_allow` dengan IP-nya. Nilai kosong sekarang berarti hanya loopback.
- Jika UI diakses lewat HTTP biasa ke IP LAN, set `GOBILL_HTTPS=0` di `config.env`. Bawaannya cookie memakai flag `Secure`, sehingga login gagal tanpa pengaturan ini.
- Di belakang reverse proxy, set `trust_proxy` dan `trusted_proxies`. Lihat [konfigurasi](configuration.md#jaringan-dan-proxy).
- Pemantau yang membaca versi atau sisa disk dari `/health` harus pindah ke `/metrics` atau `/admin/status.json`. Lihat [monitoring](monitoring.md).

Daftar lengkap perubahan ada di [CHANGELOG](../../CHANGELOG.md).

## Rollback

Karena migrasi berjalan otomatis saat start, mengganti binary saja tidak cukup. Gunakan langkah ini:

1. Hentikan service:

        sudo systemctl stop gobill

2. Pasang binary lama.
3. Pulihkan database dari backup yang dibuat sebelum upgrade. Ikuti bagian "Restore manual" di [backup dan restore](backup-restore.md#restore-manual).
4. Jalankan service:

        sudo systemctl start gobill

## Lihat juga

- [Instalasi](installation.md): pemasangan awal.
- [Backup dan restore](backup-restore.md): membuat dan memulihkan backup.
- [Monitoring](monitoring.md): memantau aplikasi setelah upgrade.
- [Migrasi dari PHPNuxBill](migration-phpnuxbill.md): pindah dari sistem lama.
