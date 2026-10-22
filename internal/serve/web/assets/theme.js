// Sets the theme and the motion before the first paint, so that the page does not flash. The fragment
// (theme=light|dark, motion=off) wins over the stored choice, which wins over the system.
(function () {
  try {
    var h = location.hash, root = document.documentElement;
    var m = /[#&?]theme=(light|dark)/.exec(h), t = m && m[1];
    if (!t) { try { t = localStorage.getItem('pravo.theme'); } catch (e) { t = null; } }
    if (t === 'light' || t === 'dark') root.setAttribute('data-theme', t);
    if (/[#&?]motion=off/.test(h)) root.setAttribute('data-motion', 'off');
  } catch (e) { /* the system theme stays */ }
})();
