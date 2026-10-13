<div align="center">

# gobill

**Billing ISP untuk hotspot dan PPPoE MikroTik. Satu binary, satu file database, tanpa PHP.**

Penulisan ulang PHPNuxBill dalam Go untuk RT/RW Net dan ISP kecil-menengah.
Berjalan di STB Armbian dengan RAM 1 GB, VPS, atau Docker.

[![Release](https://img.shields.io/github/v/release/frand-kod/gobill?sort=semver)](https://github.com/frand-kod/gobill/releases)
[![CI](https://img.shields.io/github/actions/workflow/status/frand-kod/gobill/ci.yml?branch=main&label=CI)](https://github.com/frand-kod/gobill/actions/workflows/ci.yml)
[![Go](https://img.shields.io/github/go-mod/go-version/frand-kod/gobill)](go.mod)
[![License](https://img.shields.io/badge/license-GPL--3.0--or--later-blue)](LICENSE)

[Mulai cepat](#mulai-cepat) · [Benchmark](#benchmark) · [Dokumentasi](docs/id/README.md) · [English docs](docs/en/README.md) · [Changelog](CHANGELOG.md)

</div>

## Terima kasih untuk PHPNuxBill

gobill tidak akan ada tanpa [PHPNuxBill](https://github.com/hotspotbilling/phpnuxbill). Terima kasih kepada [Ibnu Maksum](https://github.com/ibnux) dan [seluruh kontributor PHPNuxBill](https://github.com/hotspotbilling/phpnuxbill/graphs/contributors). Model data, alur bisnis, kontrak `radius.php`, dan file bahasa di `lang/` berasal dari kerja mereka.

gobill adalah karya turunan PHPNuxBill dan tetap berlisensi copyleft. Jika PHPNuxBill sudah cukup untuk Anda, pakai PHPNuxBill dan dukung pembuatnya lewat [donasi ke ibnux](https://paypal.me/ibnux).

## Kenapa ditulis ulang

PHPNuxBill membuktikan kebutuhannya. Masalahnya ada di dua hal: target perangkat dan dasar kode.

- **Perangkat kecil.** PHPNuxBill butuh web server, PHP-FPM atau Apache, MySQL, crontab, dan biasanya FreeRADIUS. Di STB dengan RAM 1-2 GB dan eMMC, lima proses itu berebut memori dan sulit dirawat.
- **Keamanan.** Audit PHPNuxBill 2025.3.20 menemukan SQL injection di `radius.php`, sekitar 63 raw query, password admin `sha1` tanpa salt, dan password pelanggan plaintext.
- **Integritas data.** Harga disimpan sebagai `varchar`, masa aktif dipecah ke dua kolom tanpa timezone, dan tidak ada foreign key.
- **Tanpa test.** Kode lama tidak punya test otomatis, sehingga setiap perubahan berisiko.

Detail temuan ada di [audit kode lama](docs/internal/rencana/audit-legacy.md). Alasan memilih Go ada di [keputusan stack](docs/internal/rencana/keputusan-stack.md).

## Benchmark

PHPNuxBill 2025.3.20 dan gobill diuji berdampingan. Keduanya memakai data yang sama, yaitu 5.000 pelanggan aktif hasil impor, di host yang sama. Setiap server dibatasi 2 CPU.

**Sumber daya**

| | PHPNuxBill | gobill | Selisih |
|---|---:|---:|---:|
| Memori idle | 152 MB | 45 MB | **3,4× lebih kecil** |
| Memori puncak saat beban | 229 MB | 66 MB | **3,5× lebih kecil** |
| Ukuran image | 853 MB | 26 MB | **33× lebih kecil** |
| Jumlah proses | 32 | 1 | |
| Cold start | 817 ms | 1.006 ms | 0,2 detik lebih lambat |

**Throughput**, `ab -k -c 50` selama 30 detik per skenario

| Skenario | PHPNuxBill | gobill | Selisih |
|---|---:|---:|---:|
| RADIUS REST authenticate | 432 req/s | 928 req/s | **2,1× lebih cepat** |
| RADIUS REST authorize | 317 req/s | 825 req/s | **2,6× lebih cepat** |
| Dashboard admin | 12 req/s | 20 req/s | **1,7× lebih cepat** |
| Daftar pelanggan admin | 31 req/s | 29 req/s | setara |

Latensi p50 login RADIUS turun dari 100 ms menjadi 50 ms, dan p99 dari 285 ms menjadi 130 ms.

Yang perlu Anda ketahui:

- Daftar pelanggan belum lebih cepat. SQLite pure-Go lebih lambat daripada MariaDB untuk mengurutkan ribuan baris.
- Cold start sedikit lebih lambat karena gobill memperbarui statistik database saat start. Cold start PHPNuxBill tidak menghitung start MariaDB.
- Benchmark ini menemukan lima masalah performa dan satu bug sesi admin di gobill. Semuanya sudah diperbaiki sebelum angka di atas diambil, lihat [CHANGELOG](CHANGELOG.md).
- Angka diukur di laptop Intel i7-8650U, bukan di STB. Rasio antar aplikasi lebih bermakna daripada angka absolutnya.

Metodologi, batasan, dan hasil mentah ada di [bench/README.md](bench/README.md). Untuk mengulang di mesin Anda:

```sh
git clone https://github.com/hotspotbilling/phpnuxbill ../phpnuxbill
PHPNUXBILL=../phpnuxbill bench/run.sh
```

## Fitur

**Billing**

- Paket hotspot, PPPoE, dan saldo. Prepaid dan postpaid.
- Voucher dengan QR, kupon, perpanjangan otomatis dari saldo.
- Masa aktif bisa mulai saat login pertama lewat RADIUS.
- Isolir: pelanggan yang langganannya kedaluwarsa masuk paket Isolir dan diarahkan ke halaman pembayaran.

**Jaringan**

- Server RADIUS bawaan untuk auth, accounting, CoA, dan login voucher hotspot.
- Message-Authenticator otomatis per NAS sebagai mitigasi BlastRADIUS.
- Endpoint `/radius.php` kompatibel dengan FreeRADIUS `rlm_rest`, jadi konfigurasi FreeRADIUS lama tetap jalan.
- Driver MikroTik lewat RouterOS API, atau mode RADIUS tanpa API.
- Sesi online live: kecepatan per pelanggan dibanding kecepatan paket, tanpa reload.

**Pelanggan dan pembayaran**

- Portal pelanggan: beli paket, aktivasi voucher, transfer saldo, OTP, dan inbox.
- Pembayaran online lewat Tripay, atau QRIS statis dengan nominal terkunci per transaksi.
- Notifikasi Telegram, WhatsApp, SMS, email, dan webhook.

**Operasional**

- Setup cepat langkah demi langkah untuk router, paket, WhatsApp, QRIS, dan Isolir.
- Update satu perintah `sudo gobill update` dengan cek checksum, dan `gobill rollback`.
- Backup harian, mirror di luar perangkat, dan restore dari UI.
- Halaman Status Sistem, `/health`, `/metrics` untuk Prometheus, dan alert operator lewat Telegram atau WhatsApp.
- Aman untuk perangkat tanpa RTC: job kedaluwarsa tidak jalan selama jam sistem belum dipercaya.
- Multi-role akun admin, 2FA TOTP, dan beberapa bahasa antarmuka.

**Migrasi**

- Impor satu kali dari PHPNuxBill lewat MySQL, atau dari file backup JSON di UI maupun CLI.

## Mulai cepat

1. Unduh binary dari [Releases](https://github.com/frand-kod/gobill/releases): `gobill-linux-amd64`, `gobill-linux-arm64`, atau `gobill-linux-armv7`. Anda juga bisa build sendiri dengan `make build`.
2. Jalankan gobill:

   ```sh
   GOBILL_DB=./gobill.db GOBILL_HTTP=:8080 ./gobill
   ```

3. Buka file `initial-admin-password.txt` di folder database. Password admin pertama ada di file itu, tidak di log.
4. Buka `http://localhost:8080/admin/login`, login sebagai `admin`, lalu ganti password.
5. Ikuti **Setup cepat** di dashboard. Pelanggan login di `/login`.

Sudah memakai PHPNuxBill? Impor datanya dengan satu perintah:

```sh
gobill import --mysql-dsn 'user:<SECRET>@tcp(127.0.0.1:3306)/phpnuxbill'
```

Untuk STB, systemd, dan Docker, baca [instalasi](docs/id/installation.md). Untuk cutover dari sistem lama, baca [migrasi dari PHPNuxBill](docs/id/migration-phpnuxbill.md).

## Tech stack

| Bagian | Pilihan | Alasan |
|---|---|---|
| Bahasa | Go 1.26, `net/http` stdlib | Satu binary statis tanpa runtime. Tanpa web framework. |
| Database | SQLite lewat `modernc.org/sqlite` | Pure Go tanpa CGO, sehingga cross-compile ke ARM mudah. |
| Query | `sqlc` | SQL biasa, hasilnya fungsi Go yang type-safe. Tanpa ORM. |
| UI | `html/template`, Tailwind CSS v4, Alpine.js | Render di server, tanpa build step JS dan tanpa Node. |
| Grafik dan peta | Chart.js, Leaflet | Di-embed ke binary. |
| RADIUS | `layeh.com/radius` | Server RADIUS di dalam proses yang sama. |
| MikroTik | `go-routeros/routeros` | RouterOS API. |
| Session dan auth | `alexedwards/scs`, `bcrypt`, AES-GCM | Library teruji untuk bagian sensitif. |
| Job terjadwal | goroutine dan `time.Ticker` | Tanpa crontab. |
| Test | `testing` dan `httptest` stdlib | Tanpa framework test. |
| Deploy | Binary amd64, arm64, armv7. systemd atau Docker. | |

Daftar lengkap dependency dan lisensinya ada di [NOTICE](NOTICE).

## Arsitektur

Semua komponen berjalan dalam satu proses. Tidak ada PHP-FPM, web server, crontab, atau database server terpisah.

```
 Browser admin / pelanggan --HTTP :8080--+
 Tripay (callback) ----------HTTP--------+
 FreeRADIUS (rlm_rest) ------/radius.php-+
                                         v
                     +------------- gobill (1 proses) -------------+
                     |  Web (admin, portal)   RADIUS :1812/:1813    |
                     |  Job: expiry, reminder, backup, clock guard  |
                     |                    |                         |
                     |              SQLite (file)                   |
                     +-------+----------------------+---------------+
                             |                      |
          API :8728 / CoA :3799 ke MikroTik    Telegram, WA/SMS, SMTP,
          RADIUS dari MikroTik (UDP)           webhook, Tripay
```

Detail ada di [arsitektur](docs/internal/arsitektur.md).

## Dokumentasi

Panduan operator tersedia dalam [bahasa Indonesia](docs/id/README.md) dan [English](docs/en/README.md) dengan isi yang sama. Panduan yang sama juga ada di aplikasi, menu **Panduan**.

| Topik | Dokumen |
|---|---|
| Mulai | [Instalasi](docs/id/installation.md), [upgrade](docs/id/upgrade.md), [migrasi dari PHPNuxBill](docs/id/migration-phpnuxbill.md) |
| Konfigurasi | [Konfigurasi](docs/id/configuration.md), [integrasi](docs/id/integrations.md), [keamanan](docs/id/security.md) |
| Jaringan | [Setup MikroTik](docs/id/mikrotik.md), [FreeRADIUS lewat REST](docs/id/freeradius-rest.md) |
| Operasional | [Monitoring](docs/id/monitoring.md), [backup dan restore](docs/id/backup-restore.md) |
| Pengembangan | [Arsitektur](docs/internal/arsitektur.md), [pengembangan](docs/internal/pengembangan.md), [progres](docs/internal/progres.md), [rencana](docs/internal/rencana/README.md), [panduan menulis dokumentasi](docs/internal/panduan-menulis-docs.md) |

## Berkontribusi

Issue dan pull request diterima. Baca [CONTRIBUTING.md](CONTRIBUTING.md) sebelum mengirim perubahan. Laporkan kerentanan secara privat lewat [GitHub Security Advisories](https://github.com/frand-kod/gobill/security/advisories/new), bukan issue publik.

## Lisensi

gobill berlisensi [GPL-3.0-or-later](LICENSE). gobill adalah turunan PHPNuxBill yang berlisensi GPL-2.0-or-later. Atribusi dan lisensi pihak ketiga ada di [NOTICE](NOTICE). Versi mengikuti SemVer, lihat [CHANGELOG.md](CHANGELOG.md).
