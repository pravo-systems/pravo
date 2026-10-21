/* The download block of the front page: picks the build for this system from the
   data of the latest release. Without the data or without a script the block
   keeps its link to the releases page. */
(function () {
  "use strict";
  var d = document, box = d.querySelector("[data-download]");
  if (!box) return;
  var rel;
  try { rel = JSON.parse(box.querySelector(".download-data").textContent); } catch (e) { return; }
  if (!rel || !rel.assets || !rel.assets.length) return;
  var OS = { darwin: "macOS", linux: "Linux", windows: "Windows" };
  var ARCH = { amd64: "Intel or AMD", arm64: "Arm" };
  var btn = box.querySelector(".download-btn"), note = box.querySelector(".download-note");
  var cmd = box.querySelector(".download-cmd"), all = box.querySelector(".download-all");

  function find(os, arch) {
    for (var i = 0; i < rel.assets.length; i++) {
      if (rel.assets[i].os === os && rel.assets[i].arch === arch) return rel.assets[i];
    }
  }
  function mb(n) { return (n / 1048576).toFixed(1) + " MB"; }
  function show(os, arch) {
    var a = find(os, arch);
    if (!a) return;
    btn.href = a.url;
    btn.firstChild.nodeValue = "Download pravo " + rel.version + " for " + OS[os] + " ";
    note.textContent = OS[os] + ", " + ARCH[arch] + " (" + arch + "), " + mb(a.size) + ". Checksums are in SHA256SUMS on the release page.";
    var line = os === "windows" ? "irm https://pravo.systems/install.ps1 | iex" : "curl -fsSL https://pravo.systems/install.sh | sh";
    cmd.textContent = line;
    box.querySelector(".copy").setAttribute("data-copy", line);
    box.querySelector(".prompt").textContent = os === "windows" ? ">" : "$";
  }

  var list = box.querySelector(".download-list");
  rel.assets.forEach(function (a) {
    var li = d.createElement("li"), link = d.createElement("a");
    link.href = a.url;
    link.textContent = OS[a.os] + " " + a.arch;
    li.appendChild(link);
    li.appendChild(d.createTextNode(" " + mb(a.size)));
    list.appendChild(li);
  });
  all.hidden = false;

  var uad = navigator.userAgentData, ua = navigator.userAgent || "";
  var sys = ((uad && uad.platform) || navigator.platform || "") + " " + ua;
  var os = /Win/i.test(sys) ? "windows" : /Mac|iPhone|iPad/i.test(sys) ? "darwin" : /Linux|X11|CrOS/i.test(sys) ? "linux" : "";
  if (!os) return;
  /* Safari and Firefox hide the architecture. A Mac is taken to be an Arm one. */
  var arch = /arm|aarch64/i.test(ua) || os === "darwin" ? "arm64" : "amd64";
  show(os, arch);
  if (uad && uad.getHighEntropyValues) {
    uad.getHighEntropyValues(["architecture"]).then(function (h) {
      if (h.architecture) show(os, h.architecture === "arm" ? "arm64" : "amd64");
    }, function () {});
  }
})();
