# Indeks Dokumentasi

Pilih dokumen sesuai peran Anda. Setiap dokumen berdiri sendiri. Ringkasan proyek ada di [README utama](../README.md).

## Untuk operator/admin

| Dokumen | Isi |
|---|---|
| [instalasi.md](instalasi.md) | Pasang di STB Armbian, VPS, atau Docker; jam/NTP; backup ke USB; upgrade; pemecahan masalah |
| [konfigurasi.md](konfigurasi.md) | Variabel `NUXBILL_*` dan pengaturan penting di UI |
| [mikrotik.md](mikrotik.md) | Setup MikroTik mode API dan RADIUS bawaan; uji aman di router produksi |
| [freeradius-rest.md](freeradius-rest.md) | Tetap memakai FreeRADIUS lewat `rlm_rest` |
| [keamanan.md](keamanan.md) | Pengerasan RADIUS, firewall, dan keamanan aplikasi |
| [migrasi-phpnuxbill.md](migrasi-phpnuxbill.md) | Impor dari PHPNuxBill, cutover, rollback, checklist jalan paralel |

## Untuk developer

| Dokumen | Isi |
|---|---|
| [arsitektur.md](arsitektur.md) | Paket, alur request, model data, aturan STB |
| [pengembangan.md](pengembangan.md) | Layout repo, build/test, sqlc, Tailwind, migration freeze, rilis, konvensi kode |

## Riwayat & status

| Dokumen | Isi |
|---|---|
| [../CHANGELOG.md](../CHANGELOG.md) | Perubahan per versi |
| [PROGRESS.md](PROGRESS.md) | Jurnal progres dan hasil uji lapangan |
| [UI-PARITY.md](UI-PARITY.md) | Perbandingan layar dan field dengan PHPNuxBill lama |
| [plan/](plan/README.md) | Rencana awal: audit kode lama, keputusan stack, arsitektur, roadmap |
