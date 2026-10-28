// Bearbeitungsschutz (Passwort für Änderungen): Dialog zum Entsperren und Sperren, gemeinsam für
// Übersicht und Einstellungen. Ansehen und Wecken gehen auch gesperrt; der Server entscheidet.
(function () {
  "use strict";
  const { t } = window.wolwebI18n;
  const $ = (s) => document.querySelector(s);

  async function post(url, body) {
    const res = await fetch("api/v1/" + url, {
      method: "POST", cache: "no-store", body: JSON.stringify(body || {}),
      headers: { "Accept": "application/json", "Content-Type": "application/json", "X-WolWeb": "1" },
    });
    let data = {};
    try { data = await res.json(); } catch (e) { /* keine JSON-Antwort */ }
    if (!res.ok) throw Object.assign(new Error(data.message || t("ui.unexpected_response", res.status)), { data, status: res.status });
    return data;
  }

  function showError(msg) {
    const err = $("#unlock-error");
    err.textContent = msg;
    err.hidden = !msg;
    if (msg) $("#unlock-password").setAttribute("aria-invalid", "true");
    else $("#unlock-password").removeAttribute("aria-invalid");
  }

  let pending = null; // ein offener Dialog: alle, die gerade warten, bekommen dasselbe Ergebnis

  // unlock fragt nach dem Passwort und entsperrt diesen Browser; true bei Erfolg.
  function unlock() {
    if (pending) return pending;
    const d = $("#unlock-dialog");
    const form = $("#unlock-form");
    const input = $("#unlock-password");
    const back = document.activeElement;
    let ok = false;
    const submit = async (e) => {
      e.preventDefault();
      if (!input.value) { showError(t("set.pw.current_missing")); input.focus(); return; }
      const btn = $("#unlock-ok");
      btn.disabled = true;
      try {
        await post("unlock", { password: input.value });
        ok = true;
        d.close();
      } catch (err) {
        showError(err.message);
        input.select();
      } finally {
        btn.disabled = false;
      }
    };
    const cancel = () => d.close();
    input.value = "";
    showError("");
    pending = new Promise((resolve) => {
      form.addEventListener("submit", submit);
      $("#unlock-cancel").addEventListener("click", cancel);
      d.addEventListener("close", () => {
        form.removeEventListener("submit", submit);
        $("#unlock-cancel").removeEventListener("click", cancel);
        input.value = "";
        pending = null;
        if (back && back.isConnected) back.focus();
        resolve(ok);
      }, { once: true });
      d.showModal();
      input.focus();
    });
    return pending;
  }

  // lock beendet die Entsperrung dieses Browsers.
  const lock = () => post("lock");

  // button zeigt den Zustand am Schlossknopf: zu (Klick entsperrt) oder offen (Klick sperrt).
  function button(btn, locked) {
    const label = locked ? t("ui.lock.unlock_hint") : t("ui.lock.lock_hint");
    btn.title = label;
    btn.setAttribute("aria-label", label);
    btn.querySelector("use").setAttribute("href", locked ? "#i-lock" : "#i-unlock");
    btn.classList.toggle("is-locked", locked);
  }

  window.wolwebLock = { unlock, lock, button, post };
})();
