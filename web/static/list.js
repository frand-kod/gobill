// List pages. The search form is live: typing (debounced) or changing a filter or date re-fetches the page and swaps
// only [data-list], so the search box keeps its focus and caret. Bulk: the selected count. Without JS the Search
// button, the header links and the plain form submit still work.
(function () {
  var timer = 0, seq = 0;

  function bulkCount() {
    var n = document.querySelectorAll('input[name=ids]:checked').length;
    document.querySelectorAll('[data-bulk-count]').forEach(function (el) { el.textContent = n; });
  }

  function load(form) {
    if (!form) return;
    var url = new URL(form.getAttribute('action') || location.pathname, location.href);
    var params = new URLSearchParams();
    new FormData(form).forEach(function (v, k) { if (v !== '') params.append(k, v); });
    url.search = params.toString();
    var mine = ++seq;
    fetch(url.href, { credentials: 'same-origin', headers: { 'X-Requested-With': 'fetch' } })
      .then(function (r) { if (!r.ok) throw new Error('status ' + r.status); return r.text(); })
      .then(function (html) {
        if (mine !== seq) return;
        var cur = document.querySelector('[data-list]');
        var next = new DOMParser().parseFromString(html, 'text/html').querySelector('[data-list]');
        if (!cur || !next) throw new Error('no list');
        var a = document.activeElement, focus = a && cur.contains(a) && a.name ? a : null;
        var s = focus && focus.selectionStart, e = focus && focus.selectionEnd;
        cur.replaceWith(next);
        history.replaceState(null, '', url.href);
        if (focus) {
          var el = document.querySelector('[data-list] [name="' + focus.name + '"]');
          if (el) {
            el.focus({ preventScroll: true });
            try { if (s != null) el.setSelectionRange(s, e); } catch (x) { /* not a text field */ }
          }
        }
        bulkCount();
      })
      .catch(function () { location.href = url.href; });
  }

  function current() { return document.querySelector('form[data-live]'); }

  document.addEventListener('input', function (e) {
    var t = e.target;
    if (!t.form || !t.form.hasAttribute('data-live') || t.type !== 'search') return;
    seq++; // a response still in flight would put back the old text
    clearTimeout(timer);
    timer = setTimeout(function () { load(current()); }, 300);
  });

  document.addEventListener('change', function (e) {
    var t = e.target;
    if (t.hasAttribute('data-select-all')) {
      document.querySelectorAll('input[name=ids]').forEach(function (c) { c.checked = t.checked; });
      bulkCount();
    } else if (t.name === 'ids') {
      bulkCount();
    } else if (t.form && t.form.hasAttribute('data-live') && (t.tagName === 'SELECT' || t.type === 'date')) {
      clearTimeout(timer);
      load(t.form);
    }
  });

  document.addEventListener('submit', function (e) {
    var f = e.target;
    if (!f.hasAttribute('data-live')) return;
    e.preventDefault();
    clearTimeout(timer);
    load(f);
  });

  bulkCount();
})();
