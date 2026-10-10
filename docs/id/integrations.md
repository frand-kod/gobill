# Integrasi

Dokumen ini menjelaskan cara menghubungkan NuxBill ke WhatsApp, SMS, Telegram, email, webhook, dan pembayaran online. Pembayaran online memakai Tripay atau QRIS statis.

**Untuk:** operator

**Prasyarat:** pengaturan umum sudah diisi, terutama `app_url` untuk tautan di pesan. Lihat [konfigurasi](configuration.md).

## Sakelar pesan

Sakelar ini ada di **Pengaturan > Notifikasi**, di bagian paling atas, "Global switches".

| Pengaturan | Fungsi |
|---|---|
| `notify_customers` | `no` menghentikan semua pesan ke pelanggan. Pesan itu meliputi pengingat, kedaluwarsa, invoice dan tautan QRIS, pesan selamat datang, pesan saldo, dan pesan manual. Ringkasan dan notifikasi operator tetap berjalan. |
| `notify_otp` | `no` mematikan kode OTP untuk pendaftaran, lupa kata sandi, dan ganti kontak. Fitur tersebut menampilkan bahwa kode tidak tersedia. |
| `expired_notify_minutes_before` | Pesan kedaluwarsa dikirim N menit sebelum paket berakhir, dengan rentang 0 sampai 1440. Bawaan 0, yaitu saat paket berakhir. Pesannya tetap satu per periode. Paket tetap berakhir pada waktunya. Perpanjangan atau recharge baru mengirim pesan lagi pada periode barunya. |

`notify_customers` dan `notify_otp` bawaannya `yes` jika belum diisi.

