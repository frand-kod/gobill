// Recharge confirm as a modal (recharge.html, customer.html). A form marked data-recharge is sent with
// X-Fragment: 1; the server answers with the summary card (200) or the messages to show (422). The final
// "Recharge" button is a normal form post. Without JS or <dialog>, the form posts as before and the full
// confirm page opens.
(function () {
  var dlg = document.getElementById('recharge-dlg');
  if (!dlg || !dlg.showModal) return;
  var body = document.getElementById('recharge-dlg-body'), fail = document.getElementById('recharge-dlg-fail'), busy = false, opener = null;

  function open() {
    dlg.showModal();
    var c = dlg.querySelector('[data-recharge-close]');
    if (c) c.focus();
  }
  // back to the button that opened the dialog, when it closes (Escape, Cancel, backdrop)
  dlg.addEventListener('close', function () {
    if (opener && opener.isConnected) opener.focus();
    opener = null;
  });
  dlg.addEventListener('click', function (e) {
    if (e.target === dlg || (e.target.closest && e.target.closest('[data-recharge-close]'))) dlg.close();
  });

  // capture phase: runs before the double-submit lock in base.html, so this form is not locked twice
  document.addEventListener('submit', function (e) {
    var f = e.target;
    if (!f.matches || !f.matches('form[data-recharge]')) return;
    e.preventDefault();
    if (busy) return;
    busy = true;
    var btn = e.submitter || f.querySelector('[type=submit]');
    if (btn) btn.disabled = true;
    f.setAttribute('aria-busy', 'true');
    opener = btn;
    // redirect: 'manual' so a server redirect shows as a failure, never as a whole page inside the dialog
    fetch(f.action, { method: 'POST', body: new FormData(f), credentials: 'same-origin', redirect: 'manual', headers: { 'X-Fragment': '1', Accept: 'text/html' } })
      .then(function (r) { return r.text().then(function (html) { return { status: r.status, html: html }; }); })
      .then(function (res) {
        if (res.status === 200 || res.status === 422) body.innerHTML = res.html;
        else body.replaceChildren(fail.content.cloneNode(true));
      }, function () { body.replaceChildren(fail.content.cloneNode(true)); })
      .then(function () {
        open();
        busy = false;
        if (btn) btn.disabled = false;
        f.removeAttribute('aria-busy');
      });
  }, true);
  // back/forward cache restores the page with the trigger button disabled
  addEventListener('pageshow', function (e) {
    if (e.persisted) { busy = false; document.querySelectorAll('form[data-recharge] [type=submit]').forEach(function (b) { b.disabled = false; }); }
  });
})();
