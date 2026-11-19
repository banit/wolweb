// Mein Konto: E-Mail und Passwort ändern, abmelden.
(function () {
  "use strict";
  const { t } = window.wolwebI18n;
  const $ = (s) => document.querySelector(s);

  async function api(method, url, body) {
    const opts = { method, cache: "no-store", headers: { "Accept": "application/json", "X-WolWeb": "1" } };
    if (body !== undefined) {
      opts.headers["Content-Type"] = "application/json";
      opts.body = JSON.stringify(body);
    }
    const res = await fetch("api/v1/" + url, opts);
    let data = {};
    try { data = await res.json(); } catch (e) { /* keine JSON-Antwort */ }
    if (res.status === 401 && data.login) {
      location.href = "login?next=account";
      throw new Error(data.message || "");
    }
    if (!res.ok) throw Object.assign(new Error(data.message || t("ui.unexpected_response", res.status)), { data, status: res.status });
    return data;
  }
  function toast(title, text) {
    const n = document.createElement("div");
    n.className = "toast ok";
    const box = document.createElement("div");
    const strong = document.createElement("strong");
    strong.textContent = title;
    box.append(strong);
    if (text) {
      const p = document.createElement("p");
      p.textContent = text;
      box.append(p);
    }
    n.append(box);
    $("#toasts").append(n);
    setTimeout(() => { n.classList.add("out"); setTimeout(() => n.remove(), 250); }, 4200);
  }
  // showError zeigt eine Meldung unter dem Formular und markiert das Feld (name).
  function showError(form, box, name, message) {
    for (const i of form.querySelectorAll("[aria-invalid]")) i.removeAttribute("aria-invalid");
    box.textContent = message || "";
    box.hidden = !message;
    if (!message) return;
    const f = name && form.elements[name];
    if (f) {
      f.setAttribute("aria-invalid", "true");
      f.focus();
    } else {
      box.focus();
    }
  }

  function render(me) {
    const role = me.admin ? t("acc.role.admin") : t("acc.role.user");
    const groups = me.groups.length ? " · " + me.groups.join(", ") : "";
    $("#acc-who").textContent = me.name + " · " + role + groups;
    $("#acc-mail-form").elements.email.value = me.email || "";
  }

  const mail = $("#acc-mail-form");
  mail.addEventListener("submit", async (e) => {
    e.preventDefault();
    const btn = $("#acc-mail-save");
    btn.disabled = true;
    try {
      render(await api("PUT", "account", { email: mail.elements.email.value.trim() }));
      showError(mail, $("#acc-mail-error"));
      toast(t("acc.email.saved"));
    } catch (err) {
      showError(mail, $("#acc-mail-error"), err.data && err.data.field, err.message);
    } finally {
      btn.disabled = false;
    }
  });

  const pw = $("#acc-pw-form");
  pw.addEventListener("submit", async (e) => {
    e.preventDefault();
    const f = pw.elements;
    const box = $("#acc-pw-error");
    if (!f.current.value) return showError(pw, box, "current", t("set.pw.current_missing"));
    if (!f.password.value) return showError(pw, box, "password", t("set.pw.missing"));
    if (f.password.value !== f.repeat.value) return showError(pw, box, "repeat", t("set.pw.mismatch"));
    const btn = $("#acc-pw-save");
    btn.disabled = true;
    try {
      await api("PUT", "account/password", { current: f.current.value, password: f.password.value });
      for (const i of pw.querySelectorAll("input")) i.value = "";
      showError(pw, box);
      toast(t("acc.pw.saved"), t("acc.pw.saved_text"));
    } catch (err) {
      const field = err.data && err.data.field;
      showError(pw, box, field === "current" || field === "password" ? field : "", err.message);
    } finally {
      btn.disabled = false;
    }
  });

  $("#acc-logout").addEventListener("click", async () => {
    try { await api("POST", "logout"); } catch (e) { /* trotzdem zur Anmeldung */ }
    location.href = "login";
  });

  api("GET", "account").then(render).catch((err) => { $("#acc-who").textContent = err.message; });
})();
