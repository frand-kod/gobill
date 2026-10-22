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

// Duitku signs every endpoint with HMAC-SHA256 keyed by the API key (the MD5 and
// plain SHA256 schemes are obsolete since April 2026).
type Duitku struct {
	MerchantCode, APIKey string
	DefaultChannel       string
	BaseURL              string // overridable for tests
	HTTPClient           *http.Client
}

var _ PaymentGateway = (*Duitku)(nil)

// NewDuitku builds from config keys merchant_code, api_key, mode (sandbox|production), channel.
func NewDuitku(cfg map[string]string) (*Duitku, error) {
	d := &Duitku{}
	if err := d.ValidateConfig(cfg); err != nil {
		return nil, err
	}
	d.MerchantCode, d.APIKey, d.DefaultChannel = cfg["merchant_code"], cfg["api_key"], cfg["channel"]
	d.BaseURL = "https://passport.duitku.com/webapi/api/merchant"
	if cfg["mode"] != "production" {
		d.BaseURL = "https://sandbox.duitku.com/webapi/api/merchant"
	}
	return d, nil
}

func (d *Duitku) Name() string { return "duitku" }

func (d *Duitku) ValidateConfig(cfg map[string]string) error {
	for _, k := range []string{"merchant_code", "api_key"} {
		if cfg[k] == "" {
			return fmt.Errorf("duitku: missing %s", k)
		}
	}
	if m := cfg["mode"]; m != "" && m != "sandbox" && m != "production" {
		return fmt.Errorf("duitku: mode must be sandbox or production, got %q", m)
	}
	return nil
}

func (d *Duitku) sign(s string) string {
	h := hmac.New(sha256.New, []byte(d.APIKey))
	h.Write([]byte(s))
	return hex.EncodeToString(h.Sum(nil))
}

// do POSTs a JSON body and decodes the reply into out. Errors come back as HTTP 4xx
// with a "Message" field; a 200 reply carries statusCode/responseCode "00" on success.
func (d *Duitku) do(ctx context.Context, path string, body, out any) error {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", d.BaseURL+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	c := d.HTTPClient
	if c == nil {
		c = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != 200 {
		var e struct {
			Message string `json:"Message"`
		}
		json.Unmarshal(raw, &e)
		return fmt.Errorf("duitku: %s (http %d)", e.Message, resp.StatusCode)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("duitku: http %d, bad response", resp.StatusCode)
	}
	return nil
}

func (d *Duitku) CreateTransaction(ctx context.Context, trx Transaction) (Created, error) {
	ch := trx.Channel
	if ch == "" {
		ch = d.DefaultChannel
	}
	if ch == "" {
		return Created{}, errors.New("duitku: no payment channel")
	}
	// Transaction has no callback URL field: derive it from the return URL's origin.
	ru, err := url.Parse(trx.ReturnURL)
	if err != nil || ru.Host == "" {
		return Created{}, errors.New("duitku: bad return url")
	}
	name := []rune(trx.Name)
	if len(name) > 20 { // customerVaName is limited to 20 characters
		name = name[:20]
	}
	body := map[string]any{
		"merchantCode":    d.MerchantCode,
		"paymentAmount":   trx.Amount,
		"paymentMethod":   ch,
		"merchantOrderId": trx.ID,
		"productDetails":  trx.PlanName,
		"customerVaName":  string(name),
		"email":           trx.Email,
		"phoneNumber":     trx.Phone,
		"returnUrl":       trx.ReturnURL,
		"callbackUrl":     ru.Scheme + "://" + ru.Host + "/callback/duitku",
		"signature":       d.sign(d.MerchantCode + trx.ID + strconv.FormatInt(trx.Amount, 10)),
	}
	if !trx.Expiry.IsZero() {
		body["expiryPeriod"] = max(1, int(time.Until(trx.Expiry).Minutes()))
	}
	var r struct {
		Reference  string `json:"reference"`
		PaymentURL string `json:"paymentUrl"`
		StatusCode string `json:"statusCode"`
		Message    string `json:"statusMessage"`
	}
	if err := d.do(ctx, "/v2/inquiry", body, &r); err != nil {
		return Created{}, err
	}
	if r.StatusCode != "00" {
		return Created{}, fmt.Errorf("duitku: %s", r.Message)
	}
	if r.PaymentURL == "" {
		return Created{}, errors.New("duitku: empty paymentUrl")
	}
	return Created{PayURL: r.PaymentURL, Reference: r.Reference}, nil
}

// Status is keyed by the merchant order id (trx.ID), not the gateway reference.
func (d *Duitku) Status(ctx context.Context, trx Transaction) (Status, error) {
	var r struct {
		StatusCode string `json:"statusCode"`
	}
	body := map[string]any{"merchantCode": d.MerchantCode, "merchantOrderId": trx.ID, "signature": d.sign(d.MerchantCode + trx.ID)}
	if err := d.do(ctx, "/transactionStatus", body, &r); err != nil {
		return "", err
	}
	switch r.StatusCode {
	case "00":
		return Paid, nil
	case "01":
		return Pending, nil
	case "02": // Duitku does not tell failed from expired
		return Failed, nil
	}
	return "", fmt.Errorf("duitku: unknown status %q", r.StatusCode)
}

// Channels lists the methods available for a small probe amount (the API requires one).
func (d *Duitku) Channels(ctx context.Context) ([]Channel, error) {
	now := time.Now().In(time.FixedZone("WIB", 7*3600)).Format("2006-01-02 15:04:05")
	body := map[string]any{"merchantcode": d.MerchantCode, "amount": 10000, "datetime": now,
		"signature": d.sign(d.MerchantCode + "10000" + now)}
	var r struct {
		Fees []struct {
			Method string `json:"paymentMethod"`
			Name   string `json:"paymentName"`
		} `json:"paymentFee"`
		Code string `json:"responseCode"`
		Msg  string `json:"responseMessage"`
	}
	if err := d.do(ctx, "/paymentmethod/getpaymentmethod", body, &r); err != nil {
		return nil, err
	}
	if r.Code != "00" {
		return nil, fmt.Errorf("duitku: %s", r.Msg)
	}
	out := make([]Channel, len(r.Fees))
	for i, f := range r.Fees {
		out[i] = Channel{Code: f.Method, Name: f.Name, Active: true}
	}
	return out, nil
}

// HandleCallback verifies the form-encoded notification. The signature covers
// merchantCode+amount+merchantOrderId, so the amount cannot be altered in transit.
func (d *Duitku) HandleCallback(r *http.Request) (string, Status, error) {
	r.Body = http.MaxBytesReader(nil, r.Body, 1<<20)
	if err := r.ParseForm(); err != nil {
		return "", "", err
	}
	code, amt, id := r.PostFormValue("merchantCode"), r.PostFormValue("amount"), r.PostFormValue("merchantOrderId")
	got, _ := hex.DecodeString(r.PostFormValue("signature"))
	want, _ := hex.DecodeString(d.sign(code + amt + id))
	if len(got) == 0 || !hmac.Equal(got, want) || code != d.MerchantCode {
		return "", "", errors.New("duitku: bad callback signature")
	}
	if id == "" {
		return "", "", errors.New("duitku: bad callback body")
	}
	switch rc := r.PostFormValue("resultCode"); rc {
	case "00":
		return id, Paid, nil
	case "01":
		return id, Failed, nil
	default:
		return "", "", fmt.Errorf("duitku: unknown resultCode %q", rc)
	}
}
