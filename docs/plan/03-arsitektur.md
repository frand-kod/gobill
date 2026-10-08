# 03 — Arsitektur Aplikasi Baru

## Gambaran proses

Satu binary `nuxbill` menjalankan semua komponen dalam satu proses:

```
                    ┌──────────────────── nuxbill (1 proses) ────────────────────┐
 Browser admin  ──► │ HTTP :8080  (admin, portal pelanggan, callback payment)    │
 Pelanggan      ──► │                                                            │
 MikroTik NAS   ──► │ RADIUS :1812 auth / :1813 accounting                       │
                    │ Job: expiry (1 menit), reminder (harian), backup (harian)  │
                    │                    │                                       │
                    │                    ▼                                       │
                    │              SQLite (file)                                 │
                    └──────────────┬─────────────────────────────────────────────┘
                                   ▼
                     MikroTik API :8728 (driver Hotspot/PPPoE)
                     Telegram / WA gateway / SMTP / payment gateway
```

## Struktur repo

Struktur sengaja dibuat flat. Package baru hanya ditambah jika domainnya memang berbeda.

```
cmd/nuxbill/main.go        # baca config, buka DB, migrasi, start HTTP + RADIUS + job
internal/
  db/                      # kode hasil sqlc + migrator (user_version)
  billing/                 # kalkulasi masa aktif, aktivasi, perpanjangan, saldo, voucher
  device/                  # interface Device + dummy, mikrotik_hotspot, mikrotik_pppoe
  radius/                  # server RADIUS (auth + accounting)
  payment/                 # interface PaymentGateway + implementasi per gateway
  notify/                  # telegram, wa/sms (URL gateway), email, webhook keluar
  job/                     # loop ticker: expiry, reminder, backup, clock guard
  web/                     # handler HTTP, middleware auth/role, render template
  i18n/                    # load JSON bahasa
db/
  migrations/0001_init.sql ...
  queries/*.sql            # input sqlc
web/
  templates/**/*.html
  static/                  # css, htmx.min.js, gambar
lang/*.json                # disalin dari system/lan/*.json lama
```

## Interface utama

Hanya dua interface ini yang dibuat, karena masing-masing punya lebih dari satu implementasi.

### Device

Port dari kontrak `system/devices/readme.md`. Nama method disesuaikan dengan konvensi Go, dan setiap method mengembalikan `error` secara eksplisit.

```go
type Device interface {
    AddCustomer(ctx context.Context, c Customer, p Plan) error
    RemoveCustomer(ctx context.Context, c Customer, p Plan) error
    ChangeUsername(ctx context.Context, p Plan, from, to string) error
    AddPlan(ctx context.Context, p Plan) error
    UpdatePlan(ctx context.Context, oldName string, p Plan) error
    RemovePlan(ctx context.Context, p Plan) error
    IsOnline(ctx context.Context, c Customer, router string) (bool, error)
    Connect(ctx context.Context, c Customer, ip, mac, router string) error
    Disconnect(ctx context.Context, c Customer, router string) error
}
```

Implementasi awal: `Dummy`, `MikrotikHotspot`, `MikrotikPPPoE`. Mode RADIUS tidak butuh driver tulis karena data dibaca langsung dari DB oleh server RADIUS built-in.

### PaymentGateway

Port dari konvensi fungsi `{gw}_*` di `system/controllers/order.php` dan `callback.php`.

```go
type PaymentGateway interface {
    Name() string
    ValidateConfig(cfg map[string]string) error
    CreateTransaction(ctx context.Context, trx Transaction, c Customer) (payURL string, err error)
    Status(ctx context.Context, trx Transaction) (Status, error)
    HandleCallback(r *http.Request) (trxID string, st Status, err error) // wajib verifikasi signature
}
```

Gateway didaftarkan di sebuah map pada `main.go`. Tidak ada plugin runtime.

## Desain schema (perubahan penting dari schema lama)

| Hal | Lama | Baru |
|---|---|---|
| Uang | `varchar` / `decimal` | `INTEGER` dalam satuan terkecil (rupiah) |
| Waktu expiry | `expiration` date + `time` time | `expires_at INTEGER` (Unix detik, UTC). Konversi ke zona waktu hanya saat ditampilkan |
| Password admin | `sha1` tanpa salt | `bcrypt` |
| Password portal pelanggan | plaintext | `bcrypt` |
| Secret PPPoE/hotspot | plaintext | AES-GCM. Kunci ada di file config, bukan di DB |
| Status | string bebas (`'on'`, `'off'`) | `TEXT` + `CHECK (status IN (...))` |
| Relasi | tanpa foreign key | `FOREIGN KEY` + `PRAGMA foreign_keys=ON` |
| Pengaturan | `tbl_appconfig` key-value | Tetap key-value (`settings`). Bentuk ini sudah sederhana |
| Migrasi | `ALTER` di JSON | File `.sql` bernomor, dijalankan otomatis saat start, dan dicatat di `PRAGMA user_version` |

