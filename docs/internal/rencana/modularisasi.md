# Rencana modularisasi

Rencana ini memecah gobill menjadi modul yang jelas batasnya. Tujuan akhirnya adalah server API terpisah dan pemisahan backend dan frontend. Rencana ini disusun dari audit struktur pada 2026-11-12.

**Untuk:** pengembangan. Hanya tersedia dalam bahasa Indonesia.

## Prinsip

- Satu tahap = satu PR kecil. Setiap PR menghasilkan binary yang tetap jalan.
- Perilaku tidak berubah. Refactor dan perbaikan bug tidak dicampur, kecuali disebut di bawah.
- Logika turun dulu, baru paket dipecah. Memecah `internal/web` sebelum logika bisnis keluar hanya memindahkan masalah.
- Test ditulis sebelum logika dipindah, terutama untuk paket yang belum punya test.
- Tetap satu binary, satu SQLite, satu urutan migrasi. Pemisahan proses baru di tahap akhir.

## Kondisi sekarang

- `internal/web` berisi sekitar 22 ribu baris dan 91 file. `Server` punya 45 field. Semua handler adalah method `*Server`.
- 63 file di `web` meng-import `internal/db` langsung. Billing hanya dipakai untuk recharge dan sync.
- Ada raw SQL dan `BeginTx` di handler: `admin_customers.go`, `admin_settings.go`, `admin_wireguard.go`, `promo.go`, `crud.go`, `auth.go`, `admin_plans.go`.
- Aturan isolir ada di 4 tempat. Setting dibaca lewat 7 jalur. Format uang ada 3.
- Model sqlc dipakai sebagai model domain di semua paket.
- `cmd/gobill/main.go` mengerjakan config, migrasi, 14 job, RADIUS, dan HTTP sekaligus.
- Baru 4 dari sekitar 229 route yang mengembalikan JSON.

## Layout target

```
internal/
  config/      Config dari env, dibaca sekali
  app/         bootstrap (DB, migrasi, key) dan daftar job
  settings/    Store dengan cache, satu-satunya jalur baca setting
  money/ clock/ quota/   helper bersama
  service/     customer, plan, settings, promo, setup, diagnose, restore, provision
  billing/ isolir/ radius/ device/ wg/ notify/ payment/ job/ importer/
  db/          sqlc, query dipecah per modul
  web/
    httpx/       fail, formVals, pathID, flash, field builder
    middleware/  auth, role, isolirGate, ratelimit
    view/        parseTemplates, render, brand, i18n helper
    admin/ portal/ setup/ public/
    api/         /api/v1 di atas service yang sama
```

Aturan dependensi:
- `web/*` hanya memanggil `service/*` dan paket domain. Tidak ada `db.Queries` atau SQL di `web`.
- Paket domain tidak meng-import `web`. Kondisi ini sudah terpenuhi dan harus dijaga.
- `device` dan `wg` tidak meng-import `radius`.
- `job` tidak tahu soal `update`. Job didaftarkan dari `app`.

## Tahap

### M0. Kebersihan repo

- Perbaiki `.dockerignore` agar semua docs yang di-embed ikut. Pakai `!docs/id/*.md` dan `!docs/en/*.md`.
- Keluarkan binary `nuxbill` dari git. Tambahkan `/nuxbill` ke `.gitignore` dan `.dockerignore`.
- Tambahkan target `make run` dengan `GOBILL_DB=dev/dev.db`, supaya DB, key, dan password tidak lagi tercecer di root.
- Bersihkan worktree agent lama di `.claude/worktrees` setelah perubahannya dicek.

Selesai jika `docker build` lulus dan `git ls-files` tidak berisi binary.

### M1. Helper bersama

- `internal/money`: satu fungsi format uang. Ganti `notify.Money`, `web.money`, dan `web.moneyFormat`.
- `internal/clock`: satu sumber waktu dan zona. Ganti `billing.now/Location`, `radius.now/loc`, dan `metrics.Now`.
- `internal/quota`: pindahkan `LimitBytes`, `LimitSeconds`, dan `NormMAC` dari `radius`. Setelah itu `device` dan `wg` tidak lagi meng-import `radius`.
- Helper `s.t(r, key)` untuk i18n. Ganti closure `t` yang disalin di sekitar 10 handler.

Selesai jika `go list` tidak menunjukkan `device → radius` dan test lulus.

