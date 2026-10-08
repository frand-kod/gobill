# NuxBill Go

Penulisan ulang PHPNuxBill (billing hotspot/PPPoE MikroTik) dalam Go. Satu binary, database SQLite, tanpa PHP dan tanpa web server terpisah. Target utama: STB ARM dengan RAM 1-2 GB.

Status: fase F0 (login, dashboard kosong, pengaturan dasar).

## Menjalankan

    go run ./cmd/nuxbill

Buka http://localhost:8080.

## Variabel lingkungan

| Variabel | Bawaan | Fungsi |
|---|---|---|
| `NUXBILL_DB` | `./nuxbill.db` | Lokasi file SQLite |
| `NUXBILL_HTTP` | `:8080` | Alamat listen |
| `NUXBILL_HTTPS` | kosong | Isi `1` jika dilayani lewat HTTPS (cookie sesi diberi flag Secure) |

## Admin pertama

Saat database masih kosong, aplikasi membuat user `admin` (SuperAdmin) dengan kata sandi acak 16 karakter. Kata sandi itu dicetak satu kali di log (level WARN) saat start pertama. Catat, lalu ganti.

## Build untuk STB

    CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o nuxbill ./cmd/nuxbill
    CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -trimpath -ldflags="-s -w" -o nuxbill ./cmd/nuxbill

## Menambah ikon

Salin file SVG dari `lucide-static@0.460.0/icons` ke `web/static/icons/`, lalu pakai `{{icon "nama"}}` di template. Nama ikon yang tidak ada membuat server gagal start.

## Regenerasi sqlc

Ubah SQL di `internal/db/queries` atau `internal/db/migrations`, lalu dari root repo:

    sqlc generate

Jangan edit file hasil generate secara manual.

## Dokumen rencana

Lihat `../phpnuxbill/docs/plan/`.
