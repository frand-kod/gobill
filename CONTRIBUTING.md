# Berkontribusi ke gobill

Kontribusi sangat diterima, baik berupa issue maupun pull request. Untuk gambaran sistem, baca [docs/internal/arsitektur.md](docs/internal/arsitektur.md). Untuk aturan kode dan rilis, baca [docs/internal/pengembangan.md](docs/internal/pengembangan.md); dokumen itu adalah sumber aturan, jadi tidak diulang di sini.

## Persiapan

Butuh Go sesuai `go.mod`. Untuk membangun CSS, `make css` mengunduh Tailwind standalone CLI yang dipatok versinya.

    make build     # binary ./gobill
    make test      # go vet ./... && go test ./...
    make css       # build web/static/app.css

Untuk perubahan billing, radius, atau job, jalankan juga test dengan race detector:

    go test -race ./internal/billing/... ./internal/radius/... ./internal/job/...

## Aturan kontribusi

- **Gaya kode**: ikuti [konvensi kode](docs/internal/pengembangan.md#konvensi-kode). Pintasan sengaja ditandai `ponytail:`.
- **Migration freeze**: file di `internal/db/migrations/` yang sudah rilis tidak boleh diubah. Perubahan schema hanya lewat file bernomor baru, dan hash-nya ditambahkan ke `internal/db/migrations.sum`.
- **sqlc**: jika mengubah SQL di `internal/db/queries` atau `internal/db/migrations`, jalankan `sqlc generate`. Jangan pernah mengedit kode hasil generate secara manual.
- **Pesan commit**: singkat, kalimat perintah dalam bahasa Inggris (imperative), sesuai riwayat yang ada, misalnya `Add RADIUS CoA retry`.
- **Data**: jangan pernah commit data pelanggan asli, dump SQL, atau rahasia. Pakai placeholder `<SECRET>`.
- **CHANGELOG**: catat perubahan di bagian `[Unreleased]` di [CHANGELOG.md](CHANGELOG.md).

## Sign-off (DCO)

Setiap commit harus di-sign-off dengan `git commit -s`. Sign-off menyatakan bahwa Anda menyetujui [Developer Certificate of Origin 1.1](https://developercertificate.org/). Baris `Signed-off-by` ditambahkan otomatis oleh `-s`.

## Lisensi

Dengan berkontribusi, Anda setuju bahwa kontribusi Anda dilisensikan di bawah GPL-3.0-or-later, sesuai [LICENSE](LICENSE) dan [NOTICE](NOTICE).

## Isu keamanan

Jangan laporkan kerentanan sebagai issue publik. Laporkan secara privat lewat [GitHub Security Advisories](https://github.com/frand-kod/gobill/security/advisories/new).
