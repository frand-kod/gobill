package web

// Quick Setup: the form fields of each step.

import (
	"context"
	"strconv"

	"github.com/skip2/go-qrcode"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/payment"
)

type wizLine struct {
	Label string
	OK    bool
}

// wizView is the data of the setup template.
type wizView struct {
	Step       wizStep
	Num, Total int
	Pct        int
	Done       bool
	Import     bool // came from a PHPNuxBill import
	Verify     bool // imported and done: the step shows the data to check
	Prev, Next string
	Form       formPage
	Secret     bool     // the NAS secret was saved just now and is in the commands
	Commands   []string // RouterOS commands (router step)
	LiveURL    string   // JSON the live indicator polls
	Summary    []wizLine
	Voucher    string
}

func wizCompanyFields(v, e map[string]string) []field {
	return section([]field{
		text("company_name", "Company Name", v, e).Req().WithHint("Name of your business. Shown on invoices, messages and the login page."),
		text("app_url", "App URL", v, e).WithHint("Public address of this app, e.g. https://billing.example.com. Used for links in WhatsApp messages (QRIS, invoice)."),
		text("phone", "Phone Number", v, e).WithHint("Operator phone or WhatsApp. Shown to customers on the portal."),
	}, "Business", "")
}

func wizRouterFields(v, e map[string]string) []field {
	mode := text("router_mode", "How does gobill connect to MikroTik?", v, e).As("select").WithBind().Req()
	mode.Options = []option{{Value: "radius", Label: "RADIUS (recommended)"}, {Value: "api", Label: "API"}, {Value: "both", Label: "Both"}}
	out := section([]field{mode}, "Connection", "")
	nasSecret := text("nas_secret", "Shared Secret", v, e).As("password").WithHint("At least 16 characters. Use Generate, then copy the commands before you leave this page.")
	nasSecret.Value, nasSecret.Gen = "", true
	out = append(out, section([]field{
		text("nas_name", "NAS Name", v, e).Req(),
		text("nas_ip", "IP / CIDR", v, e).Req().WithHint("The MikroTik address that talks to gobill, e.g. 192.168.88.1."),
		nasSecret,
	}, "NAS (RADIUS)", "router_mode !== 'api'")...)
	pw := text("router_password", "Password", v, e).As("password").WithHint("Leave empty to keep the current password")
	pw.Value = ""
	out = append(out, section([]field{
		text("router_name", "Router Name", v, e).Req(),
		text("router_host", "Host", v, e).Req(),
		text("router_port", "Port", v, e).As("number").Req(),
		text("router_username", "Username", v, e).Req(),
		pw,
		{Name: "router_test", Type: "test", Value: "/admin/setup/router/test", Btn: "Test connection", Hint: "Saves the router first, then checks the connection."},
	}, "Router (API)", "router_mode !== 'radius'")...)
	return out
}

func wizSpeedFields(v, e map[string]string) []field {
	var fs []field
	for i := range wizSpeeds {
		f := text(speedKey(i), "Speed (Mbps)", v, e).As("number")
		f.Hint = "Empty = skip this speed."
		fs = append(fs, f)
	}
	return section(fs, "Speeds", "")
}

func (s *Server) wizPlanFields(ctx context.Context, ws wizState, v, e map[string]string) ([]field, error) {
	bws, err := s.Queries.ListBandwidths(ctx, db.ListBandwidthsParams{Limit: 1000})
	if err != nil {
		return nil, err
	}
	bw := text("plan_bandwidth", "Speed for these plans", v, e).As("select").Req()
	for _, b := range bws {
		bw.Options = append(bw.Options, option{Value: strconv.FormatInt(b.ID, 10), Label: b.Name})
	}
	out := []field{bw}
	if ws.effectiveRouter() == "api" {
		routers, err := s.Queries.ListRouters(ctx, db.ListRoutersParams{Limit: 1000})
		if err != nil {
			return nil, err
		}
		rt := text("plan_router", "Router", v, e).As("select").Req()
		for _, x := range routers {
			rt.Options = append(rt.Options, option{Value: strconv.FormatInt(x.ID, 10), Label: x.Name})
		}
		out = append(out, rt)
	}
	for i, p := range wizPlans {
		f := text(priceKey(i), p.Label, v, e).As("number")
		f.Hint = "Empty = skip this plan."
		out = append(out, f)
	}
	return section(out, "Plan templates", ""), nil
}

func wizWhatsAppFields(v, e map[string]string, importing bool) []field {
	pw := text("alt_wga_password", "Password", v, e).As("password").WithHint("Leave empty to keep the saved password")
	pw.Value = ""
	out := []field{
		text("alt_wga_server_url", "WA server URL", v, e).WithHint("Address of your GOWA gateway, e.g. http://127.0.0.1:3000"),
		text("alt_wga_device_id", "Device ID", v, e),
		text("alt_wga_username", "Username", v, e),
		pw,
	}
	if importing {
		n := text("notify_customers", "Send notifications to customers", v, e).As("select")
		n.Options = []option{{Value: "yes", Label: "Yes"}, {Value: "no", Label: "No"}}
		out = append(out, n.WithHint("No stops every message to customers. Operator alerts are not affected."))
	}
	out = append(out, field{Name: "wa_test_phone", Label: "Test number", Type: "test", Inp: "tel", Value: "/admin/setup/whatsapp/test",
		Btn: "Send test message", Error: e["wa_test_phone"], Hint: "Enter a number to receive a test message."})
	return section(out, "WhatsApp (GOWA)", "")
}

func wizQRISFields(v, e map[string]string) []field {
	qris := field{Name: "qris_payload", Label: "Static QRIS", Type: "qris", Value: v["qris_payload"], Error: e["qris_payload"],
		Hint: "Upload the merchant's static QRIS photo (PNG/JPG). The system only stores its text"}
	if m, n := payment.QRISInfo(v["qris_payload"]); m != "" {
		qris.Hint = "Active: " + m
		if n != "" {
			qris.Hint += " (NMID " + n + ")"
		}
		if png, err := qrcode.Encode(v["qris_payload"], qrcode.Medium, 256); err == nil {
			qris.Img = wizDataURI(png)
		}
	}
	return section([]field{qris}, "QRIS", "")
}

func wizTripayFields(v, e map[string]string, st map[string]string) []field {
	on := field{Name: "tripay_on", Label: "Use Tripay for payments", Type: "checkbox", Checked: v["tripay_on"] == "1"}
	sec := func(name, label string) field {
		f := text(name, label, nil, e).As("password")
		f.Hint = "Leave empty to keep the current secret"
		if st[name] != "" {
			f.Hint = "Saved. Leave empty to keep the current secret"
		}
		return f
	}
	mode := text("tripay_mode", "Tripay Mode", v, e).As("select")
	mode.Options = []option{{Value: "sandbox", Label: "Sandbox"}, {Value: "production", Label: "Production"}}
	return section([]field{on, sec("tripay_api_key", "Tripay API Key"), sec("tripay_private_key", "Tripay Private Key"),
		text("tripay_merchant_code", "Tripay Merchant Code", v, e), mode,
		text("tripay_channel", "Default Channel", v, e).WithHint("Optional channel code, e.g. QRIS")}, "Tripay", "")
}
