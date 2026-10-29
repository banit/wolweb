// Weckseite: prüft das Gerät, sendet den Weckruf (Magic Packet) und verfolgt den Start bis "online".
// Die Schritte sprechen Alltagssprache; IP, MAC, Ziel und Ping/Port/ms stehen unter „Technische Details“.
(function () {
  "use strict";
  const { t } = window.wolwebI18n;
  const card = document.getElementById("wake");
  const name = card.dataset.name;
  const path = card.dataset.path; // "wake/<Name>", relativ zu <base>
  const query = card.dataset.key ? "?key=" + encodeURIComponent(card.dataset.key) : ""; // Weckschlüssel
  const hasIP = card.dataset.hasIp === "1";
  // Privatsphäre: Online-Status ausgeblendet – die Seite sendet nur und verfolgt den Start nicht.
  const hidden = card.dataset.statusHidden === "1";
  const timeout = (parseInt(card.dataset.timeout, 10) || 180) * 1000;
  const retry = (parseInt(card.dataset.retry, 10) || 0) * 1000; // dann sendet der Server erneut
  const title = document.getElementById("wake-title");
  const result = document.getElementById("wake-result");
  const again = document.getElementById("wake-again");
  const recheckBtn = document.getElementById("wake-check");
  const note = document.getElementById("wake-note"); // wie lange geprüft wird
  const live = document.getElementById("wake-live"); // sagt Screenreadern die Phasen an
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
  // duration: „3 Minuten“, „90 Sekunden“ – für die Ansage, wie lange geprüft wird.
  function duration(sec) {
    return sec >= 60 && sec % 60 === 0 ? window.wolwebI18n.tn("wake.minutes", sec / 60) : window.wolwebI18n.tn("wake.seconds", sec);
  }
  function human(ms) {
    const s = Math.round(ms / 1000);
    if (s < 60) return window.wolwebI18n.tn("wake.seconds", s);
    const m = Math.floor(s / 60), r = s % 60;
    return m + " min" + (r ? " " + r + " s" : "");
  }
  // tech trägt die technische Antwort (Ping/Port, Millisekunden) in „Technische Details“ ein.
  function tech(st) {
    const answer = document.getElementById("tech-answer");
    if (answer) answer.textContent = via(st) || "–";
  }
  function via(st) {
    if (!st || !st.method) return "";
    const port = /^tcp\/(\d+)( \((?:refused|abgelehnt)\))?/.exec(st.method);
    const m = st.method === "icmp" ? "Ping" : port ? t(port[2] ? "wake.port_closed" : "ui.port", port[1]) : st.method;
    return m + (st.rtt_ms ? " · " + st.rtt_ms + " ms" : "");
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
    if (tips) { // technische Tipps aufklappbar unter „Hilfe“
      const box = document.createElement("details");
      box.className = "help";
      const sum = document.createElement("summary");
      sum.textContent = t("wake.help");
      const ul = document.createElement("ul");
      for (const tip of tips) {
        const li = document.createElement("li");
        li.textContent = tip;
        ul.append(li);
      }
      box.append(sum, ul);
      result.append(box);
    }
    result.hidden = false;
  }
  // announce sagt einen Phasenwechsel einmal an – ohne Zeitangaben, die sich ständig ändern.
  let lastLive = "";
  function announce(text) {
    if (text && text !== lastLive) live.textContent = lastLive = text;
  }
  function setTitle(text, tab) {
    title.textContent = text;
    announce(text);
    document.title = (tab || text) + " · Wake on LAN";
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
        throw Object.assign(new Error((body && body.message) || t("ui.unexpected_response", res.status)), { body: body || {} });
      }
      return body;
    } finally {
      clearTimeout(timer);
    }
  }
  const status = () => call("GET", path + "/status" + query);

  let running = false;
  async function run(force) {
    if (running) return;
    running = true;
    again.hidden = true;
    recheckBtn.hidden = true;
    result.hidden = true;
    for (const id of ["check", "send", "wait", "online"]) step(id, "", "");
    setTitle(t("wake.title.starting", name), t("wake.tab.start", name));

    // 1. Prüfen, ob das Gerät schon läuft
    setState("check");
    step("check", "active", hasIP ? t("wake.check.active") : "");
    const t0 = Date.now();
    if (hasIP && !force) {
      let st = null;
      try { st = await status(); } catch (e) { /* weiter mit Senden */ }
      await sleep(Math.max(0, 500 - (Date.now() - t0)));
      if (st && st.state === "online") {
        step("check", "done", t("wake.check.running"));
        step("send", "skip", t("wake.send.not_needed"));
        step("wait", "skip", "");
        step("online", "done", t("wake.online.reachable"));
        tech(st);
        setState("online");
        setTitle(t("wake.title.running", name), "✓ " + name);
        showResult("ok", t("wake.result.ready"), t("wake.result.ready_text", name));
        again.querySelector("span").textContent = t("wake.btn.send_anyway");
        again.hidden = false;
        running = false;
        return;
      }
      step("check", "done", t("wake.check.off"));
    } else {
      await sleep(350);
      step("check", "skip", hidden ? t("wake.check.hidden") : hasIP ? t("wake.check.skipped") : t("wake.check.no_ip"));
    }

    // 2. Weckruf senden
    setState("send");
    announce(t("wake.live.sending"));
    step("send", "active", t("wake.send.active"));
    let sent;
    try {
      sent = await call("POST", path + query);
      if (sent.success !== true) throw Object.assign(new Error(sent.message || t("wake.send.failed")), { body: sent });
      await sleep(450);
    } catch (e) {
      step("send", "error", (e.body && e.body.error) || e.message);
      setState("error");
      setTitle(t("wake.send.failed"), "✗ " + name);
      showResult("err", t("wake.result.send_failed"), (e.body && e.body.message) || e.message);
      again.querySelector("span").textContent = t("wake.btn.retry");
      again.hidden = false;
      running = false;
      return;
    }
    // Eben erst geweckt (zweimal getippt, Seite neu geladen): kein neuer Weckruf, der Start läuft weiter.
    step("send", "done", sent.already ? t("wake.send.already") : t("wake.send.done"));
    if (sent.target) document.getElementById("tech-target").textContent = sent.target;

    if (!hasIP) {
      step("wait", "skip", hidden ? t("wake.wait.hidden") : t("wake.wait.no_ip"));
      step("online", "skip", "");
      setState("sent"); // gesendet, aber nicht bestätigt – kein grüner Haken
      setTitle(t("wake.title.sent"), "→ " + name);
      showResult("sent", t("wake.result.sent"), hidden ? t("wake.result.sent_hidden", name) : t("wake.result.sent_text", name));
      again.querySelector("span").textContent = t("wake.btn.again");
      again.hidden = false;
      running = false;
      return;
    }

    // 3. Warten, bis das Gerät antwortet
    setState("wait");
    announce(sent.already ? t("wake.send.already") : t("wake.live.waiting"));
    note.textContent = t(retry ? "wake.wait.note_retry" : "wake.wait.note", duration(timeout / 1000));
    note.hidden = false;
    const start = Date.now();
    step("wait", "active", t("wake.wait.active", "0:00"));
    const tick = setInterval(() => {
      const ms = Date.now() - start;
      step("wait", "active", t(retry && ms >= retry ? "wake.wait.resent" : "wake.wait.active", fmt(ms)));
      if (retry && ms >= retry) announce(t("wake.live.resent")); // einmal: die Wiederholung, nicht die Uhr
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
    note.hidden = true;
    const took = Date.now() - start;

    if (online) {
      step("wait", "done", t("ui.started_after", fmt(took)));
      step("online", "done", t("wake.online.reachable"));
      tech(online);
      setState("online");
      setTitle(t("wake.title.confirmed", name), "✓ " + name);
      showResult("ok", t("wake.result.started"), t("wake.result.started_text", name, human(took)));
    } else {
      step("wait", "warn", t("wake.wait.no_answer", fmt(took)));
      step("online", "", "");
      setState("timeout");
      setTitle(t("wake.title.no_answer"), "! " + name);
      // Bleibt stehen, bis der Nutzer etwas tut: erst das Naheliegende, Technisches unter „Hilfe“.
      showResult("warn", t("wake.result.timeout"), t("wake.result.timeout_text"), [t("wake.tip.bios"), t("wake.tip.windows"), t("wake.tip.power"), t("wake.tip.firewall")]);
      again.querySelector("span").textContent = t("wake.btn.wake_again");
      again.hidden = false;
      recheckBtn.hidden = false;
    }
    running = false;
  }

  // recheck prüft nach Ablauf noch einmal, ohne zu wecken – der Start kann länger gedauert haben.
  async function recheck() {
    if (running) return;
    running = true;
    recheckBtn.disabled = true;
    let st = null;
    try { st = await status(); } catch (e) { /* zählt als keine Antwort */ }
    recheckBtn.disabled = false;
    running = false;
    if (st && st.state === "online") {
      step("wait", "done", "");
      step("online", "done", t("wake.online.reachable"));
      tech(st);
      setState("online");
      setTitle(t("wake.title.confirmed", name), "✓ " + name);
      showResult("ok", t("wake.result.started"), t("wake.result.late_text", name));
      recheckBtn.hidden = true;
      again.querySelector("span").textContent = t("wake.btn.send_anyway");
      return;
    }
    const clock = new Date().toLocaleTimeString(window.wolwebI18n.locale, { hour: "2-digit", minute: "2-digit" });
    let p = result.querySelector("[data-still]");
    if (!p) {
      p = document.createElement("p");
      p.dataset.still = "";
      result.querySelector("strong").after(p);
    }
    p.textContent = t("wake.result.still_no_answer", clock);
    live.textContent = lastLive = p.textContent; // auch beim zweiten Mal ansagen
  }

  again.addEventListener("click", () => run(true));
  recheckBtn.addEventListener("click", recheck);
  run(false);
})();
