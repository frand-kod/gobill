# Monitoring

Dokumen ini menjelaskan cara memantau kondisi gobill. Ada tiga cara: halaman Status Sistem di admin, endpoint `/health` untuk uptime monitor, dan endpoint `/metrics` untuk Prometheus. gobill juga mengirim alert ke operator secara otomatis.

**Untuk:** operator

**Prasyarat:** gobill sudah berjalan. Untuk alert, kanal Telegram atau WhatsApp sudah diatur di [integrasi](integrations.md).

## Halaman Status Sistem

Buka **Admin > Status Sistem** (`/admin/status`). Halaman ini bisa dibuka oleh SuperAdmin dan Admin. Halaman diperbarui sendiri setiap 30 detik.

| Bagian | Isi |
|---|---|
| Aplikasi | Versi, waktu mulai, uptime, memori yang dipakai, dan jumlah goroutine. |
| Penyimpanan | Ukuran database dan WAL, disk kosong, pertumbuhan database per hari, dan perkiraan hari sampai disk penuh. Perkiraan baru muncul setelah ada dua sampel harian. Sampel disimpan sekali sehari, dan 30 hari terakhir disimpan. |
| RADIUS | Jumlah paket diterima dan ditolak dalam 24 jam terakhir, rata-rata waktu proses, sesi terbuka, dan paket terakhir per NAS. NAS yang diam lebih lama dari batas alert ditandai merah. |
| Job latar belakang | Waktu jalan terakhir, durasi, error terakhir, dan jumlah gagal berturut-turut. |
| Notifikasi | Jumlah terkirim dan gagal per kanal, yaitu WA, SMS, email, Telegram, dan webhook, beserta error terakhir. Error sudah dibersihkan dari URL, token, dan nomor HP. |
| Pembayaran | Callback Tripay yang berhasil dan gagal, serta waktu callback terakhir. |
| Keamanan | Login gagal dalam 24 jam dan 10 menit terakhir, gagal 2FA, kunci login yang aktif, dan permintaan `/radius.php` yang ditolak. |
| Backup | Backup lokal terakhir, yang diberi tanda jika lebih dari 36 jam, dan status salinan mirror. |

Data yang sama tersedia sebagai JSON di `/admin/status.json`, dengan sesi admin yang sama.

Angka "sejak start" hanya ada di memori. Restart aplikasi mengosongkannya. Angka 24 jam terisi lagi seiring waktu.

## /health dan /metrics

| | `/health` | `/metrics` |
|---|---|---|
| Untuk | Uptime monitor, misalnya Uptime Kuma atau UptimeRobot | Prometheus, Grafana, atau scraper lain |
| Format | JSON | Teks Prometheus |
| Autentikasi | Tidak perlu | Bearer token |
| Aktif | Selalu | Hanya jika token diisi |
| Isi | `status` dan `db` | Semua counter dan gauge, termasuk sisa disk dan versi |

### /health

Contoh respons:

    curl -s https://domain-anda/health
    {"status":"ok","db":"ok"}

Nilai `status`:

- `ok`: normal.
- `degraded`: disk kosong di bawah 200 MB pada folder database. Respons tetap HTTP 200.
- `down`: database tidak bisa dibaca. Respons HTTP 503.

Endpoint ini tidak memerlukan login. Ia tidak menampilkan data pelanggan dan tetap bisa diakses saat mode perawatan.

### Mengaktifkan /metrics

1. Login sebagai SuperAdmin.
2. Buka **Pengaturan > Integrasi**, bagian **Prometheus /metrics**.
3. Klik **Buat token baru**. Token 64 karakter hex ditampilkan sekali di bagian atas halaman, lengkap dengan contoh konfigurasi scrape. Salin sekarang. Di halaman berikutnya token tidak ditampilkan lagi. Kolom token hanya menandai "tersimpan".
4. Untuk mematikan, klik **Nonaktifkan**. Setelah itu `/metrics` mengembalikan `404`.

Permintaan tanpa header yang benar mendapat `401`. Token dibandingkan dengan perbandingan konstan. Nilai token tersimpan di pengaturan `metrics_token`. Kosong berarti `/metrics` mati.

### Contoh scrape Prometheus

    scrape_configs:
      - job_name: gobill
        metrics_path: /metrics
        scrape_interval: 60s
        authorization:
          type: Bearer
          credentials: TOKEN_DARI_PENGATURAN
        static_configs:
          - targets: ['192.168.1.10:8080']

