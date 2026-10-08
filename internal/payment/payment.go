// Package payment defines the gateway interface and implementations.
// It has no DB dependency; callers persist Transaction state themselves.
package payment

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

type Status string

const (
	Pending Status = "pending"
	Paid    Status = "paid"
	Failed  Status = "failed"
	Expired Status = "expired"
)

// Transaction is a payment request. Customer fields are folded in (instead of a
// separate Customer arg) so the package stays DB-free.
type Transaction struct {
	ID        string // merchant reference, unique per payment
	Reference string // gateway reference, set after create (needed by Status)
	Amount    int64  // rupiah
	PlanName  string
	Name      string
	Email     string
	Phone     string
	ReturnURL string
	Channel   string // overrides gateway default channel when set
	Expiry    time.Time
}

type Channel struct {
	Code   string
	Name   string
	Group  string
	Active bool
}

// Created is the result of CreateTransaction. Reference must be stored by the
// caller because Tripay's detail endpoint is keyed by it. (Interface change vs
// docs: payURL alone is not enough to poll Status later.)
type Created struct {
	PayURL    string
	Reference string
}

type PaymentGateway interface {
	Name() string
	ValidateConfig(cfg map[string]string) error
	CreateTransaction(ctx context.Context, trx Transaction) (Created, error)
	Status(ctx context.Context, trx Transaction) (Status, error)
	// HandleCallback must verify the signature; error means the caller answers 4xx.
	HandleCallback(r *http.Request) (trxID string, st Status, err error)
}

// Registry maps gateway name to instance; filled in main.go.
type Registry map[string]PaymentGateway

func (r Registry) Get(name string) (PaymentGateway, error) {
	g, ok := r[name]
	if !ok {
		return nil, fmt.Errorf("payment: unknown gateway %q", name)
	}
	return g, nil
}
