package billing

import (
	"net/http"

	"github.com/frand-kod/gobill/internal/web/admin/adminx"
)

// Register adds the recharge, deposit, voucher, agent batch and coupon routes.
func Register(mux *http.ServeMux, h *Handlers, m adminx.Mw) {
	all, managers, staff, money := m.All, m.Managers, m.Staff, m.Money
	// Old PHP plan.php: the voucher list is open to all admins, generate and redeem to staff, delete to managers.
	// Agent consignment batches: managers write, an Agent reads its own.
	agentRead := m.Require("SuperAdmin", "Admin", "Agent")
	// Old PHP coupons.php: every action is SuperAdmin, Admin or Sales only.
	cpn := m.Require("SuperAdmin", "Admin", "Sales")
	mux.Handle("POST /admin/customers/{id}/recharge/confirm", staff(http.HandlerFunc(h.custRechargeConfirm)))
	mux.Handle("POST /admin/customers/{id}/recharge", staff(http.HandlerFunc(h.custRecharge)))
	mux.Handle("POST /admin/customers/recharge", staff(http.HandlerFunc(h.bulkRechargeConfirm)))
	mux.Handle("POST /admin/customers/recharge/run", staff(http.HandlerFunc(h.bulkRechargeRun)))
	mux.Handle("GET /admin/customers/recharge/result", staff(http.HandlerFunc(h.bulkRechargeResult)))
	mux.Handle("GET /admin/recharge", staff(http.HandlerFunc(h.rechargePick)))
	mux.Handle("POST /admin/recharge", staff(http.HandlerFunc(h.rechargeStart)))
	mux.Handle("GET /admin/deposit", money(http.HandlerFunc(h.depositForm)))
	mux.Handle("POST /admin/deposit", money(http.HandlerFunc(h.depositSave)))
	mux.Handle("GET /admin/vouchers", all(http.HandlerFunc(h.vchList)))
	mux.Handle("GET /admin/vouchers/new", money(http.HandlerFunc(h.vchNew)))
	mux.Handle("POST /admin/vouchers", money(http.HandlerFunc(h.vchGenerate)))
	mux.Handle("GET /admin/vouchers/print", staff(http.HandlerFunc(h.vchPrint)))
	mux.Handle("GET /admin/vouchers/view", staff(http.HandlerFunc(h.vchView)))
	mux.Handle("GET /admin/vouchers/redeem", staff(http.HandlerFunc(h.vchRedeemForm)))
	mux.Handle("POST /admin/vouchers/redeem", staff(http.HandlerFunc(h.vchRedeem)))
	mux.Handle("POST /admin/vouchers/delete-many", managers(http.HandlerFunc(h.vchDeleteMany)))
	mux.Handle("POST /admin/vouchers/remove-old", managers(http.HandlerFunc(h.vchRemoveOld)))
	mux.Handle("POST /admin/vouchers/{id}/delete", managers(http.HandlerFunc(h.vchDelete)))
	mux.Handle("GET /admin/agent-batches", agentRead(http.HandlerFunc(h.abList)))
	mux.Handle("GET /admin/agent-batches/new", managers(http.HandlerFunc(h.abNew)))
	mux.Handle("POST /admin/agent-batches", managers(http.HandlerFunc(h.abCreate)))
	mux.Handle("GET /admin/agent-batches/{id}", agentRead(http.HandlerFunc(h.abView)))
	mux.Handle("GET /admin/agent-batches/{id}/payment", managers(http.HandlerFunc(h.abPayment)))
	mux.Handle("POST /admin/agent-batches/{id}/payment", managers(http.HandlerFunc(h.abPay)))
	mux.Handle("POST /admin/agent-batches/{id}/return", managers(http.HandlerFunc(h.abReturn)))
	mux.Handle("GET /admin/coupons", cpn(http.HandlerFunc(h.cpnList)))
	mux.Handle("GET /admin/coupons/new", cpn(http.HandlerFunc(h.cpnNew)))
	mux.Handle("POST /admin/coupons", cpn(http.HandlerFunc(h.cpnSave)))
	mux.Handle("GET /admin/coupons/{id}/edit", cpn(http.HandlerFunc(h.cpnEdit)))
	mux.Handle("POST /admin/coupons/{id}", cpn(http.HandlerFunc(h.cpnSave)))
	mux.Handle("POST /admin/coupons/{id}/toggle", cpn(http.HandlerFunc(h.cpnToggle)))
	mux.Handle("POST /admin/coupons/delete-many", cpn(http.HandlerFunc(h.cpnDeleteMany)))
	mux.Handle("POST /admin/coupons/{id}/delete", cpn(http.HandlerFunc(h.cpnDelete)))
}
