// Draws the dashboard charts from data-* attributes (see dashboard.html). data-chart="pie" uses fixed colours.
document.querySelectorAll('canvas[data-chart]').forEach(function (el) {
  var css = getComputedStyle(document.documentElement);
  var text = css.getPropertyValue('--text-2').trim() || '#575f69';
  var grid = css.getPropertyValue('--border').trim() || '#e3e6ea';
  var accent = css.getPropertyValue('--accent').trim() || '#0b7f79';
  var money = !!el.dataset.money;
  var pie = el.dataset.chart === 'pie';
  new Chart(el, {
    type: el.dataset.chart,
    data: {
      labels: JSON.parse(el.dataset.labels),
      datasets: [{
        label: el.dataset.label,
        data: JSON.parse(el.dataset.values),
        backgroundColor: pie ? [accent, '#d92d20'] : accent,
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
