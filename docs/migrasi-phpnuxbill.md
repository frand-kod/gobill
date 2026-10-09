# Migrasi dari PHPNuxBill

Untuk operator yang pindah dari PHPNuxBill (MySQL) ke NuxBill Go. Impor dilakukan satu kali dengan perintah `nuxbill import`; sistem lama tidak diubah.

## Perintah impor

    nuxbill import --mysql-dsn='<user>:<password>@tcp(127.0.0.1:3306)/phpnuxbill' --db=./nuxbill.db [--timezone=Asia/Jakarta] [--dry-run] [--force]

- Semua berjalan dalam satu transaksi SQLite. Target harus kosong (`--force` menghapus isinya).
- `--dry-run` hanya membuat laporan: baris dibaca, diimpor, dilewati, beserta alasannya.
- Zona waktu bawaan diambil dari setting lama (atau `Asia/Jakarta`).
- Pakai `NUXBILL_SECRET_KEY` yang sama dengan yang akan dipakai produksi (atau biarkan membuat `<db>.key`, lalu backup).

## Yang dikonversi

Setting, admin, router, bandwidth, IP pool, paket, pelanggan, langganan aktif, transaksi, voucher, log, dan NAS.

- Password admin sha1 ditandai `legacy_sha1` dan diganti ke bcrypt saat login pertama.
- Password pelanggan dan secret perangkat dienkripsi ulang (bcrypt / AES-GCM).
- Waktu expiry diubah ke Unix UTC; uang menjadi INTEGER rupiah. Harga yang gagal dikonversi dilewati dan dilaporkan.
- Plan `RadiusRest` menjadi plan `Radius` ([freeradius-rest.md](freeradius-rest.md)).
- Belum diimpor: kupon, ODP, dan inbox.
- Atribut pelanggan (`tbl_customers_fields`) diimpor sebagai kolom kustom dengan nama yang sama (tampil di form pelanggan). NuxBill Go ikut memakainya seperti PHP:
  - `<nama> Bill` (mis. `Router Bill`): tagihan tambahan, ditambahkan ke harga setiap recharge (admin, saldo, voucher, gateway, auto-renew). Nilai `biaya:sisa` (mis. `50000:3`) adalah cicilan: berkurang satu setiap recharge, berhenti saat sisa 0.
  - `Invoice`: harga tagihan paket Period berikutnya (menggantikan harga paket), diisi otomatis setelah recharge Period seperti PHP.
  - `Expired Date`: tanggal jatuh tempo paket Period per pelanggan; masuk ke `billing_day` pelanggan (bukan kolom kustom). Nilai yang bukan angka 1-31 dilewati dan dilaporkan.
  - Beda: pembayaran gateway online memakai harga paket + tagihan saat pesanan dibuat; kupon hanya mendiskon harga paket, tagihan ditambahkan di atasnya.
- Kolom lama yang sengaja dibuang: `account_type`, kota/kecamatan/provinsi/kode pos, `price_old`, `plan_type`.

## Verifikasi yang sudah dilakukan

Import diuji dengan dump produksi asli: semua tanggal expiry dan total transaksi cocok dengan sistem lama. Ulangi pembandingan itu pada dump terbaru Anda sebelum cutover.

## Checklist jalan paralel

1. Uji CoA Disconnect di MikroTik (setelah perbaikan NAS-IP-Address).
2. Uji jalur FreeRADIUS REST: arahkan `connect_uri` server uji ke NuxBill di instance uji, jalankan `freeradius -X`.
3. Uji login voucher hotspot lewat RADIUS dan pembatas MAC.
4. Import dump produksi terbaru ke STB, bandingkan pelanggan aktif, expiry, dan saldo dengan sistem lama.
5. Jalankan paralel 1-3 hari dalam mode baca (accounting saja), bandingkan sesi dan expiry.
6. Build ARM dan uji di STB: RAM, startup tanpa RTC, listrik padam, backup ke USB.
7. Bersihkan sisa uji di router: `split-user-domain=no`, hapus entry `/radius` uji, profil dan user uji ([mikrotik.md](mikrotik.md#pelajaran-dari-uji-lapangan)).

## Cutover dan rollback

**Cutover:**
1. Hentikan perubahan di sistem lama (jendela singkat), ambil dump MySQL terakhir.
2. `nuxbill import` ke database baru, cek laporan, lalu start NuxBill.
3. Pindahkan RADIUS: ubah `connect_uri` FreeRADIUS ke NuxBill, atau ubah `address` dan `secret` pada `/radius` MikroTik ke NuxBill.
4. Pantau log RADIUS dan sesi aktif beberapa jam.

**Rollback:** kembalikan `connect_uri` atau `/radius address` ke entri lama. Data sistem lama tidak berubah, tetapi transaksi yang terjadi di NuxBill setelah cutover tidak ikut kembali. Catat dan masukkan manual bila perlu.
