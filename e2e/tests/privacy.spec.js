// Privatsphäre: Online-Status bzw. „Zuletzt geweckt“ und Verlauf nur nach dem Entsperren oder für
// niemanden – Übersicht, Weckseite, Einstellungsseite.
const { test, expect } = require("@playwright/test");
const { HEADERS, PASSWORD, reset, onlineDevice, offlineDevice } = require("./helpers");

test.beforeEach(async ({ request }) => {
  await reset(request);
});
test.afterAll(async ({ request }) => {
  await reset(request);
});

// Beide Auswahlen der Privatsphäre haben dieselben Beschriftungen – gefunden wird über die Gruppe.
const STATUS = "Online-Status in der Übersicht";
const WAKES = "„Zuletzt geweckt“ und Verlauf";
const radio = (page, label, group = STATUS) => page.getByRole("group", { name: group }).getByRole("radio", { name: label });

// choose stellt die Privatsphäre auf der Einstellungsseite ein (page muss sie sehen dürfen).
async function choose(page, label, group) {
  await page.goto("settings");
  await radio(page, label, group).check();
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
  // Wer weckt, startet das Gerät ohnehin: Die Karte verfolgt den Start und blendet den Status danach wieder aus.
  await c.getByRole("button", { name: "Wecken", exact: true }).click();
  await expect(guest.locator("#toasts")).toContainText("NAS ist online", { timeout: 15000 });
  await expect(c).toHaveAttribute("data-state", "hidden");
  await expect(c.locator(".pill")).toHaveCount(0);
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

test("Wecklink trotz ausgeblendetem Status: ohne Vorabprüfung, aber mit Startbestätigung", async ({ page, browser, request }) => {
  await onlineDevice(request, "NAS", { group: "Büro" });
  await setPassword(page);
  await choose(page, "Nur nach dem Entsperren anzeigen");
  const guest = await (await browser.newContext({ locale: "de-DE" })).newPage();
  // Gerät: Wer weckt, startet es ohnehin – die Seite zeigt, ob es hochkommt.
  await guest.goto("wake/NAS");
  await expect(guest.locator('[data-step="check"] [data-detail]')).toHaveText("Übersprungen (Privatsphäre)");
  await expect(guest.locator("#wake")).toHaveAttribute("data-state", "online", { timeout: 15000 });
  await expect(guest.locator('[data-step="online"]')).toHaveAttribute("data-s", "done");
  // Gruppe ebenso
  await guest.goto("wake-group/B%C3%BCro");
  await expect(guest.locator("#group")).toHaveAttribute("data-state", "online", { timeout: 15000 });
  // Die Übersicht bleibt ohne Status.
  await guest.goto("./");
  await expect(card(guest, "NAS")).toHaveAttribute("data-state", "hidden");
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
  await radio(page, "Nur nach dem Entsperren anzeigen").check();
  await expect(page.locator("#privacy-no-pw")).toBeVisible();
  await expect(radio(page, "Nur nach dem Entsperren anzeigen")).toHaveAttribute("aria-describedby", /privacy-no-pw/);
  await expect(page.locator("#wakes-no-pw")).toBeHidden(); // die andere Auswahl steht noch auf „für alle“
  await radio(page, "Für alle anzeigen (Standard)").check();
  await expect(page.locator("#privacy-no-pw")).toBeHidden();
  await radio(page, "Nur nach dem Entsperren anzeigen", WAKES).check();
  await expect(page.locator("#wakes-no-pw")).toBeVisible();
  await expect(radio(page, "Nur nach dem Entsperren anzeigen", WAKES)).toHaveAttribute("aria-describedby", /wakes-no-pw/);
});

test("„Zuletzt geweckt“ und Verlauf: eigene Auswahl, nur nach dem Entsperren", async ({ page, browser, request }) => {
  const d = await offlineDevice(request, "PC");
  await setPassword(page);
  // Standard: für alle – der Online-Status bleibt davon unberührt.
  await expect(radio(page, "Für alle anzeigen (Standard)", WAKES)).toBeChecked();
  await choose(page, "Nur nach dem Entsperren anzeigen", WAKES);
  await expect(radio(page, "Für alle anzeigen (Standard)")).toBeChecked();
  expect((await request.post("api/v1/devices/" + d.id + "/wake", { headers: HEADERS })).ok()).toBeTruthy();

  // Entsperrt: „geweckt vor …“ auf der Karte und der Verlauf
  await page.goto("./");
  await expect(card(page, "PC")).toContainText("geweckt");
  await expect(page.locator("#open-history")).toBeVisible();

  const guest = await (await browser.newContext({ locale: "de-DE" })).newPage();
  await guest.goto("./");
  const c = card(guest, "PC");
  await expect(c.locator(".pill.offline")).toBeVisible({ timeout: 15000 }); // Status bleibt sichtbar
  await expect(c).not.toContainText("geweckt");
  await expect(guest.locator("#open-history")).toBeHidden();
  const hist = await guest.request.get("api/v1/history");
  expect(hist.status()).toBe(403);
  expect((await hist.json()).hidden).toBe(true);
  expect((await (await guest.request.get("api/v1/devices")).json())[0].last_wake).toBeUndefined();

  // Entsperren zeigt beides.
  await guest.click("#lock-toggle");
  await guest.locator("#unlock-password").fill(PASSWORD);
  await guest.keyboard.press("Enter");
  await expect(c).toContainText("geweckt");
  await guest.click("#open-history");
  await expect(guest.locator("#history-list li").first()).toContainText("PC");
  await guest.context().close();
});

test("Aufzeichnung: Aufbewahrung einstellen, Ausschalten löscht den Verlauf nach Rückfrage", async ({ page, request }) => {
  const d = await offlineDevice(request, "PC");
  expect((await request.post("api/v1/devices/" + d.id + "/wake", { headers: HEADERS })).ok()).toBeTruthy();
  await page.goto("settings");
  const days = page.getByRole("spinbutton", { name: "Aufbewahren (Tage)" });
  const record = page.getByRole("checkbox", { name: "Weckverlauf und „zuletzt online“ aufzeichnen" });
  await expect(days).toHaveValue("30");
  await expect(record).toBeChecked();
  // Ungültige Dauer: Meldung am Feld, nichts gespeichert
  await days.fill("400");
  await page.click("#set-save");
  await expect(page.locator("#set-error")).toHaveText("Aufbewahrung: eine ganze Zahl von 1 bis 365 Tagen.");
  await expect(days).toHaveAttribute("aria-invalid", "true");
  await expect(days).toBeFocused();
  // Ausschalten fragt nach; Abbrechen ändert nichts.
  await days.fill("30");
  await record.uncheck();
  await page.click("#set-save");
  await expect(page.locator("#confirm-title")).toHaveText("Aufzeichnung ausschalten?");
  await page.locator("#confirm-dialog").evaluate((dlg) => dlg.close());
  expect((await (await request.get("api/v1/history")).json()).events).not.toHaveLength(0); // auch aus früheren Tests
  await page.click("#set-save");
  await page.click("#confirm-ok");
  await expect(page.locator("#toasts")).toContainText("Gespeichert");
  expect((await (await request.get("api/v1/history")).json()).events).toHaveLength(0);
  await page.goto("./");
  await expect(card(page, "PC").locator(".pill.offline")).toBeVisible({ timeout: 15000 });
  await expect(card(page, "PC")).not.toContainText("geweckt");
  await expect(page.locator("#open-history")).toBeHidden();
});
