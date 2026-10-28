// wolweb – Übersicht, Geräteverwaltung und Netzwerksuche.
(function () {
  "use strict";

  const $ = (sel, root = document) => root.querySelector(sel);
  const { t, tn, locale } = window.wolwebI18n; // Texte aus de.json/en.json (siehe i18n.js)
  const ICONS = ["desktop", "laptop", "server", "nas", "tv", "console", "router", "printer", "other"];
  const state = {
    info: { read_only: false, networks: [], wake_timeout: 180 },
    devices: [],
    groups: [], // [{name, path, key, members}]
    found: [],
    job: null,
    filter: "all",
    foundFilter: "new",
    search: "",
    waking: new Map(), // id -> { start }
    editing: null,
    live: false, // Live-Verbindung (Server-Sent Events) steht – dann kaum noch Abfragen
  };

  // ---------- Hilfen ----------
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
  function icon(name, cls) {
    const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
    svg.setAttribute("class", "ico" + (cls ? " " + cls : ""));
    const use = document.createElementNS("http://www.w3.org/2000/svg", "use");
    use.setAttribute("href", "#i-" + name);
    svg.append(use);
    return svg;
  }
  async function api(method, url, body, raw) {
    const opts = { method, cache: "no-store", headers: { "Accept": "application/json", "X-WolWeb": "1" } };
    if (body !== undefined) {
      opts.headers["Content-Type"] = "application/json";
      opts.body = raw ? body : JSON.stringify(body);
    }
    const res = await fetch("api/v1/" + url, opts);
    let data = null;
    try { data = await res.json(); } catch (e) { /* leer */ }
    if (!res.ok || !data) {
      const err = new Error((data && data.message) || t("ui.unexpected_response", res.status));
      err.status = res.status;
      err.data = data;
      throw err;
    }
    return data;
  }
  const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
  function ago(iso) {
    if (!iso) return "";
    const s = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000);
    if (s < 45) return t("ui.ago.now");
    if (s < 3600) return t("ui.ago.min", Math.round(s / 60));
    if (s < 86400) return t("ui.ago.h", Math.round(s / 3600));
    return tn("ui.ago.days", Math.round(s / 86400));
  }
  function fmtDuration(ms) {
    const s = Math.round(ms / 1000);
    return s < 60 ? s + " s" : Math.floor(s / 60) + ":" + String(s % 60).padStart(2, "0") + " min";
  }
  // Server-Werte, die als Schlüssel oder deutsch gespeichert sind, für die Anzeige übersetzen.
  function methodText(m) {
    if (!m) return "";
    if (m === "icmp") return "Ping";
    const port = /^tcp\/(\d+)/.exec(m);
    return port ? t("ui.port", port[1]) : m;
  }
  function vendorText(v) { return v === "Private/zufällige MAC" ? t("ui.vendor_private") : v || ""; }
  // Zeitpläne speichert der Server einheitlich deutsch ("Mo-Fr 07:30"); Englisch versteht er auch.
  const DAYS_EN = { Mo: "Mon", Di: "Tue", Mi: "Wed", Do: "Thu", Fr: "Fri", Sa: "Sat", So: "Sun" };
  function scheduleText(s) {
    if (!s || window.wolwebI18n.lang === "de") return s || "";
    return s.replace(/^täglich/, "daily").replace(/\b(Mo|Di|Mi|Do|Fr|Sa|So)\b/g, (d) => DAYS_EN[d]);
  }
  // Mit auth.wake_keys gehört der Schlüssel in jeden Link, der ohne Anmeldung funktionieren soll.
  function wakeURL(d) {
    const u = new URL(d.wake_path, document.baseURI);
    if (state.info.wake_keys && d.wake_key) u.searchParams.set("key", d.wake_key);
    return u.href;
  }

  // Neu zeichnen, ohne den Tastaturfokus zu verlieren: Karten und Listeneinträge werden komplett
  // neu gebaut; der fokussierte Knopf wird über data-id (Eintrag) und data-focus (Knopf) wiedergefunden.
  function keepFocus(box, draw) {
    const a = document.activeElement;
    const item = a && a !== document.body && box.contains(a) ? a.closest("[data-id]") : null;
    const key = item && a.dataset.focus;
    draw();
    if (!key) return;
    const n = box.querySelector('[data-id="' + CSS.escape(item.dataset.id) + '"] [data-focus="' + key + '"]');
    if (n) n.focus({ preventScroll: true });
  }

  function toast(kind, title, text) {
    const box = $("#toasts");
    const t = el("div", { class: "toast " + kind, role: "status" },
      icon(kind === "ok" ? "check" : kind === "err" ? "x" : "alert"),
      el("div", {}, el("strong", { text: title }), text ? el("p", { text }) : null));
    box.append(t);
    setTimeout(() => { t.classList.add("out"); setTimeout(() => t.remove(), 250); }, kind === "err" ? 7000 : 4200);
  }

  // ---------- Geräte ----------
  async function loadDevices() {
    try {
      state.devices = await api("GET", "devices");
      state.groups = state.devices.some((d) => d.group) ? await api("GET", "groups") : [];
      renderDevices();
    } catch (e) {
      if (e.status === 401) location.reload();
      $("#summary").textContent = t("ui.server_unreachable");
    }
  }

  function deviceState(d) {
    if (state.waking.has(d.id)) return "waking";
    return (d.status && d.status.state) || (d.check_ip ? "offline" : "unknown");
  }

  function matches(d) {
    const q = state.search.trim().toLowerCase();
    if (q && ![d.name, d.ip, d.check_ip, d.mac, d.vendor, d.note].some((v) => v && v.toLowerCase().includes(q))) return false;
    const s = deviceState(d);
    if (state.filter === "online") return s === "online";
    if (state.filter === "offline") return s !== "online";
    return true;
  }

  function renderDevices() {
    const grid = $("#devices");
    const total = state.devices.length;
    const online = state.devices.filter((d) => deviceState(d) === "online").length;
    $("#summary").textContent = total === 0 ? t("ui.no_devices") : t("ui.summary", tn("ui.n_devices", total), online);
    for (const b of $("#filter").children) {
      const f = b.dataset.filter;
      const n = f === "all" ? total : f === "online" ? online : total - online;
      b.replaceChildren(b.dataset.label || (b.dataset.label = b.textContent), el("span", { class: "count", text: n }));
    }
    const list = state.devices.filter(matches);
    $("#empty").hidden = total !== 0;
    $("#empty-filter").hidden = total === 0 || list.length !== 0;
    const existing = new Map([...grid.querySelectorAll(".card")].map((c) => [c.dataset.id, c]));
    const cards = (devs) => devs.map((d) => {
      const card = buildCard(d);
      const old = existing.get(d.id);
      if (old) {
        card.style.animation = "none";
        if (old.classList.contains("card-flash")) card.classList.add("card-flash");
      }
      return card;
    });
    // Mit Gruppen: je Gruppe ein Abschnitt mit Kopf (Wecken, Link), danach die übrigen Geräte.
    let next;
    if (!state.groups.length) {
      next = [el("div", { class: "grid" }, cards(list))];
    } else {
      next = [];
      for (const g of state.groups) {
        const members = list.filter((d) => d.group && d.group.toLowerCase() === g.name.toLowerCase());
        if (members.length) next.push(groupHead(g), el("div", { class: "grid" }, cards(members)));
      }
      const rest = list.filter((d) => !d.group);
      if (rest.length) next.push(el("div", { class: "group-head" }, el("h2", { text: t("ui.other_devices") })), el("div", { class: "grid" }, cards(rest)));
    }
    keepFocus(grid, () => grid.replaceChildren(...next));
  }

  function groupHead(g) {
    const members = state.devices.filter((d) => d.group && d.group.toLowerCase() === g.name.toLowerCase());
    const online = members.filter((d) => deviceState(d) === "online").length;
    return el("div", { class: "group-head", "data-id": "gruppe:" + g.name },
      el("h2", { text: g.name }),
      el("span", { class: "muted", text: t("ui.x_of_y_online", online, members.length) }),
      el("span", { class: "spacer" }),
      el("button", { class: "btn btn-secondary btn-sm", "data-focus": "wake", onclick: () => wakeGroup(g) }, icon("power"), el("span", { text: t("ui.wake_group") })),
      el("button", { class: "btn btn-ghost btn-sm icon-only", title: t("ui.copy_group_link"), "aria-label": t("ui.copy_group_link"), "data-focus": "copy", onclick: () => copyURL(groupURL(g), t("ui.group_link_copied"), t("ui.copy_group_link")) }, icon("link")));
  }

  function groupURL(g) {
    const u = new URL(g.path, document.baseURI);
    if (state.info.wake_keys && g.key) u.searchParams.set("key", g.key);
    return u.href;
  }

  function nextWakeText(iso) {
    const d = new Date(iso);
    const days = Math.round((new Date(d).setHours(0, 0, 0, 0) - new Date().setHours(0, 0, 0, 0)) / 86400000);
    const clock = d.toLocaleTimeString(locale, { hour: "2-digit", minute: "2-digit" });
    if (days === 0) return t("ui.today_at", clock);
    if (days === 1) return t("ui.tomorrow_at", clock);
    return d.toLocaleDateString(locale, { weekday: "short" }) + " " + clock;
  }

  function statusLine(d) {
    const s = deviceState(d);
    const st = d.status || {};
    if (s === "waking") {
      const w = state.waking.get(d.id);
      return [el("span", { class: "pill waking", text: t("ui.starting") }), el("span", { class: "timer", "data-timer": d.id, text: fmtDuration(Date.now() - w.start) })];
    }
    if (s === "online") {
      return [el("span", { class: "pill online", text: t("ui.online") }), el("span", { text: methodText(st.method) + (st.rtt_ms ? " · " + st.rtt_ms + " ms" : "") })];
    }
    if (s === "unknown") {
      return [el("span", { class: "pill unknown", text: t("ui.no_status") }), el("span", { text: t("ui.no_ip_known") })];
    }
    return [el("span", { class: "pill offline", text: t("ui.offline") }),
      st.conflict_mac ? el("span", { class: "pill warn", text: t("ui.ip_taken"), title: t("ui.ip_taken_title", st.ip, st.conflict_mac) }) : null,
      el("span", { text: st.last_seen ? t("ui.last_online", ago(st.last_seen)) : d.last_wake ? t("ui.last_woken", ago(d.last_wake)) : "" })];
  }

  function buildCard(d) {
    const ro = state.info.read_only;
    const s = deviceState(d);
    const ip = d.ip || d.check_ip;
    const card = el("article", { class: "card", "data-id": d.id, "data-state": s },
      el("div", { class: "card-top" },
        el("div", { class: "dev-icon", title: vendorText(d.vendor) }, icon(d.icon || "desktop")),
        el("div", { class: "card-title" },
          el("h3", { text: d.name, title: d.name }),
          el("div", { class: "card-meta" },
            ip ? el("span", { class: "mono", text: ip, title: d.ip ? t("ui.ip_address") : t("ui.ip_from_scan") }) : null,
            el("span", { class: "mono", text: d.mac, title: vendorText(d.vendor) || t("ui.mac_address") }),
            d.next_wake ? el("span", { class: "sched", title: t("ui.schedule_title", (d.schedule || []).map(scheduleText).join("; ")) }, icon("clock"), nextWakeText(d.next_wake)) : null)),
        ro ? null : el("button", { class: "btn btn-ghost btn-sm icon-only", title: t("ui.edit"), "aria-label": t("ui.edit"), "data-focus": "edit", onclick: () => openDialog(d) }, icon("edit"))),
      d.note ? el("p", { class: "card-note", text: d.note }) : null,
      el("div", { class: "status-line" }, statusLine(d)),
      el("div", { class: "card-actions" },
        el("button", { class: "btn btn-primary btn-wake", disabled: s === "waking", "data-focus": "wake", onclick: () => wake(d) },
          icon("power"), el("span", { text: s === "waking" ? t("ui.being_started") : s === "online" ? t("ui.wake_again") : t("ui.wake") })),
        el("button", { class: "btn btn-secondary icon-only", title: t("ui.copy_link"), "aria-label": t("ui.copy_link"), "data-focus": "copy", onclick: () => copyLink(d) }, icon("link")),
        el("a", { class: "btn btn-secondary icon-only", href: d.wake_path, target: "_blank", rel: "noopener", title: t("ui.open_wake_page"), "aria-label": t("ui.open_wake_page"), "data-focus": "open" }, icon("external"))));
    if (s === "waking") {
      const w = state.waking.get(d.id);
      const pct = Math.min(100, ((Date.now() - w.start) / (state.info.wake_timeout * 1000)) * 100);
      const bar = el("div", { class: "progress-bar" });
      bar.style.width = pct + "%";
      card.append(el("div", { class: "progress" }, bar));
    }
    return card;
  }

  function copyLink(d) { return copyURL(wakeURL(d), t("ui.link_copied"), t("ui.copy_link")); }
  async function copyURL(url, done, ask) {
    try {
      await navigator.clipboard.writeText(url);
      toast("ok", done, url);
    } catch (e) {
      // Ohne HTTPS gibt es keine Zwischenablage-API – Link zum manuellen Kopieren anzeigen.
      window.prompt(ask + ":", url);
    }
  }

  async function wake(d) {
    if (state.waking.has(d.id)) return;
    let res;
    try {
      res = await api("POST", "devices/" + encodeURIComponent(d.id) + "/wake");
    } catch (e) {
      toast("err", t("ui.wake_failed"), (e.data && e.data.error) || e.message);
      return;
    }
    if (!d.check_ip) {
      toast("ok", t("ui.packet_sent"), t("ui.sent_no_ip", d.name, res.target));
      loadDevices();
      return;
    }
    toast("ok", t("ui.packet_sent"), t("ui.is_starting", d.name));
    track(d);
  }

  async function wakeGroup(g) {
    let res;
    try {
      res = await api("POST", "groups/" + encodeURIComponent(g.name) + "/wake");
    } catch (e) {
      toast("err", t("ui.group_wake_failed"), (e.data && e.data.message) || e.message);
      return;
    }
    const failed = res.results.filter((r) => !r.success);
    toast(failed.length ? "warn" : "ok", t("ui.group_x", g.name), res.message + (failed.length ? " " + t("ui.errors_for", failed.map((r) => r.device).join(", ")) : ""));
    for (const r of res.results) {
      const d = state.devices.find((x) => x.id === r.id);
      if (r.success && d && d.check_ip) track(d, true);
    }
    loadDevices();
  }

  // track verfolgt den Start eines Geräts, bis es online ist oder die Wartezeit um ist.
  async function track(d, quiet) {
    if (state.waking.has(d.id)) return;
    const start = Date.now();
    state.waking.set(d.id, { start });
    renderDevices();
    const deadline = start + state.info.wake_timeout * 1000;
    while (Date.now() < deadline) {
      await sleep(2000);
      try {
        const st = await api("GET", "devices/" + encodeURIComponent(d.id) + "/status");
        const cur = state.devices.find((x) => x.id === d.id);
        if (cur) cur.status = st;
        if (st.state === "online") {
          state.waking.delete(d.id);
          renderDevices();
          const card = $('.card[data-id="' + CSS.escape(d.id) + '"]');
          if (card) { card.classList.add("card-flash"); setTimeout(() => card.classList.remove("card-flash"), 1300); }
          if (!quiet) toast("ok", t("ui.x_is_online", d.name), t("ui.started_after", fmtDuration(Date.now() - start)));
          return;
        }
      } catch (e) { /* weiter */ }
      renderDevices();
    }
    state.waking.delete(d.id);
    renderDevices();
    toast("warn", t("ui.x_no_answer", d.name), t("ui.no_answer_after", fmtDuration(Date.now() - start)));
  }

  // Laufende Zeitanzeige beim Wecken
  setInterval(() => {
    for (const [id, w] of state.waking) {
      const t = document.querySelector('[data-timer="' + CSS.escape(id) + '"]');
      if (t) t.textContent = fmtDuration(Date.now() - w.start);
      const bar = document.querySelector('.card[data-id="' + CSS.escape(id) + '"] .progress-bar');
      if (bar) bar.style.width = Math.min(100, ((Date.now() - w.start) / (state.info.wake_timeout * 1000)) * 100) + "%";
    }
  }, 500);

  // ---------- Dialog Gerät ----------
  const dlg = $("#device-dialog");
  const form = $("#device-form");

  function buildIconPicker() {
    const box = $("#icon-picker");
    for (const key of ICONS) {
      const label = t("ui.icon." + key);
      box.append(el("label", { title: label },
        el("input", { type: "radio", name: "icon", value: key, "aria-label": label }),
        el("span", {}, icon(key))));
    }
  }

  function openDialog(d, prefill) {
    state.editing = d || null;
    form.reset();
    $("#form-error").hidden = true;
    for (const i of form.querySelectorAll("input")) i.removeAttribute("aria-invalid");
    const v = d || prefill || {};
    $("#device-dialog-title").textContent = d ? t("ui.dialog.edit") : prefill ? t("ui.dialog.adopt") : t("ui.dialog.add");
    form.name.value = v.name || "";
    form.mac.value = v.mac || "";
    form.ip.value = v.ip || "";
    form.broadcast.value = v.broadcast || "";
    form.interface.value = v.interface || "";
    form.note.value = v.note || "";
    form.ports.value = (v.ports || []).join(", ");
    form.secureon.value = v.secureon || "";
    form.group.value = v.group || "";
    form.schedule.value = (v.schedule || []).map(scheduleText).join("; ");
    $("#group-list").replaceChildren(...[...new Set(state.devices.map((x) => x.group).filter(Boolean))].map((g) => el("option", { value: g })));
    form.ports.placeholder = state.info.ports && state.info.ports.length ? t("ui.default_ports", state.info.ports.slice(0, 5).join(", ") + (state.info.ports.length > 5 ? " …" : "")) : t("ui.default");
    const ic = form.querySelector('input[name="icon"][value="' + (v.icon || "desktop") + '"]');
    if (ic) ic.checked = true;
    $("#vendor-hint").textContent = v.vendor ? t("ui.vendor", vendorText(v.vendor)) : "";
    form.broadcast.placeholder = state.info.default_broadcast ? t("ui.automatic_x", state.info.default_broadcast) : t("ui.automatic");
    $(".advanced").open = !!(v.broadcast || v.interface || v.note || v.secureon || (v.ports && v.ports.length));
    $("#delete-device").hidden = !d;
    $("#rotate-key").hidden = !(d && state.info.wake_keys);
    dlg.showModal();
    (v.name ? form.mac : form.name).focus();
    if (prefill && !prefill.name) form.name.focus();
  }

  form.addEventListener("submit", async (ev) => {
    ev.preventDefault();
    const body = {
      name: form.name.value, mac: form.mac.value, ip: form.ip.value, broadcast: form.broadcast.value,
      interface: form.interface.value, note: form.note.value,
      icon: (form.querySelector('input[name="icon"]:checked') || {}).value || "desktop",
      ports: form.ports.value.split(/[\s,;]+/).filter(Boolean).map(Number),
      secureon: form.secureon.value,
      group: form.group.value,
      schedule: form.schedule.value.split(";").map((x) => x.trim()).filter(Boolean),
    };
    const err = $("#form-error");
    for (const i of form.querySelectorAll("input")) i.removeAttribute("aria-invalid");
    if (body.ports.some((p) => !Number.isInteger(p))) {
      err.textContent = t("ui.ports_numbers");
      err.hidden = false;
      $(".advanced").open = true;
      form.ports.setAttribute("aria-invalid", "true");
      form.ports.focus();
      return;
    }
    if (!body.name.trim()) { form.name.setAttribute("aria-invalid", "true"); form.name.focus(); return; }
    if (!body.mac.trim()) { form.mac.setAttribute("aria-invalid", "true"); form.mac.focus(); return; }
    const btn = $("#save-device");
    btn.disabled = true;
    try {
      let saved;
      if (state.editing) {
        saved = await api("PUT", "devices/" + encodeURIComponent(state.editing.id), Object.assign(body, { updated_at: state.editing.updated_at }));
      } else {
        saved = await api("POST", "devices", body);
      }
      dlg.close();
      toast("ok", state.editing ? t("ui.saved") : t("ui.device_added"), saved.name + " · " + saved.mac);
      await loadDevices();
      if (drawerOpen()) loadDiscovery();
    } catch (e) {
      err.textContent = e.message;
      err.hidden = false;
      // Der Server nennt das betroffene Feld; Felder unter "Erweitert" dafür aufklappen.
      const field = e.data && e.data.field && form.elements[e.data.field];
      if (field) {
        field.setAttribute("aria-invalid", "true");
        if (field.closest(".advanced")) $(".advanced").open = true;
      }
    } finally {
      btn.disabled = false;
    }
  });

  $("#rotate-key").addEventListener("click", async () => {
    const d = state.editing;
    if (!d) return;
    const ok = await confirmDialog(t("ui.rotate.title", d.name), t("ui.rotate.text"), t("ui.rotate.ok"));
    if (!ok) return;
    try {
      const nd = await api("POST", "devices/" + encodeURIComponent(d.id) + "/wake-key");
      state.editing = nd;
      toast("ok", t("ui.rotate.done"), wakeURL(nd));
      loadDevices();
    } catch (e) {
      toast("err", t("ui.not_possible"), e.message);
    }
  });

  $("#delete-device").addEventListener("click", async () => {
    const d = state.editing;
    if (!d) return;
    const ok = await confirmDialog(t("ui.delete.title", d.name), t("ui.delete.text"));
    if (!ok) return;
    try {
      await api("DELETE", "devices/" + encodeURIComponent(d.id));
      dlg.close();
      toast("ok", t("ui.deleted"), d.name);
      loadDevices();
    } catch (e) {
      toast("err", t("ui.delete_failed"), e.message);
    }
  });

  function confirmDialog(title, text, okLabel) {
    const c = $("#confirm-dialog");
    $("#confirm-title").textContent = title;
    $("#confirm-text").textContent = text;
    $("#confirm-ok").textContent = okLabel || t("ui.delete");
    c.returnValue = "";
    c.showModal();
    return new Promise((resolve) => c.addEventListener("close", () => resolve(c.returnValue === "ok"), { once: true }));
  }

  for (const b of document.querySelectorAll("#device-dialog [data-close]")) b.addEventListener("click", () => dlg.close());
  // Nur schließen, wenn der Klick auf dem Hintergrund auch begann: Wer Text in einem Feld
  // markiert und die Maus außerhalb loslässt, erzeugt sonst einen Klick auf den Dialog selbst –
  // der Dialog ging zu und die Eingaben waren weg.
  let downOnBackdrop = false;
  dlg.addEventListener("pointerdown", (e) => { downOnBackdrop = e.target === dlg; });
  dlg.addEventListener("click", (e) => { if (e.target === dlg && downOnBackdrop) dlg.close(); });

  // ---------- Netzwerksuche ----------
  const drawer = $("#discovery");
  const drawerOpen = () => drawer.classList.contains("open");
  let discoveryTimer = null;

  function openDrawer() {
    drawer.classList.add("open");
    drawer.setAttribute("aria-hidden", "false");
    loadDiscovery();
    setTimeout(() => $("#scan-btn").focus(), 50);
  }
  function closeDrawer() {
    // Fokus zurück auf den Knopf, sonst landet er im unsichtbar werdenden Bereich.
    if (drawer.contains(document.activeElement)) $("#open-discovery").focus();
    drawer.classList.remove("open");
    drawer.setAttribute("aria-hidden", "true");
  }
  for (const b of drawer.querySelectorAll("[data-close]")) b.addEventListener("click", closeDrawer);
  document.addEventListener("keydown", (e) => { if (e.key === "Escape" && drawerOpen() && !dlg.open) closeDrawer(); });

  function classify(f) {
    if (f.device_id) return "known";
    if (f.ignored) return "ignored";
    return "new";
  }

  async function loadDiscovery() {
    clearTimeout(discoveryTimer);
    try {
      const data = await api("GET", "discovery");
      state.found = data.found;
      state.job = data.job;
      renderDiscovery();
      if (data.job.running && !state.live) discoveryTimer = setTimeout(loadDiscovery, 1000);
      else if (drawerOpen()) discoveryTimer = setTimeout(loadDiscovery, 15000);
    } catch (e) {
      if (drawerOpen()) discoveryTimer = setTimeout(loadDiscovery, 5000);
    }
  }

  let wasRunning = false;
  function renderDiscovery() {
    const job = state.job || {};
    const counts = { new: 0, known: 0, ignored: 0 };
    for (const f of state.found) counts[classify(f)]++;
    const badge = $("#new-badge");
    badge.textContent = counts.new;
    badge.hidden = counts.new === 0;

    for (const b of $("#found-filter").children) {
      b.replaceChildren(b.dataset.label || (b.dataset.label = b.textContent), el("span", { class: "count", text: counts[b.dataset.filter] }));
    }

    const nets = state.info.networks || [];
    $("#discovery-networks").textContent = nets.length ? t("ui.scan.segments", nets.join(", ")) : "";
    const btn = $("#scan-btn");
    const prog = $("#scan-progress");
    if (!nets.length) {
      $("#scan-title").textContent = t("ui.scan.no_segment");
      $("#scan-detail").textContent = t("ui.scan.no_segment_hint");
      btn.disabled = true;
    } else if (job.running) {
      $("#scan-title").textContent = t("ui.scan.running");
      $("#scan-detail").textContent = t("ui.scan.progress", job.done, job.total);
      btn.disabled = false;
      btn.replaceChildren(icon("x"), el("span", { text: t("ui.cancel") }));
      btn.dataset.mode = "cancel";
      prog.hidden = false;
      prog.classList.toggle("indeterminate", !job.total);
      $(".progress-bar", prog).style.width = (job.total ? Math.round((job.done / job.total) * 100) : 0) + "%";
    } else {
      btn.disabled = false;
      btn.replaceChildren(icon("radar"), el("span", { text: t("ui.scan.start") }));
      btn.dataset.mode = "scan";
      prog.hidden = true;
      const methods = (job.method || "").split(", ").filter(Boolean).map((m) => t("ui.scan.method." + m)).join(", ");
      const found = t("ui.scan.found", tn("ui.n_devices", job.found)) + (job.new ? t("ui.scan.new_suffix", job.new) : "");
      if (job.finished_at) {
        $("#scan-title").textContent = job.error ? t("ui.scan.with_problems") : found;
        $("#scan-detail").textContent = t(job.auto ? "ui.scan.auto_ago" : "ui.scan.last_ago", ago(job.finished_at)) +
          (methods ? " · " + methods : "") + (job.error ? " · " + job.error : "");
      } else {
        $("#scan-title").textContent = t("ui.scan.ready");
        $("#scan-detail").textContent = t("ui.scan.ready_hint");
      }
      if (wasRunning && !job.error) toast("ok", t("ui.scan.done"), found);
    }
    wasRunning = !!job.running;

    const list = $("#found-list");
    const items = state.found.filter((f) => classify(f) === state.foundFilter);
    keepFocus(list, () => list.replaceChildren(...items.map(buildFound)));
    const empty = $("#found-empty");
    empty.hidden = items.length !== 0;
    empty.textContent = state.foundFilter === "new"
      ? (state.found.length ? t("ui.found.none_new") : t("ui.found.no_scan"))
      : state.foundFilter === "known" ? t("ui.found.none_known") : t("ui.found.none_ignored");
  }

  function guessIcon(f) {
    const s = ((f.vendor || "") + " " + (f.hostname || "")).toLowerCase();
    const rules = [
      ["nas", /synology|qnap|asustor|terramaster|western digital|nas/],
      ["printer", /brother|epson|canon|lexmark|kyocera|printer|drucker|hewlett/],
      ["console", /nintendo|sony interactive|xbox|playstation/],
      ["tv", /lg electronics|samsung electronics|tcl|hisense|roku|chromecast|fire ?tv|tv/],
      ["router", /avm|tp-link|ubiquiti|netgear|mikrotik|cisco|zyxel|juniper|aruba|fritz|router|switch|unifi/],
      ["server", /supermicro|proxmox|server|vmware|dell|hpe|intel corporate/],
      ["laptop", /apple|laptop|notebook|macbook/],
    ];
    for (const [ic, re] of rules) if (re.test(s)) return ic;
    return "desktop";
  }

  function buildFound(f) {
    const ro = state.info.read_only;
    const kind = classify(f);
    const title = f.hostname || vendorText(f.vendor) || t("ui.found.unknown");
    const actions = [];
    if (!ro && kind === "new") {
      actions.push(el("button", { class: "btn btn-primary btn-sm", "data-focus": "adopt", onclick: () => adopt(f) }, icon("plus"), el("span", { text: t("ui.found.adopt") })));
      actions.push(el("button", { class: "btn btn-ghost btn-sm icon-only", title: t("ui.found.ignore"), "aria-label": t("ui.found.ignore"), "data-focus": "ignore", onclick: () => setIgnored(f, true) }, icon("eye-off")));
    } else if (!ro && kind === "ignored") {
      actions.push(el("button", { class: "btn btn-secondary btn-sm", "data-focus": "unignore", onclick: () => setIgnored(f, false) }, el("span", { text: t("ui.found.unignore") })));
    } else if (kind === "known") {
      actions.push(el("span", { class: "tag", text: f.device_name }));
    }
    return el("li", { class: "found", "data-id": f.mac },
      el("div", { class: "dev-icon" }, icon(guessIcon(f))),
      el("div", { class: "found-main" },
        el("strong", { text: title, title }),
        el("div", { class: "card-meta" },
          el("span", { class: "mono", text: f.ip }),
          el("span", { class: "mono", text: f.mac }),
          f.hostname && f.vendor ? el("span", { text: vendorText(f.vendor) }) : null,
          el("span", { text: t("ui.found.seen", ago(f.last_seen)) }))),
      el("div", { class: "found-actions" }, actions));
  }

  function adopt(f) {
    let name = (f.hostname || "").split(".")[0];
    if (name && state.devices.some((d) => d.name.toLowerCase() === name.toLowerCase())) name += "-" + f.ip.split(".").pop();
    openDialog(null, { name, mac: f.mac, ip: f.ip, vendor: f.vendor, icon: guessIcon(f) });
  }

  async function setIgnored(f, ignored) {
    try {
      await api("POST", "discovery/" + encodeURIComponent(f.mac) + "/ignore", { ignored });
      f.ignored = ignored;
      renderDiscovery();
    } catch (e) {
      toast("err", t("ui.error"), e.message);
    }
  }

  $("#scan-btn").addEventListener("click", async () => {
    const btn = $("#scan-btn");
    try {
      if (btn.dataset.mode === "cancel") await api("POST", "discovery/cancel");
      else await api("POST", "discovery/scan");
    } catch (e) {
      toast("err", t("ui.scan.impossible"), e.message);
    }
    loadDiscovery();
  });

  // ---------- Verlauf, Export, Import ----------
  const SOURCE_ICON = { schedule: "clock" };
  // Quellen im Verlauf sind Schlüssel ("ui", "link", "schedule", "group" …), Gruppen mit Namen.
  function sourceText(ev) {
    const key = "ui.src." + ev.source;
    const s = t(key, ev.group || "");
    return s === key ? ev.source : s; // unbekannte Quelle: so anzeigen, wie sie gespeichert ist
  }
  async function openHistory() {
    const d = $("#history-dialog");
    let events = [];
    try { events = (await api("GET", "history?limit=200")).events || []; } catch (e) { toast("err", t("ui.history_unavailable"), e.message); return; }
    const fmt = new Intl.DateTimeFormat(locale, { day: "2-digit", month: "2-digit", hour: "2-digit", minute: "2-digit" });
    $("#history-list").replaceChildren(...events.map((ev) => el("li", { class: ev.success ? "ok" : "err" },
      icon(ev.success ? (SOURCE_ICON[ev.source] || "check") : "x"),
      el("div", { class: "h-main" },
        el("strong", { text: ev.device }),
        el("div", { class: "h-meta", text: [sourceText(ev), ev.user, ev.client, ev.target && t("ui.to_target", ev.target)].filter(Boolean).join(" · ") }),
        ev.error ? el("div", { class: "h-meta", text: ev.error }) : null),
      el("time", { datetime: ev.time, title: new Date(ev.time).toLocaleString(locale), text: fmt.format(new Date(ev.time)) }))));
    $("#history-empty").hidden = events.length !== 0;
    d.showModal();
  }
  $("#open-history").addEventListener("click", openHistory);
  for (const b of document.querySelectorAll("#history-dialog [data-close]")) b.addEventListener("click", () => $("#history-dialog").close());

  $("#import-btn").addEventListener("click", () => $("#import-file").click());
  $("#import-file").addEventListener("change", async (e) => {
    const file = e.target.files[0];
    e.target.value = "";
    if (!file) return;
    try {
      const res = await api("POST", "import", await file.text(), true);
      const parts = [t("ui.import.added", res.added)];
      if (res.skipped) parts.push(t("ui.import.skipped", res.skipped));
      if (res.errors && res.errors.length) parts.push(t("ui.import.faulty", res.errors.length));
      if (res.secret_restored) parts.push(t("ui.import.secret"));
      toast(res.errors && res.errors.length ? "warn" : "ok", t("ui.import.done"), parts.join(", ") + (res.errors && res.errors.length ? ": " + res.errors.slice(0, 3).join("; ") : ""));
      loadDevices();
    } catch (err) {
      toast("err", t("ui.import.failed"), err.message);
    }
  });

  // ---------- Filter, Suche, Theme ----------
  function segmented(box, key, onChange) {
    box.addEventListener("click", (e) => {
      const b = e.target.closest("button");
      if (!b) return;
      for (const x of box.children) x.setAttribute("aria-selected", x === b ? "true" : "false");
      state[key] = b.dataset.filter;
      onChange();
    });
  }
  segmented($("#filter"), "filter", renderDevices);
  segmented($("#found-filter"), "foundFilter", renderDiscovery);
  $("#search").addEventListener("input", (e) => { state.search = e.target.value; renderDevices(); });
  document.addEventListener("keydown", (e) => {
    if (e.key === "/" && document.activeElement.tagName !== "INPUT" && document.activeElement.tagName !== "TEXTAREA" && !dlg.open) {
      e.preventDefault();
      $("#search").focus();
    }
  });

  function currentTheme() {
    const t = document.documentElement.getAttribute("data-theme");
    if (t) return t;
    return matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
  }
  function updateThemeIcon() {
    $("#theme-toggle").replaceChildren(icon(currentTheme() === "dark" ? "sun" : "moon"));
  }
  $("#theme-toggle").addEventListener("click", () => {
    const next = currentTheme() === "dark" ? "light" : "dark";
    document.documentElement.setAttribute("data-theme", next);
    try { localStorage.setItem("wolweb-theme", next); } catch (e) { /* egal */ }
    updateThemeIcon();
  });

  // Sprache: Wahl im Cookie (gilt auch für Meldungen des Servers), dann neu laden.
  $("#lang-toggle").addEventListener("click", (e) => {
    const next = e.currentTarget.dataset.lang;
    document.cookie = "wolweb_lang=" + next + "; path=" + new URL(document.baseURI).pathname + "; max-age=31536000; SameSite=Lax";
    location.reload();
  });

  $("#add-device").addEventListener("click", () => openDialog());
  $("#open-discovery").addEventListener("click", openDrawer);
  document.addEventListener("click", (e) => {
    const a = e.target.closest("[data-action]");
    if (!a) return;
    if (a.dataset.action === "add") openDialog();
    if (a.dataset.action === "discovery") openDrawer();
  });

  // ---------- Live-Updates ----------
  let reloadTimer = null;
  function loadDevicesSoon() { // mehrere Änderungen kurz hintereinander: nur einmal laden
    clearTimeout(reloadTimer);
    reloadTimer = setTimeout(loadDevices, 250);
  }
  function connectEvents() {
    if (!window.EventSource) return;
    const es = new EventSource("api/v1/events"); // verbindet sich bei Abbruch selbst neu
    es.addEventListener("open", () => {
      if (!state.live) loadDevicesSoon(); // Verpasstes nachholen
      state.live = true;
    });
    es.addEventListener("error", () => { state.live = false; });
    es.addEventListener("devices", loadDevicesSoon);
    es.addEventListener("status", (e) => {
      const m = JSON.parse(e.data);
      const d = state.devices.find((x) => x.id === m.id);
      if (!d) return;
      d.status = m.status;
      if (m.status.ip) d.check_ip = m.status.ip;
      renderDevices();
    });
    es.addEventListener("scan", (e) => {
      const wasRun = state.job && state.job.running;
      state.job = JSON.parse(e.data);
      if (wasRun && !state.job.running) loadDiscovery(); // fertig: neue Treffer holen
      else renderDiscovery();
    });
  }

  // ---------- Start ----------
  async function init() {
    buildIconPicker();
    updateThemeIcon();
    try {
      state.info = await api("GET", "info");
    } catch (e) { /* Standardwerte */ }
    if (state.info.read_only) {
      $("#add-device").hidden = true;
      $("#import-btn").hidden = true;
      for (const b of document.querySelectorAll('[data-action="add"]')) b.hidden = true;
    }
    if (state.info.version) $("#version").textContent = "wolweb " + state.info.version;
    await loadDevices();
    loadDiscovery();
    // Abfragen nur als Rückfall, wenn die Live-Verbindung fehlt; sonst selten zur Sicherheit.
    let tick = 0;
    setInterval(() => { tick++; if (!document.hidden && (!state.live || tick % 6 === 0)) loadDevices(); }, 10000);
    setInterval(() => { if (!document.hidden && !drawerOpen()) loadDiscovery(); }, 60000);
    connectEvents();
    document.addEventListener("visibilitychange", () => { if (!document.hidden) loadDevices(); });
  }
  init();
})();
