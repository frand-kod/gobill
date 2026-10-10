# Panduan menulis dokumentasi

Dokumen ini mengatur cara menulis dan merapikan semua Markdown di repo: README, CONTRIBUTING, CHANGELOG, `docs/`, dan `docs/internal/`. Dokumen ini hanya untuk tim, tidak ditampilkan di aplikasi.

**Untuk:** pengembangan (penulis dokumen)

**Prasyarat:** tidak ada

## Bahasa dan gaya

- Tulis dalam bahasa Indonesia yang lugas. Dokumen operator di `docs/en/` ditulis dalam bahasa Inggris yang lugas.
- Kalimat pendek dan aktif. Satu gagasan per kalimat.
- Hindari tanda kurung. Jika isinya penting, buat kalimat sendiri, daftar, atau baris tabel.
- Kurung hanya boleh untuk singkatan pertama kali (mis. `Role-Based Access Control (RBAC)`) dan untuk satuan.
- Istilah teknis, perintah, nama file, nama variabel, dan kunci pengaturan tetap seperti aslinya dan ditulis dalam backtick.
- Nama menu dan tab UI ditulis persis seperti di aplikasi, dalam tebal, mis. **Pengaturan > Integrasi**.
- Jangan pakai kata "kami" atau "kita". Langsung ke pembaca dengan "Anda".

## Glosarium

Satu istilah untuk satu hal. Pakai istilah di kolom kiri, jangan campur dengan sinonimnya.

| Istilah | Arti | Jangan pakai |
|---|---|---|
| pelanggan | orang yang berlangganan internet | customer, user (untuk pelanggan), klien |
| akun admin | login staf di aplikasi | user admin, operator (untuk akun) |
| operator | staf ISP yang menjalankan NuxBill | admin (untuk orang), pengelola |
| paket | produk internet yang dijual | plan, produk |
| langganan | satu masa aktif paket milik satu pelanggan | subscription, recharge (untuk langganan) |
| masa aktif | jangka waktu paket berlaku | validity, periode (untuk masa aktif) |
| kedaluwarsa | keadaan langganan yang sudah habis | expired (kecuali di nama pengaturan dan pesan) |
| saldo | uang deposit pelanggan | balance, dompet |
| voucher | kode sekali pakai untuk login hotspot | token |
| kupon | potongan harga | coupon, diskon (untuk kupon) |
| router | perangkat MikroTik yang dikelola lewat RouterOS API | mikrotik (untuk perangkat umum), NAS |
| NAS | perangkat yang mengirim paket RADIUS ke NuxBill, termasuk router dalam mode RADIUS bawaan | client RADIUS |
| pengaturan | nilai di tabel `settings`, diubah lewat menu **Pengaturan** | setting, konfigurasi (untuk nilai di UI) |
| variabel lingkungan | `NUXBILL_*`, dibaca saat start | env, env var |
| alert operator | pesan peringatan untuk operator | alarm, notifikasi operator |
| ringkasan harian | laporan harian untuk operator | daily report |
| gateway pesan | URL HTTP untuk kirim WhatsApp dan SMS (`wa_url`) | API WA (untuk `wa_url`) |
| GOWA | server WhatsApp go-whatsapp-web-multidevice | server WA (kecuali di kutipan pengaturan lama) |
| backup | salinan database harian | cadangan |
| mirror | salinan kedua backup di luar perangkat | replika |
| restore | memulihkan database dari file backup | restore data (tanpa "database") |
| impor | memasukkan data PHPNuxBill | import (kecuali untuk perintah `nuxbill import`) |
| cutover | pindah resmi dari sistem lama ke NuxBill | switch-over |
| STB | perangkat kecil ARM tempat NuxBill berjalan | box |

Padanan Inggris untuk `docs/en/`: pelanggan = customer, paket = plan, langganan = subscription, saldo = balance, voucher = voucher, kupon = coupon, router = router, NAS = NAS, pengaturan = settings, alert operator = operator alert, backup = backup, mirror = mirror, restore = restore, impor = import, cutover = cutover, masa aktif = validity period, kedaluwarsa = expired, operator = operator (`For: operators`).

