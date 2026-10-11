# Upgrade

Dokumen ini menjelaskan cara mengganti binary gobill ke versi baru tanpa mengubah data dan konfigurasi. Cara yang disarankan adalah `sudo gobill update`. Cara manual tetap ada sebagai cadangan. Bagian akhir berisi catatan untuk setiap versi yang butuh tindakan khusus.

**Untuk:** operator

**Prasyarat:** akses root (`sudo`) ke perangkat. Untuk `gobill update`, perangkat perlu akses internet ke GitHub. Simpan juga salinan `gobill.db.key` di luar perangkat. Lihat [backup dan restore](backup-restore.md).

## Upgrade dengan `gobill update`

Jalankan sebagai root:

        sudo gobill update

Perintah ini melakukan langkah berikut.

1. Mengambil info rilis terbaru dari GitHub. Jika versinya sama dengan yang berjalan, perintah berhenti dengan pesan `already up to date`.
2. Menampilkan versi lama dan baru, serta beberapa baris pertama catatan rilis. Perintah meminta konfirmasi, kecuali dengan `--yes`.
3. Mengunduh binary untuk arsitektur perangkat dan file `SHA256SUMS`. Checksum harus cocok. Jika tidak cocok, atau file checksum tidak ada, instalasi dibatalkan dan tidak ada yang berubah.
4. Membuat backup database dengan `VACUUM INTO` di folder backup, dengan nama `gobill-YYYYMMDD-HHMMSS-pre-update.db`. Path-nya ditampilkan. Rotasi backup harian tidak menghapus file ini.
5. Menyimpan binary lama sebagai `/usr/local/bin/gobill.prev`, lalu mengganti binary dengan versi baru.
6. Me-restart service, lalu memeriksa `/health` selama sampai 60 detik.
7. Jika pemeriksaan gagal, perintah mengembalikan binary lama dan database dari backup, menjalankan lagi service, lalu keluar dengan kode error.

Opsi:

| Opsi | Fungsi |
|---|---|
| `--version vX.Y.Z` | Pasang rilis tertentu. Dipakai juga untuk kembali ke versi lama. |
| `--yes` | Lewati konfirmasi. |

Build tanpa tag rilis, misalnya versi `dev`, tidak diperbarui otomatis. Pakai `--version` untuk memasang rilis.

Setelah update, cek versi di **Status Sistem** dan pastikan aplikasi berjalan normal. Lihat [monitoring](monitoring.md).

## Rollback dengan `gobill rollback`

        sudo gobill rollback

Perintah ini menukar binary dengan `/usr/local/bin/gobill.prev`, lalu memulai service. Jalankan lagi perintah yang sama untuk maju kembali ke versi baru.

Jika update terakhir sempat menjalankan migrasi, database baru tidak cocok dengan binary lama. Perintah lalu menampilkan backup pra-update yang tercatat saat update dan bertanya sebelum memulihkannya. Jawab `y` hanya jika Anda yakin. Data yang dibuat setelah backup itu ikut hilang. Jika Anda jawab `n`, perintah menampilkan langkah untuk memulihkan nanti.

## Upgrade manual (cadangan)

Pakai cara ini jika `gobill update` tidak bisa dipakai, misalnya perangkat tidak punya akses ke GitHub.

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

## Rollback manual (cadangan)

Karena migrasi berjalan otomatis saat start, mengganti binary saja tidak cukup. Gunakan langkah ini:

1. Hentikan service:

        sudo systemctl stop gobill

2. Pasang binary lama.
3. Pulihkan database dari backup yang dibuat sebelum upgrade. Ikuti bagian "Restore manual" di [backup dan restore](backup-restore.md#restore-manual).
4. Jalankan service:

        sudo systemctl start gobill

## Pemeriksaan versi baru

Setting `update_check` ada di **Pengaturan > Sistem & keamanan**, bagian **System**. Bawaannya **Ya**.

Jika aktif, gobill memeriksa rilis terbaru di GitHub beberapa menit setelah start, lalu sekali sehari. Jika ada rilis yang lebih baru, SuperAdmin melihat lencana `vX.Y.Z tersedia` di header. Lencana itu menuju ke halaman rilis dan ke panduan upgrade ini. Pilih **Tidak** untuk mematikan pemeriksaan. Jika pemeriksaan gagal, gobill hanya mencatatnya di log.

## Upgrade dari v0.1.3 ke v0.1.4

Migrasi 0011 sampai 0014 berjalan otomatis saat start. Migrasi ini menambah kolom `start_on_first_login`, indeks, snapshot pelanggan pada pembayaran, dan 2FA admin.

Sebelum upgrade, periksa hal berikut.

- Jika FreeRADIUS berjalan di host lain, isi `radius_rest_allow` dengan IP-nya. Nilai kosong sekarang berarti hanya loopback.
- Jika UI diakses lewat HTTP biasa ke IP LAN, set `GOBILL_HTTPS=0` di `config.env`. Bawaannya cookie memakai flag `Secure`, sehingga login gagal tanpa pengaturan ini.
- Di belakang reverse proxy, set `trust_proxy` dan `trusted_proxies`. Lihat [konfigurasi](configuration.md#jaringan-dan-proxy).
- Pemantau yang membaca versi atau sisa disk dari `/health` harus pindah ke `/metrics` atau `/admin/status.json`. Lihat [monitoring](monitoring.md).

Daftar lengkap perubahan ada di [CHANGELOG](../../CHANGELOG.md).

## Lihat juga

- [Instalasi](installation.md): pemasangan awal.
- [Backup dan restore](backup-restore.md): membuat dan memulihkan backup.
- [Monitoring](monitoring.md): memantau aplikasi setelah upgrade.
- [Migrasi dari PHPNuxBill](migration-phpnuxbill.md): pindah dari sistem lama.
