// Variable buttons under a message text box: a tap puts [[name]] at the cursor of that box.
// Guarded: an in-place settings tab swap runs this script again, and one listener is enough.
(function () {
  if (window.gbTplVars) return;
  window.gbTplVars = true;
  document.addEventListener('click', function (e) {
    var btn = e.target.closest('[data-var-target]');
    if (!btn) return;
    var box = document.getElementById(btn.dataset.varTarget);
    if (!box) return;
    var text = btn.dataset.varInsert;
    var start = box.selectionStart == null ? box.value.length : box.selectionStart;
    var end = box.selectionEnd == null ? start : box.selectionEnd;
    box.value = box.value.slice(0, start) + text + box.value.slice(end);
    box.selectionStart = box.selectionEnd = start + text.length;
    box.focus();
    box.dispatchEvent(new Event('input', { bubbles: true }));
  });
})();
