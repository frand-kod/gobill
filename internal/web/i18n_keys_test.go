package web

import (
	"io/fs"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	gobill "github.com/frand-kod/gobill"
	"github.com/frand-kod/gobill/internal/i18n"
)

// uiKeyPatterns find the English keys of the UI in the Go sources: catalog lookups, the field
// labels, hints, buttons and titles of the forms, and the flash messages of done and flashTo.
var uiKeyPatterns = []*regexp.Regexp{
	regexp.MustCompile(`catalog\.T\(\w+, "((?:[^"\\]|\\.)*)"`),
	regexp.MustCompile(`\b(?:Label|Hint|Btn|Title|Section|Notice|Confirm):\s*"((?:[^"\\]|\\.)*)"`),
	regexp.MustCompile(`\.hint\("((?:[^"\\]|\\.)*)"`),
	regexp.MustCompile(`\bs\.(?:done|flashTo)\(w, r, [^,]+, "((?:[^"\\]|\\.)*)"`),
	// field constructors take the label second: text("name", "Label", v, e)
	regexp.MustCompile(`\b(?:text|sel|area|upl|chk|notif)\("[^"]*", "((?:[^"\\]|\\.)*)"`),
	// section titles follow the field list: }, "Title", "")
	regexp.MustCompile(`\}, "((?:[^"\\]|\\.)*)", ""\)`),
}

// indonesianWords are function words and UI nouns that never appear in English UI text.
var indonesianWords = regexp.MustCompile(`(?i)\b(dan|yang|untuk|tidak|dengan|dari|sudah|belum|gagal|berhasil|silakan|silahkan|simpan|hapus|tambah|pelanggan|paket|di|ke|bisa|mohon|kembali|pengaturan|kadaluarsa|kedaluwarsa|terlalu|tersedia|aktif|nonaktif|bawaan|kosong|atau|saat|jika|agar|akan|ada|lihat|ubah|bila|oleh|pada|hanya|semua|tampilkan|pilih|gunakan|dipakai|kirim|terakhir|baru|hari|sesi|akun|kode|isi|ulang|huruf|angka|garis|bawah|contoh|maks|wajib|diisi|tulis|sembunyikan|saldo|tagihan|langganan|pesan|nomor|alamat|sandi|dihapus|disimpan|diubah|dibuat|dikirim|diaktifkan|mulai|kecepatan|tanggal|jumlah|harga|keterangan|catatan|laporan|pembayaran|unggah|gambar|tanpa|sendiri|lalu|sebelum|setelah|bagian|halaman|bulan|tahun|menit|bukan|diperbarui|ditemukan|menghubungi|hubungi)\b`)

// uiKeyAllow lists keys that look Indonesian but are technical or brand names.
var uiKeyAllow = map[string]bool{
	"github.com/aldinokemal/go-whatsapp-web-multidevice": true, // GOWA project link
}

// uiKeysInSources returns the UI keys of the templates and the internal/web Go sources.
func uiKeysInSources(t *testing.T) map[string]string {
	t.Helper()
	keys := map[string]string{}
	tplRe := regexp.MustCompile(`\bTT? "([^"]+)"`)
	fs.WalkDir(gobill.FS, "web/templates", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".html") {
			return err
		}
		b, _ := fs.ReadFile(gobill.FS, p)
		for _, m := range tplRe.FindAllStringSubmatch(string(b), -1) {
			keys[m[1]] = p
		}
		return nil
	})
	// internal/ is not embedded, so the Go sources are read from the package directory (go test runs there).
	fs.WalkDir(os.DirFS("."), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return err
		}
		b, _ := fs.ReadFile(os.DirFS("."), p)
		for _, re := range uiKeyPatterns {
			for _, m := range re.FindAllStringSubmatch(string(b), -1) {
				if k, err := strconv.Unquote(`"` + m[1] + `"`); err == nil {
					keys[k] = p
				}
			}
		}
		return nil
	})
	return keys
}

// Every literal {{T "..."}} / {{TT "..."}} in the templates and every UI literal in the Go sources
// must exist in lang/indonesia.json (exact or sanitized key, as T looks them up).
func TestTemplateKeysTranslated(t *testing.T) {
	cat, err := i18n.Load(gobill.FS, "lang")
	if err != nil {
		t.Fatal(err)
	}
	id := cat["indonesia"]
	nonWord := regexp.MustCompile(`[^A-Za-z0-9]`)
	for k, src := range uiKeysInSources(t) {
		if uiKeyAllow[k] || !strings.ContainsAny(k, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ") {
			continue
		}
		if id[k] == "" && id[nonWord.ReplaceAllString(k, "_")] == "" {
			t.Errorf("%s: %q missing from lang/indonesia.json", src, k)
		}
	}
}

// UI keys are English: no key may read as Indonesian, and the English catalog must not turn one into
// Indonesian text (a key with no english.json entry shows as itself, which is fine).
func TestUIKeysAreEnglish(t *testing.T) {
	cat, err := i18n.Load(gobill.FS, "lang")
	if err != nil {
		t.Fatal(err)
	}
	for k, src := range uiKeysInSources(t) {
		if uiKeyAllow[k] {
			continue
		}
		if m := indonesianWords.FindString(k); m != "" {
			t.Errorf("%s: key %q looks Indonesian (%q)", src, k, m)
		}
		if v := cat.T("english", k); indonesianWords.MatchString(v) && !uiKeyAllow[v] {
			t.Errorf("%s: english text for %q looks Indonesian: %q", src, k, v)
		}
	}
}

// The English UI renders the admin pages with no Indonesian text. Stored values (textarea and input
// values such as the default isolir message) and scripts are left out; only the visible text is checked.
func TestEnglishAdminPagesHaveNoIndonesian(t *testing.T) {
	s, _ := newTestApp(t)
	s.lang.Store("english")
	h := s.Handler()
	cookie := login(t, h, "alice")
	visible := regexp.MustCompile(`(?s)<textarea.*?</textarea>|<script.*?</script>|<style.*?</style>|<[^>]*>`)
	for _, page := range []string{"/admin", "/admin/customers", "/admin/customers/new", "/admin/plans", "/admin/plans/new",
		"/admin/settings/app", "/admin/settings/isolir", "/admin/settings/notifications", "/admin/settings/promo", "/admin/setup/isolir", "/admin/reports",
		"/admin/transactions", "/admin/subscriptions", "/admin/recharge", "/admin/vouchers/new", "/admin/payment-gateway"} {
		w := do(h, "GET", page, nil, cookie)
		if w.Code != http.StatusOK {
			t.Errorf("%s: status %d", page, w.Code)
			continue
		}
		text := visible.ReplaceAllString(w.Body.String(), " ")
		if m := indonesianWords.FindStringIndex(text); m != nil {
			from := max(0, m[0]-60)
			t.Errorf("%s: Indonesian text in the English UI: %q", page, text[from:min(len(text), m[1]+60)])
		}
	}
}
