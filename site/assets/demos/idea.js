/* Demo "idea". Follows the unify widget (assets/widgets.js) and shows
   the program and the output of pravo eval for the value that was dropped.
   The four programs are in the HTML, recorded by tools/capture; this script
   only chooses which one to show and which sentence to light. */
document.addEventListener("DOMContentLoaded", function () {
  "use strict";
  var band = document.getElementById("idea");
  var w = band && band.querySelector('.widget[data-widget="unify"]');
  var prog = document.getElementById("idea-prog");
  if (!w || !prog) return;
  var pairs = [].slice.call(prog.querySelectorAll(".idea-pair"));
  var sents = [].slice.call(band.querySelectorAll(".idea-s"));
  var shown = null;

  band.classList.add("is-live");

  function light(n) {
    band.setAttribute("data-lit", n);
    sents.forEach(function (s) { s.classList.toggle("on", +s.getAttribute("data-s") === n); });
  }
  function show(i, pending) {
    pairs.forEach(function (p, k) {
      var on = k === (i < 0 ? 0 : i);
      p.classList.toggle("is-on", on);
      p.classList.toggle("is-pending", on && pending);
      if (on) {
        p.classList.remove("is-enter");
        if (shown !== k) { void p.offsetWidth; p.classList.add("is-enter"); }
      }
    });
    shown = i < 0 ? 0 : i;
  }
  function chosen() {
    var chips = [].slice.call(w.querySelectorAll(".w-chips .chip"));
    for (var k = 0; k < chips.length; k++) if (chips[k].getAttribute("aria-pressed") === "true") return k;
    return -1;
  }
  function sync() {
    var state = w.getAttribute("data-state");
    var m = w.querySelector(".w-merged");
    var bad = m && m.classList.contains("is-bad"), ok = m && m.classList.contains("is-ok");
    var k = chosen();
    if (state === "apart") { light(1); show(-1, true); return; }
    if (k >= 0 && (bad || ok)) { light(bad ? 3 : 2); show(k, false); return; }
    light(2);
    show(-1, true);
  }

  w.addEventListener("click", function (e) {
    if (e.target.closest(".w-chips .chip")) shown = null; /* the same value again replays the entry */
  });
  new MutationObserver(sync).observe(w, { attributes: true, subtree: true, attributeFilter: ["class", "data-state", "aria-pressed"] });
  sync();
});
