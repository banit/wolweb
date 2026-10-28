// Weck- und Gruppenseite: alle Ausgänge (gesendet, läuft schon, keine Antwort, unbekannt).
const { test, expect } = require("@playwright/test");
const AxeBuilder = require("@axe-core/playwright").default;
const { reset, addDevice, onlineDevice, offlineDevice } = require("./helpers");

test.beforeEach(async ({ request }) => {
  await reset(request);
});

test("ohne IP: Paket gesendet", async ({ page, request }) => {
  await addDevice(request, { name: "Büro-PC" });
  await page.goto("wake/" + encodeURIComponent("Büro-PC"));
  await expect(page.locator("#wake")).toHaveAttribute("data-state", "sent");
  await expect(page.locator("#wake-result")).toContainText("Weckruf gesendet");
  await expect(page.locator("#wake-result")).toHaveClass(/\bsent\b/); // kein grüner „Start bestätigt“
  await expect(page.locator("#wake-live")).toHaveText("Weckruf gesendet"); // Ansage für Screenreader
});

test("läuft schon: nichts senden, aber anbieten", async ({ page, request }) => {
  await onlineDevice(request, "Proxmox");
  await page.goto("wake/Proxmox");
  await expect(page.locator("#wake")).toHaveAttribute("data-state", "online", { timeout: 15000 });
  await expect(page.locator("#wake-result")).toHaveClass(/\bok\b/);
  await expect(page.locator('[data-step="send"]')).toHaveAttribute("data-s", "skip");
  await expect(page.locator("#wake-again")).toBeVisible();
});

test("keine Antwort: Paket wird wiederholt, dann Tipps", async ({ page, request }) => {
  test.setTimeout(45000);
  const nas = await offlineDevice(request, "NAS");
  // Jede Ansage der Live-Region mitschreiben (Screenreader hören genau diese Folge).
  await page.addInitScript(() => {
    window.said = [];
    new MutationObserver((list) => {
      const live = document.getElementById("wake-live");
      if (live && list.some((m) => live.contains(m.target) && m.target !== live.parentNode)) window.said.push(live.textContent);
    }).observe(document, { childList: true, characterData: true, subtree: true });
  });
  await page.goto("wake/NAS");
  // Server: WOLWEB_WAKE_RETRY=2s, WOLWEB_WAKE_TIMEOUT=8s
  await expect(page.locator('[data-step="check"] [data-detail]')).toHaveText("Noch nicht erreichbar – Weckruf wird gesendet");
  await expect(page.locator("#wake-note")).toHaveText("Wir prüfen den Start bis zu 8 Sekunden und versuchen es bei Bedarf automatisch erneut.");
  await expect(page.locator('[data-step="wait"] [data-detail]')).toContainText("erneut gesendet", { timeout: 10000 });
  await expect(page.locator("#wake")).toHaveAttribute("data-state", "timeout", { timeout: 20000 });
  const result = page.locator("#wake-result");
  await expect(result).toContainText("Start nicht bestätigt");
  await expect(result).toContainText("Prüfe zuerst Stromversorgung und Netzwerkkabel. Ist der Rechner bereits an, kann die Rückmeldung blockiert sein.");
  await expect(result.locator("details.help li")).toHaveCount(4); // Technisches unter „Hilfe“
  await expect(result.locator("details.help li").first()).toBeHidden();
  await result.getByText("Hilfe").click();
  await expect(result.locator("details.help li").first()).toBeVisible();
  await expect(page.getByRole("button", { name: "Erneut wecken" })).toBeVisible();
  await page.getByRole("button", { name: "Nochmals prüfen" }).click();
  await expect(result).toContainText("Weiterhin keine Antwort");
  await expect(page.locator("#wake")).toHaveAttribute("data-state", "timeout");
  // Phasen genau einmal und ohne mitlaufende Uhr
  const said = (await page.evaluate(() => window.said)).filter(Boolean);
  expect(said.slice(0, 5)).toEqual(["NAS wird gestartet", "Weckruf wird gesendet …", "Weckruf gesendet. Warte auf den Start.",
    "Noch keine Antwort – Weckruf erneut gesendet.", "Start noch nicht bestätigt"]);
  expect(said[5]).toMatch(/^Weiterhin keine Antwort/);
  expect(said.filter((x) => /\d:\d\d(?! Uhr)/.test(x) && !/geprüft um/.test(x))).toEqual([]);
  await expect(async () => {
    const { events } = await (await request.get("api/v1/history")).json();
    expect(events.filter((e) => e.id === nas.id).map((e) => !!e.retry).sort()).toEqual([false, true]);
  }).toPass({ timeout: 10000 });
  const axe = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21aa", "wcag22aa"]).analyze();
  expect(axe.violations.map((v) => v.id)).toEqual([]);
});

test("unbekanntes Gerät", async ({ page }) => {
  const res = await page.goto("wake/Gibtsnicht");
  expect(res.status()).toBe(404);
  await expect(page.locator("#wake")).toHaveAttribute("data-state", "missing");
});

test("Gruppenseite weckt alle und wartet auf die mit IP", async ({ page, request }) => {
  await onlineDevice(request, "Proxmox", { group: "Homelab" });
  await addDevice(request, { name: "Switch", group: "Homelab" });
  await page.goto("wake-group/Homelab");
  // Gemischt: einer bestätigt, beim anderen nur gesendet – das ist kein „alle gestartet“.
  await expect(page.locator("#group")).toHaveAttribute("data-state", "sent", { timeout: 15000 });
  await expect(page.locator('#members li[data-s="online"]')).toHaveCount(1);
  await expect(page.locator('#members li[data-s="skip"]')).toHaveCount(1);
  await expect(page.locator("#group-result")).toContainText("1 von 2 Geräten erreichbar.");
  await expect(page.locator("#group-result")).toContainText("Beim anderen wurde der Weckruf gesendet");
  await expect(page.locator("#group-live")).not.toBeEmpty();
});

test("Gruppenseite: alle mit IP erreichbar heißt Start bestätigt", async ({ page, request }) => {
  await onlineDevice(request, "Proxmox", { group: "Rack" });
  await onlineDevice(request, "NAS", { group: "Rack" });
  await page.goto("wake-group/Rack");
  await expect(page.locator("#group")).toHaveAttribute("data-state", "online", { timeout: 15000 });
  await expect(page.locator("#group-result")).toHaveClass(/\bok\b/);
});

test("Gruppenseite: keine Antwort – Hinweis, Hilfe, nochmals prüfen", async ({ page, request }) => {
  test.setTimeout(45000);
  await offlineDevice(request, "NAS", { group: "Keller" });
  await onlineDevice(request, "Proxmox", { group: "Keller" });
  await page.goto("wake-group/Keller");
  await expect(page.locator("#group-note")).toContainText("Wir prüfen den Start bis zu 8 Sekunden");
  await expect(page.locator("#group")).toHaveAttribute("data-state", "timeout", { timeout: 20000 });
  const result = page.locator("#group-result");
  await expect(result).toContainText("Prüfe zuerst Stromversorgung und Netzwerkkabel der fehlenden Geräte.");
  await expect(result.locator("details.help li")).toHaveCount(4);
  await expect(page.locator('#members li[data-s="late"]')).toHaveCount(1);
  await page.getByRole("button", { name: "Nochmals prüfen" }).click();
  await expect(result).toContainText("Weiterhin keine Antwort");
  await expect(page.getByRole("button", { name: "Erneut wecken" })).toBeVisible();
});
