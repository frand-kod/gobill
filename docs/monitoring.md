# Monitoring

gobill punya tiga cara untuk tahu kondisi aplikasi: halaman status di admin, endpoint `/health` untuk uptime monitor, dan endpoint `/metrics` untuk Prometheus. Alert ke operator dikirim otomatis dari aplikasi.

## Halaman Status Sistem

Menu **Admin > Status Sistem** (`/admin/status`) dapat dibuka SuperAdmin dan Admin. Halaman ini diperbarui sendiri setiap 30 detik. Isinya:

- **Aplikasi**: versi, waktu mulai, uptime, memori yang dipakai, jumlah goroutine.
- **Penyimpanan**: ukuran database dan WAL, disk kosong, pertumbuhan database per hari, dan perkiraan hari sampai disk penuh. Perkiraan baru muncul setelah ada dua sampel harian (sampel disimpan sekali sehari dan 30 hari terakhir dipertahankan).
- **RADIUS**: jumlah diterima dan ditolak 24 jam terakhir, rata-rata waktu proses, sesi terbuka, dan paket terakhir per NAS. NAS yang diam lebih lama dari batas alert diberi tanda merah.
- **Job latar belakang**: waktu jalan terakhir, durasi, error terakhir, dan jumlah gagal berturut-turut.
- **Notifikasi**: terkirim, gagal, dan error terakhir per kanal (WA, SMS, email, Telegram, webhook). Error sudah dibersihkan dari URL, token, dan nomor HP.
- **Pembayaran**: callback Tripay yang berhasil dan gagal, serta waktu callback terakhir.
- **Keamanan**: gagal login (24 jam dan 10 menit terakhir), gagal 2FA, kunci login yang aktif, dan permintaan `/radius.php` yang ditolak.
- **Backup**: backup lokal terakhir (diberi tanda jika lebih dari 36 jam) dan status salinan mirror.

Data yang sama tersedia sebagai JSON di `/admin/status.json` (dengan sesi admin yang sama).

Angka "sejak start" di halaman ini hanya ada di memori. Restart aplikasi mengosongkannya; jumlah 24 jam terisi lagi seiring waktu.

## /health dan /metrics

| | `/health` | `/metrics` |
|---|---|---|
| Untuk | uptime monitor (Uptime Kuma, UptimeRobot) | Prometheus, Grafana, atau scraper lain |
| Format | JSON | teks Prometheus |
| Autentikasi | tidak perlu | bearer token |
| Aktif | selalu | hanya jika token diisi |
| Isi | status database dan status disk (`ok` atau `degraded`) | semua counter dan gauge di atas, termasuk sisa disk dan versi di halaman Status Sistem |

`/health` mengembalikan `200` (ok atau degraded), atau `503` jika database tidak bisa dibaca. Endpoint ini tidak memerlukan sesi dan tidak terpengaruh mode maintenance.

### Mengaktifkan /metrics

1. Login sebagai SuperAdmin, buka **Pengaturan > Integrasi**, bagian **Prometheus /metrics**.
2. Tekan **Buat token baru**. Token 64 karakter hex ditampilkan sekali di bagian atas halaman, lengkap dengan contoh konfigurasi scrape. Salin sekarang; setelah halaman berikutnya token tidak ditampilkan lagi (kolom token hanya menampilkan tanda "tersimpan").
3. Untuk mematikan, tekan **Nonaktifkan**. Setelah itu `/metrics` mengembalikan `404`.

Permintaan tanpa header yang benar mendapat `401`. Token dibandingkan dengan perbandingan konstan.

### Contoh scrape Prometheus

```yaml
scrape_configs:
  - job_name: gobill
    metrics_path: /metrics
    scrape_interval: 60s
    authorization:
      type: Bearer
      credentials: TOKEN_DARI_PENGATURAN
    static_configs:
      - targets: ['192.168.1.10:8080']
```

Uji dengan curl:

```sh
curl -H 'Authorization: Bearer TOKEN_DARI_PENGATURAN' http://192.168.1.10:8080/metrics
```

Nama metrik memakai awalan `gobill_`, misalnya `gobill_radius_auth_accepted_total`, `gobill_notifications_failed_total{channel="wa"}`, dan `gobill_db_size_bytes`.

### Tip Uptime Kuma

Pakai monitor tipe **HTTP(s) - Keyword** ke `http://IP:8080/health` dengan kata kunci `"status":"ok"`. Dengan begitu status `degraded` (disk di bawah 200 MB) juga terbaca sebagai "tidak ok" jika Anda ingin notifikasi lebih cepat. Untuk memantau RADIUS, pakai monitor TCP/UDP ke port 1812 di server.

## Alert operator

Aplikasi memeriksa aturan berikut setiap menit. Setiap aturan mengirim **satu** alert saat mulai bermasalah dan **satu** alert saat pulih. Selama masih bermasalah tidak ada pesan lagi. Status disimpan di memori, jadi setelah restart kondisi yang masih berjalan akan dilaporkan ulang.

| Aturan | Terpicu saat | Pulih saat |
|---|---|---|
| Disk hampir penuh | kosong di folder database di bawah 200 MB | kosong 200 MB atau lebih |
| NAS diam | NAS yang ada di Jaringan > NAS pernah mengirim paket RADIUS, lalu tidak mengirim selama `alert_nas_silent_minutes` (default 15) | paket datang lagi |
| Job gagal | satu job gagal 3 kali berturut-turut | satu kali jalan berhasil |
| Kanal notifikasi gagal | satu kanal gagal 5 kali berturut-turut | satu kirim berhasil |
| Brute force | lebih dari 30 kegagalan login (admin, portal, 2FA) dalam 10 menit | kegagalan turun di bawah batas |
| Backup | backup lokal lebih dari 36 jam lalu, atau salinan mirror gagal | backup baru, atau salinan mirror berhasil |

Backup mirror yang gagal dicoba ulang sekali per jam sepanjang hari itu, sampai berhasil.

### Pengaturan

Di **Pengaturan > Notifikasi**, bagian **Alert operator**:

- **Kanal alert operator**: `Telegram` (default), `WhatsApp`, atau keduanya. Telegram memakai ID Telegram di Integrasi. WhatsApp memakai server WA atau URL WhatsApp di Integrasi.
- **Nomor WhatsApp alert operator**: kosong berarti memakai nomor ringkasan harian.
- **Alert NAS diam (menit)**: default 15.

Alert lama tentang router offline dan perangkat yang restart juga memakai kanal ini.
