// Einstellungsseite: Netze und VLANs, gesperrte Felder, Fehler, Testnachricht, Zurücksetzen,
// Übernahme ohne Neustart und Barrierefreiheit.
const http = require("http");
const { test, expect, devices } = require("@playwright/test");
const AxeBuilder = require("@axe-core/playwright").default;
const { reset, addDevice, settled, smallTargets } = require("./helpers");

test.beforeEach(async ({ request }) => {
  await reset(request);
});
test.afterAll(async ({ request }) => {
  await reset(request);
});

async function open(page) {
  await page.goto("settings");
  await expect(page.locator("#set-source")).toContainText("Standardwerte");
}

test("Schnittstelle als Netz übernehmen, benennen und speichern", async ({ page }) => {
  await open(page);
  await expect(page.locator("#net-empty")).toBeVisible();
  const adopt = page.locator("#if-list button").first(); // die Schnittstelle dieses Rechners
  await expect(adopt).toBeVisible();
  await adopt.click();
  const row = page.locator(".net-row").last();
  await expect(row.locator('input[name="net-name"]')).toBeFocused();
  await row.locator('input[name="net-name"]').fill("Server");
  await row.locator('input[name="net-vlan"]').fill("30");
  await expect(row.locator("legend")).toHaveText("Server (VLAN 30)");
  await expect(page.locator("#set-dirty")).toBeVisible();
  await page.click("#set-save");
  await expect(page.locator("#toasts")).toContainText("Gespeichert");
  await expect(page.locator(".net-row .net-status.ok")).toContainText("Direkt angeschlossen über");
  await expect(page.locator("#set-source")).toContainText("1 Einstellung hier geändert");
  await expect(page.locator("#if-list .pill")).toHaveText(["in der Liste"]);

  await page.reload(); // gespeichert
  await expect(page.locator(".net-row legend")).toHaveText(["Server (VLAN 30)"]);

  // Die Übersicht kennt das Netz sofort: als Broadcast-Vorschlag im Gerätedialog
  await page.goto("./");
  await page.click("#add-device");
  await expect(page.locator("#broadcast-list option")).toHaveCount(1);
  expect(await page.locator("#broadcast-list option").getAttribute("label")).toBe("Server (VLAN 30)");
});

test("Netz ohne Schnittstelle wird als nicht direkt erreichbar gezeigt", async ({ page }) => {
  await open(page);
  await page.click("#net-add");
  const row = page.locator(".net-row").last();
  await expect(row.locator('input[name="net-cidr"]')).toBeFocused();
  await row.locator('input[name="net-cidr"]').fill("198.51.100.0/24");
  await row.locator('input[name="net-name"]').fill("IoT");
  await row.locator('input[name="net-scan"]').uncheck();
  await page.click("#set-save");
  await expect(row.locator(".net-status.warn")).toContainText("198.51.100.255:9");
  // Entfernen
  await page.getByRole("button", { name: "Netz IoT entfernen" }).click();
  await expect(page.locator(".net-row")).toHaveCount(0);
  await expect(page.locator("#net-add")).toBeFocused();
});

test("Umgebungsvariablen sperren ihre Felder", async ({ page }) => {
  await open(page);
  // server.sh setzt WOLWEB_WAKE_RETRY, WOLWEB_WAKE_TIMEOUT, WOLWEB_BCASTIP und WOLWEB_LANGUAGE
  for (const [name, env] of [["status.wake_retry", "WOLWEB_WAKE_RETRY"], ["bcastip", "WOLWEB_BCASTIP"], ["language", "WOLWEB_LANGUAGE"]]) {
    await expect(page.locator(`[name="${name}"]`)).toBeDisabled();
    await expect(page.locator(`[data-key="${name}"] .lock-note`)).toContainText(env);
  }
  await expect(page.locator('[name="status.interval"]')).toBeEnabled();
});

