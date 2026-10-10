// Customer picker (customer_pick.html): Alpine combobox over /admin/customers/pick. The chosen username
// lives in a hidden input named like the old text box, so the form handlers stay unchanged.
function gbCustomerPick(cfg) {
  var seq = 0;
  return {
    username: cfg.value || '', fullname: '', status: '', balance: '',
    editing: !cfg.value, q: '', items: [], open: false, active: -1,
    init: function () {
      // prefill (?customer=): look the chosen username up once to get its name and status
      if (!this.username) return;
      var me = this;
      fetch('/admin/customers/pick?q=' + encodeURIComponent(this.username) + '&limit=1', { headers: { Accept: 'application/json' } })
        .then(function (r) { return r.ok ? r.json() : []; })
        .then(function (rows) { var r = rows.find(function (x) { return x.username === me.username; }); if (r) { me.fill(r); me.$dispatch('customer-picked', { id: r.id }); } })
        .catch(function () {});
    },
    fill: function (r) { this.username = r.username; this.fullname = r.fullname || ''; this.status = r.status; this.balance = r.balance || ''; },
    statusDot: function () { return this.status === 'Active' ? 'bg-ok-fg' : 'bg-ink-2'; },
    openList: function () { this.find(); },
    find: function () {
      var me = this, q = this.$refs.q.value.trim(), n = ++seq;
      this.open = true;
      fetch('/admin/customers/pick?q=' + encodeURIComponent(q) + '&limit=20', { headers: { Accept: 'application/json' } })
        .then(function (r) { return r.ok ? r.json() : []; })
        .then(function (rows) { if (n === seq) { me.items = rows; me.active = -1; } })
        .catch(function () {});
    },
    move: function (d) {
      if (!this.open) { this.openList(); return; }
      var n = this.items.length;
      if (n) this.active = (this.active + d + n) % n;
    },
    choose: function (r) {
      this.fill(r);
      this.$dispatch('customer-picked', { id: r.id }); // the recharge page shows this customer's summary
      this.editing = false; this.open = false; this.q = ''; this.items = []; this.active = -1;
    },
    enter: function (e) {
      // Enter picks the highlighted row (or the first one) and does not submit the form
      if (!this.open || !this.items.length) return;
      e.preventDefault();
      this.choose(this.items[this.active >= 0 ? this.active : 0]);
    },
    change: function () {
      this.username = ''; this.fullname = ''; this.status = ''; this.balance = '';
      this.editing = true;
      this.$dispatch('customer-picked', { id: 0 });
      var me = this;
      this.$nextTick(function () { me.$refs.q.focus(); me.openList(); });
    },
    close: function () { this.open = false; this.active = -1; }
  };
}
