package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

// CustomersOff is the notify_customers switch: "no" stops every customer message. Missing means on.
func CustomersOff(st map[string]string) bool { return st["notify_customers"] == "no" }

// send picks the channel like sendPackageNotification: phone must be > 5 chars for sms/wa.
func (n *Notifier) send(ctx context.Context, c db.Customer, via, subject, msg string) error {
	if CustomersOff(n.Settings) {
		slog.Info("customer notification off", "kind", subject)
		return nil
	}
	switch via {
	case "sms", "wa":
		if len(c.Phone) <= 5 {
			return nil
		}
		if via == "sms" {
			return n.SMS(ctx, c.Phone, msg)
		}
		return n.WhatsApp(ctx, c.Phone, msg)
	case "email":
		return n.Email(ctx, c.Email, n.EmailSubject(subject), msg)
	}
	slog.Warn("customer notification has no channel", "kind", subject, "via", via, "customer", c.Username)
	return nil
}

func custVars(c db.Customer, v map[string]string) map[string]string {
	out := map[string]string{"name": c.Fullname, "username": c.Username, "user_name": c.Username}
	for k, x := range v {
		out[k] = x
	}
	return out
}

// RechargeSuccess sends the invoice_paid message via user_notification_payment (Package.php).
// vars: invoice, date, payment_gateway, payment_channel, type, plan_name, plan_price, expired_date, ...
func (n *Notifier) RechargeSuccess(ctx context.Context, c db.Customer, vars map[string]string) error {
	return n.invoice(ctx, c, "invoice_paid", vars)
}

// RechargeBalance sends the invoice_balance message for a balance top-up (Package.php #rechargeBalance),
// in place of the paid invoice. vars are the RechargeSuccess ones plus balance_before and balance.
func (n *Notifier) RechargeBalance(ctx context.Context, c db.Customer, vars map[string]string) error {
	return n.invoice(ctx, c, "invoice_balance", vars)
}

func (n *Notifier) invoice(ctx context.Context, c db.Customer, name string, vars map[string]string) error {
	v := custVars(c, vars)
	for k, s := range map[string]string{"company_name": "company_name", "address": "address", "phone": "phone", "footer": "note"} {
		if _, ok := v[k]; !ok {
			v[k] = n.get(s)
		}
	}
	to, subject, text := n.get("user_notification_payment"), "Invoice #"+v["invoice"], n.fill(n.template(name), v)
	err := n.send(ctx, c, to, subject, text)
	if err != nil {
		n.retryInvoice(c, to, subject, text)
	}
	return err
}

// invoiceRetry are the waits before each retry of a failed invoice message (3 retries, no new table).
var invoiceRetry = []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute}

// retryInvoice resends a failed invoice in the background, each retry with its own timeout.
func (n *Notifier) retryInvoice(c db.Customer, to, subject, text string) {
	go func() {
		for _, wait := range invoiceRetry {
			time.Sleep(wait)
			ctx, cancel := context.WithTimeout(context.Background(), n.Timeout)
			err := n.send(ctx, c, to, subject, text)
			cancel()
			if err == nil {
				return
			}
			slog.Warn("invoice retry failed", "kind", subject, "err", err)
		}
	}()
}

// Expired sends the expired message via user_notification_expired (cron.php).
func (n *Notifier) Expired(ctx context.Context, c db.Customer, pkg string, vars map[string]string) error {
	v := custVars(c, vars)
	v["package"], v["plan"] = pkg, pkg
	return n.send(ctx, c, n.get("user_notification_expired"), "Internet Plan Expired", n.fill(n.template("expired"), v))
}

// Custom sends template `name` (settings notif_<name>) via user_notification_payment.
func (n *Notifier) Custom(ctx context.Context, c db.Customer, name, subject string, vars map[string]string) error {
	return n.send(ctx, c, n.get("user_notification_payment"), subject, n.fill(n.template(name), custVars(c, vars)))
}

// CustomOn is Custom on explicit channels (sms, wa, email) instead of user_notification_payment.
// A real Password (not the "********" mask) that the template has no [[Password]] for goes on its
// own line, so it is not silently dropped.
func (n *Notifier) CustomOn(ctx context.Context, c db.Customer, vias []string, name, subject string, vars map[string]string) error {
	tpl := n.template(name)
	msg, err := n.fill(tpl, custVars(c, vars)), error(nil)
	if pw := vars["Password"]; pw != "" && pw != "********" && !strings.Contains(tpl, "[[Password]]") {
		msg += "\nPassword: " + pw
	}
	for _, v := range vias {
		err = errors.Join(err, n.send(ctx, c, v, subject, msg))
	}
	return err
}

// Reminder sends the 7/3/1-day message via user_notification_reminder (cron_reminder.php);
// notification_reminder_<N>day == "no" disables that day.
func (n *Notifier) Reminder(ctx context.Context, c db.Customer, days int, pkg string, vars map[string]string) error {
	if n.get(fmt.Sprintf("notification_reminder_%dday", days)) == "no" {
		return nil
	}
	v := custVars(c, vars)
	v["package"], v["plan"] = pkg, pkg
	return n.send(ctx, c, n.get("user_notification_reminder"), "Internet Plan Reminder", n.fill(n.template(fmt.Sprintf("reminder_%d_day", days)), v))
}
