package web

// Agent consignment batches: SuperAdmin/Admin create batches and record payments and returns;
// an Agent only reads its own batches (usage, unused codes, debt).

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/frand-kod/gobill/internal/billing"
	"github.com/frand-kod/gobill/internal/db"
)

const batchBase = "/admin/agent-batches"

// batchScope is the agent id whose batches the admin may see; 0 = every agent (managers).
func batchScope(a *db.Admin) int64 {
	if oneOf(a.Role, "SuperAdmin", "Admin") {
		return 0
	}
	return a.ID
}

// batchFor loads the batch of the path. An Agent asking for another agent's batch gets the same 404
// as for a batch that does not exist.
func (s *Server) batchFor(w http.ResponseWriter, r *http.Request) (db.AgentBatch, bool) {
	b, err := s.queries.GetAgentBatch(r.Context(), pathID(r))
	if errors.Is(err, sql.ErrNoRows) || (err == nil && batchScope(adminFrom(r)) != 0 && b.AgentID != adminFrom(r).ID) {
		http.NotFound(w, r)
		return b, false
	} else if err != nil {
		s.fail(w, "get agent batch", err)
		return b, false
	}
	return b, true
}

func nonNeg(s string) (int64, bool) {
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil && n >= 0
}

func (s *Server) abList(w http.ResponseWriter, r *http.Request) {
	_, pg := paging(r)
	scope := batchScope(adminFrom(r))
	rows, err := s.queries.ListAgentBatches(r.Context(), db.ListAgentBatchesParams{AgentID: scope, PageLimit: pg.limit(), PageOffset: pg.offset()})
	if err != nil {
		s.fail(w, "list agent batches", err)
		return
	}
	total, err := s.queries.CountAgentBatches(r.Context(), scope)
	if err != nil {
		s.fail(w, "count agent batches", err)
		return
	}
	lp := listPage{Heading: "Agent batches", Base: batchBase, ViewLink: true, NoDelete: true, DeleteOnly: true,
		CanCreate: scope == 0,
		Note:      "Used is the least the agent has sold; the real sales are not known to the system.",
		Cols:      []string{"Batch", "Agent", "Plan Name", "Paid", "Bonus", "Price", "Used", "Unused", "Returned", "Debt"}}
	for _, b := range rows {
		lp.Rows = append(lp.Rows, listRow{b.ID, batchCells(b)})
	}
	lp.finish(pg, total)
	s.renderList(w, r, lp)
}

func batchCells(b db.ListAgentBatchesRow) []string {
	unused := b.PaidCount + b.BonusCount - b.Used - b.Returned
	return []string{"#" + strconv.FormatInt(b.ID, 10), b.AgentName, b.PlanName, strconv.FormatInt(b.PaidCount, 10), strconv.FormatInt(b.BonusCount, 10),
		money(b.UnitPrice), strconv.FormatInt(b.Used, 10), strconv.FormatInt(unused, 10), strconv.FormatInt(b.Returned, 10), money(b.Debt)}
}

