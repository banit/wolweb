// Privatsphäre: Online-Status nur nach dem Entsperren oder für niemanden – Übersicht, Weckseite,
// Einstellungsseite.
const { test, expect } = require("@playwright/test");
const { PASSWORD, reset, onlineDevice } = require("./helpers");

test.beforeEach(async ({ request }) => {
  await reset(request);
});
test.afterAll(async ({ request }) => {
  await reset(request);
});

// choose stellt die Privatsphäre auf der Einstellungsseite ein (page muss sie sehen dürfen).
async function choose(page, label) {
  await page.goto("settings");
  await page.getByRole("radio", { name: label }).check();
  await page.click("#set-save");
  await expect(page.locator("#toasts")).toContainText("Gespeichert");
}

async function setPassword(page) {
  await page.goto("settings");
  await page.locator('#pw-form input[name="password"]').fill(PASSWORD);
  await page.locator('#pw-form input[name="repeat"]').fill(PASSWORD);
  await page.click("#pw-save");
  await expect(page.locator("#pw-state")).toHaveText("Ein Passwort ist gesetzt.");
}

const card = (page, name) => page.locator(".card", { has: page.locator("h3", { hasText: name }) });

test("Nur nach dem Entsperren: Gäste sehen keinen Status, Entsperrte schon", async ({ page, browser, request }) => {
  await onlineDevice(request, "NAS", { group: "Büro" });
  await setPassword(page);
  await choose(page, "Nur nach dem Entsperren anzeigen");
  await expect(page.locator("#privacy-no-pw")).toBeHidden(); // Passwort ist gesetzt

  // Wer entsperrt hat, sieht den Status weiter (Fehlersuche).
  await page.goto("./");
  await expect(card(page, "NAS").locator(".pill.online")).toBeVisible({ timeout: 15000 });

  const guest = await (await browser.newContext({ locale: "de-DE" })).newPage();
  await guest.goto("./");
  const c = card(guest, "NAS");
  await expect(c).toHaveAttribute("data-state", "hidden");
  await expect(c.locator(".pill")).toHaveCount(0);
  await expect(guest.locator("#filter")).toBeHidden();
  await expect(guest.locator("#summary")).toHaveText("1 Gerät");
  await expect(guest.locator('.group-head[data-id="gruppe:Büro"]')).toContainText("1 Gerät");
  await expect(c.getByRole("button", { name: "Wecken", exact: true })).toBeVisible(); // nicht „Erneut wecken“
  await c.getByRole("button", { name: "Wecken", exact: true }).click();
  await expect(guest.locator("#toasts")).toContainText("Ob das Gerät läuft, zeigt die Übersicht nicht an");
  await expect(c.locator(".pill.waking")).toHaveCount(0);
  // Auch die API verrät nichts.
  const devs = await (await guest.request.get("api/v1/devices")).json();
  expect(devs[0].status).toBeUndefined();

  // Entsperren zeigt den Status.
  await guest.click("#lock-toggle");
  await guest.locator("#unlock-password").fill(PASSWORD);
  await guest.keyboard.press("Enter");
  await expect(c.locator(".pill.online")).toBeVisible({ timeout: 15000 });
  await expect(guest.locator("#filter")).toBeVisible();
  await guest.context().close();
});

test("Weckseite ohne Status: nur senden, keine Startverfolgung", async ({ page, browser, request }) => {
  await onlineDevice(request, "NAS");
  await setPassword(page);
  await choose(page, "Nur nach dem Entsperren anzeigen");
  const guest = await (await browser.newContext({ locale: "de-DE" })).newPage();
  await guest.goto("wake/NAS");
  await expect(guest.locator('[data-step="check"] [data-detail]')).toHaveText("Online-Status ausgeblendet");
  await expect(guest.locator("#wake-result")).toContainText("Weckruf gesendet");
  await expect(guest.locator("#wake-result")).toContainText("Privatsphäre");
  await expect(guest.locator("#tech-answer")).toHaveCount(0);
  await expect(guest.locator("#wake")).toHaveAttribute("data-state", "sent");
  await guest.context().close();
});

test("Für niemanden: auch Entsperrte sehen keinen Status", async ({ page, request }) => {
  await onlineDevice(request, "NAS");
  await setPassword(page);
  await choose(page, "Für niemanden anzeigen");
  await page.goto("./");
  await expect(card(page, "NAS")).toHaveAttribute("data-state", "hidden");
  await expect(page.locator("#summary")).toHaveText("1 Gerät");
  // Suchtreffer bleiben für Entsperrte sichtbar (zum Übernehmen von Geräten).
  expect((await (await page.request.get("api/v1/discovery")).json()).hidden).toBe(false);
});

test("Ohne Passwort weist die Seite darauf hin, dass dann niemand den Status sieht", async ({ page }) => {
  await page.goto("settings");
  await page.getByRole("radio", { name: "Nur nach dem Entsperren anzeigen" }).check();
  await expect(page.locator("#privacy-no-pw")).toBeVisible();
  await expect(page.getByRole("radio", { name: "Nur nach dem Entsperren anzeigen" })).toHaveAttribute("aria-describedby", /privacy-no-pw/);
  await page.getByRole("radio", { name: "Für alle anzeigen" }).check();
  await expect(page.locator("#privacy-no-pw")).toBeHidden();
});
