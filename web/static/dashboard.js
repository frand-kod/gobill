// Draws the dashboard charts from data-* attributes (see dashboard.html). data-chart="pie" uses fixed colours.
document.querySelectorAll('canvas[data-chart]').forEach(function (el) {
  var dark = document.documentElement.classList.contains('dark');
  var text = dark ? '#cbd5e1' : '#475569';
  var grid = dark ? '#334155' : '#e2e8f0';
  var money = !!el.dataset.money;
  var pie = el.dataset.chart === 'pie';
  new Chart(el, {
    type: el.dataset.chart,
    data: {
      labels: JSON.parse(el.dataset.labels),
      datasets: [{
        label: el.dataset.label,
        data: JSON.parse(el.dataset.values),
        backgroundColor: pie ? ['#0e8f89', '#e11d48'] : money ? '#059669' : '#0e8f89',
        borderRadius: pie ? 0 : 4
      }]
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      plugins: {
        legend: pie ? { position: 'bottom', labels: { color: text } } : { display: false },
        tooltip: money ? { callbacks: { label: function (c) { return 'Rp ' + c.parsed.y.toLocaleString('id-ID'); } } } : {}
      },
      scales: pie ? {} : {
        x: { ticks: { color: text }, grid: { display: false } },
        y: { beginAtZero: true, ticks: { color: text, precision: 0 }, grid: { color: grid } }
      }
    }
  });
});
