# Panduan Developer

Untuk developer yang ingin membangun, mengubah, atau merilis gobill. Gambaran sistem ada di [arsitektur.md](arsitektur.md).

**Untuk:** pengembangan. Hanya tersedia dalam bahasa Indonesia.

## Layout repo

| Path | Isi |
|---|---|
| `cmd/gobill/` | Entry point dan subperintah `import` |
| `internal/billing/` | Logika bisnis billing |
| `internal/db/` | Kode `sqlc`, migrator, `migrations/`, `queries/`, `migrations.sum` |
| `internal/device/` | Driver MikroTik, `Radius`, `Dummy` |
| `internal/radius/` | Server RADIUS dan CoA |
| `internal/web/` | Handler HTTP dan middleware |
| `internal/payment/` | Gateway pembayaran (Tripay) |
| `internal/notify/` | Telegram, WA/SMS, email, webhook |
| `internal/job/` | Job terjadwal dan clock guard |
| `internal/importer/` | Impor dari PHPNuxBill |
| `internal/secret/` | AES-GCM dan kunci |
| `internal/i18n/`, `lang/` | Terjemahan |
| `web/` | `templates/`, `static/` (CSS, JS, ikon), `tailwind.css` |
| `deploy/` | `install.sh` dan unit systemd |
| `tools/tailwind.sh` | Build CSS |
| `docs/` | Dokumentasi ([indeks](../README.md)) |

## Build dan test

    make build     # CGO_ENABLED=0, versi dari git describe -> ./gobill
    make test      # go vet ./... && go test ./...
    make css       # build web/static/app.css lewat Tailwind standalone

Test dengan race detector (wajib untuk perubahan billing, radius, job):

    go test -race ./internal/billing/... ./internal/radius/... ./internal/job/...

Test importer memakai `GOBILL_TEST_MYSQL_DSN` dan `GOBILL_TEST_PHP_SQL`; tanpa keduanya test itu dilewati. Dump produksi tidak boleh di-commit.

## sqlc

Ubah SQL di `internal/db/queries` atau `internal/db/migrations`, lalu dari root repo:

    sqlc generate

Jangan edit file hasil generate secara manual.

## Tailwind dan aset

Tailwind v4 standalone CLI (tanpa Node): `make css`. `web/static/app.css` hasil build ikut di-commit dan CI memeriksa hasilnya sama. Alpine.js, Chart.js, dan Leaflet adalah file statis yang di-embed.

Menambah ikon: salin SVG dari `lucide-static@0.460.0/icons` ke `web/static/icons/`, lalu pakai `{{icon "nama"}}`. Nama yang tidak ada membuat server gagal start.

## Migration freeze

Sejak v0.1.0, file di `internal/db/migrations/` tidak boleh diubah. Schema baru masuk file bernomor berikutnya, dan hash-nya ditambahkan ke `internal/db/migrations.sum`. `TestMigrationsFrozen` menggagalkan build bila file rilis berubah, hilang, atau belum tercatat.

## Versi dan rilis

Versi mengikuti SemVer; selama 0.x, perubahan yang memutus kompatibilitas bisa masuk di versi minor. Catat perubahan di [CHANGELOG.md](../../CHANGELOG.md) (bagian `[Unreleased]`, lalu pindahkan saat rilis).

    git tag -a v0.1.1 -m "v0.1.1"
    git push --tags

CI (`.github/workflows/ci.yml`) menjalankan cek `app.css`, `go vet`, dan `go test`, lalu untuk tag `v*` membangun binary `linux` amd64, arm64, dan armv7 dengan `sha256sums.txt` dan menerbitkannya di [Releases](https://github.com/frand-kod/gobill/releases). Build lokal memasang `git describe` ke `--version`.

## Konvensi kode

Dari [rencana/keputusan-stack.md](rencana/keputusan-stack.md):

- Handler HTTP adalah fungsi biasa `func(w http.ResponseWriter, r *http.Request)`; routing pakai `net/http` stdlib.
- Tanpa DI container, ORM, atau reflection; satu-satunya code generation adalah `sqlc`.
- Satu package per domain. Interface hanya bila ada lebih dari satu implementasi (`Device`, `PaymentGateway`).
- Error dikembalikan eksplisit; tanpa panic untuk alur normal.
- Dependensi baru hanya jika stdlib dan daftar library di dokumen keputusan tidak cukup; catat alasannya di sana.
- Pintasan sengaja ditandai komentar `ponytail:` (cari dengan `grep -rn "ponytail:" --include=*.go .`).
- Uang selalu INTEGER rupiah; perubahan saldo atomik dalam satu transaksi; router dihubungi setelah commit.
- Jangan commit rahasia atau dump database; pakai placeholder `<SECRET>` di dokumen dan test.

## Aturan efisiensi

Aturan ini berlaku untuk setiap fitur baru. Target: RSS saat idle di STB di bawah 100 MB.

- Setiap query berat punya indeks. Cek dengan `EXPLAIN QUERY PLAN`.
- Setiap daftar dipaginasi.
- Setiap map, cache, atau ring di memori punya batas ukuran keras.
- Data lama punya retensi. Atur lewat `log_keep_days`, lihat [konfigurasi](../id/configuration.md).
- Jangan polling lebih sering dari perlu. Polling router atau NAS minimal 1 menit. Lebih baik pakai push atau event.
- Pengaturan yang dibaca di setiap request tidak boleh memicu query DB per request. Simpan di cache, dan muat ulang saat disimpan.
- Job latar belakang punya timeout.
- Ukur RSS sebelum dan sesudah fitur besar.

## Lihat juga

- [arsitektur](arsitektur.md)
- [panduan-menulis-docs](panduan-menulis-docs.md)
- [CONTRIBUTING](../../CONTRIBUTING.md)
- [configuration](../id/configuration.md)
- [progres](progres.md)
