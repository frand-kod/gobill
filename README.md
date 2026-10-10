# NuxBill Go

Billing ISP untuk hotspot dan PPPoE MikroTik, ditulis ulang dari PHPNuxBill dalam Go. Satu binary, satu database SQLite, tanpa PHP dan tanpa web server terpisah. Cocok untuk RT/RW Net dan ISP kecil-menengah yang ingin menjalankannya di STB Armbian (RAM 1-2 GB), VPS, atau Docker.

## Fitur utama

- Paket hotspot, PPPoE, dan saldo (prepaid/postpaid), voucher dengan QR, kupon, perpanjangan otomatis, dan opsi mulai masa aktif saat login pertama (RADIUS).
- Server RADIUS bawaan (auth, accounting, CoA, login voucher hotspot) dan endpoint kompatibel FreeRADIUS REST (`/radius.php`).
- Driver MikroTik lewat RouterOS API, atau mode RADIUS tanpa API.
- Portal pelanggan: pesan paket, aktivasi voucher, transfer saldo, OTP, inbox.
- Pembayaran online lewat Tripay (opsional), atau QRIS statis: QR terkunci nominal per recharge dikirim lewat WhatsApp.
- Notifikasi Telegram, WhatsApp (server WA langsung), SMS (URL gateway), email, dan webhook. Sakelar global untuk pesan pelanggan dan kode OTP.
- Dashboard, laporan, peta/ODP, multi-role admin, verifikasi dua langkah (TOTP) untuk admin, dan 5 bahasa.
- Aman untuk perangkat tanpa RTC: job expiry menolak jalan saat jam sistem tidak dipercaya.
- Impor satu kali dari PHPNuxBill: lewat MySQL, atau dari file backup JSON di UI atau CLI.
- Backup harian, salinan mirror di luar perangkat, dan restore database dari UI.
- Halaman Status Sistem, `/health` untuk uptime monitor, `/metrics` untuk Prometheus, dan alert operator lewat Telegram atau WhatsApp.

## Teknologi

| Bagian | Pilihan |
|---|---|
| Bahasa | Go, `net/http` stdlib, tanpa framework |
| Database | SQLite (`modernc.org/sqlite`, tanpa CGO) + `sqlc` |
| UI | `html/template`, Tailwind CSS v4, Alpine.js, Chart.js, Leaflet |
| RADIUS | `layeh.com/radius` |
| MikroTik | `go-routeros` (API) |
| Deploy | Binary statis (amd64, arm64, armv7), systemd, Docker |

## Arsitektur

```
 Browser admin / pelanggan --HTTP :8080--+
 Tripay (callback) ----------HTTP--------+
 FreeRADIUS (rlm_rest) ------/radius.php-+
                                         v
                     +------------- nuxbill (1 proses) -------------+
                     |  Web (admin, portal)   RADIUS :1812/:1813    |
                     |  Job: expiry, reminder, backup, clock guard  |
                     |                    |                         |
                     |              SQLite (file)                   |
                     +-------+----------------------+---------------+
                             |                      |
          API :8728 / CoA :3799 ke MikroTik    Telegram, WA/SMS, SMTP,
          RADIUS dari MikroTik (UDP)           webhook, Tripay
```

Detail: [docs/internal/arsitektur.md](docs/internal/arsitektur.md).

## Mulai cepat

1. Unduh binary dari [Releases](https://github.com/frand-kod/gobill/releases) (`nuxbill-linux-amd64`, `-arm64`, `-armv7`), atau build sendiri: `make build`.
2. Jalankan:

       NUXBILL_DB=./nuxbill.db NUXBILL_HTTP=:8080 ./nuxbill

3. Ambil password admin pertama dari file `initial-admin-password.txt` di folder database (`./` saat jalan manual, `/var/lib/nuxbill` di systemd). Password tidak dicetak ke log.
4. Buka http://localhost:8080, login sebagai `admin`, lalu ganti password.

Untuk STB, systemd, atau Docker, lihat [instalasi](docs/id/installation.md).

## Dokumentasi

Panduan operator tersedia dalam dua bahasa, dengan isi yang sama:

- [Bahasa Indonesia](docs/id/README.md)
- [English](docs/en/README.md)

Ringkasan per topik (versi Indonesia):

- Mulai: [instalasi](docs/id/installation.md), [upgrade](docs/id/upgrade.md), [migrasi dari PHPNuxBill](docs/id/migration-phpnuxbill.md).
- Konfigurasi: [konfigurasi](docs/id/configuration.md), [integrasi](docs/id/integrations.md), [keamanan](docs/id/security.md).
- Jaringan: [setup MikroTik](docs/id/mikrotik.md), [FreeRADIUS lewat REST](docs/id/freeradius-rest.md).
- Operasional: [monitoring](docs/id/monitoring.md), [backup dan restore](docs/id/backup-restore.md).
- Untuk tim, hanya bahasa Indonesia: [arsitektur](docs/internal/arsitektur.md), [pengembangan](docs/internal/pengembangan.md), [progres](docs/internal/progres.md), [rencana](docs/internal/rencana/README.md), dan [panduan menulis dokumentasi](docs/internal/panduan-menulis-docs.md).
- Riwayat: [CHANGELOG.md](CHANGELOG.md).

## Versi dan lisensi

Versi mengikuti SemVer, lihat [CHANGELOG.md](CHANGELOG.md) dan [aturan rilis](docs/internal/pengembangan.md#versi-dan-rilis).

Lisensi: GPL-3.0-or-later — lihat [LICENSE](LICENSE) dan [NOTICE](NOTICE). Turunan dari PHPNuxBill (GPL-2.0-or-later).

Berkontribusi: lihat [CONTRIBUTING.md](CONTRIBUTING.md).
