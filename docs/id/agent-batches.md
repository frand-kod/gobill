# Voucher titip jual agen

Dokumen ini menjelaskan cara menitipkan voucher ke agen, mencatat setoran, dan menarik kembali voucher yang tidak terjual.

**Untuk:** operator (SuperAdmin, Admin), agen

**Prasyarat:** ada akun admin dengan peran Agent dan minimal satu paket. Lihat [konfigurasi](configuration.md).

## Cara kerja

Anda menyerahkan sekumpulan voucher ke agen lebih dulu. Agen menyetor kemudian. gobill mencatat utang agen, setoran, dan retur dalam satu buku besar per batch yang hanya bertambah. Utang selalu dihitung dari buku itu, tidak disimpan sebagai saldo.

Tidak ada saldo deposit agen. Utang tidak pernah minus.

## Membuat batch

Buka **Batch agen > Tambah**. Isi:

- **Agen**: akun admin dengan peran Agent.
- **Paket**: paket semua voucher di batch.
- **Voucher berbayar**: jumlah voucher yang harus dibayar agen.
- **Voucher bonus**: jumlah voucher gratis yang diberikan di depan.
- **Harga per voucher berbayar**: dalam rupiah.

gobill langsung membuat semua kode (berbayar ditambah bonus) dan mencatat utang sebesar voucher berbayar dikali harga. Semua langkah ini satu transaksi. Jika gagal, tidak ada kode atau utang yang tersisa.

Contoh untuk dua model bagi hasil:

| Model | Berbayar | Bonus | Harga | Utang |
|---|---|---|---|---|
| Bonus voucher | 10 | 4 | Rp5.000 | Rp50.000 untuk 14 kode |
| Selisih harga | 20 | 0 | Rp5.000 | Rp100.000 untuk 20 kode |

Untuk model selisih harga, isi bonus 0 dan harga dengan harga agen. Keuntungan agen adalah selisih dengan harga jualnya, di luar gobill.

Kode batch ikut muncul di daftar **Voucher** sebagai voucher belum dipakai, jadi bisa dicetak dengan **Print** di sana.

## Membaca batch

Setiap batch menampilkan:

- **Terpakai**: kode yang sudah dipakai pelanggan. Ini jumlah minimal yang pasti terjual.
- **Belum dipakai**: kode yang masih bisa dipakai.
- **Dikembalikan**: kode yang dibatalkan.
- **Utang**: sisa yang harus disetor.

gobill tidak bisa tahu voucher yang sudah dijual agen tetapi belum dipakai pelanggan. Setoran dihitung dari voucher yang terjual. Jadi jumlah terpakai adalah batas bawah. Jika klaim agen lebih kecil dari jumlah terpakai, klaim itu pasti kurang.

Agen (peran Agent) bisa melihat batch, pemakaian, dan utangnya sendiri di **Batch agen**. Agen tidak bisa melihat batch agen lain, mencatat setoran, atau meretur.

## Mencatat setoran

Buka batch, lalu **Catat setoran**. Isi jumlah dan catatan. Utang berkurang sebesar jumlah itu. Setoran melebihi utang ditolak.

## Retur

Buka batch, centang kode yang belum dipakai, lalu **Kembalikan yang dipilih**.

- Kode yang dikembalikan dibatalkan. Kode itu tidak bisa dipakai lagi, baik lewat portal, halaman admin, maupun login RADIUS.
- Setiap kode yang dikembalikan mengurangi utang sebesar harga per voucher.
- Jumlah retur dibatasi sebanyak voucher berbayar di batch. Pada batch 10 berbayar dan 4 bonus, retur maksimal 10 kode.
- Kode yang sudah dipakai tidak bisa dikembalikan.

Setoran dan retur tidak bisa dihapus atau diubah. Buku besar sengaja hanya bertambah.

## Batasan

- Voucher dalam batch tidak bisa dihapus dari daftar voucher.
- Setoran belum masuk ke laporan pendapatan. Sisa utang ada di halaman batch.
