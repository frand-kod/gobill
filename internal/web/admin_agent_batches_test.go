package web

import (
	"net/url"
	"strings"
	"testing"

	"github.com/frand-kod/gobill/internal/db"
)

func batchForm(agent, plan int64, paid, bonus, price string) url.Values {
	return url.Values{"agent": {itoa(agent)}, "plan": {itoa(plan)}, "paid": {paid}, "bonus": {bonus}, "price": {price}, "note": {"n"}}
}

func TestAgentBatchFlowAndRoles(t *testing.T) {
	e := billApp(t)
	h, q := e.h, e.q
	plan := e.plan(t, "p", "PPPoE", 10000)
	ag1, ag2 := mk(t, q, "ag1", "Agent", 0), mk(t, q, "ag2", "Agent", 0)
	adm, sales, report := mk(t, q, "adm", "Admin", 0), mk(t, q, "sl", "Sales", ag1.ID), "rita"
	c1, c2 := login(t, h, "ag1"), login(t, h, "ag2")
	ca, cs, cr := login(t, h, "adm"), login(t, h, sales.Username), login(t, h, report)
	_ = adm

	// create: managers only, validated server-side
	wantCode(t, do(h, "POST", "/admin/agent-batches", batchForm(ag1.ID, plan.ID, "10", "4", "5000"), c1), 403, "agent creates")
	wantCode(t, do(h, "POST", "/admin/agent-batches", batchForm(ag1.ID, plan.ID, "10", "4", "5000"), cs), 403, "sales creates")
	wantCode(t, do(h, "POST", "/admin/agent-batches", batchForm(ag1.ID, plan.ID, "10", "4", "5000"), cr), 403, "report creates")
	wantCode(t, do(h, "GET", "/admin/agent-batches/new", nil, c1), 403, "agent form")
	wantCode(t, do(h, "POST", "/admin/agent-batches", batchForm(sales.ID, plan.ID, "10", "4", "5000"), ca), 422, "batch for a Sales")
	wantCode(t, do(h, "POST", "/admin/agent-batches", batchForm(ag1.ID, plan.ID, "0", "0", "5000"), ca), 422, "empty batch")
	wantCode(t, do(h, "POST", "/admin/agent-batches", batchForm(ag1.ID, plan.ID, "-1", "5", "5000"), ca), 422, "negative")
	wantCode(t, do(h, "POST", "/admin/agent-batches", batchForm(ag1.ID, plan.ID, "10", "0", "-5"), ca), 422, "negative price")
	wantCode(t, do(h, "POST", "/admin/agent-batches", batchForm(ag1.ID, plan.ID, "400", "200", "5"), ca), 422, "over 500 codes")
	wantCode(t, do(h, "GET", "/admin/agent-batches/new", nil, ca), 200, "manager form")
	wantCode(t, do(h, "POST", "/admin/agent-batches", batchForm(ag1.ID, plan.ID, "10", "4", "5000"), ca), 303, "create 10+4")
	wantCode(t, do(h, "POST", "/admin/agent-batches", batchForm(ag2.ID, plan.ID, "3", "0", "7000"), e.c), 303, "create for agent 2")

	b1, b2 := int64(1), int64(2)
	if debt, _ := q.AgentBatchDebt(t.Context(), b1); debt != 50000 {
		t.Fatalf("debt %d", debt)
	}
	logs, _ := q.ListActivityLogs(t.Context(), db.ListActivityLogsParams{Limit: 5})
	if len(logs) == 0 || logs[1].Action != "agent_batch.create" {
		t.Fatalf("logs %+v", logs)
	}

	// IDOR: each agent sees only its own batches, by list and by id
	w := do(h, "GET", "/admin/agent-batches", nil, c1)
	wantCode(t, w, 200, "agent list")
	if !strings.Contains(w.Body.String(), "ag1") || strings.Contains(w.Body.String(), "ag2") {
		t.Fatal("agent 1 list leaks or misses rows")
	}
	if w := do(h, "GET", "/admin/agent-batches", nil, c2); strings.Contains(w.Body.String(), "ag1") || !strings.Contains(w.Body.String(), "ag2") {
		t.Fatal("agent 2 list leaks or misses rows")
	}
	own := do(h, "GET", "/admin/agent-batches/"+itoa(b1), nil, c1)
	wantCode(t, own, 200, "own batch")
	if !strings.Contains(own.Body.String(), "Rp50.000") && !strings.Contains(own.Body.String(), "50.000") {
		t.Fatalf("debt not shown: %s", own.Body.String())
	}
	wantCode(t, do(h, "GET", "/admin/agent-batches/"+itoa(b2), nil, c1), 404, "agent 1 reads agent 2 batch")
	wantCode(t, do(h, "GET", "/admin/agent-batches/"+itoa(b1), nil, c2), 404, "agent 2 reads agent 1 batch")
	wantCode(t, do(h, "GET", "/admin/agent-batches/999", nil, c1), 404, "missing batch")
	wantCode(t, do(h, "GET", "/admin/agent-batches", nil, cs), 403, "sales list")
	wantCode(t, do(h, "GET", "/admin/agent-batches/"+itoa(b1), nil, cs), 403, "sales view")
	wantCode(t, do(h, "GET", "/admin/agent-batches", nil, cr), 403, "report list")
	wantCode(t, do(h, "GET", "/admin/agent-batches", nil, nil), 303, "anonymous")
	wantCode(t, do(h, "GET", "/admin/agent-batches/"+itoa(b2), nil, ca), 200, "admin reads any")

	// an agent can neither record payments nor return codes, on its own batch or another's
	pay := url.Values{"amount": {"1000"}}
	for _, id := range []int64{b1, b2} {
		wantCode(t, do(h, "GET", "/admin/agent-batches/"+itoa(id)+"/payment", nil, c1), 403, "agent payment form")
		wantCode(t, do(h, "POST", "/admin/agent-batches/"+itoa(id)+"/payment", pay, c1), 403, "agent payment")
		wantCode(t, do(h, "POST", "/admin/agent-batches/"+itoa(id)+"/return", url.Values{"ids": {"1"}}, c1), 403, "agent return")
	}
	if debt, _ := q.AgentBatchDebt(t.Context(), b1); debt != 50000 {
		t.Fatalf("debt moved by an agent: %d", debt)
	}

	// payment by the operator
	wantCode(t, do(h, "GET", "/admin/agent-batches/"+itoa(b1)+"/payment", nil, ca), 200, "payment form")
	wantCode(t, do(h, "POST", "/admin/agent-batches/"+itoa(b1)+"/payment", url.Values{"amount": {"20000"}, "note": {"cash"}}, ca), 303, "payment")
	wantCode(t, do(h, "POST", "/admin/agent-batches/"+itoa(b1)+"/payment", url.Values{"amount": {"30001"}}, ca), 422, "overpay")
	wantCode(t, do(h, "POST", "/admin/agent-batches/"+itoa(b1)+"/payment", url.Values{"amount": {"0"}}, ca), 422, "zero payment")
	wantCode(t, do(h, "POST", "/admin/agent-batches/"+itoa(b1)+"/payment", url.Values{"amount": {"x"}}, ca), 422, "junk payment")
	if debt, _ := q.AgentBatchDebt(t.Context(), b1); debt != 30000 {
		t.Fatalf("debt %d", debt)
	}

	// return: the codes of ANOTHER batch cannot be returned through this one
	codes, _ := q.ListBatchVouchers(t.Context(), db.ListBatchVouchersParams{BatchID: b1, Limit: 20})
	other, _ := q.ListBatchVouchers(t.Context(), db.ListBatchVouchersParams{BatchID: b2, Limit: 20})
	if len(codes) != 14 || len(other) != 3 {
		t.Fatalf("codes %d %d", len(codes), len(other))
	}
	wantCode(t, do(h, "POST", "/admin/agent-batches/"+itoa(b1)+"/return", url.Values{"ids": {itoa(other[0].ID)}}, ca), 303, "foreign code")
	if debt, _ := q.AgentBatchDebt(t.Context(), b1); debt != 30000 {
		t.Fatalf("foreign return moved the debt: %d", debt)
	}
	wantCode(t, do(h, "POST", "/admin/agent-batches/"+itoa(b1)+"/return", url.Values{"ids": {itoa(codes[0].ID), itoa(codes[1].ID)}}, ca), 303, "return two")
	if debt, _ := q.AgentBatchDebt(t.Context(), b1); debt != 20000 {
		t.Fatalf("debt after return %d", debt)
	}
	wantCode(t, do(h, "POST", "/admin/agent-batches/"+itoa(b1)+"/return", nil, ca), 303, "return nothing")

	// the cancelled code no longer redeems, an untouched one does
	cust := url.Values{"customer": {"u1"}, "code": {codes[0].Code}}
	if w := do(h, "POST", "/admin/vouchers/redeem", cust, ca); w.Code != 422 {
		t.Fatalf("returned code redeemed: %d", w.Code)
	}
	cust.Set("code", codes[5].Code)
	wantCode(t, do(h, "POST", "/admin/vouchers/redeem", cust, ca), 303, "other code redeems")
	// ... and the list shows the cancelled one as returned, the redeemed one as used
	view := do(h, "GET", "/admin/agent-batches/"+itoa(b1), nil, c1).Body.String()
	if !strings.Contains(view, "Returned") && !strings.Contains(view, "Dikembalikan") {
		t.Fatal("returned status not shown")
	}
}
