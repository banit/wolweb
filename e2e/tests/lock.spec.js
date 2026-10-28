// Passwort für Änderungen: setzen, gesperrte Übersicht und Einstellungen, Entsperren und Sperren,
// abgelaufene Entsperrung, Barrierefreiheit.
const { test, expect } = require("@playwright/test");
const AxeBuilder = require("@axe-core/playwright").default;
const { HEADERS, PASSWORD, reset, offlineDevice, settled } = require("./helpers");

test.beforeEach(async ({ request }) => {
  await reset(request);
});
test.afterAll(async ({ request }) => {
  await reset(request);
});

async function setPassword(page) {
  await page.goto("settings");
  const pw = page.locator("#pw-form");
  await expect(page.locator("#pw-state")).toHaveText("Kein Passwort gesetzt – jeder im Netz kann alles ändern.");
  await expect(page.locator("#pw-current-field")).toBeHidden();
  await pw.locator('input[name="password"]').fill(PASSWORD);
  await pw.locator('input[name="repeat"]').fill(PASSWORD + "x");
  await page.click("#pw-save");
  await expect(page.locator("#pw-error")).toHaveText("Die beiden neuen Passwörter stimmen nicht überein.");
  await expect(pw.locator('input[name="repeat"]')).toBeFocused();
  await expect(pw.locator('input[name="repeat"]')).toHaveAttribute("aria-invalid", "true");
  await pw.locator('input[name="repeat"]').fill(PASSWORD);
  await page.click("#pw-save");
  await expect(page.locator("#toasts")).toContainText("Passwort gesetzt");
  await expect(page.locator("#pw-state")).toHaveText("Ein Passwort ist gesetzt.");
  await expect(page.locator("#pw-current-field")).toBeVisible();
  await expect(page.locator("#lock-toggle")).toHaveAttribute("aria-label", "Entsperrt – Änderungen wieder sperren");
}

async function enterPassword(page, password) {
  const d = page.locator("#unlock-dialog");
  await expect(d).toBeVisible();
  await expect(page.locator("#unlock-password")).toBeFocused();
  await page.locator("#unlock-password").fill(password);
  await page.keyboard.press("Enter");
}

test("Mit Passwort: ansehen und wecken frei, ändern erst nach dem Entsperren", async ({ page, browser, request }) => {
  await offlineDevice(request, "NAS");
  await setPassword(page);

  // Wer das Passwort gesetzt hat, bleibt entsperrt.
  await page.goto("./");
  await expect(page.locator("#add-device")).toBeVisible();
  await expect(page.getByRole("button", { name: "Bearbeiten" })).toBeVisible();

  // Ein anderer Browser sieht alles, kann wecken, aber nichts ändern.
  const other = await (await browser.newContext({ locale: "de-DE" })).newPage();
  await other.goto("./");
  const lock = other.locator("#lock-toggle");
  await expect(lock).toHaveAttribute("aria-label", "Gesperrt – zum Ändern entsperren");
  await expect(other.locator(".card h3")).toHaveText(["NAS"]);
  await expect(other.locator("#add-device")).toBeHidden();
  await expect(other.locator("#import-btn")).toBeHidden();
  await expect(other.locator("#export-link")).toBeHidden();
  await expect(other.getByRole("button", { name: "Bearbeiten" })).toHaveCount(0);
  await other.getByRole("button", { name: "Wecken", exact: true }).click();
  await expect(other.locator(".card .pill.waking")).toBeVisible();

  // Entsperren über das Schloss: falsches Passwort wird abgelehnt, das richtige gibt frei.
  await lock.click();
  await enterPassword(other, "falsch-123");
  await expect(other.locator("#unlock-error")).toHaveText("Das Passwort stimmt nicht.");
  await expect(other.locator("#unlock-password")).toHaveAttribute("aria-invalid", "true");
  await other.locator("#unlock-password").fill(PASSWORD);
  await other.click("#unlock-ok");
  await expect(other.locator("#unlock-dialog")).toBeHidden();
  await expect(other.locator("#toasts")).toContainText("Entsperrt");
  await expect(lock).toBeFocused();
  await expect(lock).toHaveAttribute("aria-label", "Entsperrt – Änderungen wieder sperren");
  await expect(other.locator("#add-device")).toBeVisible();
  await expect(other.getByRole("button", { name: "Bearbeiten" })).toBeVisible();

  // Sperren wirkt sofort.
  await lock.click();
  await expect(other.locator("#toasts")).toContainText("Gesperrt");
  await expect(other.locator("#add-device")).toBeHidden();
  const res = await other.request.post("api/v1/devices", { headers: HEADERS, data: { name: "PC", mac: "02:00:00:00:00:fe" } });
  expect(res.status()).toBe(403);
  expect((await res.json()).locked).toBe(true);
  await other.context().close();
});

