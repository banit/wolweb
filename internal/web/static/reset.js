// Passwort vergessen: Link per Mail anfordern (ohne ?token) oder mit dem Link ein neues Passwort setzen.
(function () {
  "use strict";
  const { t } = window.wolwebI18n;
  const $ = (s) => document.querySelector(s);
  const token = new URLSearchParams(location.search).get("token");

  async function post(url, body) {
    const res = await fetch("api/v1/" + url, {
      method: "POST", cache: "no-store", body: JSON.stringify(body),
      headers: { "Accept": "application/json", "Content-Type": "application/json", "X-WolWeb": "1" },
    });
    let data = {};
    try { data = await res.json(); } catch (e) { /* keine JSON-Antwort */ }
    if (!res.ok) throw Object.assign(new Error(data.message || t("ui.unexpected_response", res.status)), { data });
    return data;
  }
  function showError(box, form, field, msg) {
    box.textContent = msg || "";
    box.hidden = !msg;
    for (const i of form.querySelectorAll("input")) i.removeAttribute("aria-invalid");
    if (!msg) return;
    const f = field && form.elements[field];
    if (f) {
      f.setAttribute("aria-invalid", "true");
      f.focus();
    } else {
      box.focus();
    }
  }

  if (!token) {
    const form = $("#reset-request");
    form.hidden = false;
    const name = form.elements.name;
    if (name) name.focus();
    form.addEventListener("submit", async (e) => {
      e.preventDefault();
      const box = $("#reset-request-error");
      if (!name.value.trim()) return showError(box, form, "name", t("login.name_missing"));
      const btn = $("#reset-request-ok");
      btn.disabled = true;
      try {
        await post("password-reset", { name: name.value.trim() });
        form.hidden = true;
        $("#reset-sent").hidden = false;
        $("#reset-sent").focus();
      } catch (err) {
        showError(box, form, "", err.message);
      } finally {
        btn.disabled = false;
      }
    });
    return;
  }

  // Den Link nicht in der Adresszeile (und im Verlauf) stehen lassen.
  history.replaceState(null, "", location.pathname);
  const form = $("#reset-confirm");
  form.hidden = false;
  form.elements.password.focus();
  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    const f = form.elements;
    const box = $("#reset-confirm-error");
    if (!f.password.value) return showError(box, form, "password", t("set.pw.missing"));
    if (f.password.value !== f.repeat.value) return showError(box, form, "repeat", t("set.pw.mismatch"));
    const btn = $("#reset-confirm-ok");
    btn.disabled = true;
    try {
      await post("password-reset/confirm", { token, password: f.password.value });
      location.replace(new URL(document.baseURI).href); // angemeldet: zur Übersicht
    } catch (err) {
      showError(box, form, err.data && err.data.field === "password" ? "password" : "", err.message);
    } finally {
      btn.disabled = false;
    }
  });
})();
