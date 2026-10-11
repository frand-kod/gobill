# Setup cepat

Setup cepat memandu Anda memasang gobill langkah demi langkah, seperti pemasang sistem operasi. Setiap langkah hanya menampilkan isian yang dibutuhkan, dan langkah yang sudah terisi otomatis ditandai selesai. Setup cepat hanya tampil untuk akun SuperAdmin.

**Untuk:** operator

**Prasyarat:** akun SuperAdmin, router MikroTik yang bisa dijangkau dari gobill, dan (opsional) file backup PHPNuxBill.

## Kapan muncul

Saat pertama kali login sebagai SuperAdmin, gobill menampilkan dialog dengan tiga pilihan:

- **Mulai setup cepat**: buka langkah pertama.
- **Impor dari PHPNuxBill**: buka halaman impor. Setelah impor berhasil, setiap langkah menampilkan data yang diimpor untuk diperiksa, bukan membuat data baru.
- **Lewati**: dialog tidak muncul lagi.

Selama belum selesai dan belum dilewati, menu **Setup cepat** ada di bagian atas sidebar dengan lencana progres, misalnya `3/8`. Lencana menghitung langkah wajib yang sudah terisi.

## Langkah-langkah

Urutan langkah:

1. **Selamat datang**: pilih mulai dari awal atau impor dari PHPNuxBill.
2. **Usaha dan URL aplikasi**: nama usaha, URL aplikasi, dan nomor telepon. Isian ini sama dengan **Pengaturan > Umum**.
3. **Router atau NAS**: pilih cara gobill terhubung ke MikroTik, lalu isi formulir yang sesuai (lihat di bawah).
4. **Kecepatan**: tiga preset 5, 10, dan 20 Mbps. Ubah nilainya, atau kosongkan untuk melewati.
5. **Paket**: template Harian, Mingguan, dan Bulanan dengan harga saran. Harga bisa diubah.
6. **WhatsApp**: isi alamat gateway GOWA, lalu kirim pesan uji.
7. **QRIS**: unggah gambar QRIS statis. Pratinjau QR yang terbaca harus bisa dipindai aplikasi bank.
8. **Tripay (opsional)**: kunci API Tripay, jika Anda memakai Tripay.
9. **Isolir (opsional)**: aktifkan Isolir, lalu salin perintah MikroTik yang ditampilkan.
10. **Pelanggan atau voucher pertama**: buat satu voucher uji untuk paket yang dipilih.
11. **Selesai**: tes akhir. Login di hotspot memakai voucher uji. Indikator menjadi hijau saat router melaporkan sesi itu.

Setiap langkah punya bagian **Pelajari lebih lanjut** dan tautan ke panduan terkait. Gunakan **Kembali** untuk membuka langkah sebelumnya, dan **Lewati langkah** untuk melanjutkan tanpa menyimpan.

## Router atau NAS

Pilih salah satu cara berikut.

| Pilihan | Dipakai untuk | Yang dibuat |
|---|---|---|
| RADIUS (disarankan) | Login hotspot dan PPPoE lewat RADIUS | NAS dengan nama, IP, dan secret |
| API | gobill membaca dan mengatur router langsung | Router dengan host, port, dan login |
| Keduanya | Kedua hal di atas | NAS dan router |

Untuk RADIUS, gobill menampilkan perintah RouterOS yang sudah berisi IP gobill dan secret. Salin perintah itu ke terminal MikroTik. Secret hanya ditampilkan tepat setelah disimpan. Jika Anda membuka langkah ini lagi, ganti `<secret>` dengan secret NAS.

Indikator **Menunggu paket dari router** berubah hijau begitu router mengirim paket RADIUS pertama. Untuk API, tombol **Uji koneksi** menyimpan router lalu mengecek koneksinya.

Jika NAS atau router dengan IP atau host yang sama sudah ada, langkah ini mengubah data itu. Data baru tidak dibuat.

## Data yang dibuat

Setup cepat tidak punya data sendiri. Setiap simpanan memakai halaman dan aturan yang sama dengan menu biasa:

| Langkah | Halaman biasa |
|---|---|
| Usaha dan URL aplikasi | **Pengaturan > Umum** |
| Router atau NAS | **Router** dan **NAS** |
| Kecepatan | **Bandwidth** |
| Paket | **Paket Layanan** |
| WhatsApp dan QRIS | **Pengaturan > Integrasi** dan **Pengaturan > Pembayaran** |
| Tripay dan Isolir | **Pengaturan > Pembayaran** dan **Pengaturan > Isolir** |

Semua nilai bisa diubah di halaman itu kapan saja.

## Setelah selesai

Di langkah terakhir, tekan **Buka dashboard**. gobill menyimpan status selesai, dan menu **Setup cepat** hilang dari sidebar. Untuk membuka lagi, pilih **Panduan**, lalu **Setup cepat**.

Jika Anda menekan **Lewati** di dialog, menu tidak muncul lagi. Buka `/admin/setup` langsung di browser untuk memakainya.

## Lihat juga

- [Instalasi](installation.md): memasang gobill sebelum menjalankan setup cepat.
- [Setup MikroTik](mikrotik.md): detail perintah RADIUS dan API.
- [Integrasi](integrations.md): WhatsApp, QRIS, dan Tripay.
- [Isolasi](isolation.md): halaman isolir dan perintah MikroTik.
- [Migrasi dari PHPNuxBill](migration-phpnuxbill.md): impor data dari PHPNuxBill.
