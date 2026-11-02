# Design system GoBill

Aturan UI admin dan portal. Lahir dari audit UX v0.3, lihat `audit-ux-v0.3.md`.

**Untuk:** pengembangan. Hanya tersedia dalam bahasa Indonesia.

## Prinsip

1. Status lebih dulu, uang kemudian. Dashboard dibuka dengan kondisi layanan.
2. Satu tombol utama per halaman.
3. Warna aksen hanya untuk aksi utama, keadaan aktif, dan link. Status memakai warna status.
4. Warna tidak pernah jadi satu-satunya tanda. Selalu ada teks atau ikon.
5. Pengguna selalu tahu langkah berikutnya. Tidak ada halaman buntu.
6. Aksi uang dan aksi merusak selalu dikonfirmasi dan hasilnya tidak hilang sendiri.

## Token

Token ada di `web/tailwind.css`. Setelah mengubahnya, jalankan `make css`.

| Token | Fungsi | Syarat |
|---|---|---|
| `--accent`, `--on-accent` | Latar dan teks tombol utama | Kontras teks 4,5:1 |
| `--accent-fg` | Link, fokus, teks beraksen | 4,5:1 di atas `--surface` |
| `--bg`, `--surface`, `--surface-2`, `--field` | Latar halaman, kartu, hover, input | |
| `--border` | Garis kartu dan tabel | |
| `--border-strong` | Garis input | 3:1 di atas `--field` |
| `--ok-*`, `--warn-*`, `--err-*`, `--info-*` | Pasangan teks dan latar status | 4,5:1 |

Tes `theme_contrast_test.go` menjaga kontras token teks. Kontras garis dan fokus masih dicek manual.

## Komponen

**Tombol.** `btn-primary` untuk aksi utama halaman. `btn` untuk aksi kedua. `btn-ghost` untuk aksi ketiga dan toolbar. `btn-danger` untuk aksi hapus. `btn-icon` wajib punya `aria-label` dan `data-tip`. Label tombol berupa kata kerja perintah: Simpan, Hapus, Aktifkan.

**Badge.** `badge-ok`, `badge-warn`, `badge-bad`, `badge-info`, `badge-muted`. Selalu berisi teks.

**Alert.** `alert-error`, `alert-warn`, `alert-ok`. Error dan peringatan penting memakai `role="alert"` dan tidak hilang sendiri.

**Toast.** Untuk hasil biasa, hilang setelah 4 detik. Hasil yang menyangkut uang memakai `flash_sticky` dan tetap tampil sampai ditutup.

**Field form.** Gunakan helper `field` di `internal/web/forms.go` dan partial `formfields.html`. Helper itu sudah mengatur:

- label di atas input dan tanda `*` untuk kolom wajib;
- hint yang terhubung lewat `aria-describedby`;
- error di bawah kolom dengan `aria-invalid`;
- ringkasan error di atas form yang langsung difokus;
- kolom bersyarat lewat `show` dan `reqIf`. Kolom yang tersembunyi tidak ikut dikirim, dan kolom yang punya error selalu terlihat.

**Konfirmasi.** Pakai `data-confirm` dengan nama objeknya, misalnya "Hapus Budi?". Tambah `data-confirm-danger` untuk aksi merusak dan `data-confirm-ok` untuk label tombol OK. Teks konfirmasi menyebut akibatnya: apa yang hilang dan apa yang tetap. Pilihan select yang berisiko memakai `data-warn` dan `data-warn-to`.

**Proses lambat.** Tombol yang memanggil router atau layanan luar memakai `data-busy-label`, misalnya "Memeriksa…".

**Daftar.** Pakai `listPage` di `internal/web/lists.go`. Daftar kosong dan hasil filter kosong punya pesan berbeda. Hasil filter kosong menyediakan tombol reset. Live search menampilkan status loading dan jumlah hasil.

**Halaman error.** 403, 404, 405, dan 500 memakai satu tampilan bersama lewat `deny` dan `errorHTML`. Respons JSON dan API tetap teks polos.

## State wajib

Setiap halaman data menangani state berikut:

| State | Tampilan |
|---|---|
| Kosong | Penjelasan singkat dan satu aksi, misalnya "Belum ada paket" dan Tambah |
| Filter kosong | "Tidak ada hasil untuk filter ini" dan Reset |
| Memuat | `aria-busy` dan tampilan redup |
| Error | Pesan yang bisa dipahami dan jalan kembali. Satu kartu gagal tidak menjatuhkan halaman |
| Berhasil | Toast, atau alert tetap untuk uang |
| Nonaktif | Tombol nonaktif dengan alasan yang terhubung lewat `aria-describedby` |
| Sesuai peran | Aksi yang tidak boleh untuk peran itu tidak ditampilkan |

## Aksesibilitas

- Target sentuh minimal 24 px, di HP 40 px. Halaman masuk portal memakai 48 px.
- Skip link ke `#main` di admin dan portal.
- Menu, drawer, dan bell bisa ditutup dengan Esc, dan fokus kembali ke tombolnya.
- Tab memakai panah kiri-kanan, Home, dan End.
- Animasi mengikuti `prefers-reduced-motion`.
- `aria-sort` dipasang di `<th>`, bukan di link.

## Bahasa

Semua teks yang tampil ditulis sebagai key bahasa Inggris dan diterjemahkan di `lang/indonesia.json`. Tes `i18n_keys_test.go` menangkap teks yang lupa diterjemahkan. Angka di dalam kalimat memakai key format, misalnya `"Tunggu %d detik"`.

| Konsep | Istilah |
|---|---|
| Tanggal akhir paket | Berakhir |
| Paket yang sudah lewat | Kedaluwarsa |
| Membeli atau memperpanjang paket | Isi Ulang |
| Menambah uang ke akun | Tambah Saldo |
| Orang yang berlangganan | Pelanggan |
| Mengedit | Ubah |
| Login dan logout | Masuk dan Keluar |

Format: uang lewat `money()`, misalnya `Rp 1.000` dan `-Rp 1.000`. Tanggal di tabel dan grafik memakai `2006-01-02`.

## Istilah teknis yang perlu penjelasan

Istilah berikut tampil untuk operator dan sebaiknya diberi hint saat halaman terkait disentuh:

NAS, RADIUS, CoA, Acct-Interim-Interval, NAS-Port-Id, Calling-Station-Id, address-list, walled garden, trust proxy, PAP dan CHAP, shared users, Recharge Zero, mode akses PHPNuxBill, peer dan handshake WireGuard, token metrics, signature webhook, QRIS NMID, callback URL, kunci enkripsi, kode pemulihan 2FA.
