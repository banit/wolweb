// Gruppen: auf der Einstellungsseite anlegen, sortieren, umbenennen, löschen; in der Übersicht über
// den Stift am Gruppenkopf bearbeiten.
const { test, expect } = require("@playwright/test");
const AxeBuilder = require("@axe-core/playwright").default;
const { HEADERS, reset, offlineDevice, settled } = require("./helpers");

test.beforeEach(async ({ request }) => {
  await reset(request);
});
test.afterAll(async ({ request }) => {
  await reset(request);
});

const rows = (page) => page.locator("#grp-list .grp-row strong");

async function saveGroup(page, name, members) {
  const d = page.locator("#group-dialog");
  await expect(d).toBeVisible();
  await d.locator('input[name="name"]').fill(name);
  for (const m of members || []) await d.getByLabel(m).check();
  await page.click("#group-save");
  await expect(d).toBeHidden();
}

test("Gruppen auf der Einstellungsseite anlegen, sortieren, umbenennen und löschen", async ({ page, request }) => {
  await offlineDevice(request, "NAS");
  await offlineDevice(request, "PC");
  await page.goto("settings");
  await expect(page.locator("#grp-empty")).toBeVisible();

  // Anlegen: mit Mitglied und leer
  await page.click("#grp-add");
  await expect(page.locator("#group-dialog-title")).toHaveText("Neue Gruppe");
  await expect(page.locator('#group-dialog input[name="name"]')).toBeFocused();
  await saveGroup(page, "Büro", ["NAS"]);
  await expect(page.locator("#toasts")).toContainText("Gruppe „Büro“ angelegt");
  await page.click("#grp-add");
  await saveGroup(page, "Keller");
  await expect(rows(page)).toHaveText(["Büro", "Keller"]);
  await expect(page.locator(".grp-row").first()).toContainText("1 Gerät");

  // Doppelter Name: Fehler am Feld
  await page.click("#grp-add");
  await page.locator('#group-dialog input[name="name"]').fill("büro");
  await page.click("#group-save");
  await expect(page.locator("#group-error")).toHaveText("Es gibt schon eine Gruppe namens „Büro“.");
  await expect(page.locator('#group-dialog input[name="name"]')).toHaveAttribute("aria-invalid", "true");
  await page.click("#group-cancel");

  // Sortieren mit den Pfeilen: Fokus bleibt am verschobenen Eintrag
  await page.getByRole("button", { name: "„Keller“ nach oben" }).click();
  await expect(rows(page)).toHaveText(["Keller", "Büro"]);
  await expect(page.getByRole("button", { name: "„Keller“ nach unten" })).toBeFocused();
  await expect(page.locator("#grp-status")).toHaveText("„Keller“ ist jetzt an Stelle 1 von 2.");

  // Umbenennen und Mitglieder ändern; der alte Gruppen-Wecklink gilt weiter
  await page.getByRole("button", { name: "„Büro“ bearbeiten" }).click();
  await page.locator('#group-dialog input[name="name"]').fill("Office");
  await expect(page.locator("#group-name-hint")).toContainText("alten Namen „Büro“");
  await page.locator("#group-dialog").getByLabel("PC").check();
  await page.click("#group-save");
  await expect(rows(page)).toHaveText(["Keller", "Office"]);
  await expect(page.getByRole("button", { name: "„Office“ bearbeiten" })).toBeFocused();
  const res = await request.post("wake-group/B%C3%BCro", { headers: HEADERS });
  expect(res.ok()).toBeTruthy();
  expect((await res.json()).group).toBe("Office");

  // Die Übersicht zeigt die Gruppen in dieser Reihenfolge; leere bleiben dort unsichtbar
  await page.goto("./");
  await expect(page.locator("#devices .group-head h2")).toHaveText(["Office"]);
  await page.goto("settings");

  // Löschen: Geräte bleiben
  await page.getByRole("button", { name: "„Office“ bearbeiten" }).click();
  await page.click("#group-delete");
  await expect(page.locator("#confirm-title")).toHaveText("Gruppe „Office“ löschen?");
  await page.click("#confirm-ok");
  await expect(page.locator("#toasts")).toContainText("Gruppe „Office“ gelöscht");
  await expect(rows(page)).toHaveText(["Keller"]);
  expect((await (await request.get("api/v1/devices")).json()).length).toBe(2);
});

