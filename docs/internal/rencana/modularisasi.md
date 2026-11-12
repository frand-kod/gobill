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

Status: selesai.

- Perbaiki `.dockerignore` agar semua docs yang di-embed ikut. Pakai `!docs/id/*.md` dan `!docs/en/*.md`.
- Keluarkan binary `nuxbill` dari git. Tambahkan `/nuxbill` ke `.gitignore` dan `.dockerignore`.
- Tambahkan target `make run` dengan `GOBILL_DB=dev/dev.db`, supaya DB, key, dan password tidak lagi tercecer di root.
- Bersihkan worktree agent lama di `.claude/worktrees` setelah perubahannya dicek.

Selesai jika `docker build` lulus dan `git ls-files` tidak berisi binary.

### M1. Helper bersama

Status: sebagian. `money` dan `clock` selesai, `device` dan `wg` tidak lagi meng-import `radius`. `LimitBytes` dan `NormMAC` masih di `radius`, belum jadi `internal/quota`.

- `internal/money`: satu fungsi format uang. Ganti `notify.Money`, `web.money`, dan `web.moneyFormat`.
- `internal/clock`: satu sumber waktu dan zona. Ganti `billing.now/Location`, `radius.now/loc`, dan `metrics.Now`.
- `internal/quota`: pindahkan `LimitBytes`, `LimitSeconds`, dan `NormMAC` dari `radius`. Setelah itu `device` dan `wg` tidak lagi meng-import `radius`.
- Helper `s.t(r, key)` untuk i18n. Ganti closure `t` yang disalin di sekitar 10 handler.

Selesai jika `go list` tidak menunjukkan `device → radius` dan test lulus.

### M2. Settings store

Status: selesai.

- Buat `internal/settings` dengan `Get`, `All`, cache, dan invalidasi saat simpan.
- Ganti semua pembacaan setting langsung: `billing/service.go:96`, `billing/health.go:69`, `billing/isolir.go:53`, `billing/routers.go:43`, `isolir/isolir.go:107`, `radius.go:477-495`, `notify.Load`, dan `web.loadSettings`.
- Pindahkan validasi setting (`settingsErrors`, 166 baris) dari `web` ke `settings.Validate`.

Selesai jika tidak ada lagi `GetSetting` atau `ListSettings` di luar `internal/settings`.

### M3. Isolir punya satu pemilik

Status: selesai. Aturannya jadi fungsi paket `internal/isolir`, belum tipe `Service`.

- Tulis test untuk `internal/isolir` lebih dulu. Paket ini sekarang belum punya test.
- Satukan aturan ke `isolir.Service`: `IsIsolated`, `Isolate`, `End`, `PlanFor`.
- `billing/isolir.go`, `radius.isolirPlanFor`, dan `web/isolir.go:139-174` memanggil service itu.
- Router rules dan `lanIPv4` dari `web/isolir.go:383-457` pindah ke `internal/isolir`.

Selesai jika lookup `isolir_plan_id` hanya ada di satu tempat.

#### Isolir: diselesaikan di fix/isolir-rules

Perbedaan perilaku yang dulu ditandai `// M3: kept as-is` sudah diseragamkan. Aturan barunya:

1. "Terisolir" punya satu arti: `isolir_enabled=yes` dan ada isolasi berjalan (`ended_at=0` dan `ends_at > now`). Fungsinya `isolir.Active`, dipakai RADIUS dan gerbang web. Plan id tidak ikut menentukan. RADIUS tetap butuh plan id untuk memilih profil. Gerbang web memakai clock server yang bisa diganti di test.
2. `isolir.Isolate` mengakhiri isolasi terbuka milik customer yang sudah lewat `ends_at` dengan alasan `expired`, di transaksi yang sama, sebelum membuat isolasi baru. Jadi hanya ada satu baris terbuka.
3. `EnsurePlan` dipanggil setelah cek langganan aktif dan cek sudah terisolir. Isolasi yang dilewati tidak membuat plan.
4. Nama address-list dari `isolir.AddressList`: isi setting, atau `isolir` jika kosong. RADIUS dan rule RouterOS memakai fungsi yang sama.
5. Simpan setting Isolir memberi peringatan (bukan error) jika `isolir_plan_id` menunjuk plan yang dijual atau bukan plan RADIUS. Saat berjalan, plan itu tetap diterima.
6. `isolir.Days`: negatif atau tidak valid jadi 30 hari, `0` tetap sampai bayar. `ValidHost` menolak label yang diawali atau diakhiri `-`. IPv4 mentah tetap boleh di allow-list, tetapi hanya masuk address-list, tanpa rule `tls-host`.

### M4. Logika keluar dari handler

Status: selesai. Service ada untuk provision, customer, plan, restore, settings, diagnose, promo, setup, dan admin.

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

Status: sebagian. `Notifier` dan `UsageRecorder` selesai. Dispatcher efek perangkat dan notifikasi belum ada.

- `billing` bergantung pada interface `Notifier`, bukan `*notify.Notifier` konkret.
- Interface `Gateway` per provider payment, kalau belum ada.
- Efek ke perangkat dan notifikasi dikumpulkan di satu dispatcher. Transaksi DB tidak lagi bercampur dengan panggilan jaringan.
- `radius/acctbatch.go` tidak lagi menulis usage subscription langsung. Pakai fungsi milik billing.

### M6. Pecah file besar

Status: sebagian. `radius`, `notify`, `importer`, dan query radius selesai. `billing/health.go` belum pindah ke `internal/health`.

