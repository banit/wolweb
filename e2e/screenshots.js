// Screenshots fürs README: node screenshots.js (im Ordner e2e, nach npm ci).
//
// Startet einen eigenen Testserver (server.sh) und legt erfundene Geräte an. Alles, was vom
// echten Rechner käme – Online-Status, Suchtreffer, Schnittstellen, Verlauf –, wird in den
// Antworten des Servers durch Beispieldaten ersetzt: nur 192.168.1.x / 10.0.0.x und lokal
// verwaltete MACs (02:…), damit keine echten Adressen in die Bilder geraten. Der Server selbst
// kennt die Geräte nur unter 127.0.0.x – so prüft er keine Adressen im echten Netz.
const { chromium } = require("@playwright/test");
const { spawn } = require("child_process");
const path = require("path");

const PORT = 8431;
const BASE = `http://127.0.0.1:${PORT}/`;
const OUT = path.join(__dirname, "..", "docs", "screenshots");
const H = { "X-WolWeb": "1", "Content-Type": "application/json" };

const ago = (min) => new Date(Date.now() - min * 60000).toISOString();
const mac = (n) => "02:00:00:00:01:" + n.toString(16).padStart(2, "0").toUpperCase();

// Geräte: online mit Methode, sonst zuletzt gesehen vor … Minuten.
const DEVICES = [
  { name: "Drucker", icon: "printer", ip: "192.168.1.40", group: "Büro", online: "icmp" },
  { name: "Gaming-PC", icon: "desktop", ip: "192.168.1.30", group: "Büro", online: "icmp", rtt: 1 },
  { name: "Workstation", icon: "desktop", ip: "192.168.1.31", group: "Büro", seen: 17 * 60, schedule: ["Mo-Fr 07:30"] },
  { name: "Backup-NAS", icon: "nas", ip: "192.168.1.21", group: "Homelab", seen: 3 * 1440, schedule: ["So 03:00"] },
  { name: "NAS", icon: "nas", ip: "192.168.1.20", group: "Homelab", online: "icmp", note: "Backups und Medien" },
  { name: "Proxmox", icon: "server", ip: "192.168.1.10", group: "Homelab", online: "tcp/8006", rtt: 2 },
  { name: "PlayStation", icon: "console", ip: "192.168.1.46", seen: 5 * 1440 },
  { name: "Wohnzimmer-TV", icon: "tv", ip: "10.0.0.45", seen: 4 * 60 },
];

const FOUND = [
  { mac: mac(0x50), ip: "192.168.1.50", hostname: "raspberrypi", vendor: "Raspberry Pi Foundation", network: "192.168.1.0/24" },
  { mac: mac(0x60), ip: "192.168.1.60", hostname: "homeassistant", vendor: "Raspberry Pi Trading Ltd", network: "192.168.1.0/24" },
  { mac: mac(0x70), ip: "10.0.0.70", hostname: "steckdose-kueche", vendor: "Espressif Inc.", network: "10.0.0.0/24" },
  { mac: mac(0x80), ip: "192.168.1.80", hostname: "MacBook-Air", vendor: "Apple, Inc.", network: "192.168.1.0/24" },
];

const NETS = [
  { cidr: "192.168.1.0/24", name: "Heimnetz", vlan: 0, iface: "eth0", addr: "192.168.1.2" },
  { cidr: "10.0.0.0/24", name: "IoT", vlan: 20, iface: "eth0.20", addr: "10.0.0.2" },
];

function startServer() {
  // Wartezeiten wie im Betrieb (die Browsertests nehmen kurze)
  const env = { ...process.env, PORT: String(PORT), WOLWEB_WAKE_TIMEOUT: "3m", WOLWEB_WAKE_RETRY: "45s" };
  const srv = spawn("sh", ["server.sh"], { cwd: __dirname, env, stdio: "ignore" });
  return srv;
}
async function waitUp(request) {
  for (let i = 0; i < 180; i++) {
    try { if ((await request.get(BASE + "health")).ok()) return; } catch (e) { /* startet noch */ }
    await new Promise((r) => setTimeout(r, 1000));
  }
  throw new Error("Testserver startet nicht");
}

