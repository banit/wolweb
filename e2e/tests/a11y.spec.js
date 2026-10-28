// Barrierefreiheit: axe-core (WCAG 2.2 AA und best-practice) auf allen Seiten und Dialogen, Tastatur,
// Screenreader-Namen und Touch-Ziele.
const { test, expect, devices } = require("@playwright/test");
const AxeBuilder = require("@axe-core/playwright").default;
const { reset, addDevice, onlineDevice, offlineDevice, settled, smallTargets, contrast } = require("./helpers");

const TAGS = ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa", "best-practice"];

async function expectAccessible(page) {
  await settled(page);
  const r = await new AxeBuilder({ page }).withTags(TAGS).analyze();
  const found = r.violations.map((v) => `${v.id}: ${v.help} – ${v.nodes.map((n) => n.target.join(" ")).join(", ")}`);
  expect(found).toEqual([]);
}

test.beforeEach(async ({ request }) => {
  await reset(request);
  await offlineDevice(request, "NAS", { group: "Homelab" });
  await onlineDevice(request, "Proxmox", { group: "Homelab" });
  await addDevice(request, { name: "Büro-PC", icon: "laptop", note: "Schreibtisch", schedule: ["Mo-Fr 07:30"] });
});

async function overview(page, url = "./") {
  await page.goto(url);
  await expect(page.locator(".card")).toHaveCount(3);
  await expect(page.locator(".card", { hasText: "Proxmox" }).locator(".pill.online")).toBeVisible({ timeout: 15000 });
}

for (const colorScheme of ["light", "dark"]) {
  test.describe(`axe (${colorScheme})`, () => {
    test.use({ colorScheme });

    test("Übersicht", async ({ page }) => {
      await overview(page);
      await expectAccessible(page);
    });

    test("Gerätedialog mit erweiterten Feldern", async ({ page }) => {
      await overview(page);
      await page.locator(".card", { hasText: "Büro-PC" }).locator('[data-focus="edit"]').click();
      await page.locator(".advanced summary").click();
      await expectAccessible(page);
    });

    test("Netzwerksuche und Verlauf", async ({ page, request }) => {
      await overview(page);
      await page.click("#open-discovery");
      await expect(page.locator("#discovery")).toHaveClass(/open/);
      await expectAccessible(page);
      await page.keyboard.press("Escape");
      await request.post("api/v1/groups/Homelab/wake", { headers: { "X-WolWeb": "1" } });
      await page.click("#open-history");
      await expect(page.locator("#history-list li", { hasText: "Proxmox" }).first()).toBeVisible();
      await expectAccessible(page);
    });

    test("Neu angelegt: nächste Schritte und Handy-Anleitung", async ({ page }) => {
      await overview(page);
      await page.click("#add-device");
      await page.fill('#device-form input[name="name"]', "Werkstatt");
      await page.fill('#device-form input[name="mac"]', "02:00:00:00:00:c1");
      await page.click("#save-device");
      const next = page.locator(".card-next");
      await expect(next).toBeVisible();
      await expectAccessible(page);
      await next.getByRole("button", { name: "Auf dem Handy einrichten" }).click();
      await expect(page.locator("#phone-dialog")).toBeVisible();
      await expectAccessible(page);
    });

    test("Löschen bestätigen und Treffer-Plakette (gefüllte Fehlerflächen)", async ({ page }) => {
      await overview(page);
      await expect(page.locator("#new-badge")).toBeVisible(); // Testserver: zwei neue Suchtreffer
      expect(await contrast(page, "#new-badge")).toBeGreaterThanOrEqual(4.5);
      await expectAccessible(page);
      await page.locator(".card", { hasText: "Büro-PC" }).locator('[data-focus="edit"]').click();
      await page.click("#delete-device");
      await expect(page.locator("#confirm-dialog")).toBeVisible();
      expect(await contrast(page, "#confirm-ok")).toBeGreaterThanOrEqual(4.5);
      await expectAccessible(page);
    });

    test("Mehrfachauswahl", async ({ page }) => {
      await overview(page);
      await page.click("#select-toggle");
      await page.getByRole("checkbox", { name: "NAS auswählen" }).check();
      await expect(page.locator("#select-bar")).toBeVisible();
      await expectAccessible(page);
    });

    test("einfache Ansicht", async ({ page }) => {
      await overview(page, "./?view=simple");
      await expectAccessible(page);
    });

    test("Weck- und Gruppenseite", async ({ page }) => {
      await page.goto("wake/" + encodeURIComponent("Büro-PC"));
      await expect(page.locator("#wake")).toHaveAttribute("data-state", "sent");
      await expectAccessible(page);
      await page.goto("wake-group/Homelab");
      await expect(page.locator("#group")).toHaveAttribute("data-state", "wait");
      await expectAccessible(page);
    });
  });
}

