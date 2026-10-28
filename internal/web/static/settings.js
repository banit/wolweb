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
  async function api(method, url, body) {
    const opts = { method, cache: "no-store", headers: { "Accept": "application/json", "X-WolWeb": "1" } };
    if (body !== undefined) {
      opts.headers["Content-Type"] = "application/json";
      opts.body = JSON.stringify(body);
    }
    const res = await fetch("api/v1/" + url, opts);
    let data = null;
    try { data = await res.json(); } catch (e) { /* keine JSON-Antwort */ }
    if (!res.ok) throw Object.assign(new Error((data && data.message) || t("ui.unexpected_response", res.status)), { data: data || {}, status: res.status });
    return data;
  }
  function confirmDialog(title, text, ok) {
    const d = $("#confirm-dialog");
    $("#confirm-title").textContent = title;
    $("#confirm-text").textContent = text;
    $("#confirm-ok").textContent = ok;
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
    renderEvents(s.notify.events || []);
    renderRejected(v.rejected_hosts || []);
    nets = (v.networks || []).map((n) => ({ cidr: n.cidr, name: n.name || "", vlan: n.vlan || "", scan: n.scan, orig: n }));
    renderNets();
    renderInterfaces();

    const src = v.file_found ? t("set.source.file", v.file) : t("set.source.defaults");
    $("#set-source").textContent = src + (v.stored.length ? " · " + tn("set.stored", v.stored.length) : "");
    $("#set-no-auth").hidden = v.auth;
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
    setDirty(false);
  }

  // Zuletzt abgewiesene Hostnamen (meist der eigene Reverse-Proxy): ein Klick erlaubt und speichert.
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
    form.requestSubmit();
  }

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
              el("input", { name: "net-cidr", value: n.cidr, class: "mono", autocomplete: "off", placeholder: "192.168.20.0/24", required: true, disabled: off, oninput: upd("cidr") })),
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

  // Sprunglink: "#settings-form" löst sich wegen <base> gegen die Basisadresse auf – selbst springen.
  $(".skip-link").addEventListener("click", (e) => {
    e.preventDefault();
    form.focus();
  });

  api("GET", "settings").then(render).catch((err) => {
    $("#set-source").textContent = t("set.load_failed");
    toast("err", t("set.load_failed"), err.message);
  });
})();