Password admin lama (sha1) tidak bisa dikonversi langsung ke bcrypt. Solusinya: saat login pertama, verifikasi dengan sha1 sekali, lalu langsung re-hash ke bcrypt. Kolom penanda `legacy_sha1` dihapus setelah semua admin pernah login.

## Keamanan (default, tidak opsional)

- Semua query lewat `sqlc` dengan parameter terikat. SQL yang disusun dengan string concat dilarang.
- CSRF via `http.CrossOriginProtection` untuk semua route non-GET.
- Cookie session: `HttpOnly`, `SameSite=Lax`, dan `Secure` jika HTTPS.
- Role (SuperAdmin, Admin, Report, Agent, Sales) dicek di middleware per route, bukan di dalam handler.
- Callback payment gateway wajib memverifikasi signature. Jika gagal, kembalikan 4xx dan catat di log.
- API token disimpan sebagai hash, punya masa berlaku, dan bisa di-revoke.
- RADIUS shared secret disimpan per NAS.

## Job terjadwal

Semua job berjalan sebagai goroutine dengan `time.Ticker` di dalam proses yang sama.

| Job | Interval | Isi |
|---|---|---|
| `clockGuard` | 1 menit | Simpan `last_seen_at`, lalu tandai jam valid atau tidak (lihat aturan STB) |
| `expiry` | 1 menit | Nonaktifkan langganan yang `expires_at <= now`, panggil `Device.RemoveCustomer`, kirim notifikasi. Perpanjang otomatis jika saldo cukup |
| `reminder` | Harian, jam bisa diatur | Kirim pengingat H-3, H-1, dan H-0 (meniru `system/cron_reminder.php`) |
| `backup` | Harian | `VACUUM INTO 'backup/nuxbill-YYYYMMDD.db'`, simpan N file terakhir |

Setiap job yang mengubah data memakai transaksi DB. Job expiry harus idempoten: menjalankannya dua kali menghasilkan data yang sama.

## Aturan khusus STB

Aturan di bagian ini menjaga kebenaran data billing. **Jangan disederhanakan.**

1. **Jam sistem tidak bisa dipercaya setelah reboot.** STB umumnya tidak punya RTC. Saat boot, jam bisa mundur ke 1970 atau ke waktu build image sampai NTP tersinkron.
   - Job `expiry` **menolak berjalan** jika `now < last_seen_at` di DB, atau jika NTP belum sinkron (cek `adjtimex`/`timedatectl`). Jika ditolak, catat warning di log dan tampilkan banner di dashboard admin.
   - Tanpa guard ini, jam yang melompat maju bisa meng-expire semua pelanggan sekaligus. Jam yang mundur bisa membuat masa aktif terlihat bertambah.
   - Guard bisa dimatikan lewat setting `clock_guard=off` jika perangkat punya RTC.
2. **Listrik bisa padam kapan saja.** SQLite memakai `PRAGMA journal_mode=WAL` dan `PRAGMA synchronous=FULL`. Mode FULL lebih lambat, tetapi transaksi yang sudah commit tidak hilang.
3. **eMMC cepat aus.** Log ditulis ke stdout (journald). Accounting RADIUS interim hanya meng-update baris sesi. Riwayat per paket tidak disimpan.
4. **Backup ke luar perangkat.** Folder backup bisa diarahkan ke USB atau mount jaringan. Dokumentasi instalasi wajib menyertakan langkah ini.
5. **Build:** `GOOS=linux GOARCH=arm64` (S905X dan sejenisnya) dan `GOARCH=arm GOARM=7` (perangkat 32-bit), dengan `CGO_ENABLED=0`.

## Konfigurasi

File `/etc/nuxbill/config.env` hanya berisi hal yang dibutuhkan sebelum DB terbuka:

```
NUXBILL_DB=/var/lib/nuxbill/nuxbill.db
NUXBILL_HTTP=:8080
NUXBILL_RADIUS=:1812
NUXBILL_SECRET_KEY=<32 byte hex, untuk AES-GCM & session>
```

Pengaturan lain (nama usaha, mata uang, notifikasi, gateway) disimpan di tabel `settings` dan diubah lewat UI.

## i18n

Teks memakai ulang `system/lan/*.json` lama dengan format key → teks yang sama. Di template dipanggil lewat fungsi `{{ T "Customer" }}`. Bahasa default diatur di settings.
