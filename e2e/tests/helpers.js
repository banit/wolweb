// Gemeinsame Helfer: Geräte über die API anlegen und vor jedem Test alles entfernen.
const { expect } = require("@playwright/test");

const HEADERS = { "X-WolWeb": "1" };
const PORT = Number(process.env.PORT || 8399);

async function reset(request) {
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
// Antwortet nie: 192.0.2.0/24 ist ein Dokumentationsnetz (TEST-NET-1).
const offlineDevice = (request, name, extra) => addDevice(request, { name, ip: "192.0.2.1", ports: [9], ...extra });

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
    for (const n of document.querySelectorAll("button, a[href], input, textarea, summary, .icon-picker span")) {
      if (n.matches(".icon-picker input")) continue; // unsichtbar, das Symbol daneben ist das Ziel
      const r = n.getBoundingClientRect();
      if (!r.width || !r.height || getComputedStyle(n).visibility === "hidden") continue;
      if (r.width < 44 || r.height < 44) {
        out.push(`${n.tagName.toLowerCase()}${n.id ? "#" + n.id : ""}.${[...n.classList].join(".")} ${Math.round(r.width)}×${Math.round(r.height)}`);
      }
    }
    return out;
  });
}

module.exports = { HEADERS, PORT, reset, addDevice, onlineDevice, offlineDevice, settled, smallTargets };
