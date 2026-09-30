// Gespeicherte Hell/Dunkel-Wahl vor dem ersten Zeichnen setzen (verhindert Aufblitzen).
(function () {
  try {
    var t = localStorage.getItem("wolweb-theme");
    if (t === "light" || t === "dark") document.documentElement.setAttribute("data-theme", t);
  } catch (e) { /* kein Speicher verfügbar */ }
})();
