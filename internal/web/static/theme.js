// Gespeicherte Hell/Dunkel-Wahl und Ansicht vor dem ersten Zeichnen setzen (verhindert Aufblitzen).
(function () {
  var root = document.documentElement;
  try {
    var t = localStorage.getItem("wolweb-theme");
    if (t === "light" || t === "dark") root.setAttribute("data-theme", t);
  } catch (e) { /* kein Speicher verfügbar */ }
  // Einfache Ansicht: ?view=simple im Link (etwa als Lesezeichen für jemand anderen) gilt und wird
  // gemerkt, ?view=normal schaltet zurück; sonst die gespeicherte Wahl.
  var v = new URLSearchParams(location.search).get("view");
  try {
    if (v === "simple" || v === "normal") localStorage.setItem("wolweb-view", v);
    else v = localStorage.getItem("wolweb-view");
  } catch (e) { /* kein Speicher verfügbar */ }
  if (v === "simple") root.classList.add("simple");
})();
