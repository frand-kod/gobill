package adminx

// Static QRIS upload (the admin posts a photo, only its text is kept) and the public amount-locked QRIS page
// linked from the recharge WhatsApp message.

import (
	"bytes"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"strings"

	"github.com/frand-kod/gobill/internal/qris"
	"github.com/makiuchi-d/gozxing"
	zxqr "github.com/makiuchi-d/gozxing/qrcode"
)

// QrisUpload reads the QRIS photo posted as qris_image into v["qris_payload"]. It returns the error
// text to show, or "" when nothing was posted or it was read.
func QrisUpload(r *http.Request, v map[string]string) string {
	if r.MultipartForm == nil || len(r.MultipartForm.File["qris_image"]) == 0 {
		return ""
	}
	fh := r.MultipartForm.File["qris_image"][0]
	if fh.Size > MaxUpload {
		return "File must be 2 MB or less"
	}
	f, err := fh.Open()
	if err != nil {
		return "QRIS image could not be read"
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, MaxUpload))
	if err != nil {
		return "QRIS image could not be read"
	}
	if t := http.DetectContentType(b); t != "image/png" && t != "image/jpeg" {
		return "Use a PNG or JPG image"
	}
	text, err := QrisFromImage(b)
	if err != nil {
		return err.Error()
	}
	v["qris_payload"] = text
	return ""
}

// QrisFromImage decodes the QR code in a PNG or JPEG and returns its text when it is a valid QRIS.
func QrisFromImage(b []byte) (string, error) {
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
	if err := qris.Valid(res.GetText()); err != nil {
		return "", errors.New("Not a valid QRIS")
	}
	return strings.TrimSpace(res.GetText()), nil
}
