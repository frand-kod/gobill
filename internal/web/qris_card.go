package web

// The QRIS card: the one way a QRIS code is shown to a customer (the /qris page, the isolir page and
// its preview). It names the company and the QRIS merchant so the customer can check the payee in
// their app, and it prints on its own.

import (
	"bytes"
	"fmt"
	"html/template"
	"strings"

	"github.com/frand-kod/gobill/internal/payment"
)

// qrisCardSrc is the card. Its colors are fixed (a white card with a white QR tile), so it reads the
// same in light and dark mode. Only the card prints: the print rule hides the rest of the page.
const qrisCardSrc = `<style>
.qris-card{max-width:360px;margin:0 auto;background:#fff;color:#18181b;border:1px solid #d4d4d8;border-radius:14px;overflow:hidden;font:16px/1.4 system-ui,-apple-system,"Segoe UI",sans-serif;text-align:center}
.qris-card *{box-sizing:border-box}
.qc-head{background:#1f2937;color:#fff;padding:10px 16px;font-weight:800;letter-spacing:.25em}
.qc-body{padding:16px}
.qc-title{font-size:1.15rem;font-weight:700;margin:0 0 4px}
.qc-amount{font-size:1.9rem;font-weight:800;margin:8px 0 0}
.qc-lock{font-size:.85rem;color:#3f3f46;margin:0 0 10px}
.qc-tile{background:#fff;padding:14px;border:1px solid #e4e4e7;border-radius:10px;display:inline-block}
.qc-tile img{display:block;width:240px;max-width:100%;height:auto}
.qc-ref{font-size:.9rem;color:#3f3f46;margin:10px 0 0}
.qc-instr{font-size:.9rem;margin:12px 0 0;text-align:left}
.qc-details{font-size:.8rem;color:#3f3f46;margin:12px 0 0;text-align:left;display:grid;grid-template-columns:auto 1fr;gap:2px 10px}
.qc-details dt{font-weight:600}.qc-details dd{margin:0}
.qc-sample{font-size:.85rem;font-weight:700;color:#b45309;margin:10px 0 0}
.qc-actions{display:flex;gap:8px;margin:14px 0 0}
.qc-actions a,.qc-actions button{flex:1;font:600 .95rem system-ui,sans-serif;border-radius:8px;padding:10px;border:1px solid #d4d4d8;background:#fff;color:#18181b;text-decoration:none;cursor:pointer;text-align:center}
.qc-foot{background:#f4f4f5;color:#3f3f46;font-size:.8rem;padding:10px 16px;border-top:1px solid #e4e4e7}
.qc-foot a{color:#15803d;font-weight:600}
@media print{
body *{visibility:hidden !important}
.qris-card,.qris-card *{visibility:visible !important}
.qris-card{position:absolute;left:0;top:0;width:100%;max-width:none;border:0;border-radius:0}
.qc-actions{display:none !important}
}
</style>
<section class="qris-card">
<div class="qc-head">QRIS</div>
<div class="qc-body">
<h2 class="qc-title">{{.Title}}</h2>
<p class="qc-amount">{{.Amount}}</p>
<p class="qc-lock">{{T "Amount locked"}}</p>
<div class="qc-tile"><img src="{{.QR}}" width="240" height="240" alt="QRIS {{.Amount}}"></div>
{{if .Ref}}<p class="qc-ref">{{.Ref}}</p>{{end}}
<p class="qc-instr">{{T "Scan with any bank or e-wallet app that supports QRIS."}} {{.Check}}</p>
<dl class="qc-details">
<dt>{{T "Merchant"}}</dt><dd>{{.Merchant}}</dd>
{{if .City}}<dt>{{T "City"}}</dt><dd>{{.City}}</dd>{{end}}
{{if .NMID}}<dt>NMID</dt><dd>{{.NMID}}</dd>{{end}}
{{if .Provider}}<dt>{{T "Provider"}}</dt><dd>{{.Provider}}</dd>{{end}}
</dl>
{{if .Sample}}<p class="qc-sample">{{T "Sample QR, not payable"}}</p>{{end}}
<div class="qc-actions"><a href="{{.QR}}" download="qris.png">{{T "Save image"}}</a><button type="button" onclick="window.print()">{{T "Save / print"}}</button></div>
</div>
{{if .Support}}<footer class="qc-foot">{{.Company}} · <a href="{{.Support}}" rel="noopener">{{T "Support on WhatsApp"}}</a></footer>{{else}}<footer class="qc-foot">{{.Company}}</footer>{{end}}
</section>`

// qrisCardData is what the card shows. Check is the line that names the payee.
type qrisCardData struct {
	Title, Amount, Ref, Check, Merchant, City, NMID, Provider, Company, Support string
	QR                                                                          template.URL
	Sample                                                                      bool
}

// qrisCard renders the card for a QRIS code (qr is its data URI, amount the locked price). ref is
// the invoice or customer reference, "" when none. details are the parsed merchant fields of the
// static code. Sample marks the dummy code of the preview. "" when the card cannot be made.
func (s *Server) qrisCard(st map[string]string, details payment.QRISDetails, qr template.URL, amount, ref string, sample bool) template.HTML {
	t := func(text string) string { return s.catalog.T(s.language(), text) }
	company := strings.TrimSpace(st["company_name"])
	d := qrisCardData{
		Title: fmt.Sprintf(t("Payment to %s"), company), Amount: amount, Ref: ref,
		Merchant: details.MerchantName, City: details.City, NMID: details.NMID, Provider: details.Provider,
		Company: company, QR: qr, Sample: sample,
	}
	if phone := waLink(st["phone"], "62"); phone != "" {
		d.Support = phone
	}
	if details.MerchantName == "" || strings.EqualFold(company, strings.TrimSpace(details.MerchantName)) {
		d.Check = fmt.Sprintf(t("Make sure your app shows %s before paying."), company)
	} else {
		d.Check = fmt.Sprintf(t("Your app will show the merchant name %s — this is the registered QRIS name of %s. If it shows another name, do not pay and contact us."), details.MerchantName, company)
	}
	tpl, err := template.New("qris").Funcs(template.FuncMap{"T": t}).Parse(qrisCardSrc)
	if err != nil {
		return ""
	}
	var b bytes.Buffer
	if err := tpl.Execute(&b, d); err != nil {
		return ""
	}
	return template.HTML(b.String())
}
