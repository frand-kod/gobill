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

Memori diukur sebagai PSS, yaitu RSS dengan halaman bersama dibagi rata antar proses. Angka PHPNuxBill adalah jumlah semua proses Apache, PHP, dan MariaDB. Memori puncak diambil tiap 0,2 detik selama pengukuran.

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
```

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

## RADIUS UDP: `bench/radius`

Load generator untuk server RADIUS UDP bawaan gobill. Generator ini open-loop: paket dikirim sesuai jadwal walaupun paket sebelumnya belum dijawab. Server yang lambat terlihat sebagai latensi, bukan tersembunyi di balik klien yang ikut melambat.

1. Seed database baru. Server tidak boleh sedang berjalan:

   ```sh
   go run ./bench/radius -db /tmp/rb/g.db -users 50000 -nas 50
   ```

   Pelanggan `user0` sampai `userN-1` memakai password `pw-userN`, dengan satu paket Hotspot 30 hari. NAS ke-i mengirim dari `127.10.x.y`.

2. Jalankan gobill pada database itu dengan 2 CPU:

   ```sh
   GOBILL_DB=/tmp/rb/g.db GOBILL_RADIUS=127.0.0.1:18120 GOBILL_HTTP=127.0.0.1:18080 GOBILL_HTTPS=0 \
   GOBILL_PPROF=127.0.0.1:6060 taskset -c 0,1 gobill
   ```

3. Jalankan skenario dari CPU lain. `-pid` menampilkan RSS server:

   ```sh
   taskset -c 2-7 go run ./bench/radius -addr 127.0.0.1:18120 -users 50000 -nas 50 -scenario ramp -pid $(pgrep -x gobill)
   ```

| Skenario | Isi |
|---|---|
| `ramp` | Login valid dinaikkan `-step` per `-stepdur` sampai ada paket tanpa balasan di atas 0,1% atau p99 melewati `-slo`. |
| `spike` | Semua pelanggan login, lalu Accounting-Start, masing-masing dalam `-window`. Ini meniru semua router yang menyala lagi setelah listrik padam. Setelah itu semua sesi berhenti. |
| `mixed` | Interim update semua pelanggan, `-rate` login/s, dan `-attack` paket jahat/s: password salah, user tidak dikenal, dan secret salah. |

Profil CPU selama beban: `go tool pprof http://127.0.0.1:6060/debug/pprof/profile?seconds=15`. Paket yang hilang karena buffer socket penuh terlihat di `RcvbufErrors` pada `/proc/net/snmp`.

### Hasil 2026-10-14

Host yang sama dengan hasil di atas. Server dibatasi 2 CPU dengan `taskset`, generator di 6 CPU lain. 50.000 pelanggan, 50 NAS, timeout NAS 3 detik.

**Ramp: login valid per detik.**

| | Batas (0 paket hilang, p99 < 1 s) | p99 pada 250/s |
|---|---:|---:|
| Sebelum | 250/s | 108 ms |
| Sesudah | 2000/s | 3 ms |

**Spike: 50.000 login, lalu 50.000 Accounting-Start, masing-masing dalam 30 detik (1667/s), NAS retransmit 1 detik.** Semua 150.000 paket dijawab benar.

| Paket | p50 | p99 | max |
|---|---:|---:|---:|
| Login | 5 ms | 587 ms | 1510 ms |
| Accounting-Start | 10 ms | 790 ms | 1710 ms |
| Accounting-Stop | 2 ms | 191 ms | 907 ms |

**Mixed: 300 login/s dan interim untuk 50.000 sesi, ditambah serangan.**

| Serangan | Login valid berhasil | RSS server |
|---|---:|---:|
| 2000/s | 100% (p99 73 ms) | 96 MB |
| 8000/s (4x kapasitas), tanpa pemotongan beban | 8% | 383 MB |
| 8000/s (4x kapasitas), dengan pemotongan beban | 41% | 144 MB |

Di 8000/s server sudah melewati kapasitasnya. Serangan sebesar itu butuh shared secret NAS. Paket dengan secret salah di-drop di read loop dan tidak terhitung beban. Sebelum ada pemotongan beban, setiap jawaban terlambat melewati 3 detik, sehingga tidak ada yang bisa masuk.

### Hasil 2026-10-14: 1 core, disk sungguhan

Server dibatasi 1 core dan 256 MB. DB di ext4 pada NVMe, bukan di `tmpfs`: di `tmpfs`, `fsync` tidak melakukan apa-apa, sehingga biaya tulis tidak terlihat. 5000 pelanggan, 50 NAS. Ramp sampai ada paket tanpa balasan di atas 0,1% atau p99 di atas 1 detik.

| Ramp | v0.1.14 | Sesudah |
|---|---:|---:|
| Login | 200/s | 1100/s |
| Accounting interim (`-acct`) | 150/s | 1500/s |

Faktor terbesar di accounting adalah group commit dan `GOMAXPROCS` minimal 2. Dengan satu P, setiap `fsync` (sekitar 5 ms di NVMe ini) menghentikan seluruh proses. Pembatasan IOPS lewat cgroup (`IOWriteIOPSMax` 1000 dan 200) hampir tidak mengubah hasil. Efektivitas pembatasan itu belum diverifikasi, jadi angka untuk SD card tetap perkiraan sampai diuji di perangkat ARM.

Batas memori dengan 1 core, setelah tuning otomatis, untuk 5000 pelanggan: 64 MB dan 48 MB tetap berjalan di semua skenario. 32 MB berjalan normal, tetapi mati saat diserang 2000 paket/s. Tanpa tuning, 64 MB sudah mati kena OOM.

## Lihat juga

- [README](../README.md#benchmark): ringkasan hasil
- [CHANGELOG](../CHANGELOG.md): perbaikan performa yang ditemukan lewat benchmark ini
- [Keputusan stack](../docs/internal/rencana/keputusan-stack.md): alasan memilih Go dan SQLite
