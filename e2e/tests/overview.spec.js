// Übersicht: Geräte anlegen, suchen, filtern, wecken, Sprache und einfache Ansicht.
const { test, expect } = require("@playwright/test");
const { reset, addDevice, onlineDevice } = require("./helpers");

test.beforeEach(async ({ request }) => {
  await reset(request);
});

test("Gerät über den Dialog anlegen", async ({ page }) => {
  await page.goto("./");
  await expect(page.locator("#empty")).toBeVisible();
  await page.click("#add-device");
  const dlg = page.locator("#device-dialog");
  await expect(dlg).toBeVisible();
  await dlg.locator('input[name="name"]').fill("NAS");
  await dlg.locator('input[name="mac"]').fill("00-11-22-33-44-55");
  await dlg.locator("#save-device").click();
  await expect(dlg).toBeHidden();
  const card = page.locator(".card", { hasText: "NAS" });
  await expect(card).toContainText("00:11:22:33:44:55"); // einheitliche Schreibweise
});

test("ungültige MAC wird am Feld gemeldet", async ({ page }) => {
  await page.goto("./");
  await page.click("#add-device");
  await page.fill('#device-form input[name="name"]', "Kaputt");
  await page.fill('#device-form input[name="mac"]', "00:11:22");
  await page.click("#save-device");
  await expect(page.locator("#form-error")).toBeVisible();
  await expect(page.locator('#device-form input[name="mac"]')).toHaveAttribute("aria-invalid", "true");
});

test("Suche und Filter", async ({ page, request }) => {
  await addDevice(request, { name: "NAS" });
  await onlineDevice(request, "Proxmox");
  await page.goto("./");
  await expect(page.locator(".card")).toHaveCount(2);
  await page.fill("#search", "nas");
  await expect(page.locator(".card h3")).toHaveText(["NAS"]);
  await page.fill("#search", "");
  await expect(page.locator(".card", { hasText: "Proxmox" }).locator(".pill.online")).toBeVisible({ timeout: 15000 });
  const online = page.locator('#filter [data-filter="online"]');
  await online.click();
  await expect(online).toHaveAttribute("aria-pressed", "true");
  await expect(page.locator('#filter [data-filter="all"]')).toHaveAttribute("aria-pressed", "false");
  await expect(page.locator(".card h3")).toHaveText(["Proxmox"]);
});

test("Wecken aus der Übersicht landet im Verlauf", async ({ page, request }) => {
  await addDevice(request, { name: "Drucker" });
  await page.goto("./");
  await page.locator(".card", { hasText: "Drucker" }).locator(".btn-wake").click();
  await expect(page.locator("#toasts")).toContainText("Magic Packet gesendet");
  await page.click("#open-history");
  const first = page.locator("#history-list li").first();
  await expect(first).toContainText("Drucker");
  await expect(first).toContainText("Übersicht");
});

test("Sprache umschalten", async ({ page }) => {
  await page.goto("./");
  await expect(page.locator("html")).toHaveAttribute("lang", "de");
  await page.click("#lang-toggle");
  await expect(page.locator("html")).toHaveAttribute("lang", "en");
  await expect(page.locator("#add-device")).toContainText("Device");
  await page.click("#lang-toggle");
  await expect(page.locator("html")).toHaveAttribute("lang", "de");
});

test("einfache Ansicht", async ({ page, request }) => {
  await addDevice(request, { name: "NAS", note: "im Keller" });
  await page.goto("./");
  const card = page.locator(".card");
  await expect(card.locator('[data-focus="edit"]')).toHaveCount(1);
  await page.click("#view-toggle");
  await expect(page.locator("#view-toggle")).toHaveAttribute("aria-pressed", "true");
  await expect(card.locator('[data-focus="edit"], [data-focus="copy"], [data-focus="open"], .card-meta, .card-note')).toHaveCount(0);
  await expect(page.locator("#add-device")).toBeHidden();
  expect((await card.locator(".btn-wake").boundingBox()).height).toBeGreaterThanOrEqual(70);

  await page.reload(); // gemerkt
  await expect(page.locator("#view-toggle")).toHaveAttribute("aria-pressed", "true");
  await page.goto("./?view=normal"); // per Link zurück
  await expect(card.locator('[data-focus="edit"]')).toHaveCount(1);
  await page.goto("./?view=simple"); // und wieder an
  await expect(card.locator('[data-focus="edit"]')).toHaveCount(0);
});
