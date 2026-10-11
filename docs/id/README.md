# Panduan operator (Bahasa Indonesia)

Dokumen ini adalah indeks panduan operator dalam Bahasa Indonesia. Panduan ini juga tersedia di aplikasi, di menu **Panduan**. Dokumen di folder ini dan `docs/en/` memakai nama file yang sama. Versi Inggris ada di [docs/en/README.md](../en/README.md).

**Untuk:** operator

**Prasyarat:** tidak ada

## Mulai

- [Instalasi](installation.md): pasang di STB Armbian, VPS, atau Docker, lalu pemecahan masalah.
- [Setup cepat](quick-setup.md): panduan langkah demi langkah setelah instalasi, atau impor dari PHPNuxBill.
- [Upgrade](upgrade.md): ganti binary ke versi baru, catatan per versi, dan rollback.
- [Migrasi dari PHPNuxBill](migration-phpnuxbill.md): impor data, cutover, dan checklist jalan paralel.

## Konfigurasi

- [Konfigurasi](configuration.md): variabel `GOBILL_*` dan pengaturan di UI.
- [Integrasi](integrations.md): WhatsApp, SMS, Telegram, email, webhook, Tripay, dan QRIS.
- [Keamanan](security.md): pengerasan RADIUS, firewall, keamanan aplikasi, dan 2FA admin.
- [Promosi](promotions.md): kartu promo singkat di halaman isolir dan dashboard portal.

## Jaringan

- [Setup MikroTik](mikrotik.md): mode API dan RADIUS bawaan, serta pelajaran dari uji lapangan.
- [FreeRADIUS lewat REST](freeradius-rest.md): tetap memakai FreeRADIUS yang sudah ada.

## Operasional

- [Monitoring](monitoring.md): Status Sistem, `/health`, `/metrics`, dan alert operator.
- [Backup dan restore](backup-restore.md): backup harian, mirror, dan pemulihan database.

## Pelanggan

Di daftar pelanggan, centang beberapa pelanggan lalu pilih **Isi ulang paket yang sama**. Setiap pelanggan diisi ulang dengan paket yang sedang dipakainya, dibayar tunai, dengan aturan yang sama seperti isi ulang satu per satu. Konfirmasi menampilkan jumlah pelanggan dan total. Hasilnya menampilkan berapa yang berhasil dan alasan tiap yang gagal. Batasnya 200 pelanggan per aksi. Jangan muat ulang halaman hasil, karena isi ulang akan dijalankan lagi.

Daftar pelanggan punya kolom **Tanggal habis**, yaitu tanggal berakhir paket aktif yang paling dekat. Kolom ini bisa diurutkan. Tulisan **Hari ini** berarti paket berakhir hari ini.

Strip diagnosa di halaman pelanggan memberi satu langkah untuk setiap pemeriksaan yang bermasalah: akun, paket (habis, diisolir, atau belum mulai sampai pelanggan login pertama), koneksi (online dengan NAS, IP, dan lama tersambung, atau offline dengan waktu sesi terakhir), kuota, dan router.

## Pencarian cepat

Kotak cari di bilah atas mencari pelanggan, langganan, invoice, voucher, paket, router, NAS, menu, aksi cepat, dan pengaturan. Tekan `/` atau `Ctrl+K` (`Cmd+K` di Mac) untuk langsung mengetik di kotak itu. Panah atas dan bawah memilih hasil, Enter membuka, dan Esc menutup. Di HP, hasil tampil selebar layar. Pengaturan langsung menuju kolomnya, dan kolom itu disorot sebentar. Hasil yang bisa dibuka sesuai peran Anda.

Form tambah pelanggan punya kartu opsional **Langsung aktifkan paket**. Pilih paket di sana untuk mengaktifkannya tepat setelah pelanggan dibuat, dibayar tunai. Hanya paket yang sesuai jenis layanan yang muncul, dan paket saldo tidak ditampilkan. Jika aktivasi gagal, pelanggan tetap dibuat dan halaman pelanggan menampilkan alasannya. Kosongkan paket untuk memilihnya nanti dari halaman pelanggan.

## Lihat juga

- [Panduan developer dan arsitektur](../internal/pengembangan.md): hanya untuk tim, tidak ada di aplikasi.
- [README utama](../../README.md): ringkasan proyek.
