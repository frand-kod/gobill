# Backup dan restore

Dokumen ini menjelaskan cara membuat backup NuxBill, menyimpan salinan di luar perangkat, dan memulihkan database. Backup harian dibuat otomatis. Salinan kedua, atau mirror, bisa disimpan di USB, NAS, atau Google Drive lewat rclone.

**Untuk:** operator

**Prasyarat:** NuxBill sudah terpasang. Untuk mirror, tujuan mirror sudah ter-mount dan bisa ditulis. Lihat [instalasi](installation.md).

## Backup harian

Data ada di `/var/lib/nuxbill`:

- `nuxbill.db`: database SQLite.
- `nuxbill.db.key`: kunci enkripsi.

Backup harian dibuat otomatis dengan `VACUUM INTO`. Folder tujuannya diatur oleh `NUXBILL_BACKUP_DIR`. Jumlah file yang disimpan diatur oleh pengaturan `backup_keep`, bawaannya 7.

Untuk memakai folder lain, misalnya USB:

1. Mount penyimpanan, misalnya di `/mnt/usb`. Gunakan opsi fstab `nofail`, agar boot tidak macet jika USB tidak terpasang.
2. Buat drop-in service:

        sudo systemctl edit nuxbill

   Isi dengan:

        [Service]
        ReadWritePaths=/mnt/usb/nuxbill-backup

3. Tambahkan di `/etc/nuxbill/config.env`:

        NUXBILL_BACKUP_DIR=/mnt/usb/nuxbill-backup

4. Restart service:

        sudo systemctl restart nuxbill

## Mirror

Simpan backup di luar SD card. Jika SD card atau STB rusak, backup di `NUXBILL_BACKUP_DIR` ikut hilang. Mirror adalah salinan kedua di folder lain.

Cara kerjanya:

- Isi `NUXBILL_BACKUP_MIRROR` dengan folder kedua.
- Setiap backup harian langsung disalin ke folder itu.
- Mirror dipangkas dengan `backup_keep` yang sama.
- Folder harus sudah ada. NuxBill tidak membuatnya otomatis, supaya disk yang belum ter-mount tidak menulis ke SD card.
- Jika salinan gagal, alert dikirim sekali saat mirror mulai gagal, dan sekali saat pulih. Alert memakai kanal di `alert_channel`. Backup lokal tetap dibuat.
- Mirror yang gagal dicoba ulang tiap jam.
- Status terakhir tampil di **Pengaturan > Aneka ragam**, di bawah "Download backup".

### Pilihan tujuan mirror

**USB stick.** Pasang di `/mnt/usb` dengan fstab `nofail`, seperti di atas. Set:

    NUXBILL_BACKUP_MIRROR=/mnt/usb/nuxbill-mirror

**NAS (NFS atau SMB).** Buat unit mount systemd, agar mount menunggu jaringan. Contoh NFS di `/etc/systemd/system/mnt-nas.mount`:

    [Unit]
    Description=NAS backup
    Wants=network-online.target
    After=network-online.target

    [Mount]
    What=192.168.1.10:/volume1/nuxbill
    Where=/mnt/nas
    Type=nfs
    Options=_netdev,soft,timeo=50

    [Install]
    WantedBy=multi-user.target

Lalu aktifkan unit:

    sudo systemctl enable --now mnt-nas.mount

Untuk SMB, ganti `Type=cifs` dan tambahkan `credentials=/etc/nuxbill/smb.cred` di `Options`. Nama file unit harus sama dengan path mount. Misalnya `/mnt/nas` menjadi `mnt-nas.mount`.

Lalu tambahkan drop-in service dengan `sudo systemctl edit nuxbill`:

    [Unit]
    RequiresMountsFor=/mnt/nas

    [Service]
    ReadWritePaths=/mnt/nas/nuxbill-backup

`RequiresMountsFor=` membuat NuxBill menunggu mount siap. Set `NUXBILL_BACKUP_MIRROR=/mnt/nas/nuxbill-backup`. Buat folder itu sekali secara manual di NAS.

**rclone ke Google Drive.** Pasang dengan:

    rclone mount gdrive: /mnt/gdrive --vfs-cache-mode writes --daemon

Anda juga bisa memasangnya lewat unit systemd, dengan `RequiresMountsFor=/mnt/gdrive` dan `After=network-online.target`. Set `NUXBILL_BACKUP_MIRROR=/mnt/gdrive/nuxbill-backup`, dan tambahkan `ReadWritePaths=/mnt/gdrive/nuxbill-backup` di drop-in. Google Drive punya kuota dan jeda, jadi cek folder itu sesekali.