### M2. Settings store

- Buat `internal/settings` dengan `Get`, `All`, cache, dan invalidasi saat simpan.
- Ganti semua pembacaan setting langsung: `billing/service.go:96`, `billing/health.go:69`, `billing/isolir.go:53`, `billing/routers.go:43`, `isolir/isolir.go:107`, `radius.go:477-495`, `notify.Load`, dan `web.loadSettings`.
- Pindahkan validasi setting (`settingsErrors`, 166 baris) dari `web` ke `settings.Validate`.

Selesai jika tidak ada lagi `GetSetting` atau `ListSettings` di luar `internal/settings`.

### M3. Isolir punya satu pemilik

- Tulis test untuk `internal/isolir` lebih dulu. Paket ini sekarang belum punya test.
- Satukan aturan ke `isolir.Service`: `IsIsolated`, `Isolate`, `End`, `PlanFor`.
- `billing/isolir.go`, `radius.isolirPlanFor`, dan `web/isolir.go:139-174` memanggil service itu.
- Router rules dan `lanIPv4` dari `web/isolir.go:383-457` pindah ke `internal/isolir`.

Selesai jika lookup `isolir_plan_id` hanya ada di satu tempat.

#### Isolir: perilaku yang belum seragam

M3 memindahkan aturan ke `internal/isolir` tanpa mengubah perilaku. Perbedaan di bawah sengaja dibiarkan dan ditandai `// M3: kept as-is` di kode. Test karakterisasi mengunci semuanya.

1. "Terisolir" punya tiga arti. RADIUS (`isolir.PlanFor`) butuh `isolir_enabled=yes`, plan id tidak 0, dan isolasi berjalan. Gerbang web hanya cek flag atomic dan isolasi berjalan. Web memakai `time.Now()`, billing dan RADIUS memakai clock yang bisa diganti.
2. "Berjalan" punya dua arti. `GetRunningIsolation` butuh `ends_at > now`. `ListRunningIsolations` dan `EndCustomerIsolations` hanya butuh `ended_at = 0`.
3. `isolir.Isolate` memanggil `EnsurePlan` sebelum cek langganan aktif dan isolasi yang sudah ada. Plan bisa terbuat walau isolasi dilewati.
4. `isolir_address_list` kosong: `Value` memberi "" tetapi `AddressListFor` memberi `isolir`. Rule RouterOS bisa berisi `src-address-list=` kosong, sementara RADIUS mengirim `isolir`.
5. `EnsurePlan` menerima plan id apa saja yang tersimpan, walau bukan plan isolir.
6. `Days("-3")` memberi 0 (sampai bayar). `ValidHost` menerima `-x.com` dan IP mentah.

### M4. Logika keluar dari handler

Urutan dari yang paling berisiko:

1. `service/provision`: `routerWGProvision` dan `script()` dari `admin_wireguard.go`. **Tahap ini sekaligus menambah `rosQuote`** untuk semua nilai di script RouterOS. Lihat temuan keamanan #1.
2. `service/customer`: `custSave` (233 baris), termasuk cek bentrok login, hash, seal secret, dan tx.
3. `service/plan`: `planSave` (182 baris), termasuk raw SQL hitung subscription aktif di `admin_plans.go:449`.
4. `service/restore`: logika restore dari `admin_restore.go:129-191`.
5. `service/settings`: `settingsSave` beserta efek setelah simpan.
6. `service/diagnose`: `diagnose` (189 baris), tanpa HTTP.
7. `service/promo` dan `service/setup`. Langkah wizard memanggil service yang sama dengan halaman admin, jadi duplikasinya hilang.
8. Auth: rehash `legacy_sha1` dari `auth.go:268,276` pindah ke `service/admin` atau query sqlc.

Raw SQL yang tersisa pindah ke query sqlc. Contohnya COUNT `radius_sessions` yang ditulis 3 kali di `metrics.go:82`, `status.go:164`, `setup_wizard.go:350`.

Selesai jika `grep -n 'BeginTx\|ExecContext\|QueryContext' internal/web` hanya menyisakan infra (backup, health, upload).

### M5. Interface di batas domain

- `billing` bergantung pada interface `Notifier`, bukan `*notify.Notifier` konkret.
- Interface `Gateway` per provider payment, kalau belum ada.
- Efek ke perangkat dan notifikasi dikumpulkan di satu dispatcher. Transaksi DB tidak lagi bercampur dengan panggilan jaringan.
- `radius/acctbatch.go` tidak lagi menulis usage subscription langsung. Pakai fungsi milik billing.

