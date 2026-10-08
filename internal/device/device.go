// Package device drives the network gear (MikroTik) that enforces a customer's plan.
package device

import "context"

// Customer holds only what the drivers send to the router.
type Customer struct {
	Username string
	Password string // plaintext secret, already decrypted by the caller
	FullName string
	Email    string
	Bills    []string // plan names, goes into the router comment

	// PPPoE only; empty means "use Username/Password".
	PPPoEUsername string
	PPPoEPassword string
	PPPoEIP       string // static remote-address
}

// Plan holds only what the drivers send to the router.
type Plan struct {
	Name        string
	Type        string // "Hotspot" or "PPPoE"
	SharedUsers int

	// Bandwidth (tbl_bandwidth). Units are "Kbps" or "Mbps". Burst is a raw RouterOS suffix.
	RateUp, RateDown         int
	RateUpUnit, RateDownUnit string
	Burst                    string

	// Hotspot quota, used when Limited.
	Limited   bool
	LimitType string // Time_Limit, Data_Limit, Both_Limit
	TimeLimit int
	TimeUnit  string // Hrs or Mins
	DataLimit int
	DataUnit  string // GB or MB

	// PPPoE pool.
	Pool        string
	PoolLocalIP string // optional; falls back to Pool

	OnLogin, OnLogout string // router scripts, set on UpdatePlan only

	// ExpiredPlan is the profile an expired customer is moved to instead of being deleted.
	ExpiredPlan *Plan
	// IsExpiredProfile is true when this plan is some other plan's ExpiredPlan.
	IsExpiredProfile bool
}

type Device interface {
	AddCustomer(ctx context.Context, c Customer, p Plan) error
	RemoveCustomer(ctx context.Context, c Customer, p Plan) error
	ChangeUsername(ctx context.Context, p Plan, from, to string) error
	AddPlan(ctx context.Context, p Plan) error
	UpdatePlan(ctx context.Context, oldName string, p Plan) error
	RemovePlan(ctx context.Context, p Plan) error
	IsOnline(ctx context.Context, c Customer, router string) (bool, error)
	Connect(ctx context.Context, c Customer, ip, mac, router string) error
	Disconnect(ctx context.Context, c Customer, router string) error
}

// Dummy does nothing; for setups without a router.
type Dummy struct{}

func (Dummy) AddCustomer(context.Context, Customer, Plan) error               { return nil }
func (Dummy) RemoveCustomer(context.Context, Customer, Plan) error            { return nil }
func (Dummy) ChangeUsername(context.Context, Plan, string, string) error      { return nil }
func (Dummy) AddPlan(context.Context, Plan) error                             { return nil }
func (Dummy) UpdatePlan(context.Context, string, Plan) error                  { return nil }
func (Dummy) RemovePlan(context.Context, Plan) error                          { return nil }
func (Dummy) IsOnline(context.Context, Customer, string) (bool, error)        { return false, nil }
func (Dummy) Connect(context.Context, Customer, string, string, string) error { return nil }
func (Dummy) Disconnect(context.Context, Customer, string) error              { return nil }
