// Gruppenseite: weckt alle Geräte einer Gruppe und verfolgt, bis jedes mit IP online ist.
(function () {
  "use strict";
  const { t } = window.wolwebI18n;
  const card = document.getElementById("group");
  const name = card.dataset.name;
  const path = card.dataset.path; // "wake-group/<Name>", relativ zu <base>
  const query = card.dataset.key ? "?key=" + encodeURIComponent(card.dataset.key) : "";
  const timeout = (parseInt(card.dataset.timeout, 10) || 180) * 1000;
  const retry = (parseInt(card.dataset.retry, 10) || 0) * 1000; // dann sendet der Server erneut
  const title = document.getElementById("group-title");
  const sub = document.getElementById("group-sub");
  const result = document.getElementById("group-result");
  const again = document.getElementById("group-again");
  const recheckBtn = document.getElementById("group-check");
  const note = document.getElementById("group-note"); // wie lange geprüft wird
  const items = [...document.querySelectorAll("#members li")];
  const live = document.getElementById("group-live"); // sagt Screenreadern die Phasen an
  const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

  function mark(li, s, pill, detail) {
    li.dataset.s = s;
    const p = li.querySelector("[data-pill]");
    p.className = "pill " + ({ online: "online", sent: "waking", late: "warn", error: "warn", skip: "unknown" }[s] || "offline");
    p.textContent = pill;
    if (detail !== undefined) li.querySelector("[data-detail]").textContent = detail;
  }
  // announce sagt einen Phasenwechsel einmal an – ohne Zeitangaben, die sich ständig ändern.
  let lastLive = "";
  function announce(text) {
    if (text && text !== lastLive) live.textContent = lastLive = text;
  }
  function setTitle(text) {
    title.textContent = text;
    announce(text);
  }
  function fmt(ms) {
    const s = Math.floor(ms / 1000);
    return Math.floor(s / 60) + ":" + String(s % 60).padStart(2, "0");
  }
  const { tn } = window.wolwebI18n;
  const duration = (sec) => sec >= 60 && sec % 60 === 0 ? tn("wake.minutes", sec / 60) : tn("wake.seconds", sec);
  function show(kind, head, text, tips) {
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
    recheckBtn.hidden = true;
    result.hidden = true;
    card.dataset.state = "send";
    setTitle(t("group.title.starting", name));
    let sent;
    try {
      sent = await call("POST", path + query);
    } catch (e) {
      card.dataset.state = "error";
      setTitle(t("wake.send.failed"));
      show("err", t("group.result.send_failed"), e.message);
      again.hidden = false;
      running = false;
      return;
    }
    const byID = new Map((sent.results || []).map((r) => [r.id, r]));
    for (const li of items) {
      const r = byID.get(li.dataset.id);
      if (!r || !r.success) mark(li, "error", t("ui.error"), (r && r.error) || t("group.not_sent"));
      else if (li.dataset.hasIp) mark(li, "sent", t("group.pill.starting"));
      else mark(li, "skip", t("group.pill.sent"));
    }
    const watch = items.filter((li) => li.dataset.s === "sent");
    card.dataset.state = "wait";
    if (watch.length) {
      announce(t("group.live.waiting"));
      note.textContent = t(retry ? "wake.wait.note_retry" : "wake.wait.note", duration(timeout / 1000));
      note.hidden = false;
    }
    const start = Date.now();
    while (watch.some((li) => li.dataset.s !== "online") && Date.now() - start < timeout) {
      const ms = Date.now() - start;
      sub.textContent = t("ui.x_of_y_online", watch.filter((li) => li.dataset.s === "online").length, watch.length) + " · " + fmt(ms) +
        (retry && ms >= retry ? " · " + t("ui.resent") : "");
      if (retry && ms >= retry && watch.some((li) => li.dataset.s === "sent")) announce(t("wake.live.resent"));
      await sleep(2000);
      await poll(start);
    }
    note.hidden = true;
    finish(watch);
    again.querySelector("span").textContent = card.dataset.state === "timeout" ? t("wake.btn.wake_again") : t("wake.btn.again");
    again.hidden = false;
    running = false;
  }

  // poll fragt den Stand der Mitglieder ab und markiert die, die jetzt antworten.
  async function poll(start) {
    try {
      const st = await call("GET", path + "/status" + query);
      for (const m of st.members || []) {
        const li = items.find((x) => x.dataset.id === m.id);
        if (li && (li.dataset.s === "sent" || li.dataset.s === "late") && m.status && m.status.state === "online") {
          mark(li, "online", t("ui.online"), start ? t("group.after", fmt(Date.now() - start)) : "");
          announce(t("group.live.member_online", li.querySelector("strong").textContent));
        }
      }
    } catch (e) { /* kurz nicht erreichbar – weiter */ }
  }

  // finish zeigt das Ergebnis: bestätigt, nur gesendet (ohne IP) oder nicht alle gemeldet.
  function finish(watch) {
    const online = watch.filter((li) => li.dataset.s === "online").length;
    const failed = items.filter((li) => li.dataset.s === "error").length;
    const unchecked = items.filter((li) => li.dataset.s === "skip").length; // ohne IP: gesendet, Start nicht prüfbar
    sub.textContent = t("ui.x_of_y_online", online, watch.length);
    if (!failed && online === watch.length && !unchecked) {
      // Alle geprüft und erreichbar: Start bestätigt
      card.dataset.state = "online";
      setTitle(t("group.title.online", name));
      show("ok", t("group.result.all_online"), "");
    } else if (!failed && online === watch.length) {
      // Alle prüfbaren sind da, für die übrigen ging nur der Weckruf raus – kein grüner Haken.
      card.dataset.state = "sent";
      setTitle(t("group.title.sent"));
      show("sent", watch.length ? t("group.result.mixed", online, items.length) : t("wake.result.sent"),
        watch.length ? tn("group.result.mixed_rest", unchecked) : t("group.result.no_ip"));
    } else {
      card.dataset.state = "timeout";
      setTitle(t("group.title.partial"));
      for (const li of watch) if (li.dataset.s !== "online") mark(li, "late", t("group.pill.no_answer"));
      show("warn", t("ui.x_of_y_online", online, watch.length) + (failed ? " · " + t("group.n_errors", failed) : ""), t("group.result.partial_text"),
        [t("wake.tip.bios"), t("wake.tip.windows"), t("wake.tip.power"), t("wake.tip.firewall")]);
    }
    recheckBtn.hidden = card.dataset.state !== "timeout";
  }

  // recheck: nach Ablauf noch einmal nachsehen, ohne zu wecken.
  async function recheck() {
    if (running) return;
    running = true;
    recheckBtn.disabled = true;
    await poll(0);
    recheckBtn.disabled = false;
    running = false;
    const watch = items.filter((li) => li.dataset.hasIp && li.dataset.s !== "error");
    finish(watch);
    if (card.dataset.state === "timeout") {
      const clock = new Date().toLocaleTimeString(window.wolwebI18n.locale, { hour: "2-digit", minute: "2-digit" });
      const p = document.createElement("p");
      p.textContent = t("wake.result.still_no_answer", clock);
      result.querySelector("strong").after(p);
      live.textContent = lastLive = p.textContent; // auch beim zweiten Mal ansagen
    } else {
      again.querySelector("span").textContent = t("wake.btn.again");
    }
  }
  again.addEventListener("click", run);
  recheckBtn.addEventListener("click", recheck);
  run();
})();
