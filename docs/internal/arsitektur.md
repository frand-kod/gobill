# Arsitektur

Untuk developer dan operator teknis yang ingin memahami cara NuxBill bekerja. Dokumen ini menggambarkan kondisi kode saat ini. Rencana awal dan alasan keputusan ada di [rencana/](rencana/README.md), terutama [rencana/arsitektur-awal.md](rencana/arsitektur-awal.md) dan [rencana/keputusan-stack.md](rencana/keputusan-stack.md).

**Untuk:** pengembangan. Hanya tersedia dalam bahasa Indonesia.

## Gambaran proses

Satu binary `nuxbill` menjalankan semuanya dalam satu proses:

```
 Browser admin/pelanggan --HTTP :8080--+
 Callback Tripay ----------------------+
 FreeRADIUS rlm_rest ---/radius.php----+
                                       v
              +-------------------- nuxbill ----------------------+
              | internal/web   : admin, portal, callback, REST    |
              | internal/radius: UDP :1812 auth, :1813 acct, CoA  |
              | internal/job   : clock guard, expiry, reminder,   |
              |                  backup                           |
              | internal/billing -> internal/db (sqlc) -> SQLite  |
              +--------+----------------------+-------------------+
                       |                      |
               internal/device          internal/notify
               (RouterOS API, CoA)      internal/payment (Tripay)
```

## Paket

| Paket | Fungsi |
|---|---|
| `cmd/nuxbill` | Entry point: baca env, buka DB, migrasi, start HTTP + RADIUS + job; subperintah `import` |
| `internal/web` | Handler HTTP admin dan portal, middleware auth/role/CSRF, endpoint `/radius.php`, callback Tripay, `/health`, `/metrics`, halaman Status Sistem, 2FA admin, impor dan restore dari UI |
| `internal/billing` | Logika bisnis: masa aktif (termasuk `start_on_first_login`), recharge, saldo, voucher, kupon, sinkron paket/router, reminder, alert operator (`health.go`) |
| `internal/db` | Hasil `sqlc`, migrator (`PRAGMA user_version`), file migrasi (0001-0014), query, dan restore database (`restore.go`) |
| `internal/device` | Interface `Device` dan driver: MikroTik hotspot/PPPoE (API), `Radius`, `Dummy` |
| `internal/radius` | Server RADIUS (PAP, CHAP, MS-CHAPv2), accounting, CoA/Disconnect, Message-Authenticator, dedup |
| `internal/payment` | Interface `PaymentGateway`, implementasi Tripay, dan pembacaan QRIS statis (QR terkunci nominal) |
| `internal/notify` | Telegram, WA/SMS (URL gateway), email, webhook; redaksi error |
| `internal/job` | Loop ticker: clock guard, expiry, reminder, pembersihan log, router check, backup (dengan mirror), ringkasan harian, alert; deteksi jam dan disk |
| `internal/importer` | Impor PHPNuxBill ke SQLite, dari MySQL atau dari file backup JSON |
| `internal/metrics` | Registry counter dan gauge di memori, dan format teks Prometheus untuk `/metrics` |
| `internal/secret` | Enkripsi AES-GCM dan pemuatan kunci |
| `internal/i18n` | Pemuat terjemahan dari `lang/*.json` |
| `web/`, `assets.go` | Template, CSS, JS, ikon (di-embed ke binary) |

## Alur utama

**Recharge (admin atau portal).** Handler memanggil `billing`, yang membuka satu transaksi DB: hitung masa aktif baru (`validity.go`), ubah saldo secara atomik, tulis transaksi dan langganan. Setelah commit, `device` dipanggil untuk router (API: buat user/queue; mode `Radius`: tidak ada panggilan, data dibaca RADIUS dari DB). Notifikasi dikirim sesudahnya.

**Expiry.** Job `expiry` jalan tiap menit jika clock guard lolos. Langganan dengan `expires_at <= now` dinonaktifkan (perpanjang otomatis bila saldo cukup), perangkat dihubungi setelah commit, lalu notifikasi dikirim. Idempoten: dijalankan dua kali hasilnya sama.

**Auth RADIUS.** Access-Request dari NAS terdaftar (secret per NAS, Message-Authenticator diverifikasi) diteruskan ke logika keputusan: cari langganan aktif, cek pembatas percobaan, lalu jawab Accept dengan Session-Timeout dan rate-limit dari paket, atau Reject. Voucher dikenali bila username adalah kode voucher dan diaktifkan saat itu. Accounting memperbarui satu baris sesi. Request dari NAS tak terdaftar dibuang dan dicatat.

