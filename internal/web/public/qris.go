package public

// Static QRIS upload (the admin posts a photo, only its text is kept) and the public amount-locked QRIS page
// linked from the recharge WhatsApp message.

import (
	"crypto/hmac"
	"encoding/base64"
	"html/template"
	"net/http"
	"strconv"

	"github.com/frand-kod/gobill/internal/web/core"
	"github.com/frand-kod/gobill/internal/web/view"

	"github.com/frand-kod/gobill/internal/qris"
	"github.com/skip2/go-qrcode"
)

type qrisData struct {
	Card template.HTML // the QRIS card, see qrisCard
}

func (s *Handlers) qrisPage(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || !hmac.Equal([]byte(r.PathValue("token")), []byte(qris.Token(s.SecretKey, id))) {
		http.NotFound(w, r)
		return
	}
	st, err := s.LoadSettings(r.Context())
	if err != nil {
		s.Fail(w, "qris settings", err)
		return
	}
	t, err := s.Queries.GetTransaction(r.Context(), id)
	if err != nil || st["qris_payload"] == "" || t.Price <= 0 {
		http.NotFound(w, r)
		return
	}
	code, err := qris.Amount(st["qris_payload"], t.Price)
	if err != nil {
		s.Fail(w, "qris", err)
		return
	}
	png, err := qrcode.Encode(code, qrcode.Medium, 512)
	if err != nil {
		s.Fail(w, "qris png", err)
		return
	}
	qr := template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(png))
	d := qrisData{Card: s.qrisCard(st, qris.DetailsOf(st["qris_payload"]), qr, view.Money(t.Price), t.Invoice+" · "+t.PlanName, false)}
	s.Render(w, r, 200, "p_qris", core.Page{Title: "Pay via QRIS", Data: d})
}
