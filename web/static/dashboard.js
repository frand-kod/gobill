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
// #customers-expiring-0 or #customers-expired-3 sets both, so the "Needs a look" strip can link here.
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

// Draws the dashboard bar charts from data-* attributes (see dashboard.html).
document.querySelectorAll('canvas[data-chart]').forEach(function (el) {
  var css = getComputedStyle(document.documentElement);
  var text = css.getPropertyValue('--text-2').trim() || '#575f69';
  var grid = css.getPropertyValue('--border').trim() || '#e3e6ea';
  var accent = css.getPropertyValue('--accent').trim() || '#0b7f79';
  var money = !!el.dataset.money;
  new Chart(el, {
    type: el.dataset.chart,
    data: {
      labels: JSON.parse(el.dataset.labels),
      datasets: [{
        label: el.dataset.label,
        data: JSON.parse(el.dataset.values),
        backgroundColor: accent,
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
