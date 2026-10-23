# Upgrade

Dokumen ini menjelaskan cara mengganti binary gobill ke versi baru tanpa mengubah data dan konfigurasi. Cara yang disarankan adalah `sudo gobill update`. Cara manual tetap ada sebagai cadangan. Bagian akhir berisi catatan versi yang butuh tindakan khusus.

**Untuk:** operator

**Prasyarat:** akses root (`sudo`) ke perangkat. Untuk `gobill update`, perangkat perlu akses internet ke GitHub. Simpan juga salinan `gobill.db.key` di luar perangkat. Lihat [backup dan restore](backup-restore.md).

## Upgrade dengan `gobill update`

Jalankan sebagai root:

        sudo gobill update

Perintah ini melakukan langkah berikut.

1. Mengambil info rilis terbaru dari GitHub. Jika versinya sama dengan yang berjalan, perintah berhenti dengan pesan `already up to date`.
2. Menampilkan versi lama dan baru, serta beberapa baris pertama catatan rilis. Perintah meminta konfirmasi, kecuali dengan `--yes`.
3. Mengunduh binary untuk arsitektur perangkat, file `SHA256SUMS`, dan tanda tangannya `SHA256SUMS.sig`. Tanda tangan harus cocok dengan kunci rilis yang tertanam di gobill, lalu checksum harus cocok. Jika salah satu tidak cocok, atau ada file yang tidak ada, instalasi dibatalkan dan tidak ada yang berubah. Tanda tangan membuktikan rilis dibuat oleh pengelola gobill. Checksum saja hanya membuktikan unduhan utuh.
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

Setting `update_check` ada di **Pengaturan > Sistem & keamanan**, bagian **Sistem**. Bawaannya **Ya**.

Jika aktif, gobill memeriksa rilis terbaru di GitHub beberapa menit setelah start, lalu sekali sehari. Jika ada rilis yang lebih baru, SuperAdmin melihat lencana `vX.Y.Z tersedia` di header. Lencana itu menuju ke halaman rilis dan ke panduan upgrade ini. Pilih **Tidak** untuk mematikan pemeriksaan. Jika pemeriksaan gagal, gobill hanya mencatatnya di log.

## Catatan versi

Sebelum upgrade, baca bagian "Perubahan perilaku" di [CHANGELOG](../../CHANGELOG.md) untuk versi tujuan. Bagian itu mencatat perubahan yang butuh tindakan operator.

**Upgrade ke v0.1.19:**

1. Secret di pengaturan (API key Tripay dan Duitku, password SMTP, token bot Telegram, secret webhook, token metrics) dienkripsi saat start pertama, dengan kunci yang sama dengan secret NAS.
2. Migrasi 0020 menambah pin sertifikat router. Koneksi API-SSL pertama ke tiap router mencatat sidik jari sertifikatnya.
3. Untuk kembali ke v0.1.18, pulihkan backup pra-update yang ditawarkan `gobill rollback`. Tanpa backup itu, v0.1.18 membaca secret yang sudah terenkripsi apa adanya, sehingga pembayaran, email, dan Telegram berhenti bekerja sampai secret diisi ulang.

**Upgrade ke v0.1.18:**

1. v0.1.18 adalah rilis pertama yang memeriksa tanda tangan rilis. Update dari v0.1.17 berjalan seperti biasa. Setelah itu, `gobill update` menolak rilis tanpa `SHA256SUMS.sig` yang valid.
2. Tidak ada konfigurasi yang wajib diubah. Variabel opsional baru dijelaskan di `gobill config-example`. `/etc/gobill/config.env` yang sudah ada tidak diubah.

**Upgrade dari v0.1.6 atau lebih lama ke v0.1.7:**

1. `gobill update` hanya bisa dipakai jika rilis menyertakan file `SHA256SUMS`. Rilis lama belum punya file itu, jadi upgrade pertama ke v0.1.7 memakai cara manual. Setelah itu, `sudo gobill update` bisa dipakai.
2. Login pelanggan pindah ke `/login`, dan login admin ke `/admin/login`. Perbarui bookmark.
3. Isi `GOBILL_HTTP` dengan IP LAN dan port, misalnya `192.168.99.2:8090`, di `/etc/gobill/config.env`. Perintah isolir di **Pengaturan > Isolir** memakai alamat ini.
4. Backup, impor, dan restore ada di **Pengaturan > Sistem & keamanan**.
5. Migrasi 0018 berjalan otomatis saat start. Untuk kembali ke versi lama, pulihkan backup pra-update yang dibuat saat upgrade. Lihat [rollback](#rollback-dengan-gobill-rollback).
6. Nomor telepon pelanggan dinormalisasi ke format `62…` saat start.

Daftar lengkap perubahan ada di [CHANGELOG](../../CHANGELOG.md).

## Lihat juga

- [Instalasi](installation.md): pemasangan awal.
- [Backup dan restore](backup-restore.md): membuat dan memulihkan backup.
- [Monitoring](monitoring.md): memantau aplikasi setelah upgrade.
- [Migrasi dari PHPNuxBill](migration-phpnuxbill.md): pindah dari sistem lama.
