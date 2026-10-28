// Sortieren in der Übersicht: Pfeile (auch über Gruppengrenzen), Ziehen mit der Maus, in leere Gruppen,
// Touch-Ziele und Barrierefreiheit.
const { test, expect, devices } = require("@playwright/test");
const AxeBuilder = require("@axe-core/playwright").default;
const { HEADERS, reset, offlineDevice, settled, smallTargets } = require("./helpers");

test.beforeEach(async ({ request }) => {
  await reset(request);
});
test.afterAll(async ({ request }) => {
  await reset(request);
});

const heads = (page) => page.locator("#devices .sort-group .group-head h2");
const items = (page, group) => page.locator(`#devices .sort-group[data-group="${group}"] .sort-item strong`);

// drag zieht den Griff von handle zur Mitte von target (in kleinen Schritten, wie eine echte Maus).
async function drag(page, handle, target, where = "center") {
  const a = await handle.boundingBox();
  const b = await target.boundingBox();
  await page.mouse.move(a.x + a.width / 2, a.y + a.height / 2);
  await page.mouse.down();
  const x = where === "left" ? b.x + 8 : b.x + b.width / 2;
  const y = where === "top" ? b.y + 4 : b.y + b.height / 2;
  await page.mouse.move(x, y, { steps: 12 });
  await page.mouse.up();
}

test("Mit den Pfeilen sortieren, auch in eine andere Gruppe", async ({ page, request }) => {
  await offlineDevice(request, "Anna", { group: "G1" });
  await offlineDevice(request, "Bert", { group: "G1" });
  await offlineDevice(request, "Carl", { group: "G2" });
  await offlineDevice(request, "Dora");
  await page.goto("./");
  await page.click("#sort-toggle");
  await expect(page.locator("#sort-toggle")).toHaveAttribute("aria-pressed", "true");
  await expect(page.locator("#search")).toBeDisabled();
  await expect(heads(page)).toHaveText(["G1", "G2", "Weitere Geräte"]);

  await page.getByRole("button", { name: "„Bert“ nach vorn" }).click();
  await expect(items(page, "G1")).toHaveText(["Bert", "Anna"]);
  // ganz vorn: „nach vorn“ ist aus, der Fokus wechselt auf „nach hinten“
  await expect(page.getByRole("button", { name: "„Bert“ nach vorn" })).toBeDisabled();
  await expect(page.getByRole("button", { name: "„Bert“ nach hinten" })).toBeFocused();
  await expect(page.locator("#sort-status")).toHaveText("„Bert“: Platz 1 von 2 in „G1“");

  // am Ende einer Gruppe geht es an den Anfang der nächsten
  await page.getByRole("button", { name: "„Anna“ nach hinten" }).click();
  await expect(items(page, "G1")).toHaveText(["Bert"]);
  await expect(items(page, "G2")).toHaveText(["Anna", "Carl"]);
  await expect(page.locator("#sort-status")).toHaveText("„Anna“: Platz 1 von 2 in „G2“");
  await expect(page.getByRole("button", { name: "„Anna“ nach hinten" })).toBeFocused();

  await page.getByRole("button", { name: "Gruppe „G2“ nach oben" }).click();
  await expect(heads(page)).toHaveText(["G2", "G1", "Weitere Geräte"]);
  await expect(page.getByRole("button", { name: "Gruppe „G2“ nach unten" })).toBeFocused();
  await expect(page.locator("#sort-status")).toHaveText("Gruppe „G2“: Platz 1 von 2");

  // Fertig: die Übersicht zeigt die neue Reihenfolge, auch nach dem Neuladen
  await page.click("#sort-done");
  await expect(page.locator("#sort-toggle")).toBeFocused();
  for (const p of [page, null]) {
    if (!p) await page.reload();
    await expect(page.locator("#devices .group-head h2")).toHaveText(["G2", "G1", "Weitere Geräte"]);
    await expect(page.locator('#devices .group-head[data-id="gruppe:G2"] + .grid .card h3')).toHaveText(["Anna", "Carl"]);
  }
});

test("Ziehen mit der Maus: Geräte und Gruppen, auch in eine leere Gruppe", async ({ page, request }) => {
  await offlineDevice(request, "Anna");
  await offlineDevice(request, "Bert");
  await offlineDevice(request, "Carl");
  await page.goto("./");
  await page.click("#sort-toggle");
  // ohne Gruppen: eine Liste
  await drag(page, page.locator('.sort-item:has-text("Carl") .drag-handle'), page.locator('.sort-item:has-text("Anna")'), "left");
  await expect(items(page, "")).toHaveText(["Carl", "Anna", "Bert"]);
  await expect(page.locator("#sort-status")).toHaveText("„Carl“: Platz 1 von 3");
  await expect.poll(async () => (await (await request.get("api/v1/devices")).json()).map((d) => d.name)).toEqual(["Carl", "Anna", "Bert"]);

  // leere Gruppen erscheinen als Ziel
  for (const name of ["Leer", "Zweite"]) expect((await request.post("api/v1/groups", { headers: HEADERS, data: { name } })).ok()).toBeTruthy();
  await expect(heads(page)).toHaveText(["Leer", "Zweite", "Weitere Geräte"]);
  await expect(page.locator('.sort-group[data-group="Leer"] .sort-empty')).toBeVisible();
  await drag(page, page.locator('.sort-item:has-text("Bert") .drag-handle'), page.locator('.sort-group[data-group="Leer"] .sort-list'));
  await expect(items(page, "Leer")).toHaveText(["Bert"]);
  await expect.poll(async () => (await (await request.get("api/v1/devices")).json()).find((d) => d.name === "Bert").group).toBe("Leer");

  // Gruppe am Griff nach oben ziehen
  await drag(page, page.locator('.sort-group[data-group="Zweite"] .drag-handle'), page.locator('.sort-group[data-group="Leer"] .group-head'), "top");
  await expect(heads(page)).toHaveText(["Zweite", "Leer", "Weitere Geräte"]);
  await expect.poll(async () => (await (await request.get("api/v1/groups")).json()).map((g) => g.name)).toEqual(["Zweite", "Leer"]);
});

test("Sortieren ohne axe-Befunde", async ({ page, request }) => {
  await offlineDevice(request, "Anna", { group: "G1" });
  await offlineDevice(request, "Bert");
  await page.goto("./");
  await page.click("#sort-toggle");
  await settled(page);
  const r = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa", "best-practice"]).analyze();
  expect(r.violations.map((v) => v.id + ": " + v.nodes.map((n) => n.target).join(", "))).toEqual([]);
});

test.describe("Touch", () => {
  const { defaultBrowserType, ...pixel } = devices["Pixel 7"]; // eslint-disable-line no-unused-vars
  test.use(pixel);
  test("Sortieren: alle Ziele mindestens 44 × 44 px", async ({ page, request }) => {
    await offlineDevice(request, "Anna", { group: "G1" });
    await offlineDevice(request, "Bert");
    await page.goto("./");
    await page.click("#sort-toggle");
    await expect(page.locator(".sort-item")).toHaveCount(2);
    expect(await smallTargets(page)).toEqual([]);
  });
});