// Antworten des Servers durch die Beispieldaten ergänzen.
async function fake(page, ids) {
  const byName = Object.fromEntries(DEVICES.map((d) => [d.name, d]));
  const status = (d) => d.online
    ? { state: "online", ip: d.ip, method: d.online, rtt_ms: d.rtt || 0, checked_at: ago(0), last_seen: ago(0) }
    : { state: "offline", ip: d.ip, checked_at: ago(0), last_seen: ago(d.seen) };
  const json = (route, body) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) });
  const pass = async (route, edit) => {
    const res = await route.fetch();
    json(route, edit(await res.json()));
  };
  await page.route("**/api/v1/events", (r) => r.abort()); // keine Live-Updates mit echten Werten
  await page.route("**/api/v1/devices", (r) => pass(r, (list) => list.map((v) => {
    const d = byName[v.name];
    return d ? { ...v, ip: d.ip, status: status(d), check_ip: d.ip } : v;
  })));
  await page.route(/\/api\/v1\/devices\/[^/]+\/status$/, (r) => {
    const id = r.request().url().split("/").slice(-2)[0];
    const d = DEVICES.find((x) => ids[x.name] === id);
    return json(r, status(d || DEVICES[0]));
  });
  await page.route("**/api/v1/info", (r) => pass(r, (v) => ({
    ...v, version: "1.2.0", networks: NETS.map((n) => n.cidr), networks_auto: false, network_choices: [], timezone: "Europe/Berlin",
    local_networks: NETS.map((n) => ({ interface: n.iface, addr: n.addr, prefix: n.cidr, broadcast: n.cidr.replace(/0\/24$/, "255:9") })),
  })));
  await page.route("**/api/v1/discovery", (r) => json(r, {
    job: { running: false, networks: NETS.map((n) => n.cidr), method: "arp", total: 508, done: 508, found: FOUND.length + 4, new: FOUND.length, finished_at: ago(19), auto: false },
    found: FOUND.map((f) => ({ ...f, first_seen: ago(19), last_seen: ago(19), broadcast: f.ip.replace(/\d+$/, "255:9") })),
  }));
  await page.route("**/api/v1/settings", (r) => r.request().method() !== "GET" ? r.continue() : pass(r, (v) => ({
    ...v, locked: {}, file: "config.json", settings_file: "data/settings.json", auto_network: "",
    settings: { ...v.settings, bcastip: "", discovery: { ...v.settings.discovery, networks: NETS.map((n) => ({ cidr: n.cidr, name: n.name, vlan: n.vlan, scan: true })) } },
    networks: NETS.map((n) => ({ cidr: n.cidr, name: n.name, vlan: n.vlan, scan: true, label: n.vlan ? `${n.name} (VLAN ${n.vlan})` : n.name,
      broadcast: n.cidr.replace(/0\/24$/, "255:9"), local: { interface: n.iface, addr: n.addr, prefix: n.cidr, broadcast: n.cidr.replace(/0\/24$/, "255:9") },
      devices: DEVICES.filter((d) => d.ip.startsWith(n.cidr.slice(0, -4))).length, found: FOUND.filter((f) => f.network === n.cidr).length })),
    interfaces: NETS.map((n) => ({ interface: n.iface, addr: n.addr, prefix: n.cidr, broadcast: n.cidr.replace(/0\/24$/, "255:9"), configured: true })),
  })));
  await page.route("**/api/v1/schedule/preview*", (r) => pass(r, (v) => ({ ...v, zone: "Europe/Berlin" })));
  await page.route("**/api/v1/history*", (r) => json(r, { events: [
    { time: ago(2), id: ids.NAS, device: "NAS", source: "link", client: "192.168.1.66", target: "192.168.1.255:9", success: true, checkable: true, started: ago(1.3) },
    { time: ago(60), id: ids.Proxmox, device: "Proxmox", source: "group", group: "Homelab", target: "192.168.1.255:9", success: true, checkable: true, was_online: true },
    { time: ago(61), id: ids["Backup-NAS"], device: "Backup-NAS", source: "group", group: "Homelab", target: "192.168.1.255:9", success: true, checkable: true },
    { time: ago(600), id: ids.Workstation, device: "Workstation", source: "schedule", target: "192.168.1.255:9", success: true, checkable: true, started: ago(599) },
    { time: ago(1500), id: ids["Wohnzimmer-TV"], device: "Wohnzimmer-TV", source: "ui", target: "10.0.0.255:9", success: false, error: "Netzwerkschnittstelle „eth0.20“: nicht gefunden" },
  ] }));
}