Tips: setelah impor dari PHPNuxBill untuk uji paralel, set `notify_customers` = `no`. Dengan begitu pelanggan tidak menerima pesan ganda. Lihat [migrasi](migration-phpnuxbill.md#checklist-jalan-paralel).

## Template dan kanal pesan

Template pesan ada di **Pengaturan > Notifikasi**. Pengaturan `notif_*` berisi template untuk pesan kedaluwarsa, pengingat, invoice, selamat datang, dan saldo. Pengaturan `user_notification_*` mengatur kanal untuk tiap jenis pesan.

Template bisa memakai placeholder seperti `[[price]]`. Placeholder yang tidak dikenal dihapus dari pesan.

## WhatsApp

Ada dua cara mengirim WhatsApp. NuxBill memilih cara yang dipakai secara otomatis.

1. **GOWA (disarankan).** Pakai server [go-whatsapp-web-multidevice](https://github.com/aldinokemal/go-whatsapp-web-multidevice).
2. **Gateway pesan `wa_url`.** Dipakai hanya jika `alt_wga_server_url` kosong.

### Cara 1: GOWA

Isi di **Pengaturan > Integrasi**, bagian "WhatsApp — GOWA":

| Pengaturan | Isi |
|---|---|
| `alt_wga_server_url` | Alamat server GOWA, misalnya `http://127.0.0.1:3030`. |
| `alt_wga_device_id` | Opsional. |
| `alt_wga_username` dan `alt_wga_password` | Basic auth, jika server memakainya. |

NuxBill mengirim `POST <alt_wga_server_url>/send/message` dengan body `{"phone":"628xxx@s.whatsapp.net","message":"..."}`. Cara ini sama dengan plugin "Alternative WhatsApp Gateway" di PHPNuxBill. Nomor yang diawali `0` diubah memakai `country_code_phone`.

### Cara 2: gateway pesan `wa_url`

`wa_url` adalah URL template dengan placeholder `[number]` dan `[text]`. Pengaturan ini juga dipakai untuk SMS. Gateway WhatsApp lain, misalnya Fonnte, Wablas, dan WAHA, belum punya integrasi khusus. Untuk sementara, pakai `wa_url` jika gateway itu mendukung GET dengan `[number]` dan `[text]`.

### Aturan prioritas

- Jika `alt_wga_server_url` terisi, `wa_url` diabaikan sepenuhnya.
- Jika `wa_url` masih berisi alamat plugin PHP lama, yaitu `...?_route=plugin/wga_sendMessage&...`, NuxBill mencatat peringatan di log. NuxBill tetap mengirim langsung ke server WA. Kosongkan `wa_url` agar tidak membingungkan.

Tombol "Kirim pesan uji" di halaman yang sama mengirim satu pesan ke nomor yang Anda ketik. Pesan memakai isian di form, walau belum disimpan. Jawaban server ditampilkan dalam bahasa biasa.

Perangkat WhatsApp dan login QR tidak diatur di NuxBill. Lakukan di halaman server WA sendiri.

## SMS

SMS memakai `wa_url` sebagai gateway. Pengaturan `sms_url` hanya dibaca untuk kompatibilitas dengan impor PHPNuxBill. Jika `sms_url` berisi, SMS memakai `sms_url`. Di **Pengaturan > Integrasi**, nilainya tampil di kolom gateway dan dipindah ke `wa_url` saat disimpan.

## Telegram

| Pengaturan | Fungsi |
|---|---|
| `telegram_bot` | Token bot Telegram. |
| `telegram_target_id` | ID tujuan Telegram, untuk pesan dan alert operator. |

Alert operator memakai kanal yang diatur di [monitoring](monitoring.md#pengaturan-alert).

## Email (SMTP)

| Pengaturan | Fungsi |
|---|---|
| `smtp_host`, `smtp_port` | Server SMTP dan portnya. |
| `smtp_user`, `smtp_pass` | Akun SMTP. |
| `smtp_ssltls` | Mode TLS SMTP. |
| `mail_from`, `mail_reply_to` | Alamat pengirim dan alamat balasan. |

## Webhook

| Pengaturan | Fungsi |
|---|---|
| `webhook_url` | URL tujuan event keluar. |
| `webhook_secret` | Rahasia untuk tanda tangan. Tanda tangan dikirim di header `X-Signature`. |

## Uji koneksi

Tiap bagian punya tombol uji di **Pengaturan > Integrasi**. Hanya SuperAdmin yang bisa menguji, dan maksimal 5 tes per menit. Simpan dulu sebelum menguji, karena tes memakai nilai yang tersimpan. Tes tidak terpengaruh `notify_customers`, karena tujuannya operator.

| Tombol | Yang terjadi |
|---|---|
| Kirim pesan uji Telegram | Mengirim pesan singkat ke `telegram_target_id`. |
| Kirim gateway uji | Mengirim SMS ke nomor yang Anda ketik lewat URL gateway `wa_url`. |
| Kirim email uji | Mengirim email ke alamat yang Anda ketik lewat SMTP tersimpan. |
| Kirim webhook uji | Mengirim event `test` yang ditandatangani ke `webhook_url`, lalu menampilkan status HTTP. |
| Cek koneksi | Di **Pengaturan > Gerbang Pembayaran**, untuk Tripay. Memanggil daftar channel pembayaran Tripay untuk memastikan kunci dan merchant code benar. |

## Pembayaran online

Pengaturan `payment_gateway` menentukan gateway: `""` untuk mati, atau `tripay`. Pengaturan ada di **Pengaturan > Gerbang Pembayaran**.

| Pengaturan | Fungsi |
|---|---|
| `payment_gateway` | `""` untuk mati, atau `tripay`. |
| `tripay_mode`, `tripay_merchant_code`, `tripay_api_key`, `tripay_private_key`, `tripay_channel` | Pengaturan gateway Tripay. Kunci API dan private key tersimpan dalam teks biasa di tabel `settings`. Lihat [keamanan](security.md#keamanan-aplikasi). |
| `qris_payload` | Teks QRIS statis merchant. Diisi lewat unggah foto QRIS. Gambarnya tidak disimpan. |

Callback Tripay masuk ke `POST /callback/tripay`. Callback diverifikasi dengan tanda tangan.

## QRIS statis

Di **Pengaturan > Gerbang Pembayaran**, bagian QRIS:

1. Unggah foto QRIS statis merchant. Format PNG atau JPG, maksimal 2 MB.
2. Sistem membaca kode QR dan memastikan formatnya QRIS yang valid.
3. Sistem hanya menyimpan teksnya di `qris_payload`. Gambarnya tidak disimpan.
4. Nama merchant dan NMID yang aktif tampil di bawah isian.
5. Tombol "Hapus QRIS" mengosongkan pengaturan.

Teks QRIS juga bisa ditempel lewat "Opsi lanjutan".

Untuk setiap invoice, sistem membuat QR yang terkunci nominal. Tautannya dikirim lewat WhatsApp, di placeholder `[[qris_link]]`. Jika template tidak memakai placeholder itu, tautan ditambahkan di akhir pesan. Tautan memerlukan `app_url`. Pelanggan tidak perlu login.

Catatan:

- Sistem tidak memverifikasi pembayaran QRIS. Konfirmasi pembayaran tetap dilakukan manual.
- Recharge yang sudah dibayar lewat saldo, gateway, atau voucher tidak mendapat tautan ini.

## Lihat juga

- [Konfigurasi](configuration.md): pengaturan umum dan bisnis.
- [Monitoring](monitoring.md): alert operator dan ringkasan harian.
- [Keamanan](security.md): penyimpanan secret dan verifikasi callback.
- [Migrasi dari PHPNuxBill](migration-phpnuxbill.md#whatsapp-setelah-cutover): pengaturan WhatsApp setelah pindah.
