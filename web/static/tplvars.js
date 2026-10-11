// Variable chips under a message text box: a tap puts [[name]] at the cursor of that box.
document.addEventListener('click', function (e) {
  var chip = e.target.closest('[data-var-target]');
  if (!chip) return;
  var box = document.getElementById(chip.dataset.varTarget);
  if (!box) return;
  var text = chip.dataset.varInsert;
  var start = box.selectionStart == null ? box.value.length : box.selectionStart;
  var end = box.selectionEnd == null ? start : box.selectionEnd;
  box.value = box.value.slice(0, start) + text + box.value.slice(end);
  box.selectionStart = box.selectionEnd = start + text.length;
  box.focus();
  box.dispatchEvent(new Event('input', { bubbles: true }));
});