async function shoot(browser, name, opts, run) {
  const { element, ...ctxOpts } = opts;
  const ctx = await browser.newContext({ viewport: { width: 1280, height: 900 }, deviceScaleFactor: 1, locale: "de-DE", colorScheme: "light", reducedMotion: "reduce", ...ctxOpts });
  const page = await ctx.newPage();
  await fake(page, shoot.ids);
  await run(page);
  await page.waitForTimeout(800); // Leiste und Dialoge sind eingeblendet, Symbole gezeichnet
  await page.evaluate(() => Promise.all(document.getAnimations().filter((a) => a.effect && a.effect.getComputedTiming().endTime !== Infinity).map((a) => a.finished.catch(() => {}))));
  const file = path.join(OUT, name + ".png");
  if (element) await page.locator(element).screenshot({ path: file });
  else await page.screenshot({ path: file });
  await ctx.close();
  console.log("✓", name);
}

(async () => {
  const srv = startServer();
  let browser;
  try {
    browser = await chromium.launch({ args: ["--lang=de-DE"] }); // Uhrzeitfeld im 24-Stunden-Format
    const ctx = await browser.newContext();
    await waitUp(ctx.request);
    const ids = {};
    for (const [i, d] of DEVICES.entries()) {
      const res = await ctx.request.post(BASE + "api/v1/devices", { headers: H, data: {
        name: d.name, mac: mac(i + 1), ip: "127.0.0." + (i + 10), icon: d.icon, group: d.group || "", note: d.note || "", schedule: d.schedule || [], broadcast: "127.0.0.1:9" } });
      ids[d.name] = (await res.json()).id;
    }
    await ctx.close();
    shoot.ids = ids;
    const overview = async (page, url = "./") => {
      await page.goto(BASE + url.replace(/^\.\//, ""));
      await page.locator(".card").nth(DEVICES.length - 1).waitFor();
      await page.locator(".pill.online").first().waitFor();
    };

    await shoot(browser, "overview-light", { viewport: { width: 1280, height: 1050 } }, (p) => overview(p));
    await shoot(browser, "overview-dark", { viewport: { width: 1280, height: 1050 }, colorScheme: "dark" }, (p) => overview(p));
    await shoot(browser, "overview-en", { viewport: { width: 1280, height: 1050 }, locale: "en-GB" }, (p) => overview(p, "./?lang=en"));
    await shoot(browser, "discovery", { viewport: { width: 1280, height: 860 } }, async (p) => {
      await overview(p);
      await p.click("#open-discovery");
      await p.locator(".found").first().waitFor();
    });
    await shoot(browser, "device-dialog", { viewport: { width: 1280, height: 980 }, colorScheme: "dark" }, async (p) => {
      await overview(p);
      await p.locator(".card", { hasText: "Workstation" }).locator('[data-focus="edit"]').click();
      await p.locator("#sched-summary strong").waitFor();
    });
    await shoot(browser, "history", { viewport: { width: 1280, height: 760 } }, async (p) => {
      await overview(p);
      await p.click("#open-history");
      await p.locator("#history-list li").first().waitFor();
      await p.locator("#history-list details").first().evaluate((d) => { d.open = true; });
    });
    await shoot(browser, "settings", { viewport: { width: 1280, height: 1400 }, element: 'section[aria-labelledby="sec-networks"]' }, async (p) => {
      await p.goto(BASE + "settings");
      await p.locator("#net-list li").nth(1).waitFor();
      await p.locator("#if-list li").first().waitFor();
    });

    // Weckseite: gesendet und wartend, dann bestätigt (Handygröße)
    const phone = { viewport: { width: 420, height: 860 }, deviceScaleFactor: 2, isMobile: true, hasTouch: true };
    for (const [name, online] of [["wake-waiting", false], ["wake-online", true]]) {
      await shoot(browser, name, phone, async (p) => {
        let polls = 0;
        await p.route(/\/wake\/NAS$/, (r) => r.request().method() === "POST"
          ? r.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ success: true, device: "NAS", id: ids.NAS, target: "192.168.1.255:9", message: "Weckruf an NAS gesendet." }) })
          : r.continue());
        await p.route(/\/wake\/NAS\/status/, (r) => r.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(
          online && ++polls > 2 ? { state: "online", ip: "192.168.1.20", method: "icmp", rtt_ms: 1 } : { state: "offline", ip: "192.168.1.20" }) }));
        await p.goto(BASE + "wake/NAS");
        await p.locator(`#wake[data-state="${online ? "online" : "wait"}"]`).waitFor({ timeout: 20000 });
        if (!online) await p.waitForTimeout(2600); // die Uhr läuft ein Stück
      });
    }
  } finally {
    if (browser) await browser.close();
    srv.kill(); // SIGTERM: server.sh stoppt wolweb und löscht sein temporäres Verzeichnis
  }
})().catch((e) => { console.error(e); process.exitCode = 1; });
