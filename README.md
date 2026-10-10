# NuxBill Go

Billing ISP untuk hotspot dan PPPoE MikroTik, ditulis ulang dari PHPNuxBill dalam Go. Satu binary, satu database SQLite, tanpa PHP dan tanpa web server terpisah. Cocok untuk RT/RW Net dan ISP kecil-menengah yang ingin menjalankannya di STB Armbian (RAM 1-2 GB), VPS, atau Docker.

## Fitur utama

- Paket hotspot, PPPoE, dan saldo (prepaid/postpaid), voucher dengan QR, kupon, dan perpanjangan otomatis.
- Server RADIUS bawaan (auth, accounting, CoA, login voucher hotspot) dan endpoint kompatibel FreeRADIUS REST (`/radius.php`).
- Driver MikroTik lewat RouterOS API, atau mode RADIUS tanpa API.
- Portal pelanggan: pesan paket, aktivasi voucher, transfer saldo, OTP, inbox.
- Pembayaran online lewat Tripay (opsional).
- Notifikasi Telegram, WhatsApp/SMS (URL gateway), email, dan webhook.
- Dashboard, laporan, peta/ODP, multi-role admin, dan 5 bahasa.
- Aman untuk perangkat tanpa RTC: job expiry menolak jalan saat jam sistem tidak dipercaya.
- Impor satu kali dari database MySQL PHPNuxBill.

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

Detail: [docs/arsitektur.md](docs/arsitektur.md).

## Mulai cepat

1. Unduh binary dari [Releases](https://github.com/frand-kod/gobill/releases) (`nuxbill-linux-amd64`, `-arm64`, `-armv7`), atau build sendiri: `make build`.
2. Jalankan:

       NUXBILL_DB=./nuxbill.db NUXBILL_HTTP=:8080 ./nuxbill

3. Ambil password admin pertama dari log (dicetak sekali): cari baris `first admin`. Di systemd: `journalctl -u nuxbill | grep "first admin"`.
4. Buka http://localhost:8080, login sebagai `admin`, lalu ganti password.

Untuk STB, systemd, atau Docker, lihat [docs/instalasi.md](docs/instalasi.md).

## Dokumentasi

Indeks lengkap: [docs/README.md](docs/README.md).

- [docs/instalasi.md](docs/instalasi.md): pasang di STB Armbian, VPS, atau Docker; upgrade dan pemecahan masalah.
- [docs/konfigurasi.md](docs/konfigurasi.md): semua variabel `NUXBILL_*` dan pengaturan penting di UI.
- [docs/mikrotik.md](docs/mikrotik.md): setup MikroTik (mode API dan RADIUS) dan pelajaran dari uji lapangan.
- [docs/freeradius-rest.md](docs/freeradius-rest.md): memakai FreeRADIUS yang sudah ada lewat REST.
- [docs/keamanan.md](docs/keamanan.md): pengerasan RADIUS dan catatan keamanan aplikasi.
- [docs/monitoring.md](docs/monitoring.md): halaman Status Sistem, `/health` dan `/metrics`, alert operator.
- [docs/migrasi-phpnuxbill.md](docs/migrasi-phpnuxbill.md): impor data, cutover, dan rollback.
- [docs/arsitektur.md](docs/arsitektur.md): paket, alur request, dan model data.
- [docs/pengembangan.md](docs/pengembangan.md): panduan developer, test, dan rilis.
- Riwayat dan status: [CHANGELOG.md](CHANGELOG.md), [docs/PROGRESS.md](docs/PROGRESS.md), [docs/UI-PARITY.md](docs/UI-PARITY.md), [docs/plan/](docs/plan/README.md).

## Versi dan lisensi

Versi mengikuti SemVer, lihat [CHANGELOG.md](CHANGELOG.md) dan [aturan rilis](docs/pengembangan.md#versi-dan-rilis).

Lisensi: GPL-3.0-or-later — lihat [LICENSE](LICENSE) dan [NOTICE](NOTICE). Turunan dari PHPNuxBill (GPL-2.0-or-later).

Berkontribusi: lihat [CONTRIBUTING.md](CONTRIBUTING.md).
