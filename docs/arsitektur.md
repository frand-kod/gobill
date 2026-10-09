# Arsitektur

Untuk developer dan operator teknis yang ingin memahami cara NuxBill bekerja. Dokumen ini menggambarkan kondisi kode saat ini. Rencana awal dan alasan keputusan ada di [plan/](plan/README.md), terutama [plan/03-arsitektur.md](plan/03-arsitektur.md) dan [plan/02-keputusan-stack.md](plan/02-keputusan-stack.md).

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
| `internal/web` | Handler HTTP admin dan portal, middleware auth/role/CSRF, endpoint `/radius.php`, callback Tripay |
| `internal/billing` | Logika bisnis: masa aktif, recharge, saldo, voucher, kupon, sinkron paket/router, reminder |
| `internal/db` | Hasil `sqlc`, migrator (`PRAGMA user_version`), file migrasi dan query |
| `internal/device` | Interface `Device` dan driver: MikroTik hotspot/PPPoE (API), `Radius`, `Dummy` |
| `internal/radius` | Server RADIUS (PAP, CHAP, MS-CHAPv2), accounting, CoA/Disconnect, Message-Authenticator, dedup |
| `internal/payment` | Interface `PaymentGateway` dan implementasi Tripay |
| `internal/notify` | Telegram, WA/SMS (URL gateway), email, webhook; redaksi error |
| `internal/job` | Loop ticker: clock guard, expiry, backup |
| `internal/importer` | Impor MySQL PHPNuxBill ke SQLite |
| `internal/secret` | Enkripsi AES-GCM dan pemuatan kunci |
| `internal/i18n` | Pemuat terjemahan dari `lang/*.json` |
| `web/`, `assets.go` | Template, CSS, JS, ikon (di-embed ke binary) |

## Alur utama

**Recharge (admin atau portal).** Handler memanggil `billing`, yang membuka satu transaksi DB: hitung masa aktif baru (`validity.go`), ubah saldo secara atomik, tulis transaksi dan langganan. Setelah commit, `device` dipanggil untuk router (API: buat user/queue; mode `Radius`: tidak ada panggilan, data dibaca RADIUS dari DB). Notifikasi dikirim sesudahnya.

**Expiry.** Job `expiry` jalan tiap menit jika clock guard lolos. Langganan dengan `expires_at <= now` dinonaktifkan (perpanjang otomatis bila saldo cukup), perangkat dihubungi setelah commit, lalu notifikasi dikirim. Idempoten: dijalankan dua kali hasilnya sama.

**Auth RADIUS.** Access-Request dari NAS terdaftar (secret per NAS, Message-Authenticator diverifikasi) diteruskan ke logika keputusan: cari langganan aktif, cek pembatas percobaan, lalu jawab Accept dengan Session-Timeout dan rate-limit dari paket, atau Reject. Voucher dikenali bila username adalah kode voucher dan diaktifkan saat itu. Accounting memperbarui satu baris sesi. Request dari NAS tak terdaftar dibuang dan dicatat.

**RADIUS REST.** `POST /radius.php` dari FreeRADIUS memakai logika keputusan yang sama, dibatasi `radius_rest_allow` ([freeradius-rest.md](freeradius-rest.md)).

**CoA/Disconnect.** Saat plan habis atau admin menekan Disconnect, `radius` mengirim Disconnect-Request ke port 3799 NAS dengan identitas NAS-IP-Address yang dilaporkan NAS itu. Balasan NAK didecode (Error-Cause) ke log.

**Callback Tripay.** `POST /callback/tripay` memverifikasi signature, lalu menandai pembayaran dan mengaktifkan paket secara idempoten (callback ganda tidak menggandakan saldo).

## Model data

- Uang `INTEGER` rupiah. Waktu `INTEGER` Unix detik UTC, zona waktu hanya saat tampil.
- Status memakai `TEXT` + `CHECK`; foreign key aktif (`PRAGMA foreign_keys=ON`).
- Secret (router, pelanggan, NAS) terenkripsi AES-GCM; password bcrypt.
- Transaksi milik pelanggan yang dihapus tetap disimpan.
- Pengaturan: tabel key-value `settings`.
- Migrasi: file `.sql` bernomor di `internal/db/migrations/`, dibekukan lewat `migrations.sum` ([pengembangan.md](pengembangan.md#migration-freeze)).
- SQLite: WAL, `synchronous=FULL`, `_txlock=immediate`.

## Aturan khusus STB

1. **Jam tidak dipercaya** setelah boot tanpa RTC: expiry, reminder, backup, dan RADIUS ditahan atau disesuaikan sampai NTP sinkron; `clock_guard=off` bila ada RTC.
2. **Listrik padam:** WAL + `synchronous=FULL`, sehingga transaksi yang sudah commit tidak hilang.
3. **eMMC cepat aus:** log ke stdout (journald); accounting interim hanya meng-update baris sesi.
4. **Backup ke luar perangkat:** `NUXBILL_BACKUP_DIR` ([instalasi.md](instalasi.md#4-backup-ke-usb-atau-nas)).
5. **Build:** `CGO_ENABLED=0` untuk `linux/amd64`, `arm64`, dan `arm` (GOARM=7).
