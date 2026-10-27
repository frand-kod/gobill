# Lab RouterOS CHR

Folder ini berisi lab lokal: gobill berjalan di container seperti service, melawan MikroTik RouterOS CHR asli (QEMU/KVM di dalam container) dan klien PPPoE. Dipakai untuk menguji RADIUS, CoA, sinkronisasi router, dan restart service tanpa perangkat fisik.

**Untuk:** pengembang

**Prasyarat:** `podman` (rootless cukup), Go sesuai `go.mod`, `curl`, `unzip`, serta `/dev/kvm` dan `/dev/net/tun` yang bisa diakses user Anda. Image CHR (zip 45 MB, terurai 128 MB) diunduh sekali dari `download.mikrotik.com`, diverifikasi SHA256, lalu disimpan di `lab/.cache`.

## Menjalankan

Dari root repo:

1. Bangun image dan nyalakan lab. Run pertama dengan cache kosong butuh sekitar 4 menit termasuk build image; run berikutnya sekitar 1 menit:

   ```sh
   lab/run.sh up
   ```

   Alamat UI gobill, password admin, dan secret RADIUS dicetak di akhir dan disimpan di `lab/.cache/lab.env`.

2. Jalankan tes. Exit code bukan 0 jika ada yang gagal:

   ```sh
   lab/run.sh test
   ```

3. Buka konsol RouterOS pada CHR bila perlu:

   ```sh
   lab/run.sh shell
   ```

4. Hapus semua container, network, dan volume. `lab/.cache` dipertahankan:

   ```sh
   lab/run.sh down
   ```

Mode NAT dipilih dengan `LAB_MODE=nat lab/run.sh up`, dan variabel yang sama harus dipakai saat `test`. Satu lab sekaligus: `up` menolak jalan jika masih ada container `lab-*`.

