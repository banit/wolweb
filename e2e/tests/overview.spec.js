// Übersicht: Geräte anlegen, suchen, filtern, wecken, Sprache und einfache Ansicht.
const { test, expect } = require("@playwright/test");
const { reset, addDevice, onlineDevice, offlineDevice } = require("./helpers");

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
  const mac = page.locator('#device-form input[name="mac"]');
  await expect(mac).toHaveAttribute("aria-invalid", "true");
  await expect(mac).toBeFocused();
  await expect(page.locator("#err-mac")).toBeVisible(); // Meldung am Feld …
  const msg = await page.locator("#err-mac").textContent();
  await expect(mac).toHaveAccessibleDescription(new RegExp("^" + msg.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"))); // … und vorgelesen
});

test("Pflichtfeld leer: Meldung am Feld, Fokus dorthin", async ({ page }) => {
  await page.goto("./");
  await page.click("#add-device");
  await page.fill('#device-form input[name="mac"]', "02:00:00:00:00:f1");
  await page.click("#save-device");
  const name = page.locator('#device-form input[name="name"]');
  await expect(name).toBeFocused();
  await expect(name).toHaveAttribute("aria-invalid", "true");
  await expect(name).toHaveAccessibleDescription(/^Bitte einen Gerätenamen eingeben\./);
  await name.fill("Werkstatt");
  await page.click("#save-device");
  await expect(page.locator("#device-dialog")).toBeHidden();
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

test("Gruppen: mehrere Geräte auf einmal zuordnen, Reichweite auf dem Knopf", async ({ page, request }) => {
  await addDevice(request, { name: "NAS", group: "Homelab" });
  await addDevice(request, { name: "Proxmox" });
  await addDevice(request, { name: "Switch" });
  await addDevice(request, { name: "Drucker" });
  await page.goto("./");
  await page.getByRole("button", { name: "Auswählen" }).click();
  await page.getByRole("checkbox", { name: "Proxmox auswählen" }).check();
  await page.getByRole("checkbox", { name: "Switch auswählen" }).check();
  const bar = page.getByRole("region", { name: "Auswahl" });
  await expect(bar).toContainText("2 Geräte ausgewählt");
  await bar.getByPlaceholder("Gruppenname").fill("homelab"); // vorhandene Gruppe, andere Schreibweise
  await bar.getByRole("button", { name: "Zur Gruppe „homelab“" }).click();
  await expect(page.locator("#toasts")).toContainText("2 Geräte sind jetzt in „Homelab“");
  await expect(bar).toBeHidden();
  const wakeAll = page.locator(".group-head", { hasText: "Homelab" }).getByRole("button", { name: /wecken/ });
  await expect(wakeAll).toHaveText("Alle 3 Geräte wecken");
  // Auch wenn die Suche Mitglieder ausblendet, nennt der Knopf alle, die er weckt.
  await page.fill("#search", "nas");
  await expect(page.locator(".card")).toHaveCount(1);
  await expect(wakeAll).toHaveText("Alle 3 Geräte wecken");

  // Im Gerätedialog: vorhandene Gruppe antippen statt tippen
  await page.fill("#search", "");
  await page.locator(".card", { hasText: "Drucker" }).locator('[data-focus="edit"]').click();
  const dlg = page.locator("#device-dialog");
  await expect(dlg).toContainText("Vorhandene Gruppe auswählen oder einen neuen Gruppennamen eingeben.");
  await dlg.getByRole("group", { name: "Vorhandene Gruppen" }).getByRole("button", { name: "Homelab" }).click();
  await expect(dlg.locator('input[name="group"]')).toHaveValue("Homelab");
  await dlg.locator("#save-device").click();
  await expect(wakeAll).toHaveText("Alle 4 Geräte wecken");
});

test("Übernahme aus der Suche: direkt, nur ein fehlender Name wird gefragt", async ({ page, request }) => {
  await page.goto("./");
  await page.getByRole("button", { name: /^Geräte suchen/ }).click();
  const drawer = page.locator("#discovery");
  await expect(drawer).toHaveClass(/open/);
  // Mit Hostnamen: ein Klick, kein Dialog; die Leiste schließt, die neue Karte hat den Fokus.
  await drawer.locator(".found", { hasText: "mediapc.example.com" }).getByRole("button", { name: "Übernehmen" }).click();
  await expect(drawer).not.toHaveClass(/open/);
  await expect(page.locator("#device-dialog")).toBeHidden();
  const card = page.locator(".card", { hasText: "mediapc" });
  await expect(card.locator('[data-focus="wake"]')).toBeFocused();
  await expect(card).toContainText("192.168.1.40");
  await expect(card.getByRole("group", { name: "Nächste Schritte für mediapc" })).toContainText("Übernommen");
  await card.getByRole("button", { name: "Details bearbeiten" }).click();
  await expect(page.locator("#device-dialog")).toBeVisible();
  await expect(page.locator('#device-form input[name="name"]')).toHaveValue("mediapc");
  await page.keyboard.press("Escape");

  // Ohne Hostnamen: nur der Name wird gefragt, mit Vorschlag
  await page.getByRole("button", { name: /^Geräte suchen/ }).click();
  const hit = drawer.locator(".found", { hasText: "192.168.1.41" });
  await hit.getByRole("button", { name: "Übernehmen" }).click();
  const name = hit.getByLabel("Name für dieses Gerät");
  await expect(name).toBeFocused();
  await expect(name).toHaveValue("Gerät-41");
  await name.fill("");
  await hit.getByRole("button", { name: "Übernehmen" }).click();
  await expect(hit).toContainText("Bitte einen Gerätenamen eingeben.");
  await expect(name).toHaveAttribute("aria-invalid", "true");
  await name.fill("Werkstatt");
  await name.press("Enter");
  await expect(drawer).not.toHaveClass(/open/);
  await expect(page.locator(".card", { hasText: "Werkstatt" }).locator('[data-focus="wake"]')).toBeFocused();
  const devs = await (await request.get("api/v1/devices")).json();
  expect(devs.map((d) => [d.name, d.ip]).sort()).toEqual([["Werkstatt", "192.168.1.41"], ["mediapc", "192.168.1.40"]]);
});

test("Gerät von Hand: Hinweis, wo es die MAC-Adresse gibt", async ({ page }) => {
  await page.goto("./");
  await page.click("#add-device");
  const mac = page.locator('#device-form input[name="mac"]');
  await expect(mac).toHaveAccessibleDescription(/Netzwerkeinstellungen des Rechners.*Geräte suchen/);
});

test("Wecklink im Ablauf: nach dem Anlegen anbieten, Umbenennen hält alte Links", async ({ page, request }) => {
  await page.goto("./");
  await page.click("#add-device");
  const dlg = page.locator("#device-dialog");
  await dlg.locator('input[name="name"]').fill("Büro-PC");
  await dlg.locator('input[name="mac"]').fill("02:00:00:00:00:b1");
  await dlg.locator("#save-device").click();
  const card = page.locator(".card", { hasText: "Büro-PC" });
  const next = card.getByRole("group", { name: "Nächste Schritte für Büro-PC" });
  await expect(next).toContainText("Angelegt");
  await expect(next.getByRole("button", { name: "Wecklink kopieren" })).toBeVisible();
  await next.getByRole("button", { name: "Auf dem Handy einrichten" }).click();
  const phone = page.locator("#phone-dialog");
  await expect(phone).toBeVisible();
  await expect(phone.locator("#phone-link")).toHaveText(/\/wake\/Büro-PC$/); // lesbar angezeigt
  await expect(phone).toContainText("Zum Home-Bildschirm");
  await expect(phone).toContainText("Zum Startbildschirm hinzufügen");
  await phone.getByRole("button", { name: "Fertig" }).click();
  await expect(phone).toBeHidden();

  // Umbenennen: der Dialog nennt den neuen Link, der alte leitet weiter
  await card.locator('[data-focus="edit"]').click();
  await dlg.locator('input[name="name"]').fill("Arbeitszimmer");
  await expect(dlg.locator("#name-hint")).toContainText("/wake/Arbeitszimmer – der bisherige funktioniert weiter.");
  await dlg.locator("#save-device").click();
  await expect(page.locator(".card", { hasText: "Arbeitszimmer" })).toBeVisible();
  const res = await request.get("wake/" + encodeURIComponent("Büro-PC") + "?format=json");
  expect(res.ok()).toBeTruthy();
  expect((await res.json()).device).toBe("Arbeitszimmer");
  await page.goto("wake/" + encodeURIComponent("Büro-PC"));
  await expect(page).toHaveURL(/\/wake\/Arbeitszimmer$/);
  await expect(page.locator("#wake-title")).toContainText("Arbeitszimmer");
});

test("Start nicht bestätigt: Warnung bleibt stehen, nochmals prüfen", async ({ page, request }) => {
  test.setTimeout(45000);
  await offlineDevice(request, "NAS");
  await page.goto("./");
  const card = page.locator(".card", { hasText: "NAS" });
  await card.locator(".btn-wake").click();
  // Server: WOLWEB_WAKE_TIMEOUT=8s
  const warn = card.locator(".card-warn");
  await expect(warn).toContainText("Start nicht bestätigt", { timeout: 20000 });
  const toast = page.locator(".toast.warn", { hasText: "NAS" });
  await expect(toast).toBeVisible();
  await page.waitForTimeout(5000); // früher nach 4 s verschwunden
  await expect(toast).toBeVisible();
  await expect(warn).toBeVisible();
  await warn.getByRole("button", { name: "Nochmals prüfen" }).click();
  await expect(page.locator("#toasts")).toContainText("NAS antwortet weiterhin nicht");
  await toast.getByRole("button", { name: "Schließen" }).click();
  await expect(toast).toBeHidden();
  await warn.getByRole("button", { name: "Hinweis schließen" }).click();
  await expect(card.locator(".card-warn")).toHaveCount(0);
});