test("Stift am Gruppenkopf: Gruppe in der Übersicht bearbeiten", async ({ page, request }) => {
  await offlineDevice(request, "NAS", { group: "Homelab" });
  await offlineDevice(request, "PC");
  await page.goto("./");
  await page.getByRole("button", { name: "Gruppe bearbeiten" }).click();
  await expect(page.locator("#group-dialog-title")).toHaveText("Gruppe „Homelab“ bearbeiten");
  await expect(page.locator("#group-dialog").getByLabel("NAS")).toBeChecked();
  await saveGroup(page, "Server", ["PC"]);
  await expect(page.locator("#devices .group-head h2")).toHaveText(["Server"]);
  await expect(page.locator('#devices .group-head[data-id="gruppe:Server"] + .grid .card h3')).toHaveText(["NAS", "PC"]);
  await expect(page.getByRole("button", { name: "Gruppe bearbeiten" })).toBeFocused();
});

test("Gruppendialog ohne axe-Befunde", async ({ page, request }) => {
  await offlineDevice(request, "NAS", { group: "Homelab" });
  await page.goto("./");
  await page.getByRole("button", { name: "Gruppe bearbeiten" }).click();
  await settled(page);
  const r = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa", "best-practice"]).include("#group-dialog").analyze();
  expect(r.violations.map((v) => v.id + ": " + v.nodes.map((n) => n.target).join(", "))).toEqual([]);
});

// Rückfrage ohne Wahl geschlossen: close() ohne Wert – so schließen ältere Browser auch bei Escape
// (Chromium setzt dabei den Wert selbst zurück).
test("Geschlossene Rückfrage löscht nicht, auch nach einer bestätigten", async ({ page, request }) => {
  await offlineDevice(request, "NAS", { group: "Homelab" });
  await offlineDevice(request, "Alt");
  await page.goto("./");
  // erst eine Rückfrage mit „Löschen“ bestätigen (Gerät „Alt“)
  await page.locator('.card:has-text("Alt")').getByRole("button", { name: "Bearbeiten" }).click();
  await page.click("#delete-device");
  await page.click("#confirm-ok");
  await expect(page.locator(".card h3")).toHaveText(["NAS"]);
  // dann die Gruppe löschen wollen – und mit Escape abbrechen
  await page.getByRole("button", { name: "Gruppe bearbeiten" }).click();
  await page.click("#group-delete");
  await expect(page.locator("#confirm-dialog")).toBeVisible();
  await page.evaluate(() => document.getElementById("confirm-dialog").close());
  await expect(page.locator("#confirm-dialog")).toBeHidden();
  await expect(page.locator("#group-dialog")).toBeVisible();
  expect((await (await request.get("api/v1/groups")).json()).map((g) => g.name)).toEqual(["Homelab"]);
});

test("Früherer Name einer anderen Gruppe nur mit Rückfrage", async ({ page, request }) => {
  await offlineDevice(request, "NAS", { group: "Büro" });
  expect((await request.put("api/v1/groups/B%C3%BCro", { headers: HEADERS, data: { name: "Office" } })).ok()).toBeTruthy();
  await page.goto("settings");
  await page.click("#grp-add");
  await page.locator('#group-dialog input[name="name"]').fill("Büro");
  await page.click("#group-save");
  await expect(page.locator("#confirm-title")).toHaveText("Namen „Büro“ übernehmen?");
  await expect(page.locator("#confirm-text")).toContainText("wecken bisher „Office“");
  await page.keyboard.press("Escape"); // nicht übernehmen
  await expect(page.locator("#group-error")).toContainText("war früher der Name der Gruppe „Office“");
  expect((await (await request.get("api/v1/groups")).json()).map((g) => g.name)).toEqual(["Office"]);
  await page.click("#group-save");
  await page.click("#confirm-ok");
  await expect(page.locator("#group-dialog")).toBeHidden();
  await expect(rows(page)).toHaveText(["Office", "Büro"]);
});
