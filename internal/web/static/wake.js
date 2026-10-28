// Weckseite: prüft das Gerät, sendet das Magic Packet und verfolgt den Start bis "online".
(function () {
  "use strict";
  const { t } = window.wolwebI18n;
  const card = document.getElementById("wake");
  const name = card.dataset.name;
  const path = card.dataset.path; // "wake/<Name>", relativ zu <base>
  const query = card.dataset.key ? "?key=" + encodeURIComponent(card.dataset.key) : ""; // Weckschlüssel
  const hasIP = card.dataset.hasIp === "1";
  const timeout = (parseInt(card.dataset.timeout, 10) || 180) * 1000;
  const retry = (parseInt(card.dataset.retry, 10) || 0) * 1000; // dann sendet der Server erneut
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
    if (s < 60) return window.wolwebI18n.tn("wake.seconds", s);
    const m = Math.floor(s / 60), r = s % 60;
    return m + " min" + (r ? " " + r + " s" : "");
  }
  function via(st) {
    if (!st || !st.method) return "";
    const port = /^tcp\/(\d+)( \(abgelehnt\))?/.exec(st.method);
    const m = st.method === "icmp" ? "Ping" : port ? t(port[2] ? "wake.port_closed" : "ui.port", port[1]) : st.method;
    return t("wake.answers_via", m) + (st.rtt_ms ? " · " + st.rtt_ms + " ms" : "");
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
        step("check", "done", t("wake.check.running", via(st)));
        step("send", "skip", t("wake.send.not_needed"));
        step("wait", "skip", "");
        step("online", "done", via(st));
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
      step("check", "skip", hasIP ? t("wake.check.skipped") : t("wake.check.no_ip"));
    }

    // 2. Magic Packet senden
    setState("send");
    step("send", "active", t("wake.send.active", card.dataset.target));
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
    step("send", "done", t("wake.send.done", sent.target || card.dataset.target));

    if (!hasIP) {
      step("wait", "skip", t("wake.wait.no_ip"));
      step("online", "skip", "");
      setState("sent");
      setTitle(t("ui.packet_sent"), "✓ " + name);
      showResult("ok", t("wake.result.sent"), t("wake.result.sent_text", name));
      again.querySelector("span").textContent = t("wake.btn.again");
      again.hidden = false;
      running = false;
      return;
    }

    // 3. Warten, bis das Gerät antwortet
    setState("wait");
    const start = Date.now();
    step("wait", "active", t("wake.wait.active", "0:00"));
    const tick = setInterval(() => {
      const ms = Date.now() - start;
      step("wait", "active", t(retry && ms >= retry ? "wake.wait.resent" : "wake.wait.active", fmt(ms)));
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
      step("wait", "done", t("ui.started_after", fmt(took)));
      step("online", "done", via(online));
      setState("online");
      setTitle(t("ui.x_is_online", name), "✓ " + name);
      showResult("ok", t("wake.result.started"), t("wake.result.started_text", name, human(took)));
    } else {
      step("wait", "warn", t("wake.wait.no_answer", fmt(took)));
      step("online", "", "");
      setState("timeout");
      setTitle(t("wake.title.no_answer"), "! " + name);
      showResult("warn", t("wake.result.timeout", name), t("wake.result.timeout_text"), [t("wake.tip.bios"), t("wake.tip.power"), t("wake.tip.firewall")]);
      again.querySelector("span").textContent = t("wake.btn.again");
      again.hidden = false;
    }
    running = false;
  }

  again.addEventListener("click", () => run(true));
  run(false);
})();
