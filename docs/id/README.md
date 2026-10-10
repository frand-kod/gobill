# Panduan operator (Bahasa Indonesia)

Dokumen ini adalah indeks panduan operator dalam Bahasa Indonesia. Panduan ini juga tersedia di aplikasi, di menu **Panduan**. Dokumen di folder ini dan `docs/en/` memakai nama file yang sama. Versi Inggris ada di [docs/en/README.md](../en/README.md).

**Untuk:** operator

**Prasyarat:** tidak ada

## Mulai

- [Instalasi](installation.md): pasang di STB Armbian, VPS, atau Docker, lalu pemecahan masalah.
- [Upgrade](upgrade.md): ganti binary ke versi baru, catatan per versi, dan rollback.
- [Migrasi dari PHPNuxBill](migration-phpnuxbill.md): impor data, cutover, dan checklist jalan paralel.

## Konfigurasi

- [Konfigurasi](configuration.md): variabel `NUXBILL_*` dan pengaturan di UI.
- [Integrasi](integrations.md): WhatsApp, SMS, Telegram, email, webhook, Tripay, dan QRIS.
- [Keamanan](security.md): pengerasan RADIUS, firewall, keamanan aplikasi, dan 2FA admin.

## Jaringan

- [Setup MikroTik](mikrotik.md): mode API dan RADIUS bawaan, serta pelajaran dari uji lapangan.
- [FreeRADIUS lewat REST](freeradius-rest.md): tetap memakai FreeRADIUS yang sudah ada.

## Operasional

- [Monitoring](monitoring.md): Status Sistem, `/health`, `/metrics`, dan alert operator.
- [Backup dan restore](backup-restore.md): backup harian, mirror, dan pemulihan database.

## Pencarian cepat

Kotak cari di bilah atas mencari pelanggan, langganan, invoice, voucher, paket, router, NAS, menu, aksi cepat, dan pengaturan. Tekan `/` atau `Ctrl+K` (`Cmd+K` di Mac) untuk langsung mengetik di kotak itu. Panah atas dan bawah memilih hasil, Enter membuka, dan Esc menutup. Di HP, hasil tampil selebar layar. Pengaturan langsung menuju kolomnya, dan kolom itu disorot sebentar. Hasil yang bisa dibuka sesuai peran Anda.

## Lihat juga

- [Panduan developer dan arsitektur](../internal/pengembangan.md): hanya untuk tim, tidak ada di aplikasi.
- [README utama](../../README.md): ringkasan proyek.
