// Weckseite: prüft das Gerät, sendet das Magic Packet und verfolgt den Start bis "online".
(function () {
  "use strict";
  const card = document.getElementById("wake");
  const name = card.dataset.name;
  const path = card.dataset.path; // "wake/<Name>", relativ zu <base>
  const hasIP = card.dataset.hasIp === "1";
  const timeout = (parseInt(card.dataset.timeout, 10) || 180) * 1000;
  const title = document.getElementById("wake-title");
  const result = document.getElementById("wake-result");
  const again = document.getElementById("wake-again");
  const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

  function step(id, state, detail) {
    const li = document.querySelector('[data-step="' + id + '"]');
    li.dataset.s = state;
    if (detail !== undefined) li.querySelector("[data-detail]").textContent = detail;
  }
  function setState(s) { card.dataset.state = s; }
  function fmt(ms) {
    const s = Math.floor(ms / 1000);
    return Math.floor(s / 60) + ":" + String(s % 60).padStart(2, "0");
  }
  function human(ms) {
    const s = Math.round(ms / 1000);
    if (s < 60) return s + (s === 1 ? " Sekunde" : " Sekunden");
    const m = Math.floor(s / 60), r = s % 60;
    return m + " min" + (r ? " " + r + " s" : "");
  }
  function via(st) {
    if (!st || !st.method) return "";
    const m = st.method === "icmp" ? "Ping" : st.method.replace("tcp/", "Port ").replace("(abgelehnt)", "(geschlossen)");
    return "antwortet per " + m + (st.rtt_ms ? " · " + st.rtt_ms + " ms" : "");
  }
  function showResult(kind, head, text, tips) {
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
    if (tips) {
      const ul = document.createElement("ul");
      for (const t of tips) {
        const li = document.createElement("li");
        li.textContent = t;
        ul.append(li);
      }
      result.append(ul);
    }
    result.hidden = false;
  }
  function setTitle(t, tab) {
    title.textContent = t;
    document.title = (tab || t) + " · Wake on LAN";
  }

  async function call(method, url) {
    const ctl = new AbortController();
    const timer = setTimeout(() => ctl.abort(), 8000);
    try {
      const res = await fetch(url, {
        method, signal: ctl.signal, cache: "no-store",
        headers: { "Accept": "application/json", "X-WolWeb": "1" },
      });
      let body = null;
      try { body = await res.json(); } catch (e) { /* keine JSON-Antwort */ }
      if (!res.ok || !body || res.redirected) {
        throw Object.assign(new Error((body && body.message) || "Unerwartete Antwort vom Server (HTTP " + res.status + ")"), { body: body || {} });
      }
      return body;
    } finally {
      clearTimeout(timer);
    }
  }
  const status = () => call("GET", path + "/status");

  let running = false;
  async function run(force) {
    if (running) return;
    running = true;
    again.hidden = true;
    result.hidden = true;
    for (const id of ["check", "send", "wait", "online"]) step(id, "", "");
    setTitle(name + " wird gestartet", name + " starten");

    // 1. Prüfen, ob das Gerät schon läuft
    setState("check");
    step("check", "active", hasIP ? "Ist das Gerät schon an?" : "");
    const t0 = Date.now();
    if (hasIP && !force) {
      let st = null;
      try { st = await status(); } catch (e) { /* weiter mit Senden */ }
      await sleep(Math.max(0, 500 - (Date.now() - t0)));
      if (st && st.state === "online") {
        step("check", "done", "Läuft bereits – " + via(st));
        step("send", "skip", "Nicht nötig");
        step("wait", "skip", "");
        step("online", "done", via(st));
        setState("online");
        setTitle(name + " läuft bereits", "✓ " + name);
        showResult("ok", "Alles bereit", name + " ist bereits eingeschaltet und erreichbar.");
        again.querySelector("span").textContent = "Trotzdem senden";
        again.hidden = false;
        running = false;
        return;
      }
      step("check", "done", "Aus – wird geweckt");
    } else {
      await sleep(350);
      step("check", "skip", hasIP ? "Übersprungen" : "Keine IP hinterlegt – Online-Prüfung nicht möglich");
    }

    // 2. Magic Packet senden
    setState("send");
    step("send", "active", "Sende an " + card.dataset.target + " …");
    let sent;
    try {
      sent = await call("POST", path);
      if (sent.success !== true) throw Object.assign(new Error(sent.message || "Senden fehlgeschlagen"), { body: sent });
      await sleep(450);
    } catch (e) {
      step("send", "error", (e.body && e.body.error) || e.message);
      setState("error");
      setTitle("Senden fehlgeschlagen", "✗ " + name);
      showResult("err", "Das Magic Packet konnte nicht gesendet werden", (e.body && e.body.message) || e.message);
      again.querySelector("span").textContent = "Erneut versuchen";
      again.hidden = false;
      running = false;
      return;
    }
    step("send", "done", "Gesendet an " + (sent.target || card.dataset.target));

    if (!hasIP) {
      step("wait", "skip", "Ohne IP-Adresse nicht prüfbar");
      step("online", "skip", "");
      setState("sent");
      setTitle("Magic Packet gesendet", "✓ " + name);
      showResult("ok", "Weckruf ist raus",
        "Ob " + name + " startet, lässt sich ohne IP-Adresse nicht prüfen. Trage in der Übersicht eine IP ein, dann bestätigt diese Seite den Start.");
      again.querySelector("span").textContent = "Erneut senden";
      again.hidden = false;
      running = false;
      return;
    }

    // 3. Warten, bis das Gerät antwortet
    setState("wait");
    const start = Date.now();
    step("wait", "active", "0:00 · warte auf Antwort …");
    const tick = setInterval(() => {
      step("wait", "active", fmt(Date.now() - start) + " · warte auf Antwort …");
    }, 500);
    let online = null;
    while (Date.now() - start < timeout) {
      await sleep(2000);
      try {
        const st = await status();
        if (st.state === "online") { online = st; break; }
      } catch (e) { /* kurz nicht erreichbar – weiter versuchen */ }
    }
    clearInterval(tick);
    const took = Date.now() - start;

    if (online) {
      step("wait", "done", "Gestartet nach " + fmt(took));
      step("online", "done", via(online));
      setState("online");
      setTitle(name + " ist online", "✓ " + name);
      showResult("ok", "Erfolgreich gestartet", name + " hat nach " + human(took) + " geantwortet und ist jetzt erreichbar.");
    } else {
      step("wait", "warn", "Keine Antwort nach " + fmt(took));
      step("online", "", "");
      setState("timeout");
      setTitle("Keine Rückmeldung", "! " + name);
      showResult("warn", name + " hat sich nicht gemeldet", "Das Paket wurde gesendet, das Gerät antwortet aber noch nicht. Mögliche Ursachen:", [
        "Wake on LAN ist im BIOS/UEFI oder im Netzwerktreiber nicht aktiviert.",
        "Das Gerät war komplett stromlos oder hängt an einem anderen Netzsegment.",
        "Die Firewall blockiert Ping und alle geprüften Ports – dann läuft es evtl. trotzdem.",
      ]);
      again.querySelector("span").textContent = "Erneut senden";
      again.hidden = false;
    }
    running = false;
  }

  again.addEventListener("click", () => run(true));
  run(false);
})();
