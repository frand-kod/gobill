package web

// Static QRIS upload (the admin posts a photo, only its text is kept) and the public amount-locked QRIS page
// linked from the recharge WhatsApp message.

import (
	"bytes"
	"crypto/hmac"
	"encoding/base64"
	"errors"
	"html/template"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/frand-kod/gobill/internal/payment"
	"github.com/makiuchi-d/gozxing"
	zxqr "github.com/makiuchi-d/gozxing/qrcode"
	"github.com/skip2/go-qrcode"
)

// qrisUpload reads the QRIS photo posted as qris_image into v["qris_payload"]. It returns the error
// text to show, or "" when nothing was posted or it was read.
func qrisUpload(r *http.Request, v map[string]string) string {
	if r.MultipartForm == nil || len(r.MultipartForm.File["qris_image"]) == 0 {
		return ""
	}
	fh := r.MultipartForm.File["qris_image"][0]
	if fh.Size > maxUpload {
		return "File must be 2 MB or less"
	}
	f, err := fh.Open()
	if err != nil {
		return "QRIS image could not be read"
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxUpload))
	if err != nil {
		return "QRIS image could not be read"
	}
	if t := http.DetectContentType(b); t != "image/png" && t != "image/jpeg" {
		return "Use a PNG or JPG image"
	}
	text, err := qrisFromImage(b)
	if err != nil {
		return err.Error()
	}
	v["qris_payload"] = text
	return ""
}

// qrisFromImage decodes the QR code in a PNG or JPEG and returns its text when it is a valid QRIS.
func qrisFromImage(b []byte) (string, error) {
	img, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return "", errors.New("QRIS image could not be read")
	}
	bmp, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		return "", errors.New("QRIS image could not be read")
	}
	res, err := zxqr.NewQRCodeReader().Decode(bmp, map[gozxing.DecodeHintType]interface{}{gozxing.DecodeHintType_TRY_HARDER: true})
	if err != nil {
		return "", errors.New("QRIS image could not be read")
	}
	if err := payment.QRISValid(res.GetText()); err != nil {
		return "", errors.New("Not a valid QRIS")
	}
	return strings.TrimSpace(res.GetText()), nil
}

type qrisData struct {
	Invoice, Plan, Amount string
	QR                    template.URL
}

func (s *Server) qrisPage(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || !hmac.Equal([]byte(r.PathValue("token")), []byte(payment.QRISToken(s.SecretKey, id))) {
		http.NotFound(w, r)
		return
	}
	st, err := s.loadSettings(r.Context())
	if err != nil {
		s.fail(w, "qris settings", err)
		return
	}
	t, err := s.queries.GetTransaction(r.Context(), id)
	if err != nil || st["qris_payload"] == "" || t.Price <= 0 {
		http.NotFound(w, r)
		return
	}
	code, err := payment.QRISAmount(st["qris_payload"], t.Price)
	if err != nil {
		s.fail(w, "qris", err)
		return
	}
	png, err := qrcode.Encode(code, qrcode.Medium, 512)
	if err != nil {
		s.fail(w, "qris png", err)
		return
	}
	d := qrisData{t.Invoice, t.PlanName, money(t.Price), template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(png))}
	s.render(w, r, 200, "p_qris", Page{Title: "Bayar via QRIS", Data: d})
}
