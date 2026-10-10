// Header search palette (app.html): one box for records, pages, quick actions and settings.
// GET /admin/search.json?q= (debounced) returns the groups; arrow keys and Enter pick, Esc closes.
// "/" and Ctrl/Cmd+K focus the box; "/" is ignored while typing in another field.
function gbPalette() {
  var seq = 0;
  return {
    q: '', groups: [], flat: [], open: false, active: -1, done: false,
    hotkey: function (e) {
      var t = e.target, typing = t.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName);
      var k = (e.ctrlKey || e.metaKey) && !e.altKey && e.key.toLowerCase() === 'k';
      if (!k && (e.key !== '/' || typing || e.ctrlKey || e.metaKey || e.altKey)) return;
      e.preventDefault();
      this.$refs.q.focus();
    },
    find: function () {
      var me = this, n = ++seq;
      this.active = -1;
      return fetch('/admin/search.json?q=' + encodeURIComponent(this.q.trim()), { headers: { Accept: 'application/json' } })
        .then(function (r) { return r.ok ? r.json() : { groups: [] }; })
        .then(function (d) {
          if (n !== seq) return;
          var flat = [];
          me.groups = (d.groups || []).map(function (g) {
            return { title: g.title, items: g.items.map(function (it) {
              var x = { title: it.title, sub: it.sub || '', url: it.url, i: flat.length };
              flat.push(x);
              return x;
            }) };
          });
          me.flat = flat;
          me.done = true;
          me.open = true;
        })
        .catch(function () { me.done = true; });
    },
    move: function (d) {
      var n = this.flat.length;
      if (!n) return;
      this.open = true;
      this.active = this.active < 0 ? (d > 0 ? 0 : n - 1) : (this.active + d + n) % n;
    },
    close: function () { this.open = false; this.active = -1; },
    // Enter opens the highlighted result; without one the form submits to /admin/search (the plain page)
    enter: function (e) {
      var it = this.flat[this.active];
      if (this.active >= 0 && it) { e.preventDefault(); location.href = it.url; }
    }
  };
}
