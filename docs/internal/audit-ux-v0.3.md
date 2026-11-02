# Audit UX v0.3

Dibuat 2026-11-02 di atas `main` @4ba0ab5. Pengganti `audit-ux.md` dan `audit-alur-ux.md` dari v0.1.5.

**Untuk:** pengembangan. Hanya tersedia dalam bahasa Indonesia.

## Cara audit

Lima area diperiksa terpisah: CRUD admin, dashboard dan pemantauan, portal pelanggan, settings dan setup, serta aksesibilitas lintas halaman. Setiap area dibaca dari kode lalu dirender dari database kosong dengan curl.

Acuan:

- 10 heuristik Nielsen.
- WCAG 2.2 AA.
- Pola enterprise UX Pencil & Paper untuk tabel, form, dan state.

Semua temuan HIGH dicek ulang di kode sebelum diperbaiki. Hasilnya: semua valid.

## Hasil

Ada 20 temuan HIGH. Sembilan belas diperbaiki penuh, satu sebagian, yaitu halaman 404 di dalam handler. Temuan MEDIUM dan LOW diperbaiki dalam tiga gelombang. Sisa yang sengaja ditunda tercatat di bagian akhir.

## Temuan terpenting

1. **Pilihan Ya/Tidak di settings menyimpan nilai salah.** Setting yang belum pernah disimpan tampil "Ya". Satu klik Simpan menyalakan isolir, trust proxy, dan nonaktif voucher. Sekarang setiap pilihan tampil sesuai perilaku kode, dijaga tes `TestSettingsUnsetYesNoShowsRuntimeDefault`.
2. **Pelanggan isolir tidak bisa membayar.** Halaman isolir hanya berisi kontak WA. Sekarang ada tombol bayar atau perpanjang.
3. **Pembayaran yang menunggu hilang setelah tab ditutup.** Sekarang tampil di dasbor dan riwayat.
4. **Form gagal simpan terlihat seperti tidak terjadi apa-apa.** Sekarang ada ringkasan kolom yang salah di atas form, dan kolom tersembunyi tidak lagi menyimpan error.
5. **Uang bergerak tanpa konfirmasi.** Deposit kini punya langkah konfirmasi, dan paket tidak lagi diam-diam mengganti nominal.
6. **Alert NAS dan router terlalu ramai.** NAS sepi tidak lagi dianggap mati. Router baru dianggap offline setelah dua kali gagal. Satu kejadian menghasilkan satu pesan, dan restart tidak mengirim ulang alert.

## Yang ditunda

- Halaman status masih memuat ulang halaman. Belum memperbarui angka di tempat lewat `status.json`.
- Sebagian `http.NotFound` di dalam handler masih berupa teks polos. Rute yang tidak ada sudah memakai halaman 404 bergaya aplikasi.
- Settings masih 11 tab datar.
- Tooltip penjelasan istilah teknis belum dibuat. Daftar istilahnya ada di `design-system.md`.
- Perilaku JS belum dites di browser: Alpine, bulk bar, chip jadwal, salin kode, dan jeda status.

## Cara memakai dokumen ini

Aturan yang lahir dari audit ini ada di `design-system.md`. Fitur atau halaman baru mengikuti aturan itu. Audit berikutnya cukup memeriksa penyimpangan dari aturan tersebut.
