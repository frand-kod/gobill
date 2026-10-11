# Promosi

Dokumen ini menjelaskan cara menampilkan promo singkat, misalnya toko di sekitar, di halaman isolir dan di dashboard portal pelanggan.

**Untuk:** operator (SuperAdmin)

**Prasyarat:** gobill sudah terpasang. Lihat [instalasi](installation.md).

## Tempat mengatur

Buka **Pengaturan > Promosi**. Hanya SuperAdmin yang bisa melihat dan mengubah promo.

Setiap promo punya:

- **Gambar** (opsional): PNG, JPG, WebP, atau ICO, maksimal 2 MB. Gambar dihapus dari disk saat promo dihapus atau gambarnya diganti.
- **Judul** dan **Isi**: teks biasa. Pindah baris tetap dipakai. HTML ditampilkan sebagai teks.
- **Tautan** (opsional): hanya `https://` atau `http://`. Tampil sebagai tombol **Buka** di tab baru, misalnya tautan WhatsApp: `https://wa.me/628123456789`.
- **Tampil di**: halaman isolir (aktif secara default), dan/atau dashboard portal pelanggan (mati secara default).
- **Aktif**: mati menyembunyikan promo di semua tempat.
- **Jadwal** (opsional): tanggal mulai dan tanggal selesai. Kosong berarti tanpa batas.

Gunakan panah naik dan turun di daftar untuk mengatur urutan. Promo pertama di urutan tampil lebih dulu.

## Yang dilihat pelanggan

- **Halaman isolir**: setiap promo aktif yang ditandai untuknya, sebagai kartu kecil sesuai urutan. Gambar dimuat malas (lazy) untuk menghemat kuota.
- **Dashboard portal**: satu kartu kecil di paling bawah dashboard, berisi promo pertama. **Lainnya** menampilkan promo yang lain. Tombol **×** menyembunyikan kartu di perangkat itu. Mengubah promo (atau menambah promo baru) memunculkan kartu lagi. Kartu tidak menggeser kartu paket dan status.

Tanpa JavaScript, kartu tetap tampil dengan promo pertama saja.

## Praktik baik

- Buat promo sedikit dan singkat. Pelanggan dengan paket aktif jangan sampai terganggu.
- Aktifkan kartu dashboard hanya untuk penawaran yang memang diminati pelanggan, misalnya diskon.
- Gunakan jadwal agar promo berakhir tepat waktu.
