// wolweb – Übersicht, Geräteverwaltung und Netzwerksuche.
(function () {
  "use strict";

  const $ = (sel, root = document) => root.querySelector(sel);
  const ICONS = [
    ["desktop", "PC"], ["laptop", "Laptop"], ["server", "Server"], ["nas", "NAS"], ["tv", "TV"],
    ["console", "Konsole"], ["router", "Netzwerk"], ["printer", "Drucker"], ["other", "Sonstiges"],
  ];
  const state = {
    info: { read_only: false, networks: [], wake_timeout: 180 },
    devices: [],
    found: [],
    job: null,
    filter: "all",
    foundFilter: "new",
    search: "",
    waking: new Map(), // id -> { start }
    editing: null,
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
  async function api(method, url, body) {
    const opts = { method, cache: "no-store", headers: { "Accept": "application/json", "X-WolWeb": "1" } };
    if (body !== undefined) {
      opts.headers["Content-Type"] = "application/json";
      opts.body = JSON.stringify(body);
    }
    const res = await fetch("api/v1/" + url, opts);
    let data = null;
    try { data = await res.json(); } catch (e) { /* leer */ }
    if (!res.ok || !data) {
      const err = new Error((data && data.message) || "Unerwartete Antwort vom Server (HTTP " + res.status + ")");
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
    if (s < 45) return "gerade eben";
    if (s < 3600) return "vor " + Math.round(s / 60) + " min";
    if (s < 86400) return "vor " + Math.round(s / 3600) + " h";
    const d = Math.round(s / 86400);
    return "vor " + d + (d === 1 ? " Tag" : " Tagen");
  }
  function fmtDuration(ms) {
    const s = Math.round(ms / 1000);
    return s < 60 ? s + " s" : Math.floor(s / 60) + ":" + String(s % 60).padStart(2, "0") + " min";
  }
  function wakeURL(d) { return new URL(d.wake_path, document.baseURI).href; }

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
      renderDevices();
    } catch (e) {
      if (e.status === 401) location.reload();
      $("#summary").textContent = "Server nicht erreichbar";
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
    $("#summary").textContent = total === 0 ? "Keine Geräte" :
      total + (total === 1 ? " Gerät" : " Geräte") + " · " + online + " online";
    for (const b of $("#filter").children) {
      const f = b.dataset.filter;
      const n = f === "all" ? total : f === "online" ? online : total - online;
      b.replaceChildren(b.dataset.label || (b.dataset.label = b.textContent), el("span", { class: "count", text: n }));
    }
    const list = state.devices.filter(matches);
    $("#empty").hidden = total !== 0;
    $("#empty-filter").hidden = total === 0 || list.length !== 0;
    const existing = new Map([...grid.children].map((c) => [c.dataset.id, c]));
    const next = list.map((d) => {
      const card = buildCard(d);
      const old = existing.get(d.id);
      if (old) {
        card.style.animation = "none";
        if (old.classList.contains("card-flash")) card.classList.add("card-flash");
      }
      return card;
    });
    grid.replaceChildren(...next);
  }

  function statusLine(d) {
    const s = deviceState(d);
    const st = d.status || {};
    if (s === "waking") {
      const w = state.waking.get(d.id);
      return [el("span", { class: "pill waking", text: "Startet …" }), el("span", { class: "timer", "data-timer": d.id, text: fmtDuration(Date.now() - w.start) })];
    }
    if (s === "online") {
      const m = st.method === "icmp" ? "Ping" : (st.method || "").replace("tcp/", "Port ").replace(" (abgelehnt)", "");
      return [el("span", { class: "pill online", text: "Online" }), el("span", { text: m + (st.rtt_ms ? " · " + st.rtt_ms + " ms" : "") })];
    }
    if (s === "unknown") {
      return [el("span", { class: "pill unknown", text: "Kein Status" }), el("span", { text: "keine IP bekannt" })];
    }
    return [el("span", { class: "pill offline", text: "Offline" }),
      st.note ? el("span", { class: "pill warn", text: "IP belegt", title: st.note }) : null,
      el("span", { text: st.last_seen ? "zuletzt online " + ago(st.last_seen) : d.last_wake ? "geweckt " + ago(d.last_wake) : "" })];
  }

  function buildCard(d) {
    const ro = state.info.read_only;
    const s = deviceState(d);
    const ip = d.ip || d.check_ip;
    const card = el("article", { class: "card", "data-id": d.id, "data-state": s },
      el("div", { class: "card-top" },
        el("div", { class: "dev-icon", title: d.vendor || "" }, icon(d.icon || "desktop")),
        el("div", { class: "card-title" },
          el("h3", { text: d.name, title: d.name }),
          el("div", { class: "card-meta" },
            ip ? el("span", { class: "mono", text: ip, title: d.ip ? "IP-Adresse" : "IP aus der Netzwerksuche" }) : null,
            el("span", { class: "mono", text: d.mac, title: d.vendor || "MAC-Adresse" }))),
        ro ? null : el("button", { class: "btn btn-ghost btn-sm icon-only", title: "Bearbeiten", "aria-label": "Bearbeiten", onclick: () => openDialog(d) }, icon("edit"))),
      d.note ? el("p", { class: "card-note", text: d.note }) : null,
      el("div", { class: "status-line" }, statusLine(d)),
      el("div", { class: "card-actions" },
        el("button", { class: "btn btn-primary btn-wake", disabled: s === "waking", onclick: () => wake(d) },
          icon("power"), el("span", { text: s === "waking" ? "Wird gestartet …" : s === "online" ? "Erneut wecken" : "Wecken" })),
        el("button", { class: "btn btn-secondary icon-only", title: "Wecklink kopieren", "aria-label": "Wecklink kopieren", onclick: () => copyLink(d) }, icon("link")),
        el("a", { class: "btn btn-secondary icon-only", href: d.wake_path, target: "_blank", rel: "noopener", title: "Weckseite öffnen", "aria-label": "Weckseite öffnen" }, icon("external"))));
    if (s === "waking") {
      const w = state.waking.get(d.id);
      const pct = Math.min(100, ((Date.now() - w.start) / (state.info.wake_timeout * 1000)) * 100);
      const bar = el("div", { class: "progress-bar" });
      bar.style.width = pct + "%";
      card.append(el("div", { class: "progress" }, bar));
    }
    return card;
  }

  async function copyLink(d) {
    const url = wakeURL(d);
    try {
      await navigator.clipboard.writeText(url);
      toast("ok", "Wecklink kopiert", url);
    } catch (e) {
      // Ohne HTTPS gibt es keine Zwischenablage-API – Link zum manuellen Kopieren anzeigen.
      window.prompt("Wecklink kopieren:", url);
    }
  }

  async function wake(d) {
    if (state.waking.has(d.id)) return;
    let res;
    try {
      res = await api("POST", "devices/" + encodeURIComponent(d.id) + "/wake");
    } catch (e) {
      toast("err", "Wecken fehlgeschlagen", (e.data && e.data.error) || e.message);
      return;
    }
    if (!d.check_ip) {
      toast("ok", "Magic Packet gesendet", d.name + " · an " + res.target + " (ohne IP kein Online-Nachweis)");
      loadDevices();
      return;
    }
    toast("ok", "Magic Packet gesendet", d.name + " wird gestartet …");
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
          toast("ok", d.name + " ist online", "Gestartet nach " + fmtDuration(Date.now() - start));
          return;
        }
      } catch (e) { /* weiter */ }
      renderDevices();
    }
    state.waking.delete(d.id);
    renderDevices();
    toast("warn", d.name + " antwortet nicht", "Das Paket wurde gesendet, aber nach " + fmtDuration(Date.now() - start) + " kam keine Antwort.");
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
    for (const [key, label] of ICONS) {
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
    $("#device-dialog-title").textContent = d ? "Gerät bearbeiten" : prefill ? "Gerät übernehmen" : "Gerät anlegen";
    form.name.value = v.name || "";
    form.mac.value = v.mac || "";
    form.ip.value = v.ip || "";
    form.broadcast.value = v.broadcast || "";
    form.interface.value = v.interface || "";
    form.note.value = v.note || "";
    const ic = form.querySelector('input[name="icon"][value="' + (v.icon || "desktop") + '"]');
    if (ic) ic.checked = true;
    $("#vendor-hint").textContent = v.vendor ? "Hersteller: " + v.vendor : "";
    form.broadcast.placeholder = "automatisch" + (state.info.default_broadcast ? " (" + state.info.default_broadcast + ")" : "");
    $(".advanced").open = !!(v.broadcast || v.interface || v.note);
    $("#delete-device").hidden = !d;
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
    };
    const err = $("#form-error");
    for (const i of form.querySelectorAll("input")) i.removeAttribute("aria-invalid");
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
      toast("ok", state.editing ? "Gespeichert" : "Gerät angelegt", saved.name + " · " + saved.mac);
      await loadDevices();
      if (drawerOpen()) loadDiscovery();
    } catch (e) {
      err.textContent = e.message;
      err.hidden = false;
      if (/MAC/.test(e.message)) form.mac.setAttribute("aria-invalid", "true");
      else if (/IP-Adresse/.test(e.message)) form.ip.setAttribute("aria-invalid", "true");
      else if (/Broadcast/.test(e.message)) form.broadcast.setAttribute("aria-invalid", "true");
      else if (/Name|namens/.test(e.message)) form.name.setAttribute("aria-invalid", "true");
    } finally {
      btn.disabled = false;
    }
  });

  $("#delete-device").addEventListener("click", async () => {
    const d = state.editing;
    if (!d) return;
    const ok = await confirmDialog("„" + d.name + "“ löschen?", "Der feste Wecklink funktioniert danach nicht mehr.");
    if (!ok) return;
    try {
      await api("DELETE", "devices/" + encodeURIComponent(d.id));
      dlg.close();
      toast("ok", "Gelöscht", d.name);
      loadDevices();
    } catch (e) {
      toast("err", "Löschen fehlgeschlagen", e.message);
    }
  });

  function confirmDialog(title, text) {
    const c = $("#confirm-dialog");
    $("#confirm-title").textContent = title;
    $("#confirm-text").textContent = text;
    c.returnValue = "";
    c.showModal();
    return new Promise((resolve) => c.addEventListener("close", () => resolve(c.returnValue === "ok"), { once: true }));
  }

  for (const b of document.querySelectorAll("#device-dialog [data-close]")) b.addEventListener("click", () => dlg.close());
  dlg.addEventListener("click", (e) => { if (e.target === dlg) dlg.close(); });

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
      if (data.job.running) discoveryTimer = setTimeout(loadDiscovery, 1000);
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
    $("#discovery-networks").textContent = nets.length ? "Segmente: " + nets.join(", ") : "";
    const btn = $("#scan-btn");
    const prog = $("#scan-progress");
    if (!nets.length) {
      $("#scan-title").textContent = "Kein Netzsegment eingestellt";
      $("#scan-detail").textContent = "In config.json unter discovery.networks z. B. \"192.168.1.0/24\" eintragen (oder WOLWEB_NETWORKS).";
      btn.disabled = true;
    } else if (job.running) {
      $("#scan-title").textContent = "Suche läuft …";
      $("#scan-detail").textContent = job.done + " von " + job.total + " Adressen geprüft";
      btn.disabled = false;
      btn.replaceChildren(icon("x"), el("span", { text: "Abbrechen" }));
      btn.dataset.mode = "cancel";
      prog.hidden = false;
      prog.classList.toggle("indeterminate", !job.total);
      $(".progress-bar", prog).style.width = (job.total ? Math.round((job.done / job.total) * 100) : 0) + "%";
    } else {
      btn.disabled = false;
      btn.replaceChildren(icon("radar"), el("span", { text: "Jetzt suchen" }));
      btn.dataset.mode = "scan";
      prog.hidden = true;
      if (job.finished_at) {
        $("#scan-title").textContent = job.error ? "Suche mit Problemen beendet" : job.found + (job.found === 1 ? " Gerät" : " Geräte") + " gefunden" + (job.new ? ", " + job.new + " neu" : "");
        $("#scan-detail").textContent = (job.auto ? "Automatische Suche " : "Letzte Suche ") + ago(job.finished_at) +
          (job.method ? " · " + job.method : "") + (job.error ? " · " + job.error : "");
      } else {
        $("#scan-title").textContent = "Suche bereit";
        $("#scan-detail").textContent = "Findet eingeschaltete Geräte mit IP, MAC, Name und Hersteller.";
      }
      if (wasRunning && !job.error) {
        toast("ok", "Suche beendet", job.found + (job.found === 1 ? " Gerät" : " Geräte") + " gefunden" + (job.new ? ", davon " + job.new + " neu" : ""));
      }
    }
    wasRunning = !!job.running;

    const list = $("#found-list");
    const items = state.found.filter((f) => classify(f) === state.foundFilter);
    list.replaceChildren(...items.map(buildFound));
    const empty = $("#found-empty");
    empty.hidden = items.length !== 0;
    empty.textContent = state.foundFilter === "new"
      ? (state.found.length ? "Keine neuen Geräte – alles schon angelegt oder ignoriert." : "Noch keine Suche gelaufen.")
      : state.foundFilter === "known" ? "Noch kein gefundenes Gerät angelegt." : "Nichts ignoriert.";
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
    const title = f.hostname || f.vendor || "Unbekanntes Gerät";
    const actions = [];
    if (!ro && kind === "new") {
      actions.push(el("button", { class: "btn btn-primary btn-sm", onclick: () => adopt(f) }, icon("plus"), el("span", { text: "Übernehmen" })));
      actions.push(el("button", { class: "btn btn-ghost btn-sm icon-only", title: "Ignorieren", "aria-label": "Ignorieren", onclick: () => setIgnored(f, true) }, icon("eye-off")));
    } else if (!ro && kind === "ignored") {
      actions.push(el("button", { class: "btn btn-secondary btn-sm", onclick: () => setIgnored(f, false) }, el("span", { text: "Wieder anzeigen" })));
    } else if (kind === "known") {
      actions.push(el("span", { class: "tag", text: f.device_name }));
    }
    return el("li", { class: "found" },
      el("div", { class: "dev-icon" }, icon(guessIcon(f))),
      el("div", { class: "found-main" },
        el("strong", { text: title, title }),
        el("div", { class: "card-meta" },
          el("span", { class: "mono", text: f.ip }),
          el("span", { class: "mono", text: f.mac }),
          f.hostname && f.vendor ? el("span", { text: f.vendor }) : null,
          el("span", { text: "gesehen " + ago(f.last_seen) }))),
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
      toast("err", "Fehler", e.message);
    }
  }

  $("#scan-btn").addEventListener("click", async () => {
    const btn = $("#scan-btn");
    try {
      if (btn.dataset.mode === "cancel") await api("POST", "discovery/cancel");
      else await api("POST", "discovery/scan");
    } catch (e) {
      toast("err", "Suche nicht möglich", e.message);
    }
    loadDiscovery();
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

  $("#add-device").addEventListener("click", () => openDialog());
  $("#open-discovery").addEventListener("click", openDrawer);
  document.addEventListener("click", (e) => {
    const a = e.target.closest("[data-action]");
    if (!a) return;
    if (a.dataset.action === "add") openDialog();
    if (a.dataset.action === "discovery") openDrawer();
  });

  // ---------- Start ----------
  async function init() {
    buildIconPicker();
    updateThemeIcon();
    try {
      state.info = await api("GET", "info");
    } catch (e) { /* Standardwerte */ }
    if (state.info.read_only) {
      $("#add-device").hidden = true;
      for (const b of document.querySelectorAll('[data-action="add"]')) b.hidden = true;
    }
    await loadDevices();
    loadDiscovery();
    setInterval(() => { if (!document.hidden) loadDevices(); }, 10000);
    setInterval(() => { if (!document.hidden && !drawerOpen()) loadDiscovery(); }, 60000);
    document.addEventListener("visibilitychange", () => { if (!document.hidden) loadDevices(); });
  }
  init();
})();