### M6. Pecah file besar

Ini hanya memindahkan isi file. Setelah M4 isinya sudah jauh lebih kecil.

- `radius/radius.go`: `nas.go`, `server.go`, `auth.go`, `ratelimit.go`, `password.go`, `acct.go`, `policy.go`.
- `notify/notify.go`: `vars.go`, `state.go`, `render.go`, `events.go`, dan satu file per kanal.
- `importer/importer.go`: `tables.go`, `devices.go`, `settings.go`, `run.go`.
- `billing/health.go` pindah ke `internal/health`.
- `db/queries/radius.sql` dipecah menjadi `nas.sql`, `radius_sessions.sql`, dan `radius_reports.sql`. Query billing pindah ke file billing. Generate ulang hanya file yang disentuh.
- Hapus query mati setelah dicek di test: `ListSettingsRaw`, `GetTransactionByInvoice`, `ListTransactions`, `ActivePlanNameByLogin`, `SumRadiusSessionTime`, `SumRadiusUsage`.

### M7. Wiring di `cmd`

- `internal/config`: semua `GOBILL_*` dibaca sekali ke satu struct.
- `internal/app`: `Open(cfg)` untuk DB, migrasi, dan key. `Jobs(ctx, svc)` berisi daftar job.
- Flag `GOBILL_JOBS=0` untuk mematikan job per proses. Ini syarat sebelum ada dua proses.
- `web.New(Deps{...})`. Field tidak lagi diisi setelah konstruksi.
- Self-updater pindah ke `internal/selfupdate`. Tambahkan `io.LimitReader` pada unduhan.

### M8. Pecah `internal/web`

- Ekstrak `httpx`, `middleware`, dan `view` sebagai paket dengan helper exported.
- Pecah `Server` jadi `Deps` per sub-paket: `admin`, `portal`, `setup`, `public`.
- Pecah `routes.go` (`Handler()` 250 baris). Setiap sub-paket mendaftarkan route-nya sendiri.
- Var form global (`bwNames`, `cfNames`, `formTokens`, dan lainnya) pindah ke sub-paket pemiliknya.
- Test HTTP ikut pindah. Test logika sudah pindah ke service di M4.

### M9. API

- Paket `web/api` di bawah `/api/v1`. Mulai dari 4 endpoint JSON yang sudah ada: `status`, `search`, `radius/sessions`, `portal/usage`.
- DTO untuk Customer, Admin, NAS, Router, Plan, Subscription, Transaction. Model sqlc tidak pernah di-encode langsung, karena berisi `PasswordHash`, `SecretEnc`, dan `TotpSecretEnc`.
- Auth token untuk API, terpisah dari cookie dan CSRF.
- Endpoint katalog i18n: `GET /api/v1/lang/{code}`.
- Halaman yang JS-nya sudah memakai `fetch` dipindah lebih dulu: search, customer pick dan summary, sessions, usage, maps.

### M10. Pemisahan proses dan frontend

Belum dikerjakan sekarang. Syaratnya:
- State di memori dipindah ke DB atau dibuat per proses: `notify.chans`, `job.states`, limiter login, `formTokens`.
- Metrics per proses, atau diagregasi.
- Frontend terpisah memakai `/api/v1` saja.

## Urutan dan ketergantungan

```
M0 ─ M1 ─ M2 ─ M3 ─ M4 ─┬─ M5 ─ M6
                        └─ M7 ─ M8 ─ M9 ─ M10
```

M0 sampai M3 kecil dan aman. M4 adalah bagian terbesar dan paling bernilai. M6 boleh dicicil kapan saja setelah M4.

## Keamanan yang ikut dalam rencana

- `rosQuote` masuk di M4 langkah 1.
- `io.LimitReader` updater masuk di M7.
- Temuan lain (limiter cek-saja, DoS voucher per NAS, brute force password RADIUS, kupon tidak dikembalikan, enumerasi username, bump sesi 2FA) diperbaiki sebagai PR `fix/` terpisah, di luar rencana ini.

## Lihat juga

- [arsitektur-awal.md](arsitektur-awal.md)
- [roadmap.md](roadmap.md)
- [arsitektur](../arsitektur.md)