func (s *Server) abView(w http.ResponseWriter, r *http.Request) {
	b, ok := s.batchFor(w, r)
	if !ok {
		return
	}
	_, pg := paging(r)
	sum, err := s.queries.ListAgentBatches(r.Context(), db.ListAgentBatchesParams{BatchID: b.ID, PageLimit: 1})
	if err != nil || len(sum) != 1 {
		s.fail(w, "agent batch summary", errors.Join(err, errors.New("no summary row")))
		return
	}
	codes, err := s.queries.ListBatchVouchers(r.Context(), db.ListBatchVouchersParams{BatchID: b.ID, Limit: pg.limit(), Offset: pg.offset()})
	if err != nil {
		s.fail(w, "list batch vouchers", err)
		return
	}
	total, err := s.queries.CountBatchVouchers(r.Context(), b.ID)
	if err != nil {
		s.fail(w, "count batch vouchers", err)
		return
	}
	c := batchCells(sum[0])
	manage := batchScope(adminFrom(r)) == 0
	base := fmt.Sprint(batchBase, "/", b.ID)
	lp := listPage{Heading: "Agent batch", Base: base, NoDelete: true, DeleteOnly: true,
		Note: fmt.Sprintf("%s #%d: %s, %s %s, %s %s, %s %s, %s %s, %s %s", s.catalog.T(s.language(), "Batch"), b.ID, c[1],
			s.catalog.T(s.language(), "Used"), c[6], s.catalog.T(s.language(), "Unused"), c[7],
			s.catalog.T(s.language(), "Returned"), c[8], s.catalog.T(s.language(), "Price"), c[5], s.catalog.T(s.language(), "Debt"), c[9]),
		Cols: []string{"Code Voucher", "Status", "Used"}}
	if manage {
		lp.Links = []option{{base + "/payment", "Record payment"}, {batchBase, "Agent batches"}}
		lp.Bulk = []bulkAction{{"return", "Return selected", true, false, false}}
	} else {
		lp.Links = []option{{batchBase, "Agent batches"}}
	}
	for _, v := range codes {
		status, used := v.Status, ""
		if v.Returned == 1 {
			status = "Returned"
		}
		if v.UsedAt.Valid {
			used = s.ts(v.UsedAt.Int64)
		}
		lp.Rows = append(lp.Rows, listRow{v.ID, []string{v.Code, status, used}})
	}
	lp.finish(pg, total)
	s.renderList(w, r, lp)
}

func (s *Server) abFields(r *http.Request, v, e map[string]string) ([]field, error) {
	plans, _, err := s.planOptions(r, true)
	if err != nil {
		return nil, err
	}
	agents, err := s.queries.ListAgents(r.Context())
	if err != nil {
		return nil, err
	}
	agent := text("agent", "Agent", v, e).as("select")
	for _, a := range agents {
		if a.Status == "Active" {
			agent.Options = append(agent.Options, option{fmt.Sprint(a.ID), a.Username})
		}
	}
	plan := text("plan", "Service Plan", v, e).as("select")
	plan.Options = plans
	return []field{agent, plan,
		text("paid", "Paid vouchers", v, e).as("number").req(),
		text("bonus", "Bonus vouchers", v, e).as("number").hint("Given up front with the paid ones, e.g. 10 paid + 4 bonus"),
		text("price", "Price per paid voucher", v, e).as("number").req().hint("Rupiah; the debt is paid x price"),
		text("note", "Note", v, e)}, nil
}

func (s *Server) abForm(w http.ResponseWriter, r *http.Request, status int, v, e map[string]string) {
	fs, err := s.abFields(r, v, e)
	if err != nil {
		s.fail(w, "agent batch form", err)
		return
	}
	s.renderForm(w, r, status, formPage{"Add Agent batch", batchBase, batchBase, fs})
}

func (s *Server) abNew(w http.ResponseWriter, r *http.Request) {
	s.abForm(w, r, 200, map[string]string{"paid": "10", "bonus": "0"}, nil)
}

func (s *Server) abCreate(w http.ResponseWriter, r *http.Request) {
	v := formVals(r, "agent", "plan", "paid", "bonus", "price", "note")
	e := map[string]string{}
	agentID, ok := posInt(v["agent"])
	if ag, err := s.queries.GetAdmin(r.Context(), agentID); !ok || err != nil || ag.Role != "Agent" || ag.Status != "Active" {
		e["agent"] = "Agent not found"
	}
	planID, ok := posInt(v["plan"])
	if _, err := s.queries.GetPlan(r.Context(), planID); !ok || err != nil {
		e["plan"] = "This field is required"
	}
	if iso, _ := s.isolirIDs(r.Context()); ok && planID == iso {
		e["plan"] = billing.ErrIsolirPlan.Error()
	}
	paid, ok1 := nonNeg(v["paid"])
	bonus, ok2 := nonNeg(v["bonus"])
	if v["bonus"] == "" {
		bonus, ok2 = 0, true
	}
	if !ok1 || !ok2 || paid+bonus < 1 || paid+bonus > maxVouchers {
		e["paid"] = "Enter a number between 1 and " + strconv.Itoa(maxVouchers)
	}
	price, ok := nonNeg(v["price"])
	if !ok || price > 100_000_000 {
		e["price"] = "Invalid value"
	}
	if len(v["note"]) > 200 {
		e["note"] = "Invalid value"
	}
	if len(e) > 0 {
		s.abForm(w, r, http.StatusUnprocessableEntity, v, e)
		return
	}
	id, err := s.Billing.CreateAgentBatch(r.Context(), billing.BatchParams{AgentID: agentID, PlanID: planID, AdminID: adminFrom(r).ID,
		Paid: paid, Bonus: bonus, Price: price, Note: v["note"],
		NewCode: func() (string, error) { return randomCode(upperChars, minVoucherLen) }})
	if errors.Is(err, billing.ErrBatchInvalid) {
		e["agent"] = "Invalid value"
		s.abForm(w, r, http.StatusUnprocessableEntity, v, e)
		return
	} else if err != nil {
		s.fail(w, "create agent batch", err)
		return
	}
	s.done(w, r, fmt.Sprint(batchBase, "/", id), "Agent batch created", "agent_batch.create",
		fmt.Sprintf("batch %d: agent %d, %d+%d x %d", id, agentID, paid, bonus, price))
}

