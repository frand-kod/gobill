package billing

// Customer attributes and recurring bill totals.

import (
	"context"
	"fmt"
	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/notify"
	"sort"
	"strconv"
	"strings"
)

// PHP kept per-customer "attributes" in tbl_customers_fields; gobill keeps them as custom
// fields with the same names: "<anything> Bill" (extra charge, "cost" or installment
// "cost:remaining"), "Invoice" (next Period invoice amount). "Expired Date" lives in
// customers.billing_day.

type billItem struct {
	Name string
	Cost int64
	Inst bool // installment ("cost:remaining")
}

func attrs(ctx context.Context, q *db.Queries, customerID int64) map[string]string {
	rows, err := q.ListCustomerAttrs(ctx, customerID)
	if err != nil {
		return nil
	}
	m := make(map[string]string, len(rows))
	for _, r := range rows {
		m[r.Name] = strings.TrimSpace(r.Value)
	}
	return m
}

func parseMoney(s string) int64 {
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n
	}
	f, _ := strconv.ParseFloat(s, 64)
	return int64(f)
}

// customerBills is User::getBills: attributes ending in "Bill"; a finished installment
// (remaining empty or 0) is skipped.
func customerBills(a map[string]string) (items []billItem, total int64) {
	for name, v := range a {
		if !strings.HasSuffix(strings.ToLower(name), "bill") {
			continue
		}
		cost, rem, inst := strings.Cut(v, ":")
		if inst && (rem == "" || rem == "0") {
			continue
		}
		c := parseMoney(cost)
		items = append(items, billItem{name, c, inst})
		total += c
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return
}

// BillsTotal is the extra charge a recharge of this customer adds to the plan price.
func (s *Service) BillsTotal(ctx context.Context, customerID int64) int64 {
	_, t := customerBills(attrs(ctx, s.Q, customerID))
	return t
}

func billsNote(items []billItem) string {
	var b strings.Builder
	for _, it := range items {
		fmt.Fprintf(&b, "%s : %d\n", it.Name, it.Cost)
	}
	return b.String()
}

// payBills is User::billsPaid: one installment less per paid recharge.
func payBills(ctx context.Context, q *db.Queries, customerID int64, items []billItem) error {
	for _, it := range items {
		if !it.Inst {
			continue
		}
		v := attrs(ctx, q, customerID)[it.Name]
		cost, rem, _ := strings.Cut(v, ":")
		if n, err := strconv.Atoi(rem); err == nil && n != 0 {
			if err := setAttr(ctx, q, customerID, it.Name, fmt.Sprintf("%s:%d", cost, n-1)); err != nil {
				return err
			}
		}
	}
	return nil
}

// packageVars are the [[price]] and [[bills]] placeholders of sendPackageNotification:
// bills lists the customer's extra charges and ends with the total.
func (s *Service) packageVars(ctx context.Context, customerID, price int64) map[string]string {
	items, add := customerBills(attrs(ctx, s.Q, customerID))
	var b strings.Builder
	for _, it := range items {
		fmt.Fprintf(&b, "%s : %s\n", it.Name, notify.Money(it.Cost))
	}
	fmt.Fprintf(&b, "Total : %s\n", notify.Money(price+add))
	return map[string]string{"price": notify.Money(price), "bills": b.String()}
}

func setAttr(ctx context.Context, q *db.Queries, customerID int64, name, value string) error {
	id, err := q.EnsureCustomField(ctx, name)
	if err != nil {
		return err
	}
	return q.UpsertCustomerFieldValue(ctx, db.UpsertCustomerFieldValueParams{CustomerID: customerID, FieldID: id, Value: value})
}
