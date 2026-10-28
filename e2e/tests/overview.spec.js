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
  await expect(page.locator("#add-device")).toHaveAccessibleName("Add device");
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

test("Sprachknopf wirkt auch, wenn ?lang= in der Adresse steht", async ({ page }) => {
  await page.goto("./?lang=en&view=normal");
  await expect(page.locator("html")).toHaveAttribute("lang", "en");
  await page.click("#lang-toggle");
  await expect(page.locator("html")).toHaveAttribute("lang", "de");
  expect(new URL(page.url()).searchParams.get("lang")).toBeNull();
  expect(new URL(page.url()).searchParams.get("view")).toBe("normal"); // andere Parameter bleiben
});

test("Zeitplan ohne Befehlssprache: Schalter, Wochentage, Uhrzeit, Pause", async ({ page, request }) => {
  await page.goto("./");
  await page.click("#add-device");
  const dlg = page.locator("#device-dialog");
  await dlg.locator('input[name="name"]').fill("Büro-PC");
  await dlg.locator('input[name="mac"]').fill("02:00:00:00:00:a1");
  const on = dlg.getByRole("switch", { name: /Automatisch wecken/ });
  await on.check();
  // Vorschlag: werktags um 07:30, mit Zusammenfassung, nächster Weckzeit und Zeitzone
  await expect(dlg.getByRole("checkbox", { name: "Montag" })).toBeChecked();
  await expect(dlg.getByRole("checkbox", { name: "Samstag" })).not.toBeChecked();
  const summary = dlg.locator("#sched-summary");
  await expect(summary).toContainText("Montag bis Freitag um 07:30 Uhr");
  await expect(summary).toContainText("Nächstes Wecken:");
  await expect(summary).toContainText("Zeitzone");
  await dlg.getByRole("checkbox", { name: "Samstag" }).check();
  await dlg.getByLabel("Uhrzeit").fill("06:45");
  await expect(summary).toContainText("Montag bis Samstag um 06:45 Uhr");
  await dlg.locator("#save-device").click();
  await expect(dlg).toBeHidden();
  let [dev] = await (await request.get("api/v1/devices")).json();
  expect(dev.schedule).toEqual(["Mo-Sa 06:45"]);

  // Pausieren behält die Zeiten
  const card = page.locator(".card", { hasText: "Büro-PC" });
  await card.locator('[data-focus="edit"]').click();
  await expect(on).toBeChecked();
  await expect(dlg.getByRole("checkbox", { name: "Samstag" })).toBeChecked();
  await dlg.getByRole("switch", { name: /Pausieren/ }).check();
  await expect(summary).toContainText("Pausiert");
  await dlg.locator("#save-device").click();
  await expect(card).toContainText("Zeitplan pausiert");
  [dev] = await (await request.get("api/v1/devices")).json();
  expect([dev.schedule, dev.schedule_paused]).toEqual([["Mo-Sa 06:45"], true]);

  // Mehrere Zeiten in Textform – kompatibel, die Schalter treten zurück
  await card.locator('[data-focus="edit"]').click();
  await dlg.getByRole("switch", { name: /Pausieren/ }).uncheck();
  await dlg.locator("#sched-more summary").click();
  await dlg.getByLabel("Zeitplan als Text").fill("werktags 7:30; Wochenende 9:00");
  await dlg.getByLabel("Zeitplan als Text").blur();
  await expect(dlg.locator("#sched-multi")).toBeVisible();
  await expect(dlg.locator("#sched-simple")).toBeHidden();
  await expect(summary).toContainText("Samstag und Sonntag um 09:00 Uhr");
  await dlg.locator("#save-device").click();
  await expect(dlg).toBeHidden();
  [dev] = await (await request.get("api/v1/devices")).json();
  expect([dev.schedule, !!dev.schedule_paused]).toEqual([["Mo-Fr 07:30", "Sa,So 09:00"], false]);

  // Ausschalten entfernt den Zeitplan
  await card.locator('[data-focus="edit"]').click();
  await on.uncheck();
  await dlg.locator("#save-device").click();
  await expect(dlg).toBeHidden();
  [dev] = await (await request.get("api/v1/devices")).json();
  expect(dev.schedule || []).toEqual([]);
});

test.describe("Browser in anderer Zeitzone als der Server", () => {
  test.use({ timezoneId: "Asia/Tokyo" }); // der Testserver läuft in UTC (server.sh)

  test("Zeitplan nennt die Zeitzone des Servers und die eigene Uhrzeit", async ({ page, request }) => {
    await addDevice(request, { name: "NAS", schedule: ["täglich 07:30"] });
    await page.goto("./");
    await page.locator(".card", { hasText: "NAS" }).locator('[data-focus="edit"]').click();
    const summary = page.locator("#sched-summary");
    await expect(summary).toContainText("Täglich um 07:30 Uhr");
    await expect(summary).toContainText("Zeitzone des Servers (UTC)");
    await expect(summary).toContainText("16:30"); // 07:30 UTC in Tokio
    await expect(summary).toContainText("TZ=Europe/Berlin");
  });
});