Setelah mengisi `NUXBILL_BACKUP_MIRROR` di `/etc/nuxbill/config.env`, restart NuxBill.

### Uji restore dari mirror

Lakukan uji ini sekali setelah mirror pertama kali diatur, dan setiap tujuan mirror diganti. Jangan menunggu bencana.

1. Salin file terbaru dari folder mirror ke `/tmp/uji.db`. Salin juga `nuxbill.db.key` yang sesuai. Tanpa kunci, data tidak bisa dibaca.
2. Jalankan instance terpisah dengan port lain, misalnya `NUXBILL_DB=/tmp/uji.db` dan `NUXBILL_HTTP=:8099`.
3. Cek data pelanggan di UI.

## Backup kunci bersama database

Backup `nuxbill.db.key` bersama database. Tanpa file ini, data terenkripsi, yaitu password perangkat dan gateway, tidak bisa dibaca.

Jika memakai `NUXBILL_SECRET_KEY`, simpan nilainya di tempat aman. Nilai itu menggantikan file `.key`.

Salinan manual:

    sudo cp /var/lib/nuxbill/nuxbill.db /var/lib/nuxbill/nuxbill.db.key /mnt/usb/manual-backup/

## Pulihkan database

SuperAdmin bisa memulihkan file backup `.db` dari **Pengaturan > Aneka ragam > Restore database**. Halamannya ada di `/admin/settings/miscellaneous/restore`.

Alur restore:

1. Aplikasi memeriksa file: integritas, versi skema yang tidak lebih baru dari aplikasi ini, dan kunci enkripsi.
2. Aplikasi menampilkan jumlah data di backup, dibanding data saat ini.
3. Setelah dikonfirmasi, data saat ini dibackup otomatis ke folder backup, dengan nama berakhiran `-pre-restore.db`.
4. File backup dipasang sebagai `nuxbill.db.restore`.
5. Aplikasi keluar dengan kode 3. Systemd dengan `Restart=on-failure` menjalankannya lagi.
6. Saat start, database lama dipindah ke samping file database, dengan nama `nuxbill.db.pre-restore-<waktu>-<acak>`. Nama itu beserta `-wal`-nya ikut dipindah.
7. Backup diterapkan sebelum database dibuka. Backup dari versi lama naik versi sendiri lewat migrasi.

Setelah yakin pemulihan berhasil, file `nuxbill.db.pre-restore-*` boleh dihapus. Backup otomatis sudah tersedia.

Backup harus dibuat dengan `NUXBILL_SECRET_KEY` atau file `nuxbill.db.key` yang sama dengan aplikasi tujuan. Jika tidak sama, pemulihan ditolak.

### Restore manual

Gunakan langkah ini jika aplikasi tidak kembali, atau tidak berjalan di systemd.

1. Hentikan layanan.
2. Ganti file database.
3. Hapus file `-wal` dan `-shm`. File `-wal` lama wajib dihapus. Jika tidak, isinya ikut diputar ulang ke database yang sudah dipulihkan.
4. Jalankan lagi layanan.

    sudo systemctl stop nuxbill
    sudo cp /mnt/usb/manual-backup/nuxbill.db /var/lib/nuxbill/nuxbill.db
    sudo rm -f /var/lib/nuxbill/nuxbill.db-wal /var/lib/nuxbill/nuxbill.db-shm
    sudo systemctl start nuxbill

## Pengaman impor

Sebelum impor data dari PHPNuxBill, NuxBill membuat backup database otomatis di `NUXBILL_BACKUP_DIR`. Namanya `nuxbill-YYYYMMDD-HHMMSS-pre-import.db`. Jika backup gagal, impor tidak dijalankan. Langkah impor ada di [migrasi](migration-phpnuxbill.md).

## Lihat juga

- [Instalasi](installation.md): pemasangan dan folder data.
- [Upgrade](upgrade.md): backup sebelum upgrade dan rollback.
- [Konfigurasi](configuration.md): `NUXBILL_BACKUP_DIR`, `NUXBILL_BACKUP_MIRROR`, dan `backup_keep`.
- [Monitoring](monitoring.md): alert backup dan mirror.
- [Migrasi dari PHPNuxBill](migration-phpnuxbill.md): impor dan pengaman impor.
