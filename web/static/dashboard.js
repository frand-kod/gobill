// Draws the dashboard charts from data-* attributes (see dashboard.html).
document.querySelectorAll('canvas[data-chart]').forEach(function (el) {
  var dark = document.documentElement.classList.contains('dark');
  var text = dark ? '#cbd5e1' : '#475569';
  var grid = dark ? '#334155' : '#e2e8f0';
  var money = !!el.dataset.money;
  new Chart(el, {
    type: el.dataset.chart,
    data: {
      labels: JSON.parse(el.dataset.labels),
      datasets: [{
        label: el.dataset.label,
        data: JSON.parse(el.dataset.values),
        backgroundColor: money ? '#059669' : '#0e8f89',
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