func (s *Server) abPayForm(w http.ResponseWriter, r *http.Request, status int, b db.AgentBatch, v, e map[string]string) {
	debt, err := s.queries.AgentBatchDebt(r.Context(), b.ID)
	if err != nil {
		s.fail(w, "agent batch debt", err)
		return
	}
	amount := text("amount", "Amount", v, e).as("number").req().hint(fmt.Sprintf("%s %s", s.catalog.T(s.language(), "Debt"), money(debt)))
	back := fmt.Sprint(batchBase, "/", b.ID)
	s.renderForm(w, r, status, formPage{"Record payment", back + "/payment", back,
		[]field{amount, text("note", "Note", v, e)}})
}

func (s *Server) abPayment(w http.ResponseWriter, r *http.Request) {
	if b, ok := s.batchFor(w, r); ok {
		s.abPayForm(w, r, 200, b, map[string]string{}, nil)
	}
}

func (s *Server) abPay(w http.ResponseWriter, r *http.Request) {
	b, ok := s.batchFor(w, r)
	if !ok {
		return
	}
	v := formVals(r, "amount", "note")
	e := map[string]string{}
	amount, ok := posInt(v["amount"])
	if !ok || amount > 100_000_000_000 {
		e["amount"] = "Invalid value"
	}
	if len(v["note"]) > 200 {
		e["note"] = "Invalid value"
	}
	if len(e) == 0 {
		switch err := s.Billing.RecordAgentPayment(r.Context(), b.ID, amount, v["note"], adminFrom(r).ID); {
		case errors.Is(err, billing.ErrOverpay):
			e["amount"] = "Payment is more than the debt"
		case err != nil:
			s.fail(w, "record agent payment", err)
			return
		default:
			s.done(w, r, fmt.Sprint(batchBase, "/", b.ID), "Payment recorded", "agent_batch.payment", fmt.Sprintf("batch %d: %d", b.ID, amount))
			return
		}
	}
	s.abPayForm(w, r, http.StatusUnprocessableEntity, b, v, e)
}

func (s *Server) abReturn(w http.ResponseWriter, r *http.Request) {
	b, ok := s.batchFor(w, r)
	if !ok {
		return
	}
	back := fmt.Sprint(batchBase, "/", b.ID)
	ids, ok := parseIDs(r)
	if !ok {
		s.sessions.Put(r.Context(), "error", s.catalog.T(s.language(), "Select at least one row"))
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	switch err := s.Billing.ReturnAgentVouchers(r.Context(), b.ID, ids, adminFrom(r).ID); {
	case errors.Is(err, billing.ErrReturnLimit):
		s.sessions.Put(r.Context(), "error", s.catalog.T(s.language(), "Returns are more than the paid vouchers of the batch"))
	case errors.Is(err, billing.ErrNotReturnable):
		s.sessions.Put(r.Context(), "error", s.catalog.T(s.language(), "Only unused codes of this batch can be returned"))
	case err != nil:
		s.fail(w, "return agent vouchers", err)
		return
	default:
		s.done(w, r, back, "Vouchers returned", "agent_batch.return", fmt.Sprintf("batch %d: %d codes", b.ID, len(ids)))
		return
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}
