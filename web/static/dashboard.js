// Collapsible info cards: the chevron appears only with JS (cards are expanded without it). The state is kept per card
// in localStorage; storage can be blocked, so every access is guarded.
(function () {
  function read(key) { try { return localStorage.getItem('gb-card:' + key); } catch (e) { return null; } }
  function write(key, v) { try { localStorage.setItem('gb-card:' + key, v); } catch (e) { /* private mode: stays per page view */ } }
  document.querySelectorAll('[data-collapse]').forEach(function (card) {
    var key = card.getAttribute('data-collapse');
    var body = card.querySelector('[data-collapse-body]');
    var btn = card.querySelector('[data-collapse-toggle]');
    if (!body || !btn) return;
    function show(open) {
      body.hidden = !open;
      btn.setAttribute('aria-expanded', open ? 'true' : 'false');
    }
    show(read(key) !== 'collapsed');
    btn.classList.remove('hidden');
    btn.addEventListener('click', function () {
      var open = btn.getAttribute('aria-expanded') !== 'true';
      show(open);
      write(key, open ? 'open' : 'collapsed');
    });
  });
})();

// Customers card: the tab (expiring or expired) and the day filter (0 = today, 3, 7). Rows carry data-days
// (calendar days from today) and are shown when they are under the filter. The URL hash
// #customers-expiring-0 or #customers-expired-3 sets both, so the admin bell can link here.
// Defined here, not in an inline script, because it must exist before Alpine (deferred) starts.
window.gbCustomers = function () {
  return {
    tab: 'expiring',
    f: 3,
    read: function () {
      var m = /^#customers-(expiring|expired)-(0|3|7)$/.exec(location.hash);
      if (!m) return;
      this.tab = m[1];
      this.f = +m[2];
      var card = document.getElementById('customers');
      if (card) card.scrollIntoView({ block: 'start' });
    },
    fits: function (days) { return days < Math.max(this.f, 1); },
    count: function (list) {
      var n = 0, self = this;
      document.querySelectorAll('#customers [data-list="' + list + '"] tr[data-days]').forEach(function (tr) {
        if (self.fits(+tr.dataset.days)) n++;
      });
      return n;
    }
  };
};

// Sales | New customers card: one chart per tab. A chart drawn while its tab was hidden is resized when the tab opens.
window.gbSalesTabs = function () {
  return {
    t: 'sales',
    show: function (name) {
      this.t = name;
      var root = this.$el;
      this.$nextTick(function () {
        root.querySelectorAll('canvas[data-chart]').forEach(function (c) { if (c.gbChart) c.gbChart.resize(); });
      });
    }
  };
};

// Draws the dashboard bar charts from data-* attributes (see dashboard.html).
document.querySelectorAll('canvas[data-chart]').forEach(function (el) {
  var css = getComputedStyle(document.documentElement);
  var text = css.getPropertyValue('--text-2').trim() || '#575f69';
  var grid = css.getPropertyValue('--border').trim() || '#e3e6ea';
  var bar = css.getPropertyValue('--info-fg').trim() || '#1a5a9e';
  var money = !!el.dataset.money;
  el.gbChart = new Chart(el, {
    type: el.dataset.chart,
    data: {
      labels: JSON.parse(el.dataset.labels),
      datasets: [{
        label: el.dataset.label,
        data: JSON.parse(el.dataset.values),
        backgroundColor: bar,
        borderRadius: 4
      }]
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      plugins: {
        legend: { display: false },
        tooltip: money ? { callbacks: { label: function (c) { return 'Rp ' + c.parsed.y.toLocaleString('id-ID'); } } } : {}
      },
      scales: {
        x: { ticks: { color: text }, grid: { display: false } },
        y: { beginAtZero: true, ticks: { color: text, precision: 0 }, grid: { color: grid } }
      }
    }
  });
});

// Customer rows: the username opens a small card (.gb-pop). Hover opens it on pointer devices; keyboard focus opens it;
// Escape and leaving the row close it. On touch devices the first tap opens the card and the second tap follows the link.
(function () {
  var touch = window.matchMedia('(hover: none)');
  function closeAll(keep) {
    document.querySelectorAll('.gb-who.is-open').forEach(function (w) { if (w !== keep) w.classList.remove('is-open'); });
  }
  function closest(el, sel) { return el && el.closest ? el.closest(sel) : null; }
  // .gb-pop is fixed (the table scrolls and would clip it), so it is placed under the username, or above it near the bottom
  function place(w) {
    var pop = w.querySelector('.gb-pop'), trig = w.querySelector('[data-pop-trigger]');
    if (!pop || !trig) return;
    var r = trig.getBoundingClientRect();
    pop.style.left = r.left + 'px';
    pop.style.top = r.bottom + 4 + 'px';
    if (r.bottom + 4 + pop.offsetHeight > window.innerHeight) pop.style.top = r.top - pop.offsetHeight - 4 + 'px';
  }
  function open(w) {
    closeAll(w);
    w.classList.add('is-open');
    place(w);
  }
  document.addEventListener('click', function (e) {
    var w = closest(e.target, '.gb-who');
    if (w && closest(e.target, '[data-pop-trigger]') && touch.matches && !w.classList.contains('is-open')) {
      e.preventDefault();
      open(w);
      return;
    }
    closeAll(w);
  });
  document.addEventListener('mouseover', function (e) {
    var w = closest(e.target, '.gb-who');
    if (w && !touch.matches) place(w);
  });
  document.addEventListener('focusin', function (e) {
    var w = closest(e.target, '.gb-who');
    if (w && !touch.matches && closest(e.target, '[data-pop-trigger]')) open(w);
  });
  // a placed card would sit in the wrong spot once the page moves
  window.addEventListener('scroll', function () { closeAll(); }, true);
  document.addEventListener('focusout', function (e) {
    var w = closest(e.target, '.gb-who');
    if (w && !w.contains(e.relatedTarget)) w.classList.remove('is-open');
  });
  document.addEventListener('keydown', function (e) {
    if (e.key === 'Escape') closeAll();
  });
})();
