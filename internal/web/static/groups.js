// Gruppen anlegen und bearbeiten: ein Dialog für die Übersicht (Stift am Gruppenkopf) und die
// Einstellungsseite. Gespeichert wird sofort über die API der aufrufenden Seite (sie fragt bei
// gesperrten Änderungen nach dem Passwort).
(function () {
  "use strict";
  const { t, locale } = window.wolwebI18n;
  const $ = (s) => document.querySelector(s);
  const dlg = $("#group-dialog");
  const form = $("#group-form");
  let cur = null; // { group, opts, back }

  function el(tag, attrs, ...children) {
    const n = document.createElement(tag);
    for (const [k, v] of Object.entries(attrs || {})) {
      if (v === undefined || v === null || v === false) continue;
      if (k === "class") n.className = v;
      else if (k === "text") n.textContent = v;
      else n.setAttribute(k, v === true ? "" : v);
    }
    for (const c of children.flat()) if (c !== null && c !== undefined) n.append(c instanceof Node ? c : document.createTextNode(String(c)));
    return n;
  }

  function showError(message, onName) {
    const box = $("#group-error");
    const name = form.elements.name;
    box.textContent = message || "";
    box.hidden = !message;
    if (message && onName) {
      name.setAttribute("aria-invalid", "true");
      name.setAttribute("aria-describedby", "group-error group-name-hint");
      name.focus();
    } else {
      name.removeAttribute("aria-invalid");
      name.setAttribute("aria-describedby", "group-name-hint");
      if (message) box.focus();
    }
  }

  function renderHint() {
    const g = cur && cur.group;
    const name = form.elements.name.value.trim();
    $("#group-name-hint").textContent = g && name && name.toLowerCase() !== g.name.toLowerCase()
      ? t("ui.grp.rename_hint", g.name) : t("ui.grp.name_hint");
  }
  form.elements.name.addEventListener("input", renderHint);

  // open zeigt den Dialog. o: { group: {name, members} oder null (neu), devices: [{id, name, group}],
  // api(method, url, body), toast(kind, title, text), onSaved(name) – name ist null nach dem Löschen }
  function open(o) {
    const g = o.group || null;
    cur = { group: g, opts: o, back: document.activeElement };
    $("#group-dialog-title").textContent = g ? t("ui.grp.edit_x", g.name) : t("ui.grp.new");
    form.elements.name.value = g ? g.name : "";
    const inGroup = (d) => g && d.group && d.group.toLowerCase() === g.name.toLowerCase();
    const devices = [...o.devices].sort((a, b) => a.name.localeCompare(b.name, locale));
    $("#group-members").replaceChildren(...devices.map((d) => el("label", { class: "check" },
      el("input", { type: "checkbox", name: "member", value: d.id, checked: inGroup(d) }),
      el("span", {}, d.name, d.group && !inGroup(d) ? el("small", { class: "muted", text: t("ui.grp.in_x", d.group) }) : null))));
    $("#group-no-devices").hidden = devices.length > 0;
    $("#group-delete").hidden = !g;
    $("#group-rotate-key").hidden = !(g && g.key); // Schlüssel gibt es nur mit auth.wake_keys
    showError("");
    renderHint();
    dlg.showModal();
    form.elements.name.focus();
  }

  function close() {
    dlg.close();
  }
  dlg.addEventListener("close", () => {
    if (cur && cur.back && cur.back.isConnected && !cur.saved) cur.back.focus();
    cur = null;
  });
  $("#group-close").addEventListener("click", close);
  $("#group-cancel").addEventListener("click", close);

  // Während einer Anfrage kann der Dialog geschlossen (und für eine andere Gruppe neu geöffnet)
  // werden: Was danach kommt, bezieht sich auf den Stand beim Absenden (mine) und schließt oder
  // beschriftet den Dialog nur, wenn er noch derselbe ist.
  const stillOpen = (mine) => cur === mine;
  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    if (!cur) return;
    const mine = cur;
    const { group: g, opts: o } = mine;
    const name = form.elements.name.value.trim();
    if (!name) return showError(t("ui.grp.name_missing"), true);
    const members = [...form.querySelectorAll('input[name="member"]:checked')].map((i) => i.value);
    const btn = $("#group-save");
    btn.disabled = true;
    const send = (extra) => g
      ? o.api("PUT", "groups/" + encodeURIComponent(g.name), Object.assign({ name, members }, extra))
      : o.api("POST", "groups", Object.assign({ name, members }, extra));
    try {
      let res;
      try {
        res = await send({});
      } catch (err) {
        // Der Name gehörte früher einer anderen Gruppe: Deren alte Wecklinks wecken danach diese –
        // nur nach ausdrücklicher Bestätigung.
        if (!(err.data && err.data.confirm === "take_over_name")) throw err;
        if (!stillOpen(mine)) return; // inzwischen abgebrochen: nicht nachfragen
        if (!(await confirm(t("ui.take_name.title", name), err.message, t("ui.take_name.ok")))) {
          showError(err.message, true);
          return;
        }
        res = await send({ take_over_name: true });
      }
      mine.saved = true;
      if (stillOpen(mine)) close();
      o.toast("ok", g ? t("ui.grp.saved", res.name) : t("ui.grp.created", res.name), "");
      o.onSaved(res.name);
    } catch (err) {
      if (stillOpen(mine)) showError(err.message, err.data && err.data.field === "name");
      else o.toast("err", t("ui.grp.failed"), err.message);
    } finally {
      btn.disabled = false;
    }
  });

  // confirm fragt über den Bestätigungsdialog der Seite nach.
  function confirm(title, text, ok) {
    const d = $("#confirm-dialog");
    $("#confirm-title").textContent = title;
    $("#confirm-text").textContent = text;
    $("#confirm-ok").textContent = ok;
    d.returnValue = ""; // sonst gälte Escape als „ok“, wenn die letzte Rückfrage bestätigt wurde
    d.showModal();
    return new Promise((resolve) => d.addEventListener("close", () => resolve(d.returnValue === "ok"), { once: true }));
  }
  // Löschen fragt nach: die Geräte bleiben, nur ohne Gruppe.
  $("#group-delete").addEventListener("click", async () => {
    if (!cur || !cur.group) return;
    const mine = cur;
    const { group: g, opts: o } = mine;
    if (!(await confirm(t("ui.grp.delete_q", g.name), t("ui.grp.delete_text"), t("ui.delete")))) {
      if (stillOpen(mine)) $("#group-delete").focus();
      return;
    }
    try {
      await o.api("DELETE", "groups/" + encodeURIComponent(g.name));
      mine.saved = true;
      if (stillOpen(mine)) close();
      o.toast("ok", t("ui.grp.deleted", g.name), "");
      o.onSaved(null);
    } catch (err) {
      if (stillOpen(mine)) showError(err.message);
      else o.toast("err", t("ui.grp.delete_failed"), err.message);
    }
  });

  // Neuer Schlüssel: Bisherige Gruppen-Wecklinks gelten danach ohne Anmeldung nicht mehr.
  $("#group-rotate-key").addEventListener("click", async () => {
    if (!cur || !cur.group) return;
    const mine = cur;
    const { group: g, opts: o } = mine;
    if (!(await confirm(t("ui.rotate.title", g.name), t("ui.rotate.text"), t("ui.rotate.ok")))) {
      if (stillOpen(mine)) $("#group-rotate-key").focus();
      return;
    }
    try {
      const ng = await o.api("POST", "groups/" + encodeURIComponent(g.name) + "/wake-key");
      if (stillOpen(mine)) mine.group = ng;
      o.toast("ok", t("ui.rotate.done"), "");
      o.onSaved(ng.name);
    } catch (err) {
      if (stillOpen(mine)) showError(err.message);
      else o.toast("err", t("ui.not_possible"), err.message);
    }
  });

  window.wolwebGroups = { open };
})();