## Struktur wajib setiap file

Setiap file memakai urutan ini:

1. `# Judul` (satu H1).
2. Paragraf **Ringkasan**: satu atau dua kalimat yang menjelaskan isi dokumen. Tanpa label "Ringkasan" di depannya.
3. Baris **Untuk:** berisi peran pembaca: operator, admin, atau pengembang. Pisahkan dengan garis miring.
4. Baris **Prasyarat:** bila ada. Tulis "tidak ada" jika tidak ada.
5. Isi dokumen, dengan H2 dan H3.
6. Bagian penutup `## Lihat juga` berisi tautan relatif ke dokumen terkait, satu tautan per baris, dengan keterangan singkat.

Langkah-langkah memakai daftar bernomor. Referensi (nilai, port, perintah per kondisi) memakai tabel.

Front matter YAML tidak dipakai. Dokumen tetap Markdown biasa.

## Penamaan file

- Huruf kecil dan kebab-case, dengan kata benda yang menjelaskan isi, mis. `backup-restore.md`.
- Tanpa angka urut di nama file.
- Dokumen operator memakai slug Inggris yang sama di `docs/id/` dan `docs/en/`, mis. `configuration.md`. Isinya tetap dalam bahasa folder itu. Slug yang dipakai aplikasi tidak diganti.
- Dokumen internal memakai nama Indonesia, mis. `paritas-ui.md`, `progres.md`.
- Pengecualian: `README.md` hanya untuk indeks sebuah folder atau root repo.

## Dua bahasa

- Dokumen operator ada dalam dua bahasa: `docs/id/` (Indonesia) dan `docs/en/` (Inggris). Keduanya punya nama file dan struktur yang sama.
- Versi Inggris adalah terjemahan yang setia dari versi Indonesia: fakta, langkah, tabel, dan anchor heading sama. Gaya yang sama juga berlaku: kalimat pendek, tanpa kurung.
- Setiap perubahan pada dokumen operator harus mengubah kedua bahasa dalam commit yang sama.
- Dokumen di `docs/internal/` hanya dalam bahasa Indonesia dan tidak diterjemahkan. Tidak ada di aplikasi.
- `docs/README.md` hanya memilih bahasa. Indeks ada di `docs/id/README.md` dan `docs/en/README.md`.
- Anchor heading bahasa Inggris berbeda dengan Indonesia. Tautan lintas dokumen harus memakai anchor bahasa folder itu.

## Pengelompokan

- `docs/id/` dan `docs/en/`: dokumen operator, dengan slug yang dipakai aplikasi. Dibagi per topik di indeks masing-masing: Mulai, Konfigurasi, Jaringan, Operasional.
- `docs/internal/`: dokumen tim, tidak masuk ke aplikasi. Termasuk audit, progres, arsitektur, pengembangan, dan rencana.
- Satu fakta hanya ada di satu tempat. Dokumen lain memakai tautan.

## Tautan

- Gunakan tautan relatif, mis. `[konfigurasi](configuration.md)` dari folder bahasa yang sama.
- Tautan ke bagian memakai slug heading, mis. `configuration.md#network-and-proxy`.
- Setelah mengganti nama file atau heading, cari semua tautan lama dengan `grep -rn`.

## Aturan tambahan

- Jangan menulis data nyata: nomor pelanggan, password, secret, atau dump. Pakai placeholder `<SECRET>`, `<SECRET-BARU>`, atau IP contoh seperti `192.168.88.10`.
- CHANGELOG mengikuti format Keep a Changelog. Riwayat tidak diubah. Hanya tautan dan bagian terbaru yang dirapikan.
- Untuk perubahan fitur, dokumen operator diperbarui bersama kode.

## Lihat juga

- [Indeks dokumentasi](../README.md)
- [Panduan pengembangan](pengembangan.md)