Ini hanya memindahkan isi file. Setelah M4 isinya sudah jauh lebih kecil.

- `radius/radius.go`: `nas.go`, `server.go`, `auth.go`, `ratelimit.go`, `password.go`, `acct.go`, `policy.go`.
- `notify/notify.go`: `vars.go`, `state.go`, `render.go`, `events.go`, dan satu file per kanal.
- `importer/importer.go`: `tables.go`, `devices.go`, `settings.go`, `run.go`.
- `billing/health.go` pindah ke `internal/health`.
- `db/queries/radius.sql` dipecah menjadi `nas.sql`, `radius_sessions.sql`, dan `radius_reports.sql`. Query billing pindah ke file billing. Generate ulang hanya file yang disentuh.
- Hapus query mati setelah dicek di test: `ListSettingsRaw`, `GetTransactionByInvoice`, `ListTransactions`, `SumRadiusSessionTime`, `SumRadiusUsage`.

### M7. Wiring di `cmd`

Status: sebagian. `config`, `app`, `GOBILL_JOBS`, dan `selfupdate` dengan `io.LimitReader` selesai. `web.New(Deps{...})` belum, field masih diisi setelah konstruksi.

- `internal/config`: semua `GOBILL_*` dibaca sekali ke satu struct.
- `internal/app`: `Open(cfg)` untuk DB, migrasi, dan key. `Jobs(ctx, svc)` berisi daftar job.
- Flag `GOBILL_JOBS=0` untuk mematikan job per proses. Ini syarat sebelum ada dua proses.
- `web.New(Deps{...})`. Field tidak lagi diisi setelah konstruksi.
- Self-updater pindah ke `internal/selfupdate`. Tambahkan `io.LimitReader` pada unduhan.

### M8. Pecah `internal/web`

Status: sebagian. `httpx`, `view`, `core`, `portal`, `public`, `setup`, `api`, dan `admin/*` sudah jadi paket, dan tiap paket mendaftarkan route-nya sendiri. Sisa di root: login dan sesi, middleware, `docs`, `health`, render dan decorate, serta `Server` yang merakit semuanya. `Server` belum dipecah jadi `Deps` per paket.

- Ekstrak `httpx`, `middleware`, dan `view` sebagai paket dengan helper exported.
- Pecah `Server` jadi `Deps` per sub-paket: `admin`, `portal`, `setup`, `public`.
- Pecah `routes.go` (`Handler()` 250 baris). Setiap sub-paket mendaftarkan route-nya sendiri.
- Var form global (`bwNames`, `cfNames`, `formTokens`, dan lainnya) pindah ke sub-paket pemiliknya.
- Test HTTP ikut pindah. Test logika sudah pindah ke service di M4.

### M9. API

Status: sebagian. `web/api` dengan token bearer dan DTO sudah ada, dengan endpoint baca di `/api/v1`. Belum ada: DTO Transaction, endpoint katalog `lang`, dan pemindahan halaman yang JS-nya memakai `fetch`.

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

## Peta modul

Peta ini dibuat dari import nyata (`go list`) pada 2026-11-13. Panah berarti "meng-import". Aturan yang dijaga test ada di `internal/arch_test.go`.

```
cmd/gobill ─► app ─► config, selfupdate, job
                └──► web (merakit Server dan route)

web ─► web/portal  web/public  web/setup  web/api  web/admin/{customers,plans,billing,network,reports,settings,apiaccess}
          │           │           │          │            │
          └───────────┴─────┬─────┴──────────┘            ▼
                            ▼                       web/admin/adminx
                web/core ─► web/view, web/httpx            │
                            │                              │
                            ▼                              ▼
                service/{customer,plan,provision,diagnose,restore,settingsave,promo,setup,admin,apitoken}
                            │
                            ▼
        domain: billing  radius  isolir  notify  payment  device  wg  importer  job
                            │
                            ▼
                     settings ─► db ─► secret
                            │
                            ▼
              kernel: money  clock  quota  qris  metrics  i18n   (tanpa import internal)

web/api ─► api/dto ─► db      (DTO tidak membawa field rahasia)
```

Batas yang sudah dijaga test:
- Paket domain dan `service` tidak meng-import `web`.
- `service` tidak meng-import `radius`. `settings` tidak meng-import `web`, `payment`, `radius`, dan `billing`.
- `device` dan `wg` tidak meng-import `radius`. `radius` tidak meng-import `billing` dan `notify`.
- `isolir` tidak meng-import `billing`, `radius`, dan `web`.
- `payment`, `money`, `clock`, `secret`, `quota`, dan `qris` tidak meng-import paket internal.
- Sub-paket `web` tidak meng-import paket `web` induk. `portal`, `public`, dan `api` juga tidak meng-import paket admin. `setup`, `admin/reports`, dan `admin/settings` boleh memakai paket admin lain.

Sisa kopling yang terlihat di peta:
- Helper murni ada di `internal/quota` (jendela login, normalisasi MAC) dan `internal/qris` (cek dan baca QRIS). `radius` memakai `quota`. `settings` dan `web` memakai `qris`.
- `diagnose` tidak lagi meng-import `radius`. Batas sesi basi (`radius.StaleFor`) diberikan oleh pemanggil lewat field `StaleFor`.
- `web/admin/adminx` dan `web/core` masih meng-import banyak paket domain. Ini wajar untuk perakit, tapi bisa dikurangi lewat `Deps` per paket.

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