| Variabel | Bawaan | Arti |
|---|---|---|
| `LAB_MODE` | `direct` | `direct`: gobill menjangkau API CHR. `nat`: CHR di belakang NAT, bisa memanggil gobill tetapi tidak sebaliknya |
| `LAB_WARMUP` | `1` | `0` melewati login pemanasan setelah `up` (lihat [Batasan](#batasan)) |
| `CHR_VERSION` | `7.24.5` | Versi CHR. Versi lain butuh SHA256 baru di `CHR_SHA` pada `run.sh` |
| `CHR_PASS` | acak | Password `admin` di CHR |
| `CLIENTS` | `2` | Jumlah container klien PPPoE |
| `GOBILL_PORT` | `8090` | Port host untuk UI gobill |
| `GOBILL_ADMIN_PASS` | acak | Password admin gobill |

## Topologi

```
lab-wan (10.89.77.0/24)                 lab-lan (10.89.78.0/24, internal)
  lab-gobill 10.89.77.2  <---->  CHR ether1 | ether2 (server PPPoE)  <---->  lab-client-1..N
```

- `lab-chr`: container Alpine yang menjalankan QEMU dengan KVM (1 vCPU, 256 MB). Entrypoint ([chr-entry.sh](chr-entry.sh)) menyambungkan dua NIC CHR ke network container.
- `direct`: CHR mendapat `10.89.77.11` lewat DHCP dan bisa dijangkau gobill langsung.
- `nat`: CHR berada di `192.168.77.2` di belakang container yang melakukan masquerade ke `lab-wan`, tanpa koneksi masuk. Alamat NAS di gobill adalah `10.89.77.10`.
- `lab-gobill`: gobill di image Alpine (image distroless di repo tidak punya shell untuk healthcheck), dengan `--restart=unless-stopped` dan healthcheck `/health`.
- `lab-client-N`: klien PPPoE userspace dari [pppoe](pppoe/main.go).
- [seed](seed/main.go) mengisi database gobill sebelum gobill start. [chrctl](chrctl/main.go) adalah klien API RouterOS kecil untuk mengisi dan membaca CHR.

Data awal di CHR: profil PPP `chr-5m`, `chr-10m`, `chr-weird` (rate-limit dengan burst), pool, server PPPoE di ether2, RADIUS ke gobill, `incoming accept`, secret `cust01..12` dan `legacy01..08`, serta user hotspot.

## Isi tes

| Bagian | Mode | Yang diperiksa |
|---|---|---|
| 1 Ping dan health | direct | Tes koneksi router dari gobill berhasil dan versi CHR terbaca. Di `nat` tes koneksi harus gagal |
| 2 Sinkronisasi | direct | Menyimpan paket membuat profil dengan rate-limit dan burst yang benar di CHR. Pelanggan yang hanya ada di gobill dibuat di CHR, dan password pelanggan yang bentrok ditimpa |
| 3 Login PPPoE lewat RADIUS | keduanya | Klien naik, sesi muncul di `/ppp/active`, gobill mencatat accounting |
| 4 Disconnect | keduanya | `direct`: CHR memutus klien dengan PADT dan sesi hilang. `nat`: Disconnect gagal dan sesi tetap hidup |
| 5 Password salah | keduanya | Klien ditolak (exit 1) dan tidak ada sesi di CHR |
| 6 Isolasi NAT | nat | Host di `lab-wan` tidak bisa menjangkau API CHR. CHR bisa memanggil gobill (`/tool fetch`) |
| 7 gobill sebagai service | keduanya | Healthcheck sehat. Setelah `kill -9`, gobill dijalankan ulang (restart count 1), sehat lagi, dan data tetap ada |
| 8 Ganti paket saat online | direct | Edit paket RADIUS mengirim CoA-Request (Mikrotik-Rate-Limit) ke sesi online. RouterOS 7.24.5 menerapkan rate baru tanpa memutus sesi (uptime tidak reset). Jika CoA gagal, gobill memutus sesi dengan Disconnect-Request dan tes memeriksa login ulang mendapat rate baru |
| 9 Pin sertifikat API-SSL | direct | Sertifikat self-signed di CHR, router diubah ke port 8729. Koneksi pertama mencatat sidik jari; setelah sertifikat diganti koneksi ditolak; setelah "Reset certificate pin" tersambung lagi dengan sidik jari baru. Router dikembalikan ke port 8728 |
| 10 FUP | direct | Paket RADIUS dengan batas data 1 MB dan bandwidth FUP 5M. Pemakaian lebih dari batas disisipkan lewat `lab-seed -sql` (tanpa lalu lintas data di lab). Dalam 1 menit CoA menurunkan rate sesi online ke 5M, sesi tetap hidup dan uptime tidak reset; login ulang langsung mendapat rate FUP |
| 11 Dorong semua | direct | `POST /admin/routers/1/sync` setelah profil dan secret dihapus dari CHR: status selesai, 15/15 berhasil, profil dan pelanggan aktif kembali ada, dorongan kedua tidak mengubah apa pun |

Hasil terakhir: `direct` 43/43 dan `nat` 16/16.

## Batasan

- Lisensi gratis CHR membatasi throughput 1 Mbps. Lab ini menguji kontrol, bukan kecepatan.
- Podman rootless tidak bisa membuka `/dev/ppp`, jadi `pppd` tidak bisa jalan. Klien PPPoE adalah program Go userspace lewat `AF_PACKET`. Ia mencakup PADI sampai PADS, LCP, PAP/CHAP, dan IPCP, tetapi tidak membuat interface `ppp0`, sehingga lalu lintas data tidak diuji.
- `podman kill` dihitung sebagai stop manual dan tidak memicu restart. Tes 7 memakai `kill -9` pada PID gobill untuk meniru crash.
- Container CHR memakai `--security-opt label=disable` supaya SELinux mengizinkan `/dev/kvm`.
- Mode `nat` menunjukkan bahwa CoA tidak bisa mencapai NAS di belakang NAT: Disconnect gagal. Solusinya tunnel, lihat [Setup MikroTik](../docs/id/mikrotik.md).
- CHR mengirim Accounting-On dan Start dalam milidetik yang sama pada accounting pertamanya. gobill bisa memproses Start lebih dulu lalu menutupnya dengan Accounting-On, sehingga sesi hidup tampil sudah berhenti. Satu login pemanasan setelah `up` menyerap kejadian ini. `LAB_WARMUP=0` mematikannya untuk mereproduksi masalahnya.
- Butuh KVM. Di mesin tanpa `/dev/kvm` CHR tidak akan boot dalam waktu wajar.

Job [lab.yml](../.github/workflows/lab.yml) menjalankan lab ini setiap malam di GitHub Actions untuk kedua mode.
