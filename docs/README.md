# Indeks Dokumentasi

Pilih dokumen sesuai peran Anda. Setiap dokumen berdiri sendiri. Ringkasan proyek ada di [README utama](../README.md).

## Untuk operator/admin

| Dokumen | Isi |
|---|---|
| [instalasi.md](instalasi.md) | Pasang di STB Armbian, VPS, atau Docker; jam/NTP; backup ke USB; upgrade; pemecahan masalah |
| [konfigurasi.md](konfigurasi.md) | Variabel `NUXBILL_*` dan pengaturan penting di UI |
| [mikrotik.md](mikrotik.md) | Setup MikroTik mode API dan RADIUS bawaan; uji aman di router produksi |
| [freeradius-rest.md](freeradius-rest.md) | Tetap memakai FreeRADIUS lewat `rlm_rest` |
| [keamanan.md](keamanan.md) | Pengerasan RADIUS, firewall, keamanan aplikasi, dan 2FA admin |
| [monitoring.md](monitoring.md) | Status Sistem, `/health` vs `/metrics` (Prometheus), alert operator, Uptime Kuma |
| [migrasi-phpnuxbill.md](migrasi-phpnuxbill.md) | Impor dari PHPNuxBill (JSON atau MySQL), cutover, rollback, checklist jalan paralel |

### Pencarian cepat

Kotak cari di bilah atas mencari pelanggan, langganan, invoice, voucher, paket, router, NAS, menu, aksi cepat, dan pengaturan. Tekan `/` atau `Ctrl+K` (`Cmd+K` di Mac) untuk langsung mengetik di kotak itu; panah atas/bawah memilih hasil, Enter membuka, Esc menutup. Di HP, hasil tampil selebar layar. Pengaturan langsung menuju kolomnya, dan kolom itu disorot sebentar. Hasil yang bisa dibuka sesuai peran Anda.

## Untuk developer

| Dokumen | Isi |
|---|---|
| [arsitektur.md](arsitektur.md) | Paket, alur request, model data, aturan STB |
| [pengembangan.md](pengembangan.md) | Layout repo, build/test, sqlc, Tailwind, migration freeze, rilis, konvensi kode |

## Lisensi & kontribusi

| Dokumen | Isi |
|---|---|
| [../LICENSE](../LICENSE) | Teks GPL-3.0-or-later |
| [../NOTICE](../NOTICE) | Atribusi PHPNuxBill dan daftar komponen pihak ketiga |
| [../CONTRIBUTING.md](../CONTRIBUTING.md) | Cara berkontribusi, DCO, dan aturan commit |

## Riwayat & status

| Dokumen | Isi |
|---|---|
| [../CHANGELOG.md](../CHANGELOG.md) | Perubahan per versi |
| [PROGRESS.md](PROGRESS.md) | Status saat ini, yang menunggu pengguna, dan item yang ditunda |
| [BUSINESS-PARITY.md](BUSINESS-PARITY.md) | Audit perilaku bisnis dibanding PHPNuxBill, dan status temuannya |
| [UX-AUDIT.md](UX-AUDIT.md) | Audit UX per layar, admin dan portal |
| [UX-FLOW-AUDIT.md](UX-FLOW-AUDIT.md) | Audit alur tugas operator dan pelanggan |
| [UI-PARITY.md](UI-PARITY.md) | Perbandingan layar dan field dengan PHPNuxBill lama |
| [plan/](plan/README.md) | Rencana awal: audit kode lama, keputusan stack, arsitektur, roadmap |
