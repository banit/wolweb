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
    unconfirmed: new Set(), // Geräte, deren Start nach dem Wecken nicht bestätigt wurde – bis sie antworten
    editing: null,
    fresh: null, // { id, adopted } – eben angelegtes Gerät: die Karte zeigt die nächsten Schritte
    naming: null, // MAC eines Suchtreffers ohne Namen, für den gerade ein Name abgefragt wird
    namingValue: "",
    selecting: false, // Mehrfachauswahl für Gruppen
    selected: new Set(),
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
    svg.setAttribute("aria-hidden", "true"); // Schmuck – die Knöpfe haben eigene Namen
    const use = document.createElementNS("http://www.w3.org/2000/svg", "use");
    use.setAttribute("href", "#i-" + name);
    svg.append(use);
    return svg;
  }
  // Ändern geht nicht im Nur-Lesen-Betrieb und nicht, solange der Bearbeitungsschutz gesperrt ist.
  const editable = () => !state.info.read_only && !state.info.locked;

  async function api(method, url, body, raw, again) {
    const opts = { method, cache: "no-store", headers: { "Accept": "application/json", "X-WolWeb": "1" } };
    if (body !== undefined) {
      opts.headers["Content-Type"] = "application/json";
      opts.body = raw ? body : JSON.stringify(body);
    }
    const res = await fetch("api/v1/" + url, opts);
    let data = null;
    try { data = await res.json(); } catch (e) { /* leer */ }
    // Gesperrt (Passwort für Änderungen): nach dem Passwort fragen und die Anfrage wiederholen.
    if (res.status === 403 && data && data.locked && !again) {
      if (await window.wolwebLock.unlock()) {
        setLocked(false);
        return api(method, url, body, raw, true);
      }
      setLocked(true);
    }
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

  // toast meldet kurz, was passiert ist. Warnungen bleiben stehen, bis sie geschlossen werden.
  function toast(kind, title, text) {
    const box = $("#toasts");
    const close = () => { n.classList.add("out"); setTimeout(() => n.remove(), 250); };
    const n = el("div", { class: "toast " + kind }, // der Kasten selbst ist die Ansage (role=status)
      icon(kind === "ok" ? "check" : kind === "sent" ? "send" : kind === "err" ? "x" : "alert"),
      el("div", {}, el("strong", { text: title }), text ? el("p", { text }) : null),
      kind === "warn" ? el("button", { class: "btn btn-ghost btn-sm icon-only toast-close", title: t("page.close"), "aria-label": t("page.close"), onclick: close }, icon("x")) : null);
    box.append(n);
    if (kind !== "warn") setTimeout(close, kind === "err" ? 7000 : 4200);
  }

  // ---------- Geräte ----------
  // loadDevices lädt die Geräteliste. Abrufe laufen nacheinander: Kommt während eines Abrufs ein
  // weiterer Anlass (Live-Ereignis, eigene Änderung), folgt ein zweiter. Sonst könnte eine ältere
  // Antwort eine neuere überschreiben – eine eben angelegte Karte verschwände kurz samt Fokus. Das
  // Versprechen erfüllt sich, wenn der neueste Stand gezeichnet ist.
  let devicesLoading = null, devicesAgain = false;
  function loadDevices() {
    if (devicesLoading) {
      devicesAgain = true;
      return devicesLoading;
    }
    devicesLoading = (async () => {
      do {
        devicesAgain = false;
        try {
          const devices = await api("GET", "devices");
          state.groups = devices.some((d) => d.group) ? await api("GET", "groups") : [];
          state.devices = devices;
          renderDevices();
        } catch (e) {
          if (e.status === 401) location.reload();
          $("#summary").textContent = t("ui.server_unreachable");
        }
      } while (devicesAgain);
      devicesLoading = null;
    })();
    return devicesLoading;
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
    for (const d of state.devices) if (deviceState(d) === "online") state.unconfirmed.delete(d.id); // doch noch gestartet
    $("#select-toggle").hidden = !editable() || total < 2;
    for (const id of state.selected) if (!state.devices.some((d) => d.id === id)) state.selected.delete(id);
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
    const nameID = "gn-" + groupIDs(g.name);
    return el("div", { class: "group-head", "data-id": "gruppe:" + g.name },
      el("h2", { id: nameID, text: g.name }),
      el("span", { class: "muted", text: t("ui.x_of_y_online", online, members.length) }),
      el("span", { class: "spacer" }),
      // Die Reichweite steht auf dem Knopf – alle Mitglieder, auch wenn die Suche gerade einige ausblendet.
      el("button", { class: "btn btn-secondary btn-sm", "aria-describedby": nameID, "data-focus": "wake", onclick: () => wakeGroup(g) }, icon("power"), el("span", { text: tn("ui.wake_all", members.length) })),
      state.simple ? null : el("button", { class: "btn btn-ghost btn-sm icon-only", title: t("ui.copy_group_link"), "aria-label": t("ui.copy_group_link"), "aria-describedby": nameID, "data-focus": "copy", onclick: () => copyURL(groupURL(g), t("ui.group_link_copied"), t("ui.copy_group_link")) }, icon("link")));
  }

  // groupIDs vergibt je Gruppenname eine feste Nummer für IDs (Namen dürfen Leerzeichen enthalten).
  const groupIDMap = new Map();
  function groupIDs(name) {
    const k = name.toLowerCase();
    if (!groupIDMap.has(k)) groupIDMap.set(k, groupIDMap.size + 1);
    return groupIDMap.get(k);
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
      const resent = state.info.wake_retry && Date.now() - w.start >= state.info.wake_retry * 1000; // der Server hat erneut gesendet
      return [el("span", { class: "pill waking", text: t("ui.starting") }), el("span", { class: "timer", "data-timer": d.id, text: fmtDuration(Date.now() - w.start) }),
        resent ? el("span", { text: t("ui.resent") }) : null];
    }
    if (s === "online") {
      // Ping/Port/ms nur in der normalen Ansicht – die einfache bleibt bei „Online“.
      return [el("span", { class: "pill online", text: t("ui.online") }), state.simple ? null : el("span", { text: methodText(st.method) + (st.rtt_ms ? " · " + st.rtt_ms + " ms" : "") })];
    }
    if (s === "unknown") {
      return [el("span", { class: "pill unknown", text: t("ui.no_status") }), el("span", { text: t("ui.no_ip_known") })];
    }
    return [el("span", { class: "pill offline", text: t("ui.offline") }),
      st.conflict_mac ? el("span", { class: "pill warn", text: t("ui.ip_taken"), title: t("ui.ip_taken_title", st.ip, st.conflict_mac) }) : null,
      el("span", { text: st.last_seen ? t("ui.last_online", ago(st.last_seen)) : d.last_wake ? t("ui.last_woken", ago(d.last_wake)) : "" })];
  }

  function buildCard(d) {
    const simple = state.simple; // einfache Ansicht: nur Name, Status und der Weckknopf
    const ro = !editable() || simple;
    const s = deviceState(d);
    const ip = d.ip || d.check_ip;
    // Die Knöpfe heißen auf jeder Karte gleich ("Wecken") – der Gerätename als Beschreibung sagt
    // Screenreadern, wozu sie gehören.
    const nameID = "dn-" + d.id;
    const card = el("article", { class: "card", "data-id": d.id, "data-state": s, "aria-labelledby": nameID },
      el("div", { class: "card-top" },
        state.selecting && !ro ? el("label", { class: "select-box" },
          el("input", { type: "checkbox", "aria-label": t("ui.sel.select", d.name), "data-focus": "select", checked: state.selected.has(d.id),
            onchange: (ev) => { if (ev.target.checked) state.selected.add(d.id); else state.selected.delete(d.id); updateSelectBar(); } })) : null,
        el("div", { class: "dev-icon", title: vendorText(d.vendor) }, icon(d.icon || "desktop")),
        el("div", { class: "card-title" },
          el("h3", { id: nameID, text: d.name, title: d.name }),
          simple ? null : el("div", { class: "card-meta" },
            ip ? el("span", { class: "mono", text: ip, title: d.ip ? t("ui.ip_address") : t("ui.ip_from_scan") }) : null,
            el("span", { class: "mono", text: d.mac, title: vendorText(d.vendor) || t("ui.mac_address") }),
            d.next_wake ? el("span", { class: "sched", title: t("ui.schedule_title", (d.schedule || []).map(scheduleText).join("; ")) }, icon("clock"), nextWakeText(d.next_wake)) : null,
            d.schedule_paused ? el("span", { class: "sched paused", title: t("ui.schedule_title", (d.schedule || []).map(scheduleText).join("; ")) }, icon("clock"), t("ui.sched.paused_short")) : null)),
        ro ? null : el("button", { class: "btn btn-ghost btn-sm icon-only", title: t("ui.edit"), "aria-label": t("ui.edit"), "aria-describedby": nameID, "data-focus": "edit", onclick: () => openDialog(d) }, icon("edit"))),
      d.note && !simple ? el("p", { class: "card-note", text: d.note }) : null,
      el("div", { class: "status-line" }, statusLine(d)),
      state.fresh && state.fresh.id === d.id && !ro ? nextSteps(d) : null,
      state.unconfirmed.has(d.id) && s !== "waking" ? unconfirmedNote(d) : null,
      el("div", { class: "card-actions" },
        el("button", { class: "btn btn-primary btn-wake", disabled: s === "waking", "aria-describedby": nameID, "data-focus": "wake", onclick: () => wake(d) },
          icon("power"), el("span", { text: s === "waking" ? t("ui.being_started") : s === "online" ? t("ui.wake_again") : t("ui.wake") })),
        simple ? null : el("button", { class: "btn btn-secondary icon-only", title: t("ui.copy_link"), "aria-label": t("ui.copy_link"), "aria-describedby": nameID, "data-focus": "copy", onclick: () => copyLink(d) }, icon("link")),
        simple ? null : el("a", { class: "btn btn-secondary icon-only", href: d.wake_path, target: "_blank", rel: "noopener", title: t("ui.open_wake_page"), "aria-label": t("ui.open_wake_page"), "aria-describedby": nameID, "data-focus": "open" }, icon("external"))));
    if (s === "waking") {
      const w = state.waking.get(d.id);
      const pct = Math.min(100, ((Date.now() - w.start) / (state.info.wake_timeout * 1000)) * 100);
      const bar = el("div", { class: "progress-bar" });
      bar.style.width = pct + "%";
      card.append(el("div", { class: "progress", "aria-hidden": "true" }, bar)); // die Zeit steht als Text daneben
    }
    return card;
  }

  // nextSteps: Hinweis auf einer eben angelegten Karte – Wecken steht direkt darunter.
  function nextSteps(d) {
    return el("div", { class: "card-next", role: "group", "aria-label": t("ui.next.title", d.name) },
      el("p", {}, icon("check"), el("span", { text: state.fresh.adopted ? t("ui.next.adopted") : t("ui.next.added") })),
      el("div", { class: "card-next-actions" },
        el("button", { class: "btn btn-secondary btn-sm", "data-focus": "next-copy", onclick: () => copyLink(d) }, icon("link"), el("span", { text: t("ui.copy_link") })),
        el("button", { class: "btn btn-secondary btn-sm", "data-focus": "next-phone", onclick: () => phoneSetup(d) }, icon("phone"), el("span", { text: t("page.phone.title") })),
        state.fresh.adopted ? el("button", { class: "btn btn-secondary btn-sm", "data-focus": "details", onclick: () => openDialog(d) }, icon("edit"), el("span", { text: t("ui.next.details") })) : null,
        el("span", { class: "spacer" }),
        el("button", { class: "btn btn-ghost btn-sm icon-only", title: t("ui.next.dismiss"), "aria-label": t("ui.next.dismiss"), "data-focus": "dismiss",
          onclick: () => { state.fresh = null; renderDevices(); focusCard(d.id); } }, icon("x"))));
  }
  // focusCard setzt den Fokus auf den Weckknopf einer Karte und lässt sie kurz aufleuchten.
  function focusCard(id) {
    const card = $('.card[data-id="' + CSS.escape(id) + '"]');
    if (!card) return;
    card.scrollIntoView({ block: "nearest" });
    card.querySelector('[data-focus="wake"]').focus({ preventScroll: true });
    card.classList.add("card-flash");
    setTimeout(() => card.classList.remove("card-flash"), 1300);
  }

  function copyLink(d) { return copyURL(wakeURL(d), t("ui.link_copied"), t("ui.copy_link")); }

  // phoneSetup erklärt in drei Schritten, wie der Wecklink als Symbol auf den Startbildschirm kommt.
  const phoneDlg = $("#phone-dialog");
  function phoneSetup(d) {
    const url = wakeURL(d);
    $("#phone-intro").textContent = t("ui.phone.intro", d.name);
    $("#phone-link").textContent = decodeURI(url); // lesbar ("Büro-PC"); kopiert wird die echte Adresse
    $("#phone-copy").onclick = () => copyURL(url, t("ui.link_copied"), t("ui.copy_link"));
    const share = $("#phone-share");
    share.hidden = !navigator.share; // Teilen schickt den Link z. B. per Messenger ans Handy
    share.onclick = () => navigator.share({ title: d.name, url }).catch(() => { /* abgebrochen */ });
    phoneDlg.showModal();
  }
  for (const b of phoneDlg.querySelectorAll("[data-close]")) b.addEventListener("click", () => phoneDlg.close());
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
    state.unconfirmed.delete(d.id);
    let res;
    try {
      res = await api("POST", "devices/" + encodeURIComponent(d.id) + "/wake");
    } catch (e) {
      toast("err", t("ui.wake_failed"), (e.data && e.data.error) || e.message);
      return;
    }
    if (!d.check_ip) {
      toast("sent", t("ui.packet_sent"), t("ui.sent_no_ip", d.name, res.target)); // gesendet ≠ gestartet
      loadDevices();
      return;
    }
    if (res.already) toast("sent", t("ui.already_waking", d.name), t("ui.is_starting", d.name));
    else toast("sent", t("ui.packet_sent"), t("ui.is_starting", d.name));
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
    const checking = res.results.some((r) => r.success && (state.devices.find((x) => x.id === r.id) || {}).check_ip);
    toast(failed.length ? "warn" : "sent", t("ui.group_x", g.name), res.message +
      (failed.length ? " " + t("ui.errors_for", failed.map((r) => r.device).join(", ")) : checking ? " " + t("ui.group_checking") : ""));
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
    state.unconfirmed.add(d.id); // die Karte sagt es weiter, bis das Gerät antwortet
    renderDevices();
    toast("warn", t("ui.x_no_answer", d.name), t("ui.no_answer_after", fmtDuration(Date.now() - start)));
  }

  // unconfirmedNote: Start nicht bestätigt – bleibt auf der Karte, mit „Nochmals prüfen“.
  function unconfirmedNote(d) {
    return el("div", { class: "card-warn" },
      el("p", {}, icon("alert"), el("span", { text: t("ui.unconfirmed", d.name) })),
      el("div", { class: "card-next-actions" },
        el("button", { class: "btn btn-secondary btn-sm", "data-focus": "recheck", "aria-describedby": "dn-" + d.id, onclick: () => recheck(d) }, icon("search"), el("span", { text: t("wake.btn.recheck") })),
        el("span", { class: "spacer" }),
        el("button", { class: "btn btn-ghost btn-sm icon-only", title: t("ui.next.dismiss"), "aria-label": t("ui.next.dismiss"), "data-focus": "dismiss-warn",
          onclick: () => { state.unconfirmed.delete(d.id); renderDevices(); focusCard(d.id); } }, icon("x"))));
  }
  async function recheck(d) {
    let st = null;
    try { st = await api("GET", "devices/" + encodeURIComponent(d.id) + "/status"); } catch (e) { /* keine Antwort */ }
    const cur = state.devices.find((x) => x.id === d.id);
    if (cur && st) cur.status = st;
    if (st && st.state === "online") {
      state.unconfirmed.delete(d.id);
      toast("ok", t("ui.x_is_online", d.name), t("wake.result.late_text", d.name));
    } else {
      toast("err", t("ui.still_no_answer", d.name), t("ui.unconfirmed_hint"));
    }
    renderDevices();
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

  // ---------- Mehrfachauswahl: Geräte gemeinsam einer Gruppe zuordnen ----------
  function fillGroupList() {
    $("#group-list").replaceChildren(...state.groups.map((g) => el("option", { value: g.name })));
  }
  function setSelecting(on) {
    state.selecting = on;
    state.selected.clear();
    $("#select-toggle").setAttribute("aria-pressed", String(on));
    $("#select-bar").hidden = !on;
    document.body.classList.toggle("selecting", on);
    $("#sel-group").value = "";
    if (on) fillGroupList();
    renderDevices();
    updateSelectBar();
  }
  function updateSelectBar() {
    const n = state.selected.size;
    const g = $("#sel-group").value.trim();
    const grouped = state.devices.some((d) => state.selected.has(d.id) && d.group);
    $("#sel-count").textContent = tn("ui.sel.count", n);
    $("#sel-apply-text").textContent = g ? t("ui.sel.apply", g) : grouped ? t("ui.sel.ungroup") : t("ui.sel.pick");
    $("#sel-apply").disabled = !n || (!g && !grouped);
  }
  async function applyGroup() {
    const ids = [...state.selected];
    try {
      const res = await api("POST", "devices/group", { ids, group: $("#sel-group").value.trim() });
      toast("ok", res.group ? tn("ui.sel.moved", ids.length, res.group) : tn("ui.sel.removed", ids.length));
      setSelecting(false);
      $("#select-toggle").focus();
      await loadDevices();
    } catch (e) {
      toast("err", t("ui.not_possible"), e.message);
    }
  }
  $("#select-toggle").addEventListener("click", () => setSelecting(!state.selecting));
  $("#sel-done").addEventListener("click", () => { setSelecting(false); $("#select-toggle").focus(); });
  $("#sel-group").addEventListener("input", updateSelectBar);
  $("#sel-group").addEventListener("keydown", (ev) => { if (ev.key === "Enter" && !$("#sel-apply").disabled) applyGroup(); });
  $("#sel-apply").addEventListener("click", applyGroup);
  $("#select-bar").addEventListener("keydown", (ev) => {
    if (ev.key === "Escape") { setSelecting(false); $("#select-toggle").focus(); }
  });

  // ---------- Dialog Gerät ----------
  const dlg = $("#device-dialog");
  const form = $("#device-form");

  // ---------- Zeitplan im Gerätedialog ----------
  // Schalter, Wochentage und Uhrzeit sind die einfache Form; gespeichert wird das Textfeld unter
  // „Weitere Möglichkeiten“. Die Schalter schreiben hinein, und was dort getippt wird, zeigen sie an –
  // solange es genau eine Weckzeit ist. Prüfen, vereinheitlichen und die nächste Weckzeit berechnen
  // tut der Server (Ortszeit des Servers, nicht des Browsers).
  const SCHED_DAYS = ["Mo", "Di", "Mi", "Do", "Fr", "Sa", "So"]; // Schreibweise des Servers, Montag zuerst
  const sched = {
    on: $("#sched-on"), body: $("#sched-body"), days: $("#sched-days"), time: $("#sched-time"),
    simple: $("#sched-simple"), multi: $("#sched-multi"), summary: $("#sched-summary"), more: $("#sched-more"),
    timer: 0, seq: 0,
  };
  const dayName = (i, weekday) => new Date(2024, 0, 1 + i).toLocaleDateString(locale, { weekday }); // 1.1.2024: Montag
  function buildDayPicker() {
    SCHED_DAYS.forEach((d, i) => sched.days.append(el("label", {},
      el("input", { type: "checkbox", value: d, "aria-label": dayName(i, "long") }),
      el("span", { "aria-hidden": "true", text: dayName(i, "short") }))));
  }
  // runs fasst aufeinanderfolgende Tage zusammen: [[0, 4], [5, 5], [6, 6]] für Mo-Fr, Sa, So.
  function runs(days) {
    const out = [];
    for (let i = 0; i < 7; i++) {
      if (!days[i]) continue;
      let j = i;
      while (j + 1 < 7 && days[j + 1]) j++;
      if (j - i >= 2) out.push([i, j]);
      else for (let k = i; k <= j; k++) out.push([k, k]);
      i = j;
    }
    return out;
  }
  // Einheitliche Schreibweise des Servers ("Mo-Fr 07:30", "täglich 22:00") ↔ Tage und Uhrzeit.
  function entryText(days, time) {
    if (days.every(Boolean)) return "täglich " + time;
    return runs(days).map(([a, b]) => a === b ? SCHED_DAYS[a] : SCHED_DAYS[a] + "-" + SCHED_DAYS[b]).join(",") + " " + time;
  }
  function parseEntry(s) {
    const m = /^(\S+) (\d\d:\d\d)$/.exec(s || "");
    if (!m) return null;
    const days = Array(7).fill(m[1] === "täglich");
    if (m[1] !== "täglich") {
      for (const part of m[1].split(",")) {
        const [a, b] = part.split("-").map((x) => SCHED_DAYS.indexOf(x));
        if (a < 0 || b < 0) return null;
        for (let i = a; ; i = (i + 1) % 7) { days[i] = true; if (b === undefined || i === b) break; }
      }
    }
    return { days, time: m[2] };
  }
  // describeEntry: „Montag bis Freitag um 07:30 Uhr“, „Täglich um 22:00 Uhr“.
  function describeEntry(e) {
    if (e.days.every(Boolean)) return t("ui.sched.daily", e.time);
    const parts = runs(e.days).map(([a, b]) => a === b ? dayName(a, "long") : t("ui.sched.range", dayName(a, "long"), dayName(b, "long")));
    return t("ui.sched.days", new Intl.ListFormat(locale, { type: "conjunction" }).format(parts), e.time);
  }
  const schedDays = () => [...sched.days.querySelectorAll("input")].map((i) => i.checked);
  const schedList = () => form.schedule.value.split(";").map((x) => x.trim()).filter(Boolean);

  // showEntries zeigt Weckzeiten (einheitliche Schreibweise) in den Schaltern an.
  function showEntries(list) {
    const one = list.length === 1 ? parseEntry(list[0]) : null;
    sched.simple.hidden = list.length > 1;
    sched.multi.hidden = list.length <= 1;
    if (one) {
      sched.days.querySelectorAll("input").forEach((i, k) => { i.checked = one.days[k]; });
      sched.time.value = one.time;
    }
  }
  function setSchedule(list, paused) {
    sched.on.checked = list.length > 0;
    sched.body.hidden = !sched.on.checked;
    form.schedule.value = list.map(scheduleText).join("; ");
    form.schedule_paused.checked = !!paused;
    sched.more.open = false;
    sched.days.querySelectorAll("input").forEach((i, k) => { i.checked = k < 5; }); // Vorschlag: werktags
    sched.time.value = "07:30";
    showEntries(list);
    previewSchedule(0);
  }
  function simpleChanged() {
    const days = schedDays();
    form.schedule.value = days.some(Boolean) ? scheduleText(entryText(days, sched.time.value || "07:30")) : "";
    previewSchedule(0);
  }
  function previewSchedule(delay) {
    clearTimeout(sched.timer);
    sched.timer = setTimeout(runPreview, delay);
  }
  async function runPreview() {
    const seq = ++sched.seq;
    const list = schedList();
    if (!sched.on.checked) return sched.summary.replaceChildren();
    if (!list.length) return renderSummary({ error: t("ui.sched.no_days") });
    let res;
    try {
      res = await api("GET", "schedule/preview?" + new URLSearchParams(list.map((x) => ["s", x])));
    } catch (e) {
      if (seq === sched.seq) renderSummary({ error: e.message });
      return;
    }
    if (seq !== sched.seq) return; // inzwischen geändert
    if (document.activeElement !== form.schedule) showEntries(res.schedule);
    renderSummary(res);
  }
  // renderSummary: was eingestellt ist, wann als Nächstes geweckt wird und in welcher Zeitzone.
  function renderSummary(res) {
    if (res.error) {
      sched.summary.replaceChildren(el("p", { class: "sched-error", text: res.error }));
      return;
    }
    const lines = res.schedule.map((x) => parseEntry(x)).filter(Boolean).map((e) => el("strong", { text: describeEntry(e) }));
    if (form.schedule_paused.checked) {
      lines.push(el("p", { class: "sched-paused", text: t("ui.sched.paused") }));
    } else if (res.next) {
      // Die Weckzeit gilt auf der Uhr des Servers: dessen Datum und Uhrzeit anzeigen, nicht die des Browsers.
      const m = /^(\d+)-(\d+)-(\d+)T(\d+):(\d+)/.exec(res.next);
      const wall = new Date(+m[1], m[2] - 1, +m[3], +m[4], +m[5]);
      const fmt = { weekday: "long", day: "numeric", month: "long", hour: "2-digit", minute: "2-digit" };
      lines.push(el("p", { text: t("ui.sched.next", wall.toLocaleString(locale, fmt)) }));
      const off = /([+-])(\d\d):(\d\d)$/.exec(res.next);
      const serverOffset = off ? (off[1] === "-" ? -1 : 1) * (off[2] * 60 + +off[3]) : 0; // "Z" = UTC
      const at = new Date(res.next);
      if (serverOffset === -at.getTimezoneOffset()) lines.push(el("p", { class: "muted small", text: t("ui.sched.zone", res.zone) }));
      else lines.push(el("p", { class: "sched-paused small", text: t("ui.sched.zone_differs", res.zone, at.toLocaleString(locale, fmt)) }),
        el("p", { class: "muted small", text: t("ui.sched.zone_hint") }));
    }
    sched.summary.replaceChildren(...lines);
  }
  sched.on.addEventListener("change", () => {
    sched.body.hidden = !sched.on.checked;
    if (sched.on.checked && !schedList().length) simpleChanged(); // gleich mit dem Vorschlag
    else previewSchedule(0);
  });
  sched.days.addEventListener("change", simpleChanged);
  sched.time.addEventListener("change", simpleChanged);
  form.schedule_paused.addEventListener("change", () => previewSchedule(0));
  form.schedule.addEventListener("input", () => previewSchedule(400));
  form.schedule.addEventListener("change", () => previewSchedule(0)); // nach dem Tippen die Schalter angleichen

  // Vorhandene Gruppen zum Antippen unter dem Gruppenfeld (eine Datenliste ist auf dem Handy umständlich).
  function renderGroupChips() {
    const box = $("#group-chips");
    const cur = form.group.value.trim().toLowerCase();
    box.replaceChildren(...state.groups.map((g) => el("button", {
      type: "button", class: "btn btn-secondary btn-sm chip", "aria-pressed": String(cur === g.name.toLowerCase()),
      onclick: () => { form.group.value = cur === g.name.toLowerCase() ? "" : g.name; renderGroupChips(); },
    }, g.name)));
    box.hidden = !state.groups.length;
  }
  form.group.addEventListener("input", renderGroupChips);

  // Umbenennen: neuen Wecklink zeigen – der alte leitet weiter (der Server merkt sich frühere Namen).
  function renderNameHint() {
    const d = state.editing, name = form.name.value.trim();
    const renamed = d && name && name.toLowerCase() !== d.name.toLowerCase();
    $("#name-hint").textContent = renamed
      ? t("ui.rename_hint", wakeURL(Object.assign({}, d, { wake_path: "wake/" + encodeURIComponent(name) })))
      : t("page.f.name_hint");
  }
  form.name.addEventListener("input", renderNameHint);

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
    state.prefilled = !d && !!prefill;
    form.reset();
    clearFieldErrors();
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
    setSchedule(v.schedule || [], v.schedule_paused);
    renderNameHint();
    fillGroupList();
    renderGroupChips();
    form.ports.placeholder = state.info.ports && state.info.ports.length ? t("ui.default_ports", state.info.ports.slice(0, 5).join(", ") + (state.info.ports.length > 5 ? " …" : "")) : t("ui.default");
    const ic = form.querySelector('input[name="icon"][value="' + (v.icon || "desktop") + '"]');
    if (ic) ic.checked = true;
    $("#vendor-hint").textContent = v.vendor ? t("ui.vendor", vendorText(v.vendor)) : "";
    $("#mac-hint").hidden = !!(d || prefill); // von Hand: sagen, wo es die MAC gibt – und dass die Suche leichter ist
    form.broadcast.placeholder = state.info.default_broadcast ? t("ui.automatic_x", state.info.default_broadcast) : t("ui.automatic");
    $(".advanced").open = !!(v.broadcast || v.interface || v.note || v.secureon || (v.ports && v.ports.length));
    $("#delete-device").hidden = !d;
    $("#rotate-key").hidden = !(d && state.info.wake_keys);
    dlg.showModal();
    (v.name ? form.mac : form.name).focus();
    if (prefill && !prefill.name) form.name.focus();
  }

  // fieldError zeigt eine Meldung direkt am Feld, verknüpft sie per aria-describedby (der Screenreader
  // liest sie beim Feld vor) und setzt den Fokus dorthin; aufklappbare Bereiche gehen dafür auf.
  function fieldError(input, msg) {
    const box = input.closest(".field") || input.parentElement;
    let note = box.querySelector(".field-error");
    if (!note) {
      note = el("small", { class: "field-error", id: "err-" + (input.name || input.id) });
      box.append(note);
    }
    note.textContent = msg;
    note.hidden = false;
    input.setAttribute("aria-invalid", "true");
    const ids = (input.getAttribute("aria-describedby") || "").split(" ").filter(Boolean);
    if (!ids.includes(note.id)) input.setAttribute("aria-describedby", [note.id, ...ids].join(" "));
    for (let d = input.closest("details"); d; d = d.parentElement.closest("details")) d.open = true;
    input.focus();
  }
  function clearFieldErrors() {
    $("#form-error").hidden = true;
    for (const note of form.querySelectorAll(".field-error")) {
      note.hidden = true;
      for (const i of form.querySelectorAll('[aria-describedby~="' + note.id + '"]')) {
        const rest = i.getAttribute("aria-describedby").split(" ").filter((x) => x && x !== note.id);
        if (rest.length) i.setAttribute("aria-describedby", rest.join(" "));
        else i.removeAttribute("aria-describedby");
      }
    }
    for (const i of form.querySelectorAll("[aria-invalid]")) i.removeAttribute("aria-invalid");
  }
  // formError zeigt einen Fehler des Servers: am betroffenen Feld, wenn er es nennt ("field"), sonst
  // über dem Formular (role=alert).
  function formError(e) {
    const field = e.data && e.data.field && form.elements[e.data.field];
    if (field && field.type !== "hidden") {
      fieldError(field, e.message);
      return;
    }
    const err = $("#form-error");
    err.textContent = e.message;
    err.hidden = false;
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
      schedule: sched.on.checked ? schedList() : [], // ausgeschaltet: Zeitplan entfernen
      schedule_paused: sched.on.checked && form.schedule_paused.checked,
    };
    clearFieldErrors();
    if (!body.name.trim()) return fieldError(form.name, t("ui.err.name_missing"));
    if (!body.mac.trim()) return fieldError(form.mac, t("ui.err.mac_missing"));
    if (body.ports.some((p) => !Number.isInteger(p))) return fieldError(form.ports, t("ui.ports_numbers"));
    const btn = $("#save-device");
    btn.disabled = true;
    const send = (extra) => state.editing
      ? api("PUT", "devices/" + encodeURIComponent(state.editing.id), Object.assign({}, body, { updated_at: state.editing.updated_at }, extra))
      : api("POST", "devices", Object.assign({}, body, extra));
    try {
      let saved;
      try {
        saved = await send({});
      } catch (e) {
        // Der Name gehörte früher einem anderen Gerät: Dessen alte Wecklinks wecken danach dieses –
        // nur nach ausdrücklicher Bestätigung.
        if (!(e.data && e.data.confirm === "take_over_name")) throw e;
        if (!(await confirmDialog(t("ui.take_name.title", body.name.trim()), e.message, t("ui.take_name.ok")))) {
          fieldError(form.name, e.message);
          return;
        }
        saved = await send({ take_over_name: true });
      }
      dlg.close();
      toast("ok", state.editing ? t("ui.saved") : t("ui.device_added"), saved.name + " · " + saved.mac);
      const created = !state.editing;
      if (created) {
        state.fresh = { id: saved.id, adopted: state.prefilled };
        if (state.prefilled) { state.naming = null; closeDrawer(); } // aus der Suche übernommen: Leiste zu
      }
      await loadDevices();
      if (created) focusCard(saved.id);
      if (drawerOpen()) loadDiscovery();
    } catch (e) {
      formError(e);
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

  // Solange die Leiste offen ist, ist die Seite dahinter für Tastatur und Screenreader gesperrt –
  // sonst führte Tab aus dem Dialog heraus.
  const behindDrawer = () => [$(".topbar"), $("main.container"), $(".skip-link")];
  let drawerOpener = null; // dorthin kehrt der Fokus beim Schließen zurück
  function openDrawer() {
    if (!drawerOpen()) drawerOpener = document.activeElement;
    drawer.classList.add("open");
    drawer.setAttribute("aria-hidden", "false");
    for (const n of behindDrawer()) n.inert = true;
    loadDiscovery();
    setTimeout(() => $("#scan-btn").focus(), 50);
  }
  function closeDrawer() {
    // Fokus zurück auf den Knopf, sonst landet er im unsichtbar werdenden Bereich.
    for (const n of behindDrawer()) n.inert = false;
    const a = document.activeElement;
    if (drawer.contains(a) || a === document.body) {
      (drawerOpener && drawerOpener.isConnected && drawerOpener !== document.body ? drawerOpener : $("#open-discovery")).focus();
    }
    drawer.classList.remove("open");
    drawer.setAttribute("aria-hidden", "true");
  }
  for (const b of drawer.querySelectorAll("[data-close]")) b.addEventListener("click", closeDrawer);
  document.addEventListener("keydown", (e) => { if (e.key === "Escape" && drawerOpen() && !dlg.open) closeDrawer(); });

  async function chooseNetwork(cidrs) {
    try {
      await api("POST", "discovery/network", { cidrs });
    } catch (e) {
      toast("err", t("ui.scan.impossible"), e.message);
      return;
    }
    await reloadInfo();
    $("#scan-btn").focus();
    try { await api("POST", "discovery/scan"); } catch (e) { /* Anzeige folgt über loadDiscovery */ }
    loadDiscovery();
  }

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
    const opener = $("#open-discovery"); // der Name nennt die Zahl mit, die Plakette allein hört man nicht
    opener.setAttribute("aria-label", counts.new ? t("ui.find_devices_new", counts.new) : opener.dataset.label);

    for (const b of $("#found-filter").children) {
      b.replaceChildren(b.dataset.label || (b.dataset.label = b.textContent), el("span", { class: "count", text: counts[b.dataset.filter] }));
    }

    const nets = state.info.networks || [];
    const choices = state.info.network_choices || [];
    $("#discovery-networks").textContent = !nets.length ? ""
      : state.info.networks_auto ? t("ui.scan.segment_auto", nets[0]) : t("ui.scan.segments", nets.join(", "));
    const btn = $("#scan-btn");
    const prog = $("#scan-progress");
    const pick = $("#net-choices");
    pick.hidden = nets.length || !choices.length;
    if (!nets.length && choices.length) {
      // Mehrere mögliche Heimnetze: eins antippen, gespeichert wird es in den Einstellungen.
      $("#scan-title").textContent = t("ui.scan.setup_title");
      $("#scan-detail").textContent = t("ui.scan.setup_text");
      btn.disabled = true;
      pick.replaceChildren(...choices.map((c) => el("button", { type: "button", class: "btn btn-secondary", onclick: () => chooseNetwork([c.prefix]) },
        icon("radar"), el("span", { class: "mono", text: c.prefix }), el("span", { class: "muted", text: c.interface + " · " + c.addr }))),
        el("button", { type: "button", class: "btn btn-primary", onclick: () => chooseNetwork(choices.map((c) => c.prefix)) },
          icon("radar"), el("span", { text: t("ui.scan.choose_all", choices.length) })));
    } else if (!nets.length && state.info.scan_off) {
      $("#scan-title").textContent = t("ui.scan.off_title");
      $("#scan-detail").textContent = t("ui.scan.off_text");
      btn.disabled = true;
    } else if (!nets.length) {
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
    const ro = !editable();
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
      state.naming === f.mac && kind === "new" ? nameForm(f) : el("div", { class: "found-actions" }, actions));
  }

  // Name aus dem Hostnamen ableiten ("nas.fritz.box" → "nas"); ist er schon vergeben, mit der
  // letzten Stelle der IP-Adresse.
  function suggestName(f) {
    let name = (f.hostname || "").split(".")[0];
    if (name && state.devices.some((d) => d.name.toLowerCase() === name.toLowerCase())) name += "-" + f.ip.split(".").pop();
    return name;
  }
  // Vorschlag, wenn der Treffer keinen Hostnamen hat: Hersteller oder "Gerät" plus letzte Stelle der IP.
  function fallbackName(f) {
    const vendor = f.vendor && f.vendor !== "Private/zufällige MAC" ? f.vendor.split(/[\s,]+/)[0] : t("ui.found.device");
    return vendor + "-" + f.ip.split(".").pop();
  }

  // adopt übernimmt einen Treffer direkt – ohne Dialog, wenn sich der Name ableiten lässt; sonst wird
  // nur der Name gefragt. Danach schließt die Suchleiste und die neue Karte bekommt den Fokus.
  async function adopt(f, name) {
    name = name === undefined ? suggestName(f) : name;
    if (!name) {
      state.naming = f.mac;
      state.namingValue = fallbackName(f);
      renderDiscovery();
      const input = $("#found-list [data-focus='name']");
      if (input) { input.focus(); input.select(); }
      return;
    }
    const body = { name, mac: f.mac, ip: f.ip, icon: guessIcon(f) };
    let d;
    try {
      d = await api("POST", "devices", body);
    } catch (e) {
      // z. B. Name schon vergeben: im Dialog nachbessern, mit allen Angaben und der Meldung
      openDialog(null, Object.assign(body, { vendor: f.vendor }));
      formError(e);
      return;
    }
    state.naming = null;
    state.fresh = { id: d.id, adopted: true };
    closeDrawer();
    toast("ok", t("ui.adopted", d.name), d.ip + " · " + d.mac);
    await loadDevices();
    focusCard(d.id);
    loadDiscovery();
  }

  // Nur der Name fehlt: kleines Formular direkt im Treffer.
  function nameForm(f) {
    const err = el("p", { class: "form-error small", id: "found-name-err", hidden: true });
    const input = el("input", { maxlength: "64", autocomplete: "off", "data-focus": "name", value: state.namingValue,
      "aria-describedby": "found-name-err", oninput: (ev) => { state.namingValue = ev.target.value; } });
    return el("form", { class: "found-name", novalidate: true, onsubmit: (ev) => {
      ev.preventDefault();
      if (!input.value.trim()) {
        err.textContent = t("ui.err.name_missing");
        err.hidden = false;
        input.setAttribute("aria-invalid", "true");
        input.focus();
        return;
      }
      adopt(f, input.value.trim());
    } },
      el("label", { class: "field" }, el("span", { text: t("ui.found.name_needed") }), input),
      err,
      el("div", { class: "found-actions" },
        el("button", { type: "button", class: "btn btn-ghost btn-sm", "data-focus": "cancel-name", onclick: () => { state.naming = null; renderDiscovery(); } }, t("ui.cancel")),
        el("button", { type: "submit", class: "btn btn-primary btn-sm", "data-focus": "save-name" }, icon("plus"), el("span", { text: t("ui.found.adopt") }))));
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
  // outcome: ein Satz je Eintrag – ob der Weckruf rausging und ob der Start bestätigt ist.
  function outcome(ev) {
    if (!ev.success) return { kind: "err", icon: "x", text: t("ui.hist.failed") };
    if (ev.started) return { kind: "ok", icon: "check", text: t("ui.hist.started", fmtDuration(new Date(ev.started) - new Date(ev.time))) };
    if (ev.was_online) return { kind: "ok", icon: "check", text: t("ui.hist.running") };
    if (!ev.checkable) return { kind: "sent", icon: SOURCE_ICON[ev.source] || "send", text: t("ui.hist.sent") };
    // Die Wiederholung kommt wake_retry nach dem Weckruf, die Wartezeit läuft aber ab dem Weckruf.
    const wait = (state.info.wake_timeout - (ev.retry ? state.info.wake_retry || 0 : 0)) * 1000;
    if (Date.now() - new Date(ev.time) < wait) return { kind: "sent", icon: "send", text: t("ui.hist.waiting") };
    return { kind: "warn", icon: "alert", text: t("ui.hist.unconfirmed") };
  }
  async function openHistory() {
    const d = $("#history-dialog");
    let events = [];
    try { events = (await api("GET", "history?limit=200")).events || []; } catch (e) { toast("err", t("ui.history_unavailable"), e.message); return; }
    const fmt = new Intl.DateTimeFormat(locale, { day: "2-digit", month: "2-digit", hour: "2-digit", minute: "2-digit" });
    $("#history-list").replaceChildren(...events.map((ev) => {
      const r = outcome(ev);
      const details = [
        t("ui.hist.source", sourceText(ev)),
        ev.retry && t("ui.retry_event"),
        ev.user && t("ui.hist.user", ev.user),
        ev.client && t("ui.hist.client", ev.client),
        ev.target && t("ui.hist.target", ev.target),
        ev.success && !ev.checkable && t("ui.hist.no_ip"),
        ev.error && t("ui.hist.error", ev.error),
      ].filter(Boolean);
      return el("li", { class: r.kind },
        icon(r.icon),
        el("div", { class: "h-main" },
          el("strong", { text: ev.device }),
          el("p", { class: "h-result", text: r.text + (ev.retry ? " " + t("ui.hist.retried") : "") }),
          el("details", { class: "h-details" }, el("summary", { text: t("ui.hist.details") }),
            el("ul", {}, details.map((x) => el("li", { text: x }))))),
        el("time", { datetime: ev.time, title: new Date(ev.time).toLocaleString(locale), text: fmt.format(new Date(ev.time)) }));
    }));
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
      showImport(res);
      loadDevices();
    } catch (err) {
      toast("err", t("ui.import.failed"), err.message);
    }
  });

  // showImport zeigt das Ergebnis dauerhaft (bis zum Schließen): eine Zusammenfassung und je Ergebnis
  // die vollständige Liste der Einträge – nicht nur die ersten Fehler.
  const importDlg = $("#import-dialog");
  function showImport(res) {
    const items = res.items || [];
    const of = (r) => items.filter((it) => it.result === r);
    const added = of("added"), skipped = of("skipped"), failed = of("error");
    const sum = [tn("ui.import.sum_added", added.length)];
    if (skipped.length) sum.push(tn("ui.import.sum_skipped", skipped.length));
    if (failed.length) sum.push(tn("ui.import.sum_failed", failed.length));
    $("#import-summary").textContent = sum.join(", ") + "." + (res.secret_restored ? " " + t("ui.import.secret") + "." : "");
    const section = (list, kind, head, line) => list.length ? el("section", { class: "import-part " + kind },
      el("h3", {}, icon(kind === "ok" ? "check" : kind === "warn" ? "x" : "alert"), el("span", { text: head })),
      el("ul", {}, list.map((it) => el("li", { text: line(it) })))) : null;
    $("#import-lists").replaceChildren(...[
      section(added, "ok", t("ui.import.h_added"), (it) => it.new_name ? t("ui.import.renamed", it.name, it.new_name) : it.name),
      section(skipped, "info", t("ui.import.h_skipped"), (it) => it.detail && it.detail !== it.name ? t("ui.import.same_mac", it.name, it.detail) : it.name),
      section(failed, "warn", t("ui.import.h_failed"), (it) => it.name ? it.name + ": " + it.detail : it.detail),
    ].filter(Boolean));
    importDlg.showModal();
  }
  for (const b of importDlg.querySelectorAll("[data-close]")) b.addEventListener("click", () => importDlg.close());

  // ---------- Filter, Suche, Theme ----------
  function segmented(box, key, onChange) {
    box.addEventListener("click", (e) => {
      const b = e.target.closest("button");
      if (!b) return;
      for (const x of box.children) x.setAttribute("aria-pressed", x === b ? "true" : "false");
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

  // Einfache Ansicht: große Weckknöpfe, nichts zum Bearbeiten. theme.js setzt die Klasse schon vor
  // dem ersten Zeichnen (gespeicherte Wahl oder ?view=simple / ?view=normal im Link).
  state.simple = document.documentElement.classList.contains("simple");
  function updateViewToggle() { $("#view-toggle").setAttribute("aria-pressed", state.simple ? "true" : "false"); }
  $("#view-toggle").addEventListener("click", () => {
    state.simple = !state.simple;
    document.documentElement.classList.toggle("simple", state.simple);
    try { localStorage.setItem("wolweb-view", state.simple ? "simple" : "normal"); } catch (e) { /* egal */ }
    updateViewToggle();
    renderDevices();
  });
  updateViewToggle();
  // Sprunglink: "#devices" löst sich wegen <base> gegen die Basisadresse auf und lüde die Seite
  // neu (ohne ?view=…) – daher selbst springen.
  $(".skip-link").addEventListener("click", (e) => {
    e.preventDefault();
    $("#devices").focus();
  });

  // Sprache: Wahl im Cookie (gilt auch für Meldungen des Servers), dann neu laden.
  $("#lang-toggle").addEventListener("click", (e) => {
    const next = e.currentTarget.dataset.lang;
    document.cookie = "wolweb_lang=" + next + "; path=" + new URL(document.baseURI).pathname + "; max-age=31536000; SameSite=Lax";
    // ?lang= in der Adresse ginge dem Cookie vor – beim bloßen Neuladen bliebe die alte Sprache.
    const url = new URL(location.href);
    url.searchParams.delete("lang");
    location.replace(url.href);
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
    es.addEventListener("settings", () => { reloadInfo(); loadDevicesSoon(); });
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

  // applyInfo übernimmt, was von den Einstellungen abhängt (auch nach einer Änderung im Betrieb).
  function applyInfo() {
    $("#open-settings").hidden = !state.info.settings;
    applyLock();
    // Broadcast-Vorschläge im Gerätedialog: die eingetragenen Netze
    $("#broadcast-list").replaceChildren(...(state.info.segments || []).map((s) => el("option", { value: s.broadcast, label: s.label })));
  }
  async function reloadInfo() {
    try { state.info = await api("GET", "info"); } catch (e) { return; }
    applyInfo();
    renderDevices();
    renderDiscovery();
  }

  // ---------- Bearbeitungsschutz ----------
  // applyLock blendet aus, was gerade nicht geht (Nur-Lesen oder gesperrt), und zeigt das Schloss.
  function applyLock() {
    const off = !editable();
    $("#add-device").hidden = off;
    $("#import-btn").hidden = off;
    $("#export-link").hidden = !!state.info.locked; // mit Weckschlüsseln – im Nur-Lesen-Betrieb weiter erlaubt
    for (const b of document.querySelectorAll('[data-action="add"]')) b.hidden = off;
    const btn = $("#lock-toggle");
    btn.hidden = !state.info.password || state.info.read_only;
    window.wolwebLock.button(btn, !!state.info.locked);
  }
  function setLocked(on) {
    if (!!state.info.locked === on) return;
    state.info.locked = on;
    if (on && state.selecting) setSelecting(false);
    if (on && dlg.open) dlg.close();
    applyLock();
    renderDevices();
    renderDiscovery();
  }
  $("#lock-toggle").addEventListener("click", async () => {
    if (state.info.locked) {
      if (!(await window.wolwebLock.unlock())) return;
      setLocked(false);
      toast("ok", t("ui.lock.unlocked"), t("ui.lock.unlocked_text"));
    } else {
      try { await window.wolwebLock.lock(); } catch (e) { toast("err", t("ui.lock.failed"), e.message); return; }
      setLocked(true);
      toast("ok", t("ui.lock.locked"), t("ui.lock.locked_text"));
    }
    $("#lock-toggle").focus();
  });

  // ---------- Start ----------
  async function init() {
    buildIconPicker();
    buildDayPicker();
    updateThemeIcon();
    try {
      state.info = await api("GET", "info");
    } catch (e) { /* Standardwerte */ }
    if (state.info.version) $("#version").textContent = "wolweb " + state.info.version;
    applyInfo();
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
