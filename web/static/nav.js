// Prefetch on intent. A same-origin GET link is prefetched (<link rel="prefetch">) when the pointer rests on it
// for 65 ms, when a touch starts on it, or when it gets keyboard focus. At most 20 per page, each URL once.
// Skipped: data-no-prefetch, download or non-_self links, other origins, fragment-only links, JSON, exports and
// logout. Nothing is fetched when the browser asks for saveData. Without JS the links work as before.
(function () {
  var MAX = 20, seen = {}, count = 0, timer = 0, hover = null;
  var conn = navigator.connection;
  if (conn && conn.saveData) return;

  function anchor(t) { return t && t.closest ? t.closest('a[href]') : null; }

  function ok(a) {
    if (a.origin !== location.origin || a.closest('[data-no-prefetch]')) return false;
    if (a.hasAttribute('download') || (a.target && a.target !== '_self')) return false;
    var p = a.pathname;
    if (!/^\/(admin|portal)(\/|$)/.test(p) || /\.json$|\/export$|\/logout$/.test(p)) return false;
    // a same-document link (#anchor, or the current page) has nothing to fetch
    return a.href.split('#')[0] !== location.href.split('#')[0];
  }

  function prefetch(a) {
    if (!a || !ok(a)) return;
    var url = a.href.split('#')[0];
    if (seen[url] || count >= MAX) return;
    seen[url] = true; count++;
    var l = document.createElement('link');
    l.rel = 'prefetch'; l.href = url;
    document.head.appendChild(l);
  }

  // pointerover bubbles (pointerenter does not), so one listener covers the page
  document.addEventListener('pointerover', function (e) {
    var a = anchor(e.target);
    if (a === hover) return;
    clearTimeout(timer);
    hover = a;
    if (a && e.pointerType !== 'touch') timer = setTimeout(function () { prefetch(a); }, 65);
  });
  document.addEventListener('pointerout', function (e) {
    var a = anchor(e.target);
    if (a && !a.contains(e.relatedTarget)) { clearTimeout(timer); hover = null; }
  });
  document.addEventListener('touchstart', function (e) { prefetch(anchor(e.target)); }, {passive: true});
  document.addEventListener('focusin', function (e) { prefetch(anchor(e.target)); });
})();
