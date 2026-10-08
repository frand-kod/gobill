package payment

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type Tripay struct {
	APIKey, PrivateKey, MerchantCode string
	DefaultChannel                   string
	BaseURL                          string // overridable for tests
	HTTPClient                       *http.Client
}

var _ PaymentGateway = (*Tripay)(nil)

// NewTripay builds from config keys api_key, private_key, merchant_code,
// mode (sandbox|production), channel.
func NewTripay(cfg map[string]string) (*Tripay, error) {
	t := &Tripay{}
	if err := t.ValidateConfig(cfg); err != nil {
		return nil, err
	}
	t.APIKey, t.PrivateKey, t.MerchantCode = cfg["api_key"], cfg["private_key"], cfg["merchant_code"]
	t.DefaultChannel = cfg["channel"]
	t.BaseURL = "https://tripay.co.id/api"
	if cfg["mode"] != "production" {
		t.BaseURL = "https://tripay.co.id/api-sandbox"
	}
	return t, nil
}

func (t *Tripay) Name() string { return "tripay" }

func (t *Tripay) ValidateConfig(cfg map[string]string) error {
	for _, k := range []string{"api_key", "private_key", "merchant_code"} {
		if cfg[k] == "" {
			return fmt.Errorf("tripay: missing %s", k)
		}
	}
	if m := cfg["mode"]; m != "" && m != "sandbox" && m != "production" {
		return fmt.Errorf("tripay: mode must be sandbox or production, got %q", m)
	}
	return nil
}

func (t *Tripay) sign(s string) string {
	h := hmac.New(sha256.New, []byte(t.PrivateKey))
	h.Write([]byte(s))
	return hex.EncodeToString(h.Sum(nil))
}

// do sends the request and decodes the {success,message,data} envelope into data.
func (t *Tripay) do(ctx context.Context, method, path string, body any, data any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, t.BaseURL+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+t.APIKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	c := t.HTTPClient
	if c == nil {
		c = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var env struct {
		Success bool            `json:"success"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("tripay: http %d, bad response", resp.StatusCode)
	}
	if !env.Success {
		return fmt.Errorf("tripay: %s (http %d)", env.Message, resp.StatusCode)
	}
	return json.Unmarshal(env.Data, data)
}

func (t *Tripay) CreateTransaction(ctx context.Context, trx Transaction) (Created, error) {
	ch := trx.Channel
	if ch == "" {
		ch = t.DefaultChannel
	}
	if ch == "" {
		return Created{}, errors.New("tripay: no payment channel")
	}
	body := map[string]any{
		"method":         ch,
		"merchant_ref":   trx.ID,
		"amount":         trx.Amount,
		"customer_name":  trx.Name,
		"customer_email": trx.Email,
		"customer_phone": trx.Phone,
		"order_items": []map[string]any{
			{"name": trx.PlanName, "price": trx.Amount, "quantity": 1},
		},
		"return_url": trx.ReturnURL,
		"signature":  t.sign(t.MerchantCode + trx.ID + strconv.FormatInt(trx.Amount, 10)),
	}
	if !trx.Expiry.IsZero() {
		body["expired_time"] = trx.Expiry.Unix()
	}
	var d struct {
		Reference   string `json:"reference"`
		CheckoutURL string `json:"checkout_url"`
	}
	if err := t.do(ctx, "POST", "/transaction/create", body, &d); err != nil {
		return Created{}, err
	}
	if d.CheckoutURL == "" {
		return Created{}, errors.New("tripay: empty checkout_url")
	}
	return Created{PayURL: d.CheckoutURL, Reference: d.Reference}, nil
}

func mapStatus(s string) (Status, error) {
	switch s {
	case "UNPAID":
		return Pending, nil
	case "PAID":
		return Paid, nil
	case "FAILED", "REFUND":
		return Failed, nil
	case "EXPIRED":
		return Expired, nil
	}
	return "", fmt.Errorf("tripay: unknown status %q", s)
}

func (t *Tripay) Status(ctx context.Context, trx Transaction) (Status, error) {
	var d struct {
		Status string `json:"status"`
	}
	if err := t.do(ctx, "GET", "/transaction/detail?reference="+url.QueryEscape(trx.Reference), nil, &d); err != nil {
		return "", err
	}
	return mapStatus(d.Status)
}

func (t *Tripay) Channels(ctx context.Context) ([]Channel, error) {
	var d []struct {
		Code   string `json:"code"`
		Name   string `json:"name"`
		Group  string `json:"group"`
		Active bool   `json:"active"`
	}
	if err := t.do(ctx, "GET", "/merchant/payment-channel", nil, &d); err != nil {
		return nil, err
	}
	out := make([]Channel, len(d))
	for i, c := range d {
		out[i] = Channel(c)
	}
	return out, nil
}

func (t *Tripay) HandleCallback(r *http.Request) (string, Status, error) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return "", "", err
	}
	got, _ := hex.DecodeString(r.Header.Get("X-Callback-Signature"))
	want, _ := hex.DecodeString(t.sign(string(raw)))
	if len(got) == 0 || !hmac.Equal(got, want) {
		return "", "", errors.New("tripay: bad callback signature")
	}
	if ev := r.Header.Get("X-Callback-Event"); ev != "payment_status" {
		return "", "", fmt.Errorf("tripay: unexpected event %q", ev)
	}
	var p struct {
		MerchantRef string `json:"merchant_ref"`
		Status      string `json:"status"`
	}
	if err := json.Unmarshal(raw, &p); err != nil || p.MerchantRef == "" {
		return "", "", errors.New("tripay: bad callback body")
	}
	st, err := mapStatus(p.Status)
	return p.MerchantRef, st, err
}
