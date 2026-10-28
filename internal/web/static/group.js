// Gruppenseite: weckt alle Geräte einer Gruppe und verfolgt, bis jedes mit IP online ist.
(function () {
  "use strict";
  const card = document.getElementById("group");
  const name = card.dataset.name;
  const path = card.dataset.path; // "wake-group/<Name>", relativ zu <base>
  const query = card.dataset.key ? "?key=" + encodeURIComponent(card.dataset.key) : "";
  const timeout = (parseInt(card.dataset.timeout, 10) || 180) * 1000;
  const title = document.getElementById("group-title");
  const sub = document.getElementById("group-sub");
  const result = document.getElementById("group-result");
  const again = document.getElementById("group-again");
  const items = [...document.querySelectorAll("#members li")];
  const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

  function mark(li, s, pill, detail) {
    li.dataset.s = s;
    const p = li.querySelector("[data-pill]");
    p.className = "pill " + ({ online: "online", sent: "waking", error: "warn", skip: "unknown" }[s] || "offline");
    p.textContent = pill;
    if (detail !== undefined) li.querySelector("[data-detail]").textContent = detail;
  }
  function fmt(ms) {
    const s = Math.floor(ms / 1000);
    return Math.floor(s / 60) + ":" + String(s % 60).padStart(2, "0");
  }
  function show(kind, head, text) {
    result.className = "wake-result " + kind;
    result.replaceChildren();
    const s = document.createElement("strong");
    s.textContent = head;
    result.append(s);
    if (text) {
      const p = document.createElement("p");
      p.textContent = text;
      result.append(p);
    }
    result.hidden = false;
  }
  async function call(method, url) {
    const ctl = new AbortController();
    const timer = setTimeout(() => ctl.abort(), 10000);
    try {
      const res = await fetch(url, { method, signal: ctl.signal, cache: "no-store", headers: { "Accept": "application/json", "X-WolWeb": "1" } });
      let body = null;
      try { body = await res.json(); } catch (e) { /* keine JSON-Antwort */ }
      if (!res.ok && !(body && body.results)) throw new Error((body && body.message) || "HTTP " + res.status);
      return body;
    } finally {
      clearTimeout(timer);
    }
  }

  let running = false;
  async function run() {
    if (running) return;
    running = true;
    again.hidden = true;
    result.hidden = true;
    card.dataset.state = "send";
    title.textContent = "Gruppe " + name + " wird gestartet";
    let sent;
    try {
      sent = await call("POST", path + query);
    } catch (e) {
      card.dataset.state = "error";
      title.textContent = "Senden fehlgeschlagen";
      show("err", "Die Magic Packets konnten nicht gesendet werden", e.message);
      again.hidden = false;
      running = false;
      return;
    }
    const byID = new Map((sent.results || []).map((r) => [r.id, r]));
    for (const li of items) {
      const r = byID.get(li.dataset.id);
      if (!r || !r.success) mark(li, "error", "Fehler", (r && r.error) || "nicht gesendet");
      else if (li.dataset.hasIp) mark(li, "sent", "startet …");
      else mark(li, "skip", "gesendet");
    }
    const watch = items.filter((li) => li.dataset.s === "sent");
    card.dataset.state = "wait";
    const start = Date.now();
    while (watch.some((li) => li.dataset.s !== "online") && Date.now() - start < timeout) {
      sub.textContent = watch.filter((li) => li.dataset.s === "online").length + " von " + watch.length + " online · " + fmt(Date.now() - start);
      await sleep(2000);
      try {
        const st = await call("GET", path + "/status" + query);
        for (const m of st.members || []) {
          const li = items.find((x) => x.dataset.id === m.id);
          if (li && li.dataset.s === "sent" && m.status && m.status.state === "online") mark(li, "online", "online", "nach " + fmt(Date.now() - start));
        }
      } catch (e) { /* kurz nicht erreichbar – weiter */ }
    }
    const online = watch.filter((li) => li.dataset.s === "online").length;
    const failed = items.filter((li) => li.dataset.s === "error").length;
    sub.textContent = online + " von " + watch.length + " online";
    if (!failed && online === watch.length) {
      card.dataset.state = watch.length ? "online" : "sent";
      title.textContent = watch.length ? "Gruppe " + name + " ist online" : "Magic Packets gesendet";
      show("ok", watch.length ? "Alle Geräte sind erreichbar" : "Weckrufe sind raus", watch.length ? "" : "Ohne IP-Adressen lässt sich der Start nicht prüfen.");
    } else {
      card.dataset.state = "timeout";
      title.textContent = "Nicht alle Geräte haben sich gemeldet";
      for (const li of watch) if (li.dataset.s === "sent") mark(li, "sent", "keine Antwort");
      show("warn", online + " von " + watch.length + " Geräten online" + (failed ? ", " + failed + " Fehler" : ""), "Prüfe Wake on LAN im BIOS/UEFI und ob die Geräte am Strom hängen.");
    }
    again.hidden = false;
    running = false;
  }
  again.addEventListener("click", run);
  run();
})();
