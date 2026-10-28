// Übersetzungen: Der Server legt den Katalog der gewählten Sprache als JSON in die Seite
// (<script id="i18n" type="application/json">). Platzhalter {0}, {1} … wie in Go.
(function () {
  "use strict";
  let dict = {};
  try { dict = JSON.parse(document.getElementById("i18n").textContent); } catch (e) { /* Schlüssel bleiben stehen */ }
  const lang = document.documentElement.lang || "de";
  function t(key, ...args) {
    let s = Object.prototype.hasOwnProperty.call(dict, key) ? dict[key] : key;
    args.forEach((a, i) => { s = s.split("{" + i + "}").join(String(a)); });
    return s;
  }
  // tn wählt Einzahl (key.one) oder Mehrzahl (key.other); die Zahl ist {0}.
  function tn(key, n, ...args) { return t(key + (n === 1 ? ".one" : ".other"), n, ...args); }
  const nav = navigator.language || "";
  const locale = lang === "de" ? "de-DE" : nav.toLowerCase().startsWith("en") ? nav : "en-GB";
  window.wolwebI18n = { t, tn, lang, locale };
})();
