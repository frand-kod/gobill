# Panduan Developer

Untuk developer yang ingin membangun, mengubah, atau merilis NuxBill. Gambaran sistem ada di [arsitektur.md](arsitektur.md).

## Layout repo

| Path | Isi |
|---|---|
| `cmd/nuxbill/` | Entry point dan subperintah `import` |
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
| `docs/` | Dokumentasi ([indeks](README.md)) |

## Build dan test

    make build     # CGO_ENABLED=0, versi dari git describe -> ./nuxbill
    make test      # go vet ./... && go test ./...
    make css       # build web/static/app.css lewat Tailwind standalone

Test dengan race detector (wajib untuk perubahan billing, radius, job):

    go test -race ./internal/billing/... ./internal/radius/... ./internal/job/...

Test importer memakai `NUXBILL_TEST_MYSQL_DSN` dan `NUXBILL_TEST_PHP_SQL`; tanpa keduanya test itu dilewati. Dump produksi tidak boleh di-commit.

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

Versi mengikuti SemVer; selama 0.x, perubahan yang memutus kompatibilitas bisa masuk di versi minor. Catat perubahan di [CHANGELOG.md](../CHANGELOG.md) (bagian `[Unreleased]`, lalu pindahkan saat rilis).

    git tag -a v0.1.1 -m "v0.1.1"
    git push --tags

CI (`.github/workflows/ci.yml`) menjalankan cek `app.css`, `go vet`, dan `go test`, lalu untuk tag `v*` membangun binary `linux` amd64, arm64, dan armv7 dengan `sha256sums.txt` dan menerbitkannya di [Releases](https://github.com/frand-kod/gobill/releases). Build lokal memasang `git describe` ke `--version`.

## Konvensi kode

Dari [plan/02-keputusan-stack.md](plan/02-keputusan-stack.md):

- Handler HTTP adalah fungsi biasa `func(w http.ResponseWriter, r *http.Request)`; routing pakai `net/http` stdlib.
- Tanpa DI container, ORM, atau reflection; satu-satunya code generation adalah `sqlc`.
- Satu package per domain. Interface hanya bila ada lebih dari satu implementasi (`Device`, `PaymentGateway`).
- Error dikembalikan eksplisit; tanpa panic untuk alur normal.
- Dependensi baru hanya jika stdlib dan daftar library di dokumen keputusan tidak cukup; catat alasannya di sana.
- Pintasan sengaja ditandai komentar `ponytail:` (cari dengan `grep -rn "ponytail:" --include=*.go .`).
- Uang selalu INTEGER rupiah; perubahan saldo atomik dalam satu transaksi; router dihubungi setelah commit.
- Jangan commit rahasia atau dump database; pakai placeholder `<SECRET>` di dokumen dan test.
