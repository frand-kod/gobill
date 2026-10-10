// Customer summary panel (customer_summary.html): Alpine state for the recharge page. It loads
// /admin/customers/{id}/summary when the picker reports a customer ("customer-picked") and again when
// the plan changes ("plan-picked"), so the warnings say what that plan would do. L holds the labels.
function gbCustomerSummary(L) {
  var seq = 0;
  return {
    L: L, id: 0, plan: '', state: 'empty', d: null,
    init: function () {
      var el = document.getElementById('plan');
      this.plan = el ? el.value : '';
    },
    pick: function (id) { this.id = id || 0; this.load(); },
    setPlan: function (p) { this.plan = p || ''; if (this.id) this.load(); },
    load: function () {
      var me = this, n = ++seq;
      if (!this.id) { this.state = 'empty'; this.d = null; return; }
      this.state = 'loading';
      fetch('/admin/customers/' + this.id + '/summary?plan=' + encodeURIComponent(this.plan), { headers: { Accept: 'application/json' } })
        .then(function (r) { if (!r.ok) throw new Error(String(r.status)); return r.json(); })
        .then(function (d) { if (n === seq) { me.d = d; me.state = 'ready'; } })
        .catch(function () { if (n === seq) { me.d = null; me.state = 'error'; } });
    },
    fmt: function (s) {
      var a = Array.prototype.slice.call(arguments, 1), i = 0;
      return s.replace(/%[sd]/g, function () { return String(a[i++]); });
    },
    facts: function () {
      var out = [{ k: this.L.balance, v: this.d.balance }];
      if (this.d.voucher) out.push({ k: this.L.voucherNo, v: this.d.voucher.code }, { k: this.L.plan, v: this.d.voucher.plan });
      return out;
    },
    // the warning for an active subscription: what recharging the chosen plan does to it, or only
    // the fact that it is active when no plan is chosen yet; '' when the chosen plan does not touch it
    warn: function (s) {
      var t = this.fmt(this.L.still, s.plan, s.expires_at, s.days_left);
      if (s.effect === 'extend') return t + ' ' + this.L.extend;
      if (s.effect === 'replace') return t + ' ' + this.L.replace;
      return this.plan ? '' : t;
    },
    line: function (s) { return s.plan + ' · ' + s.expires_at + ' · ' + s.days_left; }
  };
}
