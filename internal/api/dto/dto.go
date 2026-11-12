// Package dto holds the JSON contract of /api/v1.
// Rules: snake_case tags, money as int64 smallest unit, times as unix seconds,
// pointer only for a value that may be absent (JSON null).
// DTOs never carry passwords, hashes, secrets, tokens or *_enc fields;
// TestNoSecretFields guards this. Never encode a db model directly.
package dto

import (
	"database/sql"

	"github.com/frand-kod/gobill/internal/db"
)

func i64(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	return &n.Int64
}

func str(n sql.NullString) *string {
	if !n.Valid {
		return nil
	}
	return &n.String
}

type Customer struct {
	ID            int64  `json:"id"`
	Username      string `json:"username"`
	Fullname      string `json:"fullname"`
	Address       string `json:"address"`
	Phone         string `json:"phone"`
	Email         string `json:"email"`
	Balance       int64  `json:"balance"`
	ServiceType   string `json:"service_type"`
	PppoeUsername string `json:"pppoe_username"`
	PppoeIP       string `json:"pppoe_ip"`
	BillingDay    *int64 `json:"billing_day"`
	AutoRenewal   bool   `json:"auto_renewal"`
	Status        string `json:"status"`
	CreatedBy     *int64 `json:"created_by"`
	CreatedAt     int64  `json:"created_at"`
	LastLoginAt   *int64 `json:"last_login_at"`
	Coordinates   string `json:"coordinates"`
}

// FromCustomer excludes PasswordHash, SecretEnc, SessionVersion.
func FromCustomer(m db.Customer) Customer {
	return Customer{m.ID, m.Username, m.Fullname, m.Address, m.Phone, m.Email, m.Balance,
		m.ServiceType, m.PppoeUsername, m.PppoeIp, i64(m.BillingDay), m.AutoRenewal != 0,
		m.Status, i64(m.CreatedBy), m.CreatedAt, i64(m.LastLoginAt), m.Coordinates}
}

type Admin struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	Fullname    string `json:"fullname"`
	Role        string `json:"role"`
	Status      string `json:"status"`
	Email       string `json:"email"`
	Phone       string `json:"phone"`
	City        string `json:"city"`
	RootID      *int64 `json:"root_id"`
	LastLoginAt *int64 `json:"last_login_at"`
	CreatedAt   int64  `json:"created_at"`
	TwoFactor   bool   `json:"two_factor"`
}

// FromAdmin excludes PasswordHash, LegacySha1, SessionVersion, TotpSecretEnc.
// TotpEnabled is exposed as two_factor.
func FromAdmin(m db.Admin) Admin {
	return Admin{m.ID, m.Username, m.Fullname, m.Role, m.Status, m.Email, m.Phone, m.City,
		i64(m.RootID), i64(m.LastLoginAt), m.CreatedAt, m.TotpEnabled != 0}
}

type NAS struct {
	ID                 int64  `json:"id"`
	Name               string `json:"name"`
	IP                 string `json:"ip"`
	Description        string `json:"description"`
	RequireMessageAuth bool   `json:"require_message_auth"`
	Type               string `json:"type"`
}

// FromNAS excludes SecretEnc and MaSeen.
func FromNAS(m db.Na) NAS {
	return NAS{m.ID, m.Name, m.Ip, m.Description, m.RequireMessageAuth != 0, m.Type}
}