test("Fehler stehen am Feld", async ({ page }) => {
  await open(page);
  await page.fill('[name="status.interval"]', "1s");
  await page.click("#set-save");
  await expect(page.locator("#set-error")).toContainText("mindestens 5s");
  await expect(page.locator('[name="status.interval"]')).toHaveAttribute("aria-invalid", "true");
  await expect(page.locator('[name="status.interval"]')).toBeFocused();
  // Eingabefehler, die der Browser schon erkennt
  await page.fill('[name="status.interval"]', "30s");
  await page.fill('[name="status.ports"]', "22, ssh");
  await page.click("#set-save");
  await expect(page.locator("#set-error")).toContainText("„ssh“ ist kein Port");
  await expect(page.locator("#set-source")).not.toContainText("geändert");
});

test("Änderungen gelten sofort, Zurücksetzen stellt config.json wieder her", async ({ page, request }) => {
  await open(page);
  await page.fill('[name="status.ports"]', "22, 8399");
  await page.fill('[name="discovery.interval"]', "2h");
  await page.click("#set-save");
  await expect(page.locator("#toasts")).toContainText("Gespeichert");
  let info = await (await request.get("api/v1/info")).json();
  expect(info.ports).toEqual([22, 8399]);

  await page.click("#set-reset");
  await expect(page.locator("#confirm-dialog")).toBeVisible();
  await page.click("#confirm-ok");
  await expect(page.locator("#toasts")).toContainText("Zurückgesetzt");
  await expect(page.locator('[name="discovery.interval"]')).toHaveValue("30m");
  await expect(page.locator("#set-reset")).toBeHidden();
  info = await (await request.get("api/v1/info")).json();
  expect(info.ports).toHaveLength(10);
});

test("Testnachricht an den Webhook", async ({ page }) => {
  const received = [];
  const hook = http.createServer((req, res) => {
    let body = "";
    req.on("data", (c) => { body += c; });
    req.on("end", () => { received.push(JSON.parse(body)); res.end("ok"); });
  });
  await new Promise((r) => hook.listen(0, "127.0.0.1", r));
  try {
    await open(page);
    await page.fill('[name="notify.webhook"]', `http://127.0.0.1:${hook.address().port}/hook`);
    await page.click("#notify-test");
    await expect(page.locator("#toasts")).toContainText("Testnachricht gesendet");
    expect(received.map((e) => e.event)).toEqual(["test"]);
    await page.fill('[name="notify.webhook"]', "ftp://falsch");
    await page.click("#notify-test");
    await expect(page.locator("#set-error")).toContainText("keine http(s)-Adresse");
  } finally {
    hook.close();
  }
});

test("Übersicht verlinkt die Einstellungen", async ({ page, request }) => {
  await addDevice(request, { name: "NAS" });
  await page.goto("./");
  await page.getByRole("link", { name: "Einstellungen" }).click();
  await expect(page).toHaveURL(/\/settings$/);
  await page.getByRole("link", { name: "Übersicht" }).click();
  await expect(page.locator(".card")).toHaveCount(1);
});

const TAGS = ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa", "best-practice"];
for (const colorScheme of ["light", "dark"]) {
  test(`axe: Einstellungen (${colorScheme})`, async ({ page }) => {
    await page.emulateMedia({ colorScheme });
    await open(page);
    await page.click("#if-list button >> nth=0");
    await page.click("#net-add");
    await page.locator(".help summary").click();
    await settled(page);
    const r = await new AxeBuilder({ page }).withTags(TAGS).analyze();
    expect(r.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(" ")).join(", ")}`)).toEqual([]);
  });
}

test("Tastatur: Sprunglink, Speichern mit Enter", async ({ page }) => {
  await open(page);
  await page.keyboard.press("Tab");
  await expect(page.locator(".skip-link")).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(page.locator("#settings-form")).toBeFocused();
  await expect(page).toHaveURL(/\/settings$/);
  await page.locator('[name="discovery.interval"]').fill("1h");
  await page.keyboard.press("Enter"); // Formular abschicken
  await expect(page.locator("#toasts")).toContainText("Gespeichert");
});

test.describe("Touch", () => {
  const { defaultBrowserType, ...pixel } = devices["Pixel 7"]; // eslint-disable-line no-unused-vars
  test.use(pixel);
  test("Einstellungen: alle Ziele mindestens 44 × 44 px", async ({ page }) => {
    await open(page);
    await page.evaluate(() => document.querySelector("#if-list button").click());
    await page.evaluate(() => { document.querySelector(".help").open = true; });
    expect(await smallTargets(page)).toEqual([]);
  });
});
