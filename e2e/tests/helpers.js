// Gemeinsame Helfer: Geräte über die API anlegen und vor jedem Test alles entfernen.
const { expect } = require("@playwright/test");

const HEADERS = { "X-WolWeb": "1" };
const PORT = Number(process.env.PORT || 8399);

async function reset(request) {
  expect((await request.delete("api/v1/settings", { headers: HEADERS })).ok()).toBeTruthy(); // Einstellungen wie in config.json
  const res = await request.get("api/v1/devices");
  for (const d of await res.json()) {
    expect((await request.delete("api/v1/devices/" + d.id, { headers: HEADERS })).ok()).toBeTruthy();
  }
}

let macs = 0;
async function addDevice(request, data) {
  const mac = "02:00:00:00:00:" + (++macs % 256).toString(16).padStart(2, "0"); // lokal verwaltete MAC
  const res = await request.post("api/v1/devices", { headers: HEADERS, data: { mac, broadcast: "127.0.0.1:9", ...data } });
  expect(res.status(), await res.text()).toBe(201);
  return res.json();
}

// Antwortet: wolweb selbst lauscht auf 127.0.0.1:PORT.
const onlineDevice = (request, name, extra) => addDevice(request, { name, ip: "127.0.0.1", ports: [PORT], ...extra });
// Antwortet nie: 198.51.100.0/24 ist ein Dokumentationsnetz (TEST-NET-2). Nicht 192.0.2.1 – das ist in
// manchen Containern das Gateway und lehnt sofort ab, dann liefe die Prüfung anders als in der CI.
const offlineDevice = (request, name, extra) => addDevice(request, { name, ip: "198.51.100.1", ports: [9], ...extra });

// settled wartet, bis Einblend-Animationen (Dialog, Leiste) durch sind – sonst misst man sie mitten
// im Skalieren. Endlose Animationen zählen nicht.
function settled(page) {
  return page.evaluate(() => Promise.all(document.getAnimations()
    .filter((a) => a.effect && a.effect.getComputedTiming().endTime !== Infinity)
    .map((a) => a.finished.catch(() => {}))));
}

// Sichtbare Bedienelemente kleiner als 44 × 44 px (WCAG 2.5.5).
async function smallTargets(page) {
  await settled(page);
  return page.evaluate(() => {
    const out = [];
    for (let n of document.querySelectorAll("button, a[href], input, textarea, select, summary, .icon-picker span")) {
      if (n.matches(".icon-picker input")) continue; // unsichtbar, das Symbol daneben ist das Ziel
      if (n.matches(".check input, .select-box input")) n = n.closest(".check, .select-box"); // die ganze Zeile/Fläche ist anklickbar
      const r = n.getBoundingClientRect();
      if (!r.width || !r.height || getComputedStyle(n).visibility === "hidden") continue;
      if (r.width < 44 || r.height < 44) {
        out.push(`${n.tagName.toLowerCase()}${n.id ? "#" + n.id : ""}.${[...n.classList].join(".")} ${Math.round(r.width)}×${Math.round(r.height)}`);
      }
    }
    return out;
  });
}

// Kontrast von Schrift- zu Hintergrundfarbe eines Elements mit eigener, deckender Füllfarbe (WCAG-Formel).
// Für gefüllte Flächen wie Plaketten, die axe mangels eindeutigem Hintergrund nur als „unvollständig“ meldet.
function contrast(page, selector) {
  return page.locator(selector).evaluate((n) => {
    const rgb = (c) => c.match(/[\d.]+/g).slice(0, 3).map(Number);
    const lum = (c) => {
      const [r, g, b] = rgb(c).map((v) => { v /= 255; return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4; });
      return 0.2126 * r + 0.7152 * g + 0.0722 * b;
    };
    const cs = getComputedStyle(n);
    const [a, b] = [lum(cs.color), lum(cs.backgroundColor)].sort((x, y) => y - x);
    return Math.round(((a + 0.05) / (b + 0.05)) * 100) / 100;
  });
}

module.exports = { HEADERS, PORT, reset, addDevice, onlineDevice, offlineDevice, settled, smallTargets, contrast };