test("englische Oberfläche", async ({ page }) => {
  await overview(page, "./?lang=en");
  await expect(page.locator("html")).toHaveAttribute("lang", "en");
  await expectAccessible(page);
});

test("Screenreader: Knöpfe nennen ihr Gerät, Filter ihren Zustand", async ({ page }) => {
  await overview(page);
  const card = page.locator(".card", { hasText: "Büro-PC" });
  await expect(card).toHaveAccessibleName("Büro-PC");
  for (const key of ["wake", "edit", "copy", "open"]) {
    await expect(card.locator(`[data-focus="${key}"]`)).toHaveAccessibleDescription("Büro-PC");
  }
  await expect(page.getByRole("group", { name: "Geräte filtern" }).getByRole("button", { name: /Alle/, pressed: true })).toBeVisible();
  await expect(page.locator("#devices")).not.toHaveAttribute("aria-live", /.*/); // keine Daueransage beim Neuzeichnen
});

test("Tastatur: Sprunglink, Suchleiste, Dialog", async ({ page }) => {
  await overview(page);
  await page.keyboard.press("Tab");
  await expect(page.locator(".skip-link")).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(page.locator("#devices")).toBeFocused();
  await expect(page).toHaveURL(/\/$/); // kein Neuladen
  await page.keyboard.press("Tab");
  expect(await page.evaluate(() => document.querySelector("#devices").contains(document.activeElement))).toBe(true);

  // Suchleiste: Tab bleibt darin, Escape schließt und gibt den Fokus zurück
  await page.locator("#open-discovery").focus();
  await page.keyboard.press("Enter");
  await expect(page.locator("#discovery")).toHaveClass(/open/);
  for (let i = 0; i < 12; i++) {
    await page.keyboard.press("Tab");
    const escaped = await page.evaluate(() => {
      const a = document.activeElement;
      return a !== document.body && !document.querySelector("#discovery").contains(a);
    });
    expect(escaped, `Tab ${i + 1} hat die Leiste verlassen`).toBe(false);
  }
  await page.keyboard.press("Escape");
  await expect(page.locator("#discovery")).not.toHaveClass(/open/);
  await expect(page.locator("#open-discovery")).toBeFocused();

  // "/" springt in die Suche, Escape schließt den Gerätedialog
  await page.locator("#add-device").focus();
  await page.keyboard.press("Enter");
  await expect(page.locator("#device-dialog")).toBeVisible();
  await expect(page.locator('#device-form input[name="name"]')).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(page.locator("#device-dialog")).toBeHidden();
  await page.locator("body").press("/");
  await expect(page.locator("#search")).toBeFocused();
});

test.describe("Touch", () => {
  const { defaultBrowserType, ...pixel } = devices["Pixel 7"]; // eslint-disable-line no-unused-vars
  test.use(pixel);

  test("alle Ziele mindestens 44 × 44 px", async ({ page }) => {
    await overview(page);
    expect(await smallTargets(page), "Übersicht").toEqual([]);
    await page.click("#select-toggle");
    expect(await smallTargets(page), "Mehrfachauswahl").toEqual([]);
    await page.click("#sel-done");
    await page.evaluate(() => document.querySelector("#add-device").click());
    await page.evaluate(() => { document.querySelector(".advanced").open = true; });
    expect(await smallTargets(page), "Gerätedialog").toEqual([]);
    await page.keyboard.press("Escape");
    await page.evaluate(() => document.querySelector("#open-discovery").click());
    await expect(page.locator("#discovery")).toHaveClass(/open/);
    expect(await smallTargets(page), "Netzwerksuche").toEqual([]);
    await overview(page, "./?view=simple");
    expect(await smallTargets(page), "einfache Ansicht").toEqual([]);
    await page.goto("wake/" + encodeURIComponent("Büro-PC"));
    await expect(page.locator("#wake")).toHaveAttribute("data-state", "sent");
    expect(await smallTargets(page), "Weckseite").toEqual([]);
  });

  test("Hauptknöpfe: Name für Screenreader, kurze Beschriftung sichtbar", async ({ page }) => {
    await overview(page);
    // Der Suchknopf nennt neue Treffer mit („Geräte suchen – 1 neu“).
    for (const [name, visible] of [[/^Geräte suchen( – \d+ neu)?$/, "Suchen"], [/^Gerät hinzufügen$/, "Hinzufügen"]]) {
      const b = page.getByRole("button", { name });
      await expect(b).toBeVisible();
      await expect(b.getByText(visible, { exact: true })).toBeVisible();
    }
    await expectAccessible(page);
  });
});