Uji dengan curl:

    curl -H 'Authorization: Bearer TOKEN_DARI_PENGATURAN' http://192.168.1.10:8080/metrics

Nama metrik memakai awalan `gobill_`, misalnya `gobill_radius_auth_accepted_total`, `gobill_notifications_failed_total{channel="wa"}`, dan `gobill_db_size_bytes`.

### Tips Uptime Kuma

Pakai monitor tipe **HTTP(s) - Keyword** ke `http://IP:8080/health`, dengan kata kunci `"status":"ok"`. Dengan begitu, status `degraded` juga dianggap tidak normal. Notifikasi datang lebih cepat.

Untuk memantau RADIUS, pakai monitor TCP atau UDP ke port 1812 di server.

### Pantau dari luar

gobill tidak bisa memberi tahu jika dirinya sendiri mati. Pasang monitor eksternal, misalnya UptimeRobot atau Uptime Kuma. Monitor ini memanggil `https://domain-anda/health` setiap 5 menit, dan mengirim alarm jika respons bukan 200.

## Alert operator

gobill memeriksa aturan berikut setiap menit. Tiap aturan mengirim satu alert saat mulai bermasalah, dan satu alert saat pulih. Selama masih bermasalah, tidak ada pesan lagi. Status disimpan di memori. Setelah restart, kondisi yang masih berjalan dilaporkan ulang.

| Aturan | Terpicu saat | Pulih saat |
|---|---|---|
| Disk hampir penuh | Disk kosong di folder database di bawah 200 MB | Disk kosong 200 MB atau lebih |
| NAS diam | NAS di menu NAS pernah mengirim paket RADIUS, lalu diam selama `alert_nas_silent_minutes` menit. Bawaannya 15 | Paket datang lagi |
| Job gagal | Satu job gagal 3 kali berturut-turut | Satu kali jalan berhasil |
| Kanal notifikasi gagal | Satu kanal gagal 5 kali berturut-turut | Satu kirim berhasil |
| Brute force | Lebih dari 30 kegagalan login, admin, portal, dan 2FA, dalam 10 menit | Kegagalan turun di bawah batas |
| Backup | Backup lokal lebih dari 36 jam lalu, atau salinan mirror gagal | Backup baru, atau salinan mirror berhasil |
| Restart tidak normal | gobill berhenti karena crash, kill paksa, atau listrik mati | - |

Backup mirror yang gagal dicoba ulang sekali per jam, sepanjang hari itu, sampai berhasil.

Alert juga memberi tahu jika callback pembayaran lunas datang untuk pelanggan yang sudah dihapus.

### Pengaturan alert

Pengaturan ini ada di **Pengaturan > Notifikasi**, bagian **Alert operator**.

| Pengaturan | Fungsi |
|---|---|
| `alert_channel` | `telegram` (bawaan), `wa`, atau `both`. Telegram memakai `telegram_target_id`. WhatsApp memakai server GOWA atau `wa_url`. |
| `alert_wa_to` | Nomor WhatsApp untuk alert. Kosong berarti memakai `daily_summary_wa_to`. |
| `alert_nas_silent_minutes` | NAS yang diam selama N menit memicu alert. Bawaan 15, rentang 1 sampai 1440. |

Alert tentang router offline dan perangkat yang restart juga memakai kanal ini.

## Ringkasan harian

Ringkasan harian dikirim ke operator, bukan ke pelanggan. Pengaturannya ada di **Pengaturan > Notifikasi**.

| Pengaturan | Fungsi |
|---|---|
| `daily_summary_enabled` | `yes` atau `no`. Bawaan `no`. |
| `daily_summary_time` | Jam dalam format `HH:MM`, zona waktu server. Bawaan `07:00`. |
| `daily_summary_channel` | `telegram`, `wa`, atau `both`. |
| `daily_summary_wa_to` | Nomor WhatsApp operator. |

Ringkasan dikirim sekali sehari. Tanggal terakhir disimpan di `daily_summary_last`, sehingga aman saat restart. Ringkasan ditunda jika jam sistem tidak tepercaya. Tombol "Kirim ringkasan sekarang" di **Pengaturan > Notifikasi** dipakai untuk uji coba.

## Lihat juga

- [Integrasi](integrations.md): kanal Telegram, WhatsApp, dan uji koneksi.
- [Konfigurasi](configuration.md): pengaturan umum dan clock guard.
- [Backup dan restore](backup-restore.md): backup dan mirror yang dipantau alert.
- [Keamanan](security.md): endpoint publik dan pembatas login.