test("Einstellungen gesperrt: erst nach dem Passwort sichtbar", async ({ page, browser }) => {
  await setPassword(page);
  const other = await (await browser.newContext({ locale: "de-DE" })).newPage();
  await other.goto("settings");
  // Gesperrt fragt die Seite gleich nach dem Passwort; Abbrechen lässt den Hinweis stehen.
  await expect(other.locator("#unlock-dialog")).toBeVisible();
  await other.keyboard.press("Escape");
  await expect(other.locator("#set-lockscreen")).toBeVisible();
  await expect(other.locator("#settings-form")).toBeHidden();
  await expect(other.locator("#pw-section")).toBeHidden();
  await other.click("#lockscreen-unlock");
  await enterPassword(other, PASSWORD);
  await expect(other.locator("#set-lockscreen")).toBeHidden();
  await expect(other.locator("#settings-form")).toBeVisible();
  await expect(other.locator("#set-source")).toContainText("Standardwerte");

  // Passwort ändern braucht das bisherige.
  const pw = other.locator("#pw-form");
  await pw.locator('input[name="password"]').fill("anderes-123");
  await pw.locator('input[name="repeat"]').fill("anderes-123");
  await other.click("#pw-save");
  await expect(other.locator("#pw-error")).toHaveText("Gib das bisherige Passwort ein.");
  await pw.locator('input[name="current"]').fill("falsch-123");
  await other.click("#pw-save");
  await expect(other.locator("#pw-error")).toHaveText("Das Passwort stimmt nicht.");
  await expect(pw.locator('input[name="current"]')).toBeFocused();

  // Entfernen (mit Rückfrage) gibt alles wieder frei.
  await pw.locator('input[name="current"]').fill(PASSWORD);
  await other.click("#pw-remove");
  await other.click("#confirm-ok");
  await expect(other.locator("#toasts")).toContainText("Passwort entfernt");
  await expect(other.locator("#pw-state")).toHaveText("Kein Passwort gesetzt – jeder im Netz kann alles ändern.");
  await expect(other.locator("#lock-toggle")).toBeHidden();
  await other.context().close();
});

test("Abgelaufene Entsperrung: Speichern fragt nach dem Passwort und speichert dann", async ({ page, request }) => {
  await offlineDevice(request, "NAS");
  await setPassword(page);
  await page.goto("./");
  await page.getByRole("button", { name: "Bearbeiten" }).click();
  await page.locator("#device-form").locator('input[name="name"]').fill("NAS2");
  await page.context().clearCookies(); // wie nach 12 Stunden ohne Benutzung
  await page.click("#save-device");
  await enterPassword(page, PASSWORD);
  await expect(page.locator(".card h3")).toHaveText(["NAS2"]);
});

test("Entsperr-Dialog und Passwortbereich ohne axe-Befunde", async ({ page, browser }) => {
  await setPassword(page);
  await settled(page);
  let r = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa", "best-practice"]).include("#pw-section").analyze();
  expect(r.violations.map((v) => v.id + ": " + v.nodes.map((n) => n.target).join(", "))).toEqual([]);
  const other = await (await browser.newContext({ locale: "de-DE" })).newPage();
  await other.goto("./");
  await other.click("#lock-toggle");
  await expect(other.locator("#unlock-dialog")).toBeVisible();
  await settled(other);
  r = await new AxeBuilder({ page: other }).withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa", "best-practice"]).include("#unlock-dialog").analyze();
  expect(r.violations.map((v) => v.id + ": " + v.nodes.map((n) => n.target).join(", "))).toEqual([]);
  await other.context().close();
});
