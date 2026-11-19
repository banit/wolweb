// Anmeldeseite (Benutzerverwaltung): Name oder E-Mail und Passwort, danach zurück zur gewünschten Seite.
(function () {
  "use strict";
  const { t } = window.wolwebI18n;
  const form = document.querySelector("#login-form");
  const box = document.querySelector("#login-error");

  // Nur Ziele auf diesem Server (unterhalb der Basisadresse) – keine Weiterleitung nach außen.
  function target() {
    const next = new URLSearchParams(location.search).get("next") || "";
    const base = new URL(document.baseURI);
    try {
      const u = new URL(next, base);
      if (u.origin === base.origin && u.pathname.startsWith(base.pathname) && !u.pathname.endsWith("/login")) return u.href;
    } catch (e) { /* ungültig */ }
    return base.href;
  }

  function showError(msg, field) {
    box.textContent = msg || "";
    box.hidden = !msg;
    for (const i of form.querySelectorAll("input")) i.removeAttribute("aria-invalid");
    if (!msg) return;
    const f = field && form.elements[field];
    if (f) {
      f.setAttribute("aria-invalid", "true");
      f.select();
      f.focus();
    } else {
      box.focus();
    }
  }

  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    const name = form.elements.name.value.trim();
    const password = form.elements.password.value;
    if (!name) return showError(t("login.name_missing"), "name");
    if (!password) return showError(t("login.password_missing"), "password");
    const btn = document.querySelector("#login-ok");
    btn.disabled = true;
    try {
      const res = await fetch("api/v1/login", {
        method: "POST", cache: "no-store", body: JSON.stringify({ name, password }),
        headers: { "Accept": "application/json", "Content-Type": "application/json", "X-WolWeb": "1" },
      });
      let data = {};
      try { data = await res.json(); } catch (err) { /* keine JSON-Antwort */ }
      if (!res.ok) return showError(data.message || t("ui.unexpected_response", res.status), data.field);
      location.replace(target());
    } catch (err) {
      showError(err.message);
    } finally {
      btn.disabled = false;
    }
  });
})();