type Router struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Host        string `json:"host"`
	Port        int64  `json:"port"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
	Coordinates string `json:"coordinates"`
	Coverage    int64  `json:"coverage"`
	LastSeenAt  *int64 `json:"last_seen_at"`
	Online      *bool  `json:"online"`
	TLS         bool   `json:"tls"`
}

// FromRouter excludes PasswordEnc, Username (the router API login) and CertPin.
func FromRouter(m db.Router) Router {
	var on *bool
	if m.Online.Valid {
		b := m.Online.Int64 != 0
		on = &b
	}
	return Router{m.ID, m.Name, m.Host, m.Port, m.Description, m.Enabled != 0, m.Coordinates,
		m.Coverage, i64(m.LastSeenAt), on, m.Tls != 0}
}

type Plan struct {
	ID               int64   `json:"id"`
	Name             string  `json:"name"`
	Type             string  `json:"type"`
	Billing          string  `json:"billing"`
	Price            int64   `json:"price"`
	Validity         int64   `json:"validity"`
	ValidityUnit     string  `json:"validity_unit"`
	Limited          bool    `json:"limited"`
	LimitType        *string `json:"limit_type"`
	TimeLimit        *int64  `json:"time_limit"`
	TimeUnit         *string `json:"time_unit"`
	DataLimit        *int64  `json:"data_limit"`
	DataUnit         *string `json:"data_unit"`
	SharedUsers      *int64  `json:"shared_users"`
	BandwidthID      *int64  `json:"bandwidth_id"`
	RouterID         *int64  `json:"router_id"`
	PoolID           *int64  `json:"pool_id"`
	ExpiredPlanID    *int64  `json:"expired_plan_id"`
	BillingDay       *int64  `json:"billing_day"`
	Device           string  `json:"device"`
	Enabled          bool    `json:"enabled"`
	Audience         string  `json:"audience"`
	FupBandwidthID   *int64  `json:"fup_bandwidth_id"`
	IdleTimeout      *int64  `json:"idle_timeout"`
	LoginWindow      string  `json:"login_window"`
	BoostWindow      string  `json:"boost_window"`
	BoostBandwidthID *int64  `json:"boost_bandwidth_id"`
}

// FromPlan excludes OnLogin and OnLogout (router scripts).
func FromPlan(m db.Plan) Plan {
	return Plan{m.ID, m.Name, m.Type, m.Billing, m.Price, m.Validity, m.ValidityUnit, m.Limited != 0,
		str(m.LimitType), i64(m.TimeLimit), str(m.TimeUnit), i64(m.DataLimit), str(m.DataUnit),
		i64(m.SharedUsers), i64(m.BandwidthID), i64(m.RouterID), i64(m.PoolID), i64(m.ExpiredPlanID),
		i64(m.BillingDay), m.Device, m.Enabled != 0, m.Audience, i64(m.FupBandwidthID),
		i64(m.IdleTimeout), m.LoginWindow, m.BoostWindow, i64(m.BoostBandwidthID)}
}

type Subscription struct {
	ID                int64  `json:"id"`
	CustomerID        int64  `json:"customer_id"`
	PlanID            int64  `json:"plan_id"`
	RouterID          *int64 `json:"router_id"`
	Type              string `json:"type"`
	StartedAt         int64  `json:"started_at"`
	ExpiresAt         int64  `json:"expires_at"`
	Status            string `json:"status"`
	Method            string `json:"method"`
	AdminID           *int64 `json:"admin_id"`
	PendingStart      bool   `json:"pending_start"`
	QuotaCarryBytes   int64  `json:"quota_carry_bytes"`
	QuotaCarrySeconds int64  `json:"quota_carry_seconds"`
	UsedInOctets      int64  `json:"used_in_octets"`
	UsedOutOctets     int64  `json:"used_out_octets"`
	UsedSeconds       int64  `json:"used_seconds"`
}

// FromSubscription excludes ExpiredNotifiedAt (internal notifier state).
func FromSubscription(m db.Subscription) Subscription {
	return Subscription{m.ID, m.CustomerID, m.PlanID, i64(m.RouterID), m.Type, m.StartedAt, m.ExpiresAt,
		m.Status, m.Method, i64(m.AdminID), m.PendingStart != 0, m.QuotaCarryBytes, m.QuotaCarrySeconds,
		m.UsedInOctets, m.UsedOutOctets, m.UsedSeconds}
}

type Transaction struct {
	ID          int64  `json:"id"`
	Invoice     string `json:"invoice"`
	CustomerID  *int64 `json:"customer_id"`
	PlanID      *int64 `json:"plan_id"`
	Username    string `json:"username"`
	PlanName    string `json:"plan_name"`
	RouterName  string `json:"router_name"`
	Type        string `json:"type"`
	Price       int64  `json:"price"`
	Method      string `json:"method"`
	Note        string `json:"note"`
	AdminID     *int64 `json:"admin_id"`
	CreatedAt   int64  `json:"created_at"`
	PeriodStart int64  `json:"period_start"`
	PeriodEnd   int64  `json:"period_end"`
}

// FromTransaction excludes nothing.
func FromTransaction(m db.Transaction) Transaction {
	return Transaction{m.ID, m.Invoice, i64(m.CustomerID), i64(m.PlanID), m.Username, m.PlanName,
		m.RouterName, m.Type, m.Price, m.Method, m.Note, i64(m.AdminID), m.CreatedAt, m.PeriodStart, m.PeriodEnd}
}

// Voucher includes the redeemable Code. The API layer must only return it to admin roles.
type Voucher struct {
	ID          int64  `json:"id"`
	Code        string `json:"code"`
	PlanID      int64  `json:"plan_id"`
	Status      string `json:"status"`
	UsedBy      *int64 `json:"used_by"`
	UsedAt      *int64 `json:"used_at"`
	GeneratedBy *int64 `json:"generated_by"`
	CreatedAt   int64  `json:"created_at"`
}

// FromVoucher excludes nothing.
func FromVoucher(m db.Voucher) Voucher {
	return Voucher{m.ID, m.Code, m.PlanID, m.Status, i64(m.UsedBy), i64(m.UsedAt), i64(m.GeneratedBy), m.CreatedAt}
}

type PaymentRequest struct {
	ID         int64  `json:"id"`
	Ref        string `json:"ref"`
	Gateway    string `json:"gateway"`
	GatewayRef string `json:"gateway_ref"`
	CustomerID *int64 `json:"customer_id"`
	Username   string `json:"username"`
	PlanID     int64  `json:"plan_id"`
	Amount     int64  `json:"amount"`
	Coupon     string `json:"coupon"`
	Channel    string `json:"channel"`
	PayURL     string `json:"pay_url"`
	Status     string `json:"status"`
	CreatedAt  int64  `json:"created_at"`
	PaidAt     *int64 `json:"paid_at"`
	ExpiresAt  int64  `json:"expires_at"`
}

// FromPaymentRequest excludes nothing.
func FromPaymentRequest(m db.PaymentRequest) PaymentRequest {
	return PaymentRequest{m.ID, m.Ref, m.Gateway, m.GatewayRef, i64(m.CustomerID), m.Username, m.PlanID,
		m.Amount, m.Coupon, m.Channel, m.PayUrl, m.Status, m.CreatedAt, i64(m.PaidAt), m.ExpiresAt}
}