**RADIUS REST.** `POST /radius.php` dari FreeRADIUS memakai logika keputusan yang sama, dibatasi `radius_rest_allow` ([freeradius-rest.md](../id/freeradius-rest.md)).

**CoA/Disconnect.** Saat plan habis atau admin menekan Disconnect, `radius` mengirim Disconnect-Request ke port 3799 NAS dengan identitas NAS-IP-Address yang dilaporkan NAS itu. Balasan NAK didecode (Error-Cause) ke log.

**Recharge dengan `start_on_first_login`.** Untuk paket `Radius` yang baru atau habis, langganan ditandai `pending_start = 1` dan tidak punya tanggal mulai. Auth RADIUS pertama pelanggan memanggil `StartPending`, yang mengisi `started_at` dan `expires_at` dari saat itu.

**Impor dan restore.** Keduanya hanya bisa dijalankan SuperAdmin. Impor (CLI atau UI) membuat backup database di `NUXBILL_BACKUP_DIR` lebih dulu, lalu menimpa data dalam satu transaksi. Restore dari UI memvalidasi file (integritas, versi skema, kunci), menyimpan data saat ini sebagai backup `-pre-restore.db`, memasang file sebagai `nuxbill.db.restore`, lalu keluar dengan kode 3. Systemd menjalankan ulang aplikasi, dan file itu diterapkan sebelum database dibuka.

**Monitoring.** Metrik dicatat ke registry di memori. `/metrics` membacanya (dengan bearer token), `/admin/status` menampilkan ringkasannya, dan job `alert` mengevaluasi aturan alert setiap menit. Detail di [monitoring.md](../id/monitoring.md).

**Callback Tripay.** `POST /callback/tripay` memverifikasi signature, lalu menandai pembayaran dan mengaktifkan paket secara idempoten (callback ganda tidak menggandakan saldo).

## Model data

- Uang `INTEGER` rupiah. Waktu `INTEGER` Unix detik UTC, zona waktu hanya saat tampil.
- Status memakai `TEXT` + `CHECK`; foreign key aktif (`PRAGMA foreign_keys=ON`).
- Secret (router, pelanggan, NAS) terenkripsi AES-GCM; password bcrypt.
- Transaksi dan pembayaran online milik pelanggan yang dihapus tetap disimpan (`customer_id` menjadi NULL, `username` menyimpan nama saat transaksi).
- Pengaturan: tabel key-value `settings`.
- Indeks: pencarian login RADIUS (`username` atau `pppoe_username`), sesi RADIUS per user dan sesi terbuka, serta relasi langganan/voucher/transaksi memakai indeks parsial bila cocok. Query login diuji dengan `EXPLAIN QUERY PLAN` (`internal/db/indexes_test.go`).
- Retensi harian (`log_keep_days`, default 90 hari bila belum diisi): log, sesi RADIUS tertutup, pesan inbox yang sudah dibaca, dan pembayaran yang belum lunas dihapus per 5000 baris. Pembayaran lunas tidak pernah dihapus. Sesi RADIUS terbuka yang tidak diperbarui selama 1 jam ditutup pada waktu pembaruan terakhirnya.
- Migrasi: file `.sql` bernomor di `internal/db/migrations/`, dibekukan lewat `migrations.sum` ([pengembangan.md](pengembangan.md#migration-freeze)).
- SQLite: WAL, `synchronous=FULL`, `_txlock=immediate`, `busy_timeout` 5 detik, `foreign_keys` aktif, batas `journal_size_limit` 16 MB.

## Aturan khusus STB

1. **Jam tidak dipercaya** setelah boot tanpa RTC: expiry, reminder, backup, dan RADIUS ditahan atau disesuaikan sampai NTP sinkron; `clock_guard=off` bila ada RTC.
2. **Listrik padam:** WAL + `synchronous=FULL`, sehingga transaksi yang sudah commit tidak hilang.
3. **eMMC cepat aus:** log ke stdout (journald); accounting interim hanya meng-update baris sesi. Pembersihan log dilakukan dalam batch 5000 baris.
4. **Backup ke luar perangkat:** `NUXBILL_BACKUP_DIR` ([backup](../id/backup-restore.md)).
5. **Build:** `CGO_ENABLED=0` untuk `linux/amd64`, `arm64`, dan `arm` (GOARM=7).

## Lihat juga

- [pengembangan](pengembangan.md)
- [arsitektur-awal](rencana/arsitektur-awal.md)
- [installation](../id/installation.md)
- [monitoring](../id/monitoring.md)
