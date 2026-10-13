# Benchmark gobill vs PHPNuxBill

Folder ini berisi skrip yang membandingkan PHPNuxBill dan gobill dengan data yang sama di host yang sama. Ringkasan hasilnya ada di [README utama](../README.md#benchmark).

**Untuk:** pengembang

**Prasyarat:** `podman`, `ab` dari paket `httpd-tools`, `curl`, `python3`, Go sesuai `go.mod`, dan checkout PHPNuxBill.

## Menjalankan

1. Clone PHPNuxBill di sebelah repo ini:

   ```sh
   git clone https://github.com/hotspotbilling/phpnuxbill ../phpnuxbill
   ```

2. Jalankan skrip dari root repo:

   ```sh
   PHPNUXBILL=../phpnuxbill bench/run.sh
   ```

3. Tabel hasil dicetak ke stdout dalam format Markdown. Satu run butuh sekitar 8 menit.

Skrip memakai port `8081`, `8082`, dan `3307`. Semua container dan file sementara dihapus setelah selesai.

| Variabel | Bawaan | Arti |
|---|---|---|
| `CUSTOMERS` | `5000` | Jumlah pelanggan aktif di data uji |
| `DURATION` | `30` | Lama tiap skenario, dalam detik |
| `CONCURRENCY` | `50` | Jumlah koneksi paralel `ab` |
| `CPUS` | `2` | Batas CPU untuk tiap container server |

## Cara kerja

1. PHPNuxBill berjalan di image resmi `php:8.2-apache` dengan opcache aktif dan `php.ini-production`, ditambah `mariadb:10.11`. Keduanya memakai konfigurasi bawaan. Lihat [php.Containerfile](php.Containerfile).
2. [seed.sql](seed.sql) mengisi MariaDB dengan satu paket hotspot RADIUS, `CUSTOMERS` pelanggan aktif, satu langganan, dan satu transaksi per pelanggan.
3. gobill mengimpor data yang sama dengan `gobill import --mysql-dsn`, persis seperti migrasi sungguhan. Durasi impor ikut dicatat.
4. gobill berjalan di image `distroless/static`. Setiap container server dibatasi `CPUS` CPU.
5. `ab` menjalankan setiap skenario selama `DURATION` detik dengan keep-alive, ke kedua aplikasi secara bergantian.

| Skenario | PHPNuxBill | gobill |
|---|---|---|
| RADIUS REST authenticate | `POST /radius.php`, header `X-Freeradius-Section: authenticate` | Endpoint yang sama |
| RADIUS REST authorize | `POST /radius.php`, header `X-Freeradius-Section: authorize` | Endpoint yang sama |
| Daftar pelanggan admin | `/?_route=customers` | `/admin/customers` |
| Dashboard admin | `/?_route=dashboard` | `/admin` |

Memori diukur sebagai PSS, yaitu RSS dengan halaman bersama dibagi rata antar proses. Angka PHPNuxBill adalah jumlah semua proses Apache, PHP, dan MariaDB. Memori puncak diambil tiap 0,2 detik selama skenario dashboard.

## Batasan

- `ab` dan server berjalan di mesin yang sama dan berebut CPU. Rasio antar aplikasi lebih bermakna daripada angka absolutnya.
- Angka di STB ARM akan lebih rendah. Rasionya belum diukur di STB.
- RADIUS UDP bawaan gobill tidak dibandingkan, karena PHPNuxBill tidak punya server RADIUS sendiri. Yang dibandingkan adalah kontrak `rlm_rest` yang dimiliki keduanya.
- Cold start PHPNuxBill hanya mengukur Apache. MariaDB sudah berjalan lebih dulu karena dibutuhkan untuk seed.
- Halaman admin kedua aplikasi tidak identik. Isi dan ukuran HTML berbeda.

## Hasil mentah 2026-10-13

Commit `cd32e8a` ditambah perubahan di `[Unreleased]` [CHANGELOG](../CHANGELOG.md). Podman 5.8.7, ApacheBench 2.3, Go 1.26.9.

```
Host: Intel(R) Core(TM) i7-8650U CPU @ 1.90GHz, 8 threads, 7 GB RAM, Linux 7.2.9-201.fc44.x86_64
Each server container limited to 2 CPUs. 5000 customers. ab -k -t 30 -c 50 per scenario.

| Scenario | PHPNuxBill req/s | gobill req/s | Speed-up | PHPNuxBill p50 / p99 | gobill p50 / p99 | Non-2xx (PHP / Go) |
|---|---:|---:|---:|---:|---:|---:|
| RADIUS REST authenticate | 432 | 928 | **2.1x** | 100 / 285 ms | 50 / 130 ms | 0 / 0 |
| RADIUS REST authorize | 317 | 825 | **2.6x** | 142 / 381 ms | 56 / 138 ms | 0 / 0 |
| Admin customer list | 31 | 29 | **0.9x** | 1606 / 1701 ms | 1655 / 2945 ms | 0 / 0 |
| Admin dashboard | 12 | 20 | **1.7x** | 4155 / 4401 ms | 2428 / 3498 ms | 0 / 0 |

| Resource | PHPNuxBill (Apache + PHP + MariaDB) | gobill |
|---|---:|---:|
| Memory idle, after warm-up (PSS) | 152 MB | 45 MB |
| Memory peak under load (PSS) | 229 MB | 66 MB |
| Cold start to first HTTP 200 | 817 ms | 1006 ms |
| Image size | 853 MB | 26 MB |
| Import of 5000 PHPNuxBill customers | - | 217 s |
| Processes | 32 | 1 |
```

## Lihat juga

- [README](../README.md#benchmark): ringkasan hasil
- [CHANGELOG](../CHANGELOG.md): perbaikan performa yang ditemukan lewat benchmark ini
- [Keputusan stack](../docs/internal/rencana/keputusan-stack.md): alasan memilih Go dan SQLite
