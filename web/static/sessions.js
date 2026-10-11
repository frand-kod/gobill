// Live online sessions (/admin/radius/sessions). Every 10 s, while the page is visible, it fetches the same
// URL as JSON. Alpine shows the tiles and the sparkline. Rows are matched by data-id and updated in place, so
// the checkboxes and the focus stay put. New rows flash once; rows that ended fade out.
(function () {
  var EVERY_MS = 10000, NEW_MS = 1600, ENDED_MS = 600;
  var SEG = 'h-3 w-1.5 rounded-sm ';

  function setText(tr, field, text) {
    var el = tr.querySelector('[data-f="' + field + '"]');
    if (el && el.textContent !== text) el.textContent = text;
  }

  function flash(el, cls, ms) {
    el.classList.add(cls);
    setTimeout(function () { el.classList.remove(cls); }, ms);
  }

  function fill(tr, r, labels) {
    ['user', 'plan', 'nas', 'ip', 'mac', 'start', 'for', 'up', 'down', 'speed'].forEach(function (f) {
      setText(tr, f, r[f] == null ? '' : String(r[f]));
    });
    var stale = tr.querySelector('[data-f="stale"]');
    if (stale) stale.classList.toggle('hidden', !r.stale);
    var meter = tr.querySelector('[data-f="meter"]');
    if (meter) meter.setAttribute('aria-label', r.speed);
    var bars = r.bars || [];
    tr.querySelectorAll('[data-f="meter"] i').forEach(function (el, i) {
      var cls = SEG + (bars[i] || 'bg-line');
      if (el.className !== cls) el.className = cls;
    });
    var box = tr.querySelector('input[name=ids]');
    if (box) box.setAttribute('aria-label', r.user);
    var form = tr.querySelector('form');
    if (form) form.dataset.confirm = labels.disconnect + ' ' + r.user + '?';
  }

  window.liveSessions = function () {
    return {
      tiles: {},
      spark: [],
      status: '',
      labels: {},
      body: null,
      tpl: null,
      url: '',

      init: function () {
        var el = this.$el, st = {};
        try { st = JSON.parse(el.dataset.state || '{}'); } catch (e) { st = {}; }
        this.tiles = st.tiles || {};
        this.spark = st.spark || [];
        this.labels = {
          disconnect: el.dataset.disconnect || '',
          updated: el.dataset.updated || '',
          failed: el.dataset.failed || '',
          empty: el.dataset.empty || ''
        };
        this.status = el.dataset.idle || '';
        this.body = document.getElementById('sess-body');
        this.tpl = document.getElementById('sess-row-tpl');
        this.url = el.dataset.url + window.location.search;
        var self = this;
        setInterval(function () { if (!document.hidden) self.refresh(); }, EVERY_MS);
        document.addEventListener('visibilitychange', function () { if (!document.hidden) self.refresh(); });
      },

      refresh: function () {
        var self = this;
        return fetch(this.url, { credentials: 'same-origin', cache: 'no-store', headers: { Accept: 'application/json' } })
          .then(function (res) {
            if (!res.ok) throw new Error('HTTP ' + res.status);
            return res.json();
          })
          .then(function (v) {
            self.tiles = v.tiles || {};
            self.spark = v.spark || [];
            self.rows(v.rows || []);
            self.status = self.labels.updated.replace('%s', new Date().toLocaleTimeString());
          })
          .catch(function () { self.status = self.labels.failed; });
      },

      rows: function (list) {
        var body = this.body, self = this, ids = {}, prev = null;
        var empty = body.querySelector('.sess-empty');
        if (empty && list.length) empty.remove();
        list.forEach(function (r) {
          ids[r.id] = true;
          var tr = body.querySelector('tr[data-id="' + r.id + '"]');
          if (!tr) {
            tr = self.tpl.content.firstElementChild.cloneNode(true);
            tr.setAttribute('data-id', r.id);
            flash(tr, 'row-new', NEW_MS);
          }
          tr.classList.remove('row-ended');
          fill(tr, r, self.labels);
          var want = prev ? prev.nextElementSibling : body.firstElementChild;
          if (tr !== want) body.insertBefore(tr, want);
          prev = tr;
        });
        // sessions that are no longer open: fade out, then remove
        body.querySelectorAll('tr[data-id]').forEach(function (tr) {
          if (ids[tr.getAttribute('data-id')] || tr.classList.contains('row-ended')) return;
          tr.classList.add('row-ended');
          setTimeout(function () { if (tr.classList.contains('row-ended')) tr.remove(); }, ENDED_MS);
        });
        if (!list.length && !body.querySelector('tr')) {
          var none = document.createElement('tr');
          var td = document.createElement('td');
          none.className = 'sess-empty';
          td.colSpan = 12;
          td.className = 'py-8 text-center text-ink-2';
          td.textContent = this.labels.empty;
          none.appendChild(td);
          body.appendChild(none);
        }
      },

      // sparkPoints draws the 24 h online count as an SVG polyline in a 288 x 40 box.
      sparkPoints: function () {
        var s = this.spark || [], n = s.length;
        if (n < 2) return '';
        var max = 1;
        s.forEach(function (v) { if (v > max) max = v; });
        return s.map(function (v, i) {
          return (i * 288 / (n - 1)).toFixed(1) + ',' + (38 - v / max * 34).toFixed(1);
        }).join(' ');
      }
    };
  };
})();
