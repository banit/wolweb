// Einstellungsseite: lädt /api/v1/settings, zeigt Netze und Schnittstellen, speichert Änderungen.
(function () {
  "use strict";
  const { t, tn } = window.wolwebI18n;
  const $ = (s, root) => (root || document).querySelector(s);
  const form = $("#settings-form");
  let view = null; // letzte Antwort des Servers
  let nets = []; // bearbeitete Netze: { cidr, name, vlan, scan, orig } (orig = Stand vom Server)
  let dirty = false;

  function el(tag, attrs, ...children) {
    const n = document.createElement(tag);
    for (const [k, v] of Object.entries(attrs || {})) {
      if (v === undefined || v === null || v === false) continue;
      if (k === "class") n.className = v;
      else if (k === "text") n.textContent = v;
      else if (k.startsWith("on")) n.addEventListener(k.slice(2), v);
      else n.setAttribute(k, v === true ? "" : v);
    }
    for (const c of children.flat()) {
      if (c === undefined || c === null || c === false) continue;
      n.append(c instanceof Node ? c : document.createTextNode(String(c)));
    }
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
  function toast(kind, title, text) {
    const t = el("div", { class: "toast " + kind },
      icon(kind === "ok" ? "check" : kind === "err" ? "x" : "alert"),
      el("div", {}, el("strong", { text: title }), text ? el("p", { text }) : null));
    $("#toasts").append(t);
    setTimeout(() => { t.classList.add("out"); setTimeout(() => t.remove(), 250); }, kind === "err" ? 7000 : 4200);
  }
  async function api(method, url, body, again) {
    const opts = { method, cache: "no-store", headers: { "Accept": "application/json", "X-WolWeb": "1" } };
    if (body !== undefined) {
      opts.headers["Content-Type"] = "application/json";
      opts.body = JSON.stringify(body);
    }
    const res = await fetch("api/v1/" + url, opts);
    let data = null;
    try { data = await res.json(); } catch (e) { /* keine JSON-Antwort */ }
    // Entsperrung abgelaufen (Passwort für Änderungen): nachfragen und die Anfrage wiederholen.
    if (res.status === 403 && data && data.locked && !again && view && await window.wolwebLock.unlock()) {
      return api(method, url, body, true);
    }
    if (!res.ok) throw Object.assign(new Error((data && data.message) || t("ui.unexpected_response", res.status)), { data: data || {}, status: res.status });
    return data;
  }
  function confirmDialog(title, text, ok) {
    const d = $("#confirm-dialog");
    $("#confirm-title").textContent = title;
    $("#confirm-text").textContent = text;
    $("#confirm-ok").textContent = ok;
    d.returnValue = ""; // sonst gälte Escape als „ok“, wenn die letzte Rückfrage bestätigt wurde
    d.showModal();
    return new Promise((resolve) => d.addEventListener("close", () => resolve(d.returnValue === "ok"), { once: true }));
  }

  // ---------- Anzeigen ----------
  const locked = (key) => view && view.locked[key];
  const editable = () => view && !view.read_only;

  function setField(key, value) {
    const f = form.elements[key];
    if (!f) return;
    if (f.type === "checkbox") f.checked = !!value;
    else f.value = value;
  }

  function render(v) {
    view = v;
    const s = v.settings;
    setField("discovery.interval", s.discovery.interval);
    setField("status.interval", s.status.interval);
    setField("status.ports", (s.status.ports || []).join(", "));
    setField("status.wake_timeout", s.status.wake_timeout);
    setField("status.wake_retry", s.status.wake_retry);
    setField("bcastip", s.bcastip);
    setField("notify.webhook", s.notify.webhook);
    setField("notify.ntfy", s.notify.ntfy);
    setField("auth.public_wake", s.auth.public_wake);
    setField("auth.wake_keys", s.auth.wake_keys);
    setField("allowed_hosts", (s.allowed_hosts || []).join("\n"));
    setField("language", s.language);
    setField("privacy.online_status", s.privacy.online_status);
    renderAccents(s.accent);
    renderEvents(s.notify.events || []);
    renderRejected(v.rejected_hosts || []);
    nets = (v.networks || []).map((n) => ({ cidr: n.cidr, name: n.name || "", vlan: n.vlan || "", scan: n.scan, orig: n }));
    renderNets();
    renderInterfaces();

    const src = v.file_found ? t("set.source.file", v.file) : t("set.source.defaults");
    $("#set-source").textContent = src + (v.stored.length ? " · " + tn("set.stored", v.stored.length) : "");
    $("#set-no-auth").hidden = v.auth || v.password;
    $("#set-read-only").hidden = !v.read_only;
    $("#set-reset").hidden = !v.stored.length || v.read_only;
    $("#set-save").hidden = v.read_only;

    // Gesperrt durch Umgebungsvariablen, oder alles im Nur-Lesen-Modus
    for (const box of form.querySelectorAll("[data-key]")) {
      const key = box.dataset.key;
      const env = locked(key);
      for (const i of box.querySelectorAll("input, textarea, select, button")) i.disabled = !!env || v.read_only;
      let note = box.querySelector(".lock-note");
      if (!note && env) {
        note = el("small", { class: "lock-note" });
        // Bei Häkchen unter den Text, sonst unter das Feld
        (box.matches(".check") ? box.querySelector("span") : box).append(note);
      }
      if (note) {
        note.hidden = !env;
        note.textContent = env ? t("set.locked", env) : "";
      }
    }
    $("#net-add").disabled = !!locked("discovery.networks") || v.read_only;
    $("#notify-test").disabled = v.read_only;
    renderPassword();
    renderPrivacyNote();
    loadGroups();
    showLockscreen(false);
    setDirty(false);
  }

  // Zuletzt abgewiesene Hostnamen (meist der eigene Reverse-Proxy): ein Klick übernimmt den Namen nur in
  // die Liste. Gespeichert wird erst mit „Speichern“ – den Vorschlag kann auch eine fremde Seite per
  // DNS-Rebinding erzeugt haben, die Freigabe soll eine bewusste Entscheidung bleiben.
  function renderRejected(hosts) {
    const off = !!locked("allowed_hosts") || !editable();
    $("#rejected-hosts").hidden = !hosts.length || off;
    $("#rejected-list").replaceChildren(...hosts.map((h) => el("li", {},
      el("span", { class: "mono", text: h }),
      el("button", { type: "button", class: "btn btn-secondary btn-sm", "aria-label": t("set.sec.allow_x", h), onclick: () => allowHost(h) },
        icon("check"), el("span", { text: t("set.sec.allow") })))));
  }
  function allowHost(h) {
    const f = form.elements.allowed_hosts;
    const list = splitList(f.value);
    if (!list.includes(h)) list.push(h);
    f.value = list.join("\n");
    setDirty(true);
    const note = $("#rejected-note");
    note.textContent = t("set.sec.added", h);
    note.hidden = false;
    f.focus();
  }

  // Akzentfarben als Farbfelder; die Wahl gilt sofort als Vorschau, gespeichert mit „Speichern“.
  function renderAccents(current) {
    $("#accent-swatches").replaceChildren(...view.accents.map((a) => el("label", { class: "swatch", "data-accent": a.name, title: t("set.accent." + a.name) },
      el("input", { type: "radio", name: "accent", value: a.name, checked: a.name === current, "aria-label": t("set.accent." + a.name) }),
      el("span", { "aria-hidden": "true" }))));
    document.documentElement.dataset.accent = current;
  }
  $("#accent-swatches").addEventListener("change", (e) => { document.documentElement.dataset.accent = e.target.value; });

  function renderEvents(selected) {
    const box = $("#event-checks");
    const all = !selected.length; // leer = alle
    box.replaceChildren(...view.events.map((k) => el("label", { class: "check" },
      el("input", { type: "checkbox", name: "event", value: k, checked: all || selected.includes(k) }),
      el("span", { text: t("set.event." + k) }))));
  }

  function netStatus(n) {
    const o = n.orig;
    if (!o || o.cidr !== n.cidr.trim()) return el("p", { class: "net-status" }, icon("clock"), el("span", { text: t("set.net.pending") }));
    const meta = [tn("set.net.devices", o.devices), tn("set.net.found", o.found)].join(" · ");
    if (o.local) {
      return el("p", { class: "net-status ok" }, icon("check"),
        el("span", { text: t("set.net.local", o.local.interface, o.local.addr, o.broadcast) + " · " + meta }));
    }
    return el("p", { class: "net-status warn" }, icon("alert"),
      el("span", { text: t("set.net.remote", o.broadcast) + " · " + meta }));
  }

  function netLabel(n) {
    const name = n.name.trim() || n.cidr.trim() || t("set.net.new");
    return n.vlan ? t("set.net.label_vlan", name, n.vlan) : name;
  }

  function renderNets() {
    const list = $("#net-list");
    const off = !!locked("discovery.networks") || !editable();
    list.replaceChildren(...nets.map((n, i) => {
      const legend = el("legend", { text: netLabel(n) });
      const upd = (key) => (e) => {
        n[key] = e.target.type === "checkbox" ? e.target.checked : e.target.value;
        legend.textContent = netLabel(n);
        if (key === "cidr") row.querySelector(".net-status").replaceWith(netStatus(n));
      };
      const row = el("li", { class: "net-row" },
        el("fieldset", {},
          legend,
          el("div", { class: "net-fields" },
            el("label", { class: "field" }, el("span", { text: t("set.net.name") }),
              el("input", { name: "net-name", value: n.name, maxlength: "40", autocomplete: "off", placeholder: t("set.net.name_ph"), disabled: off, oninput: upd("name") })),
            el("label", { class: "field" }, el("span", { text: t("set.net.cidr") }),
              el("input", { name: "net-cidr", value: n.cidr, class: "mono", autocomplete: "off", placeholder: "10.0.0.0/24", required: true, disabled: off, oninput: upd("cidr") })),
            el("label", { class: "field net-vlan" }, el("span", { text: t("set.net.vlan") }),
              el("input", { name: "net-vlan", value: n.vlan, inputmode: "numeric", autocomplete: "off", placeholder: "–", disabled: off, oninput: upd("vlan") }))),
          netStatus(n),
          el("div", { class: "net-actions" },
            el("label", { class: "check" },
              el("input", { type: "checkbox", name: "net-scan", checked: n.scan, disabled: off, onchange: upd("scan") }),
              el("span", { text: t("set.net.scan") })),
            el("span", { class: "spacer" }),
            off ? null : el("button", { type: "button", class: "btn btn-ghost btn-sm", "aria-label": t("set.net.remove_x", netLabel(n)), onclick: () => removeNet(i) },
              icon("trash"), el("span", { text: t("set.net.remove") })))));
      return row;
    }));
    $("#net-empty").hidden = nets.length !== 0;
    // Ohne eingetragenes Netz nutzt wolweb das erkannte Heimnetz – sagen, welches.
    $("#net-empty").textContent = view.auto_network ? t("set.net.auto", view.auto_network) : t("set.net.empty");
  }

  function renderInterfaces() {
    const list = $("#if-list");
    const off = !!locked("discovery.networks") || !editable();
    const items = (view.interfaces || []).map((f) => el("li", { class: "if-row" },
      icon("radar"),
      el("div", { class: "if-main" },
        el("strong", { class: "mono", text: f.interface }),
        el("span", { class: "muted mono", text: f.addr + " · " + f.prefix + (f.vlan ? " · VLAN " + f.vlan : "") })),
      f.configured || inList(f.prefix)
        ? el("span", { class: "pill online", text: t("set.if.listed") })
        : off ? null : el("button", { type: "button", class: "btn btn-secondary btn-sm", "aria-label": t("set.if.adopt_x", f.prefix), onclick: () => adopt(f) },
          icon("plus"), el("span", { text: t("set.if.adopt") }))));
    list.replaceChildren(...items);
    if (!items.length) list.append(el("li", { class: "muted small", text: t("set.if.none") }));
  }
  const inList = (prefix) => nets.some((n) => n.cidr.trim() === prefix);

  function addNet(n) {
    nets.push(n);
    renderNets();
    renderInterfaces();
    setDirty(true);
    const rows = $("#net-list").querySelectorAll(".net-row");
    rows[rows.length - 1].querySelector(n.cidr ? 'input[name="net-name"]' : 'input[name="net-cidr"]').focus();
  }
  function removeNet(i) {
    nets.splice(i, 1);
    renderNets();
    renderInterfaces();
    setDirty(true);
    $("#net-add").focus();
  }
  function adopt(f) {
    addNet({ cidr: f.prefix, name: "", vlan: f.vlan || "", scan: true, orig: null });
  }
  $("#net-add").addEventListener("click", () => addNet({ cidr: "", name: "", vlan: "", scan: true, orig: null }));

  // ---------- Speichern ----------
  function setDirty(on) {
    dirty = on;
    $("#set-dirty").hidden = !on;
  }
  form.addEventListener("input", () => setDirty(true));
  form.addEventListener("change", () => setDirty(true));
  window.addEventListener("beforeunload", (e) => { if (dirty) { e.preventDefault(); e.returnValue = ""; } });

  function clearErrors() {
    $("#set-error").hidden = true;
    for (const i of form.querySelectorAll("[aria-invalid]")) {
      i.removeAttribute("aria-invalid");
      const rest = (i.getAttribute("aria-describedby") || "").split(" ").filter((x) => x && x !== "set-error");
      if (rest.length) i.setAttribute("aria-describedby", rest.join(" "));
      else i.removeAttribute("aria-describedby");
    }
  }
  // showError markiert das Feld (Schlüssel wie "status.interval") und springt hin.
  function showError(key, message) {
    const box = $("#set-error");
    box.textContent = message;
    box.hidden = false;
    const target = form.querySelector('[data-key="' + key + '"]');
    const inputs = target ? [...target.querySelectorAll("input, textarea, select")].filter((i) => !i.disabled) : [];
    for (const i of inputs) { // die Meldung gehört zum Feld: der Screenreader liest sie dort vor
      i.setAttribute("aria-invalid", "true");
      const ids = (i.getAttribute("aria-describedby") || "").split(" ").filter(Boolean);
      if (!ids.includes("set-error")) i.setAttribute("aria-describedby", ["set-error", ...ids].join(" "));
    }
    (inputs[0] || box).focus();
    if (!inputs.length) box.scrollIntoView({ block: "center" });
  }
  const splitList = (s) => s.split(/[\s,;]+/).map((x) => x.trim()).filter(Boolean);

  function collect() {
    const v = (k) => form.elements[k].value.trim();
    const ports = [];
    for (const p of splitList(v("status.ports"))) {
      if (!/^\d+$/.test(p)) throw { key: "status.ports", message: t("set.err.ports", p) };
      ports.push(Number(p));
    }
    const networks = nets.map((n) => {
      const vlan = String(n.vlan).trim();
      if (vlan && !/^\d+$/.test(vlan)) throw { key: "discovery.networks", message: t("set.err.vlan", vlan) };
      if (!n.cidr.trim()) throw { key: "discovery.networks", message: t("set.err.cidr_missing") };
      return { cidr: n.cidr.trim(), name: n.name.trim(), vlan: vlan ? Number(vlan) : 0, scan: !!n.scan };
    });
    const checked = [...form.querySelectorAll('input[name="event"]:checked')].map((c) => c.value);
    if (!checked.length) throw { key: "notify.events", message: t("set.err.events") };
    return {
      language: form.elements.language.value,
      accent: (form.querySelector('input[name="accent"]:checked') || {}).value || view.settings.accent,
      privacy: { online_status: form.elements["privacy.online_status"].value || view.settings.privacy.online_status },
      bcastip: v("bcastip"),
      allowed_hosts: splitList(form.elements.allowed_hosts.value),
      discovery: { networks, interval: v("discovery.interval") },
      status: { interval: v("status.interval"), ports, wake_timeout: v("status.wake_timeout"), wake_retry: v("status.wake_retry") },
      auth: { public_wake: form.elements["auth.public_wake"].checked, wake_keys: form.elements["auth.wake_keys"].checked },
      notify: { webhook: v("notify.webhook"), ntfy: v("notify.ntfy"), events: checked.length === view.events.length ? [] : checked },
    };
  }

  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    if (!editable()) return;
    clearErrors();
    let body;
    try { body = collect(); } catch (err) { showError(err.key, err.message); return; }
    const btn = $("#set-save");
    btn.disabled = true;
    try {
      render(await api("PUT", "settings", body));
      toast("ok", t("set.saved"), t("set.saved_text"));
    } catch (err) {
      if (err.data && err.data.field) showError(err.data.field, err.message);
      else toast("err", t("set.save_failed"), err.message);
    } finally {
      btn.disabled = false;
    }
  });

  $("#set-reset").addEventListener("click", async () => {
    if (!(await confirmDialog(t("set.reset_q"), t("set.reset_text", view.settings_file), t("set.reset")))) return;
    try {
      render(await api("DELETE", "settings"));
      toast("ok", t("set.reset_done"), "");
    } catch (err) {
      toast("err", t("set.save_failed"), err.message);
    }
  });

  $("#notify-test").addEventListener("click", async () => {
    clearErrors();
    const btn = $("#notify-test");
    btn.disabled = true;
    try {
      const res = await api("POST", "settings/test-notify", { webhook: form.elements["notify.webhook"].value.trim(), ntfy: form.elements["notify.ntfy"].value.trim() });
      toast("ok", res.message, "");
    } catch (err) {
      if (err.data && err.data.field) showError(err.data.field, err.message);
      else toast("err", t("set.notify.test_failed"), err.message);
    } finally {
      btn.disabled = false;
    }
  });

  // „Nur nach dem Entsperren“ ohne Passwort heißt: niemand sieht den Status – darauf hinweisen.
  function renderPrivacyNote() {
    const show = !!view && !view.password && form.elements["privacy.online_status"].value === "unlocked";
    $("#privacy-no-pw").hidden = !show;
    // nur ansagen, was zu sehen ist (ein verborgenes, aber verknüpftes Element läse der Screenreader mit)
    form.querySelector('input[name="privacy.online_status"][value="unlocked"]')
      .setAttribute("aria-describedby", show ? "hint-privacy-unlocked privacy-no-pw" : "hint-privacy-unlocked");
  }
  form.addEventListener("change", (e) => { if (e.target.name === "privacy.online_status") renderPrivacyNote(); });

  // ---------- Passwort für Änderungen ----------
  const pwForm = $("#pw-form");
  function renderPassword() {
    const on = view.password;
    $("#pw-section").hidden = false;
    $("#pw-state").textContent = on ? t("set.pw.on") : t("set.pw.off");
    $("#pw-state").className = "pw-state " + (on ? "ok" : "warn");
    pwForm.hidden = view.read_only;
    $("#pw-current-field").hidden = !on;
    $("#pw-save-text").textContent = on ? t("set.pw.change") : t("set.pw.set");
    $("#pw-remove").hidden = !on;
    pwError();
    const btn = $("#lock-toggle");
    btn.hidden = !on; // auch im Nur-Lesen-Betrieb: die Seite selbst braucht die Entsperrung
    window.wolwebLock.button(btn, false);
  }
  // pwError zeigt eine Meldung am Feld (name) oder löscht alle.
  function pwError(name, message) {
    for (const i of pwForm.querySelectorAll("[aria-invalid]")) i.removeAttribute("aria-invalid");
    const box = $("#pw-error");
    box.textContent = message || "";
    box.hidden = !message;
    for (const i of pwForm.querySelectorAll("input")) {
      const ids = (i.getAttribute("aria-describedby") || "").split(" ").filter((x) => x && x !== "pw-error");
      if (message && i.name === name) ids.unshift("pw-error");
      if (ids.length) i.setAttribute("aria-describedby", ids.join(" "));
      else i.removeAttribute("aria-describedby");
    }
    if (!message) return;
    const f = pwForm.elements[name];
    if (f) {
      f.setAttribute("aria-invalid", "true");
      f.focus();
    } else {
      box.focus();
    }
  }
  async function savePassword(password) {
    const current = view.password ? pwForm.elements.current.value : "";
    const btn = password ? $("#pw-save") : $("#pw-remove");
    btn.disabled = true;
    try {
      await api("PUT", "password", { current, password });
      view.password = !!password;
      renderPrivacyNote();
      for (const i of pwForm.querySelectorAll("input")) i.value = "";
      $("#set-no-auth").hidden = view.auth || view.password;
      renderPassword();
      if (password) toast("ok", t("set.pw.saved"), t("set.pw.saved_text"));
      else toast("ok", t("set.pw.removed"), t("set.pw.removed_text"));
      $("#pw-state").focus();
    } catch (err) {
      const field = err.data && err.data.field;
      pwError(field === "current" || field === "password" ? field : "", err.message);
    } finally {
      btn.disabled = false;
    }
  }
  pwForm.addEventListener("submit", (e) => {
    e.preventDefault();
    const f = pwForm.elements;
    if (view.password && !f.current.value) return pwError("current", t("set.pw.current_missing"));
    if (!f.password.value) return pwError("password", t("set.pw.missing"));
    if (f.password.value !== f.repeat.value) return pwError("repeat", t("set.pw.mismatch"));
    savePassword(f.password.value);
  });
  $("#pw-remove").addEventListener("click", async () => {
    if (!pwForm.elements.current.value) return pwError("current", t("set.pw.current_missing"));
    if (!(await confirmDialog(t("set.pw.remove_q"), t("set.pw.remove_text"), t("set.pw.remove")))) return;
    savePassword("");
  });

  // ---------- Gruppen ----------
  let groups = [], devices = [];
  async function loadGroups(focus) {
    try {
      [groups, devices] = await Promise.all([api("GET", "groups"), api("GET", "devices")]);
    } catch (err) {
      toast("err", t("set.load_failed"), err.message);
      return;
    }
    renderGroups(focus);
  }
  // renderGroups zeichnet die Liste; focus nennt danach den Knopf, der den Fokus bekommt ("name|aktion").
  function renderGroups(focus) {
    const off = !editable();
    $("#grp-section").hidden = false;
    $("#grp-list").replaceChildren(...groups.map((g, i) => el("li", { class: "grp-row", "data-name": g.name },
      el("div", { class: "grp-main" },
        el("strong", { text: g.name }),
        el("span", { class: "muted small", text: tn("ui.n_devices", g.members.length) })),
      off ? null : el("div", { class: "grp-actions" },
        el("button", { type: "button", class: "btn btn-ghost icon-only", "data-act": "up", disabled: i === 0, title: t("set.grp.up_x", g.name), "aria-label": t("set.grp.up_x", g.name), onclick: () => moveGroup(i, -1) }, icon("arrow-up")),
        el("button", { type: "button", class: "btn btn-ghost icon-only", "data-act": "down", disabled: i === groups.length - 1, title: t("set.grp.down_x", g.name), "aria-label": t("set.grp.down_x", g.name), onclick: () => moveGroup(i, 1) }, icon("arrow-down")),
        el("button", { type: "button", class: "btn btn-ghost icon-only", "data-act": "edit", title: t("set.grp.edit_x", g.name), "aria-label": t("set.grp.edit_x", g.name), onclick: () => editGroup(g) }, icon("edit"))))));
    $("#grp-empty").hidden = groups.length > 0;
    $("#grp-add").hidden = off;
    if (focus) {
      const [name, act] = focus.split("|");
      const row = [...$("#grp-list").children].find((r) => r.dataset.name === name);
      let btn = row && row.querySelector('[data-act="' + act + '"]');
      if (btn && btn.disabled) btn = row.querySelector('[data-act="' + (act === "up" ? "down" : "up") + '"]');
      (btn || $("#grp-add")).focus();
    }
  }
  async function moveGroup(i, dir) {
    const order = groups.map((g) => g.name);
    const [name] = order.splice(i, 1);
    order.splice(i + dir, 0, name);
    try {
      await api("PUT", "order", { groups: order });
    } catch (err) {
      toast("err", t("set.save_failed"), err.message);
      return;
    }
    await loadGroups(name + "|" + (dir < 0 ? "up" : "down"));
    $("#grp-status").textContent = t("set.grp.moved", name, i + dir + 1, order.length);
  }
  function editGroup(g) {
    window.wolwebGroups.open({
      group: g, devices, api, toast,
      onSaved: (name) => loadGroups(name ? name + "|edit" : "|add"),
    });
  }
  $("#grp-add").addEventListener("click", () => window.wolwebGroups.open({
    group: null, devices, api, toast,
    onSaved: (name) => loadGroups(name + "|edit"),
  }));

  // ---------- Sperren ----------
  // Gesperrt zeigt die Seite nur den Hinweis mit „Entsperren“ – die Einstellungen gibt der Server
  // erst danach heraus.
  function showLockscreen(on) {
    $("#set-lockscreen").hidden = !on;
    for (const id of ["#set-intro", "#settings-form", "#pw-section", "#grp-section"]) $(id).hidden = on;
    if (on) {
      for (const id of ["#set-no-auth", "#set-read-only"]) $(id).hidden = true;
      $("#set-source").textContent = t("set.lockscreen.title");
      $("#lock-toggle").hidden = true;
      setDirty(false);
    }
  }
  async function unlockAndLoad() {
    if (await window.wolwebLock.unlock()) {
      await load();
      toast("ok", t("ui.lock.unlocked"), t("ui.lock.unlocked_text"));
      form.focus();
    }
  }
  $("#lockscreen-unlock").addEventListener("click", unlockAndLoad);
  $("#lock-toggle").addEventListener("click", async () => {
    try { await window.wolwebLock.lock(); } catch (e) { toast("err", t("ui.lock.failed"), e.message); return; }
    view = null;
    showLockscreen(true);
    toast("ok", t("ui.lock.locked"), t("ui.lock.locked_text"));
    $("#lockscreen-unlock").focus();
  });

  // Sprunglink: "#settings-form" löst sich wegen <base> gegen die Basisadresse auf – selbst springen.
  $(".skip-link").addEventListener("click", (e) => {
    e.preventDefault();
    form.focus();
  });

  async function load() {
    try {
      render(await api("GET", "settings"));
    } catch (err) {
      if (err.data && err.data.locked) {
        showLockscreen(true);
        return false;
      }
      $("#set-source").textContent = t("set.load_failed");
      toast("err", t("set.load_failed"), err.message);
    }
    return true;
  }
  load().then((ok) => { if (!ok) unlockAndLoad(); }); // gesperrt: gleich nach dem Passwort fragen
})();
