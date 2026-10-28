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
  await page.goto("wake/NAS");
  // Server: WOLWEB_WAKE_RETRY=2s, WOLWEB_WAKE_TIMEOUT=8s
  await expect(page.locator('[data-step="wait"] [data-detail]')).toContainText("erneut gesendet", { timeout: 10000 });
  await expect(page.locator("#wake")).toHaveAttribute("data-state", "timeout", { timeout: 20000 });
  await expect(page.locator("#wake-result li")).toHaveCount(3);
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
