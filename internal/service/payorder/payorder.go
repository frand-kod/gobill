// Package payorder starts an online payment for a customer: the pending row, the gateway transaction and
// the pay URL. The customer portal and the chat tools both call it, so they create the same order.
package payorder

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/payment"
)

// Expiry is how long a payment request stays open.
const Expiry = 24 * time.Hour

// ErrGateway: the gateway failed to create the transaction. The request row is closed as failed.
var ErrGateway = errors.New("payment gateway error")

// Order is one order. PlanID 0 = custom balance top-up. The amount is final (tax and coupon applied by the caller).
type Order struct {
	Customer     db.Customer
	PlanID       int64
	PlanName     string
	Amount       int64
	Coupon       string
	Channel      string
	ReturnBase   string // address the customer returns to: ReturnBase + "/portal/payments/<id>"
	CallbackBase string // public address of the app: CallbackBase + "/callback/<gateway>"
}

// Create stores the pending payment, creates the gateway transaction and saves its pay URL.
// A coupon is released when the row cannot be stored. A gateway failure returns ErrGateway.
func Create(ctx context.Context, q *db.Queries, g payment.PaymentGateway, o Order) (db.PaymentRequest, error) {
	raw := make([]byte, 8)
	rand.Read(raw)
	exp := time.Now().Add(Expiry)
	c := o.Customer
	pr, err := q.CreatePaymentRequest(ctx, db.CreatePaymentRequestParams{Ref: "NB" + strings.ToUpper(hex.EncodeToString(raw)),
		Gateway: g.Name(), CustomerID: sql.NullInt64{Int64: c.ID, Valid: true}, Username: c.Username, PlanID: o.PlanID, Amount: o.Amount, Coupon: o.Coupon, Channel: o.Channel, ExpiresAt: exp.Unix()})
	if err != nil {
		if o.Coupon != "" {
			q.ReleaseCoupon(ctx, o.Coupon)
		}
		return pr, err
	}
	cr, err := g.CreateTransaction(ctx, payment.Transaction{ID: pr.Ref, Amount: o.Amount, PlanName: o.PlanName, Name: c.Fullname,
		Email: c.Email, Phone: c.Phone, ReturnURL: o.ReturnBase + "/portal/payments/" + strconv.FormatInt(pr.ID, 10), CallbackURL: o.CallbackBase + "/callback/" + g.Name(), Channel: o.Channel, Expiry: exp})
	if err != nil {
		slog.Error("pay: create transaction", "ref", pr.Ref, "err", err)
		q.ClosePaymentRequest(ctx, db.ClosePaymentRequestParams{Status: "failed", Ref: pr.Ref})
		return pr, ErrGateway
	}
	if err := q.SetPaymentGatewayRef(ctx, db.SetPaymentGatewayRefParams{GatewayRef: cr.Reference, PayUrl: cr.PayURL, ID: pr.ID}); err != nil {
		return pr, err
	}
	pr.GatewayRef, pr.PayUrl = cr.Reference, cr.PayURL
	return pr, nil
}
