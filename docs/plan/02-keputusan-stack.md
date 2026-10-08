# 02 — ADR: Pemilihan Bahasa dan Stack

- **Status:** Diterima
- **Tanggal:** 2026-10-08

## Konteks

- Kode lama tidak memakai framework, tidak punya test, dan memiliki masalah keamanan yang struktural (lihat [01-audit-legacy.md](01-audit-legacy.md)).
- Strategi yang dipilih adalah **rewrite total** dengan migrasi data satu kali.
- Target deploy utama adalah **STB Armbian** (ARM64/ARMv7, RAM 1–2 GB, storage eMMC, tanpa RTC). Aplikasi juga harus jalan di VPS dan Docker.
- Prioritas: stabil, lalu mudah di-maintain, baru kemudian fitur.
- Skill pengelola: Laravel/PHP, JS/TS. Go bisa diterima asal kodenya lugas, tanpa abstraksi tingkat tinggi.
- Plugin dan payment gateway PHP lama boleh di-port bertahap. Kompatibilitas langsung tidak wajib.

## Opsi yang dipertimbangkan

### A. Go, monolit, stdlib-first (DIPILIH)

Kelebihan:
- Satu binary statis tanpa runtime. Cross-compile ke ARM cukup dengan `GOOS=linux GOARCH=arm64`.
- RAM idle ±20–40 MB.
- Jaminan kompatibilitas Go 1: kode yang ditulis hari ini tetap bisa di-compile bertahun-tahun ke depan.
- RADIUS server, job scheduler, dan web server berjalan dalam satu proses. Tidak perlu FreeRADIUS, crontab, PHP-FPM, atau nginx.
- Goroutine cocok untuk polling banyak router MikroTik secara paralel.
- Stdlib sudah cukup untuk routing, template, CSRF, log, dan CSV.

Kekurangan:
- Kurva belajar bagi pengelola yang terbiasa Laravel.
- Tidak ada plugin runtime. Plugin harus dikompilasi ke binary.
- Fitur seperti admin CRUD harus ditulis manual (tidak ada Filament/Nova).

### B. Laravel 12 (PHP 8.3+)

Kelebihan: sesuai skill pengelola, ekosistem besar, dan plugin lama relatif mudah di-port.

Kekurangan untuk target STB:
- Butuh PHP-FPM, nginx/Apache, MariaDB, queue worker, dan cron. Totalnya ±300–500 MB RAM dan lima proses yang harus dijaga.
- Upgrade mayor Laravel tiap tahun menambah beban maintenance.
- Deploy di ARM bergantung pada paket distro.

### C. NestJS / AdonisJS (TypeScript)

Kelebihan: sesuai skill TS.

Kekurangan: runtime Node ±100 MB+, dependency npm sering berubah dan rawan supply-chain, serta tetap butuh database server terpisah atau SQLite native (butuh build native di ARM).

## Keputusan

Pakai **Go (rilis stabil terbaru, minimal 1.25)** sebagai monolit single binary **tanpa web framework**.

Aturan gaya kode supaya tetap mudah dipahami:

- Handler HTTP adalah fungsi biasa `func(w http.ResponseWriter, r *http.Request)`.
- Tidak ada dependency injection container, ORM, reflection, atau code generation selain `sqlc`.
- Satu package per domain. Interface hanya dibuat jika implementasinya lebih dari satu (`Device`, `PaymentGateway`).
- Error dikembalikan secara eksplisit, tanpa panic untuk alur normal.

## Daftar library

| Kebutuhan | Pilihan | Alasan |
|---|---|---|
| HTTP & routing | `net/http` stdlib | Sejak Go 1.22 mendukung pattern `GET /customers/{id}` |
| CSRF | `http.CrossOriginProtection` (stdlib, Go 1.25) | Tanpa library tambahan |
| Template | `html/template` stdlib | Auto-escape XSS. Konsepnya mirip Blade |
| Interaktivitas | htmx (file JS statis, di-embed) | Tanpa build step JS dan tanpa SPA |
| Aset | `embed` stdlib | Template dan aset masuk ke binary |
| Database | SQLite via `modernc.org/sqlite` | Pure Go tanpa CGO, sehingga cross-compile ARM mudah |
| Query | `sqlc` | Tulis SQL biasa, lalu dapat fungsi Go yang type-safe. Mudah dibaca developer Laravel |
| Migrasi | File `.sql` di-embed + `PRAGMA user_version` | ±30 baris kode sendiri, tanpa dependency |
| Session | `github.com/alexedwards/scs/v2` | Bagian sensitif keamanan, jadi pakai library yang sudah teruji |
| Hash password | `golang.org/x/crypto/bcrypt` | Menggantikan sha1 |
| Enkripsi secret | `crypto/aes` + `crypto/cipher` (GCM) stdlib | Untuk secret PPPoE/hotspot yang harus bisa didekripsi |
| MikroTik API | `github.com/go-routeros/routeros/v3` | Menggantikan PEAR2 RouterOS |
| RADIUS | `layeh.com/radius` (+ `rfc2759` untuk MS-CHAPv2) | Server RADIUS built-in |
| Job terjadwal | goroutine + `time.Ticker` | Tanpa crontab dan tanpa library cron |
| Email | `github.com/wneessen/go-mail` | `net/smtp` stdlib sudah frozen |
| QR code | `github.com/skip2/go-qrcode` | Untuk cetak voucher |
| Log | `log/slog` stdlib | Structured log |
| Export | `encoding/csv` stdlib + halaman HTML siap print | Menggantikan mPDF. PDF dibuat lewat print browser |
| Test | `testing` + `net/http/httptest` stdlib | Tanpa framework test |

Library lain hanya boleh ditambahkan jika stdlib dan daftar di atas terbukti tidak cukup. Catat alasannya di dokumen ini.

## Konsekuensi

- **Plugin PHP lama tidak jalan.** Payment gateway di-port satu per satu sebagai implementasi interface `PaymentGateway` yang ikut dikompilasi. Untuk kebutuhan integrasi ringan, aplikasi menyediakan **outgoing webhook** per event (`customer.activated`, `recharge.expired`, `payment.paid`, ...) sebagai pengganti `run_hook`.
- **FreeRADIUS jadi opsional.** RADIUS built-in menangani auth dan accounting. Pengguna yang tetap ingin memakai FreeRADIUS bisa memakai endpoint REST yang kompatibel dengan `rlm_rest`. Endpoint ini ditunda sampai ada permintaan.
- **SQLite artinya satu instance per database.** Ini cukup untuk ISP kecil-menengah (ribuan pelanggan). `ponytail:` jika butuh multi-instance atau puluhan ribu sesi aktif, tambahkan dukungan PostgreSQL. `sqlc` mendukung PostgreSQL, sehingga yang perlu ditulis ulang hanya file query.
- **Pengelola perlu belajar Go dasar.** Untuk memitigasinya, gaya kode dibatasi oleh aturan di atas, dan setiap package diberi contoh test.
