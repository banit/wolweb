// Benutzer und Benutzergruppen auf der Einstellungsseite. Gespeichert wird sofort, wie bei den
// Gerätegruppen. Der erste Benutzer ist man selbst (Administrator); danach braucht wolweb eine Anmeldung.
(function () {
  "use strict";
  const { t, tn, locale } = window.wolwebI18n;
  const $ = (s) => document.querySelector(s);
  let view = null; // Einstellungen (read_only, users)
  let users = [], ugroups = [], dgroups = [], devices = [];
  let mail = null; // Mailserver (ohne Passwort) – null ohne Benutzer

  function el(tag, attrs, ...children) {
    const n = document.createElement(tag);
    for (const [k, v] of Object.entries(attrs || {})) {
      if (v === undefined || v === null || v === false) continue;
      if (k === "class") n.className = v;
      else if (k === "text") n.textContent = v;
      else if (k.startsWith("on")) n.addEventListener(k.slice(2), v);
      else n.setAttribute(k, v === true ? "" : v);
    }
    for (const c of children.flat()) if (c !== null && c !== undefined && c !== false) n.append(c instanceof Node ? c : document.createTextNode(String(c)));
    return n;
  }
  function icon(name) {
    const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
    svg.setAttribute("class", "ico");
    svg.setAttribute("aria-hidden", "true");
    const use = document.createElementNS("http://www.w3.org/2000/svg", "use");
    use.setAttribute("href", "#i-" + name);
    svg.append(use);
    return svg;
  }
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
      location.href = "login?next=settings";
      throw new Error(data.message || "");
    }
    if (!res.ok) throw Object.assign(new Error(data.message || t("ui.unexpected_response", res.status)), { data, status: res.status });
    return data;
  }
  function toast(kind, title, text) {
    const n = el("div", { class: "toast " + kind }, icon(kind === "ok" ? "check" : "x"),
      el("div", {}, el("strong", { text: title }), text ? el("p", { text }) : null));
    $("#toasts").append(n);
    setTimeout(() => { n.classList.add("out"); setTimeout(() => n.remove(), 250); }, kind === "err" ? 7000 : 4200);
  }
  function confirmDialog(title, text, ok) {
    const d = $("#confirm-dialog");
    $("#confirm-title").textContent = title;
    $("#confirm-text").textContent = text;
    $("#confirm-ok").textContent = ok;
    d.returnValue = "";
    d.showModal();
    return new Promise((resolve) => d.addEventListener("close", () => resolve(d.returnValue === "ok"), { once: true }));
  }
  const byName = (a, b) => a.name.localeCompare(b.name, locale);
  const checkbox = (name, value, checked, label, note) => el("label", { class: "check" },
    el("input", { type: "checkbox", name, value, checked }),
    el("span", {}, label, note ? el("small", { class: "muted", text: note }) : null));
  // showError zeigt eine Meldung im Dialog und markiert das Feld (name).
  function showError(form, box, field, message) {
    for (const i of form.querySelectorAll("[aria-invalid]")) i.removeAttribute("aria-invalid");
    box.textContent = message || "";
    box.hidden = !message;
    if (!message) return;
    const f = field && form.elements[field];
    if (f && f.focus) {
      f.setAttribute("aria-invalid", "true");
      f.focus();
    } else {
      box.focus();
    }
  }

  // ---------- Laden und Anzeigen ----------
  async function load(v) {
    if (v) view = v;
    try {
      const [g, d] = await Promise.all([api("GET", "groups"), api("GET", "devices")]);
      dgroups = g;
      devices = d;
      if (view.users) [users, ugroups, mail] = await Promise.all([api("GET", "users"), api("GET", "user-groups"), api("GET", "mail")]);
      else { users = ugroups = []; mail = null; }
    } catch (err) {
      toast("err", t("set.load_failed"), err.message);
      return;
    }
    render();
  }

  function render() {
    const off = view.read_only;
    $("#usr-section").hidden = false;
    $("#usr-intro").textContent = view.users ? t("set.usr.intro") : t("set.usr.intro_first");
    $("#usr-add-text").textContent = view.users ? t("set.usr.add") : t("set.usr.add_first");
    $("#usr-list").replaceChildren(...users.map((u) => el("li", { class: "grp-row", "data-id": u.id },
      el("div", { class: "grp-main" },
        el("strong", { text: u.name }),
        u.admin ? el("span", { class: "badge-role", text: t("acc.role.admin") }) : null,
        el("span", { class: "muted small", text: [u.email, u.groups.join(", ")].filter(Boolean).join(" · ") || (u.admin ? "" : t("set.usr.no_group")) })),
      off ? null : el("div", { class: "grp-actions" },
        el("button", { type: "button", class: "btn btn-ghost icon-only", "data-act": "edit", title: t("ui.usr.edit_x", u.name), "aria-label": t("ui.usr.edit_x", u.name), onclick: () => openUser(u) }, icon("edit"))))));
    $("#usr-add").hidden = off;

    $("#ugrp-section").hidden = !view.users;
    $("#ugrp-list").replaceChildren(...ugroups.map((g) => el("li", { class: "grp-row", "data-id": g.id },
      el("div", { class: "grp-main" },
        el("strong", { text: g.name }),
        el("span", { class: "muted small", text: tn("set.ugrp.n_users", (g.users || []).length) + " · " + tn("ui.n_devices", sharedDevices(g).length) })),
      off ? null : el("div", { class: "grp-actions" },
        el("button", { type: "button", class: "btn btn-ghost icon-only", "data-act": "edit", title: t("ui.ugrp.edit_x", g.name), "aria-label": t("ui.ugrp.edit_x", g.name), onclick: () => openUGroup(g) }, icon("edit"))))));
    $("#ugrp-empty").hidden = ugroups.length > 0;
    $("#ugrp-add").hidden = off;
    renderMail();
  }

  // ---------- Mailserver ----------
  const mform = $("#mail-form");
  const mailReady = () => !!(mail && mail.host && mail.from && mail.link);
  function renderMail() {
    $("#mail-section").hidden = !view.users;
    if (!view.users || !mail) return;
    const f = mform.elements;
    f.host.value = mail.host || "";
    f.security.value = mail.security || "starttls";
    f.port.value = mail.port || "";
    f.username.value = mail.username || "";
    f.password.value = "";
    f.from.value = mail.from || "";
    // Vorschlag: die Adresse, unter der diese Seite gerade offen ist.
    f.link.value = mail.link || new URL(".", document.baseURI).href;
    $("#mail-pw-hint").textContent = mail.password_set ? t("set.mail.password_keep") : "";
    $("#mail-state").textContent = mailReady() ? t("set.mail.on") : t("set.mail.off");
    $("#mail-state").className = "pw-state " + (mailReady() ? "ok" : "warn");
    $("#mail-test").hidden = !mailReady();
    for (const i of mform.querySelectorAll("input, select, button")) i.disabled = view.read_only;
  }
  // Port passend zur Verschlüsselung vorschlagen, solange er nicht von Hand geändert wurde.
  mform.elements.security.addEventListener("change", () => {
    const p = mform.elements.port;
    if (!p.value || ["25", "465", "587"].includes(p.value)) p.value = { starttls: 587, tls: 465, none: 25 }[mform.elements.security.value];
  });
  mform.addEventListener("submit", async (e) => {
    e.preventDefault();
    const f = mform.elements;
    const body = { host: f.host.value.trim(), port: parseInt(f.port.value, 10) || 0, security: f.security.value,
      username: f.username.value.trim(), password: f.password.value, from: f.from.value.trim(), link: f.link.value.trim() };
    const btn = $("#mail-save");
    btn.disabled = true;
    try {
      mail = await api("PUT", "mail", body);
      showError(mform, $("#mail-error"));
      renderMail();
      toast("ok", mailReady() ? t("set.mail.saved") : t("set.mail.removed"), mailReady() ? t("set.mail.saved_text") : "");
    } catch (err) {
      showError(mform, $("#mail-error"), err.data && err.data.field, err.message);
    } finally {
      btn.disabled = false;
    }
  });
  $("#mail-test").addEventListener("click", async () => {
    const btn = $("#mail-test");
    btn.disabled = true;
    try {
      const res = await api("POST", "mail/test");
      showError(mform, $("#mail-error"));
      toast("ok", t("set.mail.test_ok"), t("set.mail.test_ok_text", res.to));
    } catch (err) {
      showError(mform, $("#mail-error"), "", err.message);
    } finally {
      btn.disabled = false;
    }
  });

  // sharedDevices: alle Geräte, die eine Benutzergruppe freigibt (einzeln oder über Gerätegruppen).
  function sharedDevices(g) {
    const names = new Set(dgroups.filter((x) => (g.device_groups || []).includes(x.id)).map((x) => x.name.toLowerCase()));
    return devices.filter((d) => (g.devices || []).includes(d.id) || (d.group && names.has(d.group.toLowerCase())));
  }

  // ---------- Benutzer-Dialog ----------
  const udlg = $("#user-dialog");
  const uform = $("#user-form");
  let ucur = null; // { user } – null = neu

  function openUser(u) {
    const first = !view.users;
    ucur = { user: u || null };
    $("#user-dialog-title").textContent = u ? t("ui.usr.edit_x", u.name) : first ? t("set.usr.add_first") : t("set.usr.add");
    $("#user-first").hidden = !first;
    uform.elements.name.value = u ? u.name : "";
    uform.elements.email.value = u ? u.email : "";
    uform.elements.password.value = "";
    $("#user-pw-label").textContent = u ? t("ui.usr.password_new") : t("ui.usr.password");
    $("#user-pw-hint").textContent = u ? t("ui.usr.password_keep") : mailReady() ? t("ui.usr.password_invite") : t("ui.usr.password_hint");
    uform.elements.admin.checked = first || !!(u && u.admin);
    uform.elements.admin.disabled = first;
    $("#user-groups-box").hidden = first || ugroups.length === 0;
    $("#user-groups").replaceChildren(...[...ugroups].sort(byName).map((g) =>
      checkbox("ugroup", g.id, !!(u && (g.users || []).includes(u.id)), g.name)));
    $("#user-delete").hidden = !u;
    showError(uform, $("#user-error"));
    udlg.showModal();
    uform.elements.name.focus();
  }
  const closeUser = () => udlg.close();
  $("#user-close").addEventListener("click", closeUser);
  $("#user-cancel").addEventListener("click", closeUser);

  uform.addEventListener("submit", async (e) => {
    e.preventDefault();
    if (!ucur) return;
    const u = ucur.user;
    const f = uform.elements;
    const body = { name: f.name.value.trim(), email: f.email.value.trim(), password: f.password.value, admin: f.admin.checked };
    if (!body.name) return showError(uform, $("#user-error"), "name", t("val.user_required"));
    // Ohne Passwort geht es nur als Einladung per Mail (Mailserver und E-Mail-Adresse nötig).
    if (!u && !body.password && !(mailReady() && body.email)) {
      return showError(uform, $("#user-error"), mailReady() ? "email" : "password", mailReady() ? t("ui.usr.invite_needs_mail") : t("set.pw.missing"));
    }
    const first = !view.users;
    const btn = $("#user-save");
    btn.disabled = true;
    try {
      const saved = u ? await api("PUT", "users/" + encodeURIComponent(u.id), body) : await api("POST", "users", body);
      if (first) { // ab jetzt mit Anmeldung – man selbst ist schon angemeldet
        location.reload();
        return;
      }
      // Benutzergruppen, die sich im Dialog geändert haben, gleich mit anpassen.
      const want = new Set([...uform.querySelectorAll('input[name="ugroup"]:checked')].map((i) => i.value));
      for (const g of ugroups) {
        const has = (g.users || []).includes(saved.id);
        if (has === want.has(g.id)) continue;
        const members = has ? g.users.filter((x) => x !== saved.id) : [...(g.users || []), saved.id];
        await api("PUT", "user-groups/" + encodeURIComponent(g.id), { name: g.name, users: members, devices: g.devices || [], device_groups: g.device_groups || [] });
      }
      udlg.close();
      toast("ok", u ? t("ui.usr.saved", saved.name) : t("ui.usr.created", saved.name), saved.invited ? t("ui.usr.invited", saved.email) : "");
      await load();
    } catch (err) {
      showError(uform, $("#user-error"), err.data && err.data.field, err.message);
    } finally {
      btn.disabled = false;
    }
  });

  $("#user-delete").addEventListener("click", async () => {
    if (!ucur || !ucur.user) return;
    const u = ucur.user;
    if (!(await confirmDialog(t("ui.usr.delete_q", u.name), t("ui.usr.delete_text"), t("ui.delete")))) return;
    try {
      await api("DELETE", "users/" + encodeURIComponent(u.id));
      udlg.close();
      toast("ok", t("ui.usr.deleted", u.name), "");
      await load();
    } catch (err) {
      showError(uform, $("#user-error"), err.data && err.data.field, err.message);
    }
  });

  // ---------- Benutzergruppen-Dialog ----------
  const gdlg = $("#ugroup-dialog");
  const gform = $("#ugroup-form");
  let gcur = null; // { group } – null = neu

  function openUGroup(g) {
    gcur = { group: g || null };
    $("#ugroup-dialog-title").textContent = g ? t("ui.ugrp.edit_x", g.name) : t("set.ugrp.add");
    gform.elements.name.value = g ? g.name : "";
    const has = (list, id) => !!(g && (list || []).includes(id));
    $("#ugroup-users").replaceChildren(...[...users].sort(byName).map((u) =>
      checkbox("user", u.id, has(g && g.users, u.id), u.name, u.admin ? t("acc.role.admin") : null)));
    $("#ugroup-dgroups").replaceChildren(...dgroups.map((x) =>
      checkbox("dgroup", x.id, has(g && g.device_groups, x.id), x.name, tn("ui.n_devices", x.members.length))));
    $("#ugroup-no-dgroups").hidden = dgroups.length > 0;
    $("#ugroup-devices").replaceChildren(...[...devices].sort(byName).map((d) =>
      checkbox("device", d.id, has(g && g.devices, d.id), d.name, d.group ? t("ui.ugrp.in_x", d.group) : null)));
    $("#ugroup-no-devices").hidden = devices.length > 0;
    $("#ugroup-delete").hidden = !g;
    showError(gform, $("#ugroup-error"));
    gdlg.showModal();
    gform.elements.name.focus();
  }
  const closeUGroup = () => gdlg.close();
  $("#ugroup-close").addEventListener("click", closeUGroup);
  $("#ugroup-cancel").addEventListener("click", closeUGroup);

  const checked = (name) => [...gform.querySelectorAll('input[name="' + name + '"]:checked')].map((i) => i.value);
  gform.addEventListener("submit", async (e) => {
    e.preventDefault();
    if (!gcur) return;
    const g = gcur.group;
    const body = { name: gform.elements.name.value.trim(), users: checked("user"), devices: checked("device"), device_groups: checked("dgroup") };
    if (!body.name) return showError(gform, $("#ugroup-error"), "name", t("val.ugroup_required"));
    const btn = $("#ugroup-save");
    btn.disabled = true;
    try {
      const saved = g ? await api("PUT", "user-groups/" + encodeURIComponent(g.id), body) : await api("POST", "user-groups", body);
      gdlg.close();
      toast("ok", g ? t("ui.ugrp.saved", saved.name) : t("ui.ugrp.created", saved.name), "");
      await load();
    } catch (err) {
      showError(gform, $("#ugroup-error"), err.data && err.data.field, err.message);
    } finally {
      btn.disabled = false;
    }
  });

  $("#ugroup-delete").addEventListener("click", async () => {
    if (!gcur || !gcur.group) return;
    const g = gcur.group;
    if (!(await confirmDialog(t("ui.ugrp.delete_q", g.name), t("ui.ugrp.delete_text"), t("ui.delete")))) return;
    try {
      await api("DELETE", "user-groups/" + encodeURIComponent(g.id));
      gdlg.close();
      toast("ok", t("ui.ugrp.deleted", g.name), "");
      await load();
    } catch (err) {
      showError(gform, $("#ugroup-error"), "", err.message);
    }
  });

  $("#usr-add").addEventListener("click", () => openUser(null));
  $("#ugrp-add").addEventListener("click", () => openUGroup(null));

  window.wolwebUsers = { load };
})();
