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

// Settings tabs switch in place. A tab link under #tab-content > nav fetches its page and swaps the #tab-content
// block (tabs and form) in, then sets the title and the history entry, moves focus to the heading and runs Alpine
// on the new block. Any error, a non-OK answer, or a page without the block falls back to a normal navigation.
// A form with unsaved edits navigates normally, as before. Scripts in the block are run again (tplvars.js guards
// itself against that).
(function () {
  var box = document.getElementById('tab-content'), seq = 0;
  if (!box || !window.DOMParser || !window.fetch || !history.pushState) return;

  function dirty() {
    var f = box.querySelector('form');
    return !!f && Array.prototype.some.call(f.elements, function (el) {
      if (el.tagName === 'SELECT') return Array.prototype.some.call(el.options, function (o) { return o.selected !== o.defaultSelected; });
      if (el.type === 'checkbox' || el.type === 'radio') return el.checked !== el.defaultChecked;
      if (el.type === 'file') return el.files.length > 0;
      return 'value' in el && el.type !== 'submit' && el.type !== 'button' && el.value !== el.defaultValue;
    });
  }

  // a script from DOMParser does not run; a fresh element does, in order
  function revive(node) {
    Array.prototype.forEach.call(node.querySelectorAll('script'), function (old) {
      var s = document.createElement('script');
      Array.prototype.forEach.call(old.attributes, function (a) { s.setAttribute(a.name, a.value); });
      s.async = false;
      s.textContent = old.textContent;
      old.parentNode.replaceChild(s, old);
    });
  }

  function show(url, push) {
    var n = ++seq;
    fetch(url, {credentials: 'same-origin', headers: {Accept: 'text/html'}}).then(function (r) {
      if (!r.ok) throw new Error('status ' + r.status);
      return r.text();
    }).then(function (html) {
      var doc = new DOMParser().parseFromString(html, 'text/html');
      var next = doc.getElementById('tab-content');
      if (!next) throw new Error('no tab content');
      if (n !== seq) return; // a newer click has won
      var swap = function () {
        var node = document.importNode(next, true);
        revive(node);
        box.replaceWith(node);
        box = node;
        // the page's toast region comes with the response (the flash of that page); the intro and toast wiring then runs on it
        var oldToasts = document.getElementById('toasts'), newToasts = doc.getElementById('toasts');
        if (oldToasts) oldToasts.remove();
        if (newToasts) document.body.prepend(document.importNode(newToasts, true));
        if (window.gbBind) gbBind(document);
        if (window.Alpine) Alpine.initTree(node);
        document.title = doc.title;
        if (push) history.pushState({gbTabs: true}, '', url);
        var h = node.querySelector('h1');
        if (h) h.focus();
      };
      if (document.startViewTransition) document.startViewTransition(swap); else swap();
    }).catch(function () {
      if (n === seq) location.href = url;
    });
  }

  document.addEventListener('click', function (e) {
    var a = e.target.closest ? e.target.closest('#tab-content > nav a[href]') : null;
    // data-no-swap: a link to a page without the block (the Promotions list), so it is not fetched first
    if (!a || a.hasAttribute('data-no-swap') || e.defaultPrevented || e.button || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    if (a.origin !== location.origin || a.target && a.target !== '_self') return;
    if (!/^\/admin\/settings\/[\w-]+$/.test(a.pathname) || a.pathname === location.pathname) return;
    if (dirty()) return;
    e.preventDefault();
    show(a.href, true);
  });
  window.addEventListener('popstate', function () { show(location.href, false); });
})();
