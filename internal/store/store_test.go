package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"wolweb/internal/i18n"
)

func TestLegacyImport(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "old.json")
	os.WriteFile(legacy, []byte(`{"devices":[
		{"name":"NAS","mac":"02-aa-bb-cc-dc-38","ip":"192.168.1.255:9","interface":""},
		{"name":"NAS","mac":"02:aa:bb:cc:dc:39","ip":"10.0.0.255"},
		{"name":"kaputt","mac":"xyz","ip":""}]}`), 0o644)
	s, notes, err := Open(filepath.Join(dir, "data"), legacy)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) == 0 {
		t.Error("kein Hinweis auf die Übernahme")
	}
	devs, _ := s.Devices()
	if len(devs) != 2 {
		t.Fatalf("%d Geräte statt 2", len(devs))
	}
	if devs[0].Name != "NAS" || devs[1].Name != "NAS 2" {
		t.Errorf("Namen: %q, %q", devs[0].Name, devs[1].Name)
	}
	if devs[0].MAC != "02:AA:BB:CC:DC:38" || devs[0].Broadcast != "192.168.1.255:9" || devs[1].Broadcast != "10.0.0.255:9" {
		t.Errorf("Umwandlung falsch: %+v", devs)
	}
	// Neu öffnen: eigenes Format, gleiche Daten
	s2, _, err := Open(filepath.Join(dir, "data"), "")
	if err != nil {
		t.Fatal(err)
	}
	if d2, _ := s2.Devices(); len(d2) != 2 || d2[0].ID != devs[0].ID {
		t.Errorf("nach Neuladen: %+v", d2)
	}
}

func TestCRUDAndValidation(t *testing.T) {
	s, _, err := Open(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.Create(Input{Name: "PC", MAC: "001122334455", IP: "192.168.1.5", Icon: "quatsch"})
	if err != nil {
		t.Fatal(err)
	}
	if d.MAC != "00:11:22:33:44:55" || d.Icon != "desktop" {
		t.Errorf("%+v", d)
	}
	var ve ValidationError
	if _, err := s.Create(Input{Name: "pc", MAC: "00:11:22:33:44:56"}); !errors.As(err, &ve) {
		t.Errorf("doppelter Name erlaubt: %v", err)
	}
	for _, in := range []Input{
		{Name: "", MAC: "00:11:22:33:44:55"},
		{Name: "a/b", MAC: "00:11:22:33:44:55"},
		{Name: "..", MAC: "00:11:22:33:44:55"},
		{Name: "x", MAC: "nope"},
		{Name: "x", MAC: "00:11:22:33:44:55", IP: "300.1.1.1"},
		{Name: "x", MAC: "00:11:22:33:44:55", Broadcast: "fe80::1"},
		{Name: "x", MAC: "00:11:22:33:44:55", Ports: []int{22, 0}},
		{Name: "x", MAC: "00:11:22:33:44:55", Group: "a/b"},
		{Name: "x", MAC: "00:11:22:33:44:55", Schedule: []string{"Mo 25:00"}},
		{Name: "a\nb", MAC: "00:11:22:33:44:55"},
	} {
		if _, err := s.Create(in); !errors.As(err, &ve) {
			t.Errorf("%+v sollte abgelehnt werden: %v", in, err)
		}
	}
	old := d.UpdatedAt.Add(-time.Second)
	if _, err := s.Update(d.ID, Input{Name: "PC2", MAC: d.MAC}, &old); !errors.Is(err, ErrConflict) {
		t.Errorf("veraltete Änderung angenommen: %v", err)
	}
	u, err := s.Update(d.ID, Input{Name: "PC2", MAC: d.MAC}, &d.UpdatedAt)
	if err != nil || u.Name != "PC2" {
		t.Fatalf("%v %+v", err, u)
	}
	if f, ok := s.FindByName("pc2"); !ok || f.ID != d.ID {
		t.Error("FindByName ohne Groß-/Kleinschreibung")
	}
	if err := s.Delete(d.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(d.ID); !errors.Is(err, ErrNotFound) {
		t.Error(err)
	}
}

func TestMergeFound(t *testing.T) {
	s, _, _ := Open(t.TempDir(), "")
	s.Create(Input{Name: "bekannt", MAC: "00:00:00:00:00:01"})
	now := time.Now()
	n, err := s.MergeFound([]Found{{MAC: "00:00:00:00:00:01", IP: "10.0.0.1", LastSeen: now}, {MAC: "00:00:00:00:00:02", IP: "10.0.0.2", LastSeen: now, Hostname: "neu"}})
	if err != nil || n != 1 {
		t.Fatalf("neu=%d err=%v", n, err)
	}
	n, _ = s.MergeFound([]Found{{MAC: "00:00:00:00:00:02", IP: "10.0.0.3", LastSeen: now}})
	if n != 0 {
		t.Error("bekannter Treffer als neu gezählt")
	}
	list := s.FoundList()
	if len(list) != 2 || list[0].DeviceName != "bekannt" || list[1].IP != "10.0.0.3" || list[1].Hostname != "neu" {
		t.Errorf("%+v", list)
	}
}

func TestBrokenDiscoveredFileStopsStart(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "discovered.json"), []byte(`{kaputt`), 0o644)
	if _, _, err := Open(dir, ""); err == nil {
		t.Fatal("defekte discovered.json wurde still überschrieben")
	}
	os.WriteFile(filepath.Join(dir, "discovered.json"), []byte(`[null,{"mac":"aa-bb-cc-dd-ee-ff","ip":"10.0.0.9"}]`), 0o644)
	s, _, err := Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.FoundByMAC("AA:BB:CC:DD:EE:FF"); !ok {
		t.Error("Eintrag nicht normalisiert übernommen")
	}
}

func TestOldFoundEntriesExpire(t *testing.T) {
	s, _, _ := Open(t.TempDir(), "")
	s.MergeFound([]Found{{MAC: "00:00:00:00:00:01", IP: "10.0.0.1", LastSeen: time.Now()}, {MAC: "00:00:00:00:00:02", IP: "10.0.0.2", LastSeen: time.Now()}})
	s.SetIgnored("00:00:00:00:00:02", true)
	for _, f := range s.found { // beide seit über 30 Tagen nicht mehr gesehen
		f.LastSeen = time.Now().Add(-FoundRetention - time.Hour)
	}
	s.MergeFound([]Found{{MAC: "00:00:00:00:00:03", IP: "10.0.0.3", LastSeen: time.Now()}})
	if _, ok := s.FoundByMAC("00:00:00:00:00:01"); ok {
		t.Error("alter Treffer nicht entfernt")
	}
	if _, ok := s.FoundByMAC("00:00:00:00:00:02"); !ok {
		t.Error("ignorierter Treffer entfernt")
	}
	if _, ok := s.FoundByMAC("00:00:00:00:00:03"); !ok {
		t.Error("neuer Treffer fehlt")
	}
}

func TestLegacyFileInDataDirIsBackedUp(t *testing.T) {
	dir := t.TempDir()
	orig := []byte(`{"devices":[{"name":"NAS","mac":"02:00:00:00:00:38","ip":"192.168.1.255:9"}]}`)
	os.WriteFile(filepath.Join(dir, "devices.json"), orig, 0o644)
	if _, _, err := Open(dir, ""); err != nil {
		t.Fatal(err)
	}
	baks, _ := filepath.Glob(filepath.Join(dir, "backup-original-*", "devices.json"))
	if len(baks) != 1 {
		t.Fatalf("Sicherungen: %v", baks)
	}
	if b, _ := os.ReadFile(baks[0]); string(b) != string(orig) {
		t.Errorf("Sicherung weicht ab: %s", b)
	}
}

// Update von 1.0.x: Vor dem ersten Schreiben im neuen Format liegt eine Sicherung mit Versionsangabe
// bereit (für den Weg zurück), einmal – nicht bei jedem Start. Unicast-Ziele bleiben, wie sie sind.
func TestUpdateFrom10BacksUpOnce(t *testing.T) {
	old := AppVersion
	AppVersion = "1.2.0"
	defer func() { AppVersion = old }()
	dir := t.TempDir()
	orig := []byte(`{"version":2,"devices":[
		{"id":"abc","name":"NAS","mac":"02:00:00:00:00:01","broadcast":"10.0.0.5:7","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z","last_wake":"2026-09-01T10:00:00Z"},
		{"id":"def","name":"PC","mac":"02:00:00:00:00:02","broadcast":"10.0.0.255:9","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}]}`)
	os.WriteFile(filepath.Join(dir, "devices.json"), orig, 0o644)
	os.WriteFile(filepath.Join(dir, "discovered.json"), []byte(`[]`), 0o644)
	s, notes, err := Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	baks, _ := filepath.Glob(filepath.Join(dir, "backup-1.0.x-*"))
	if len(baks) != 1 || !strings.Contains(strings.Join(notes, " "), "1.0.x") {
		t.Fatalf("Sicherungen: %v, Hinweise %v", baks, notes)
	}
	if b, _ := os.ReadFile(filepath.Join(baks[0], "devices.json")); string(b) != string(orig) {
		t.Errorf("Sicherung weicht ab: %s", b)
	}
	if _, err := os.Stat(filepath.Join(baks[0], "discovered.json")); err != nil {
		t.Errorf("discovered.json nicht gesichert: %v", err)
	}
	// Unicast-Ziel in einem anderen Netz unverändert, auch nach dem Speichern
	if d, _ := s.Get("abc"); d.Broadcast != "10.0.0.5:7" {
		t.Errorf("Ziel geändert: %q", d.Broadcast)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "devices.json"))
	if !strings.Contains(string(b), `"writer": "1.2.0"`) || !strings.Contains(string(b), `"broadcast": "10.0.0.5:7"`) {
		t.Errorf("neu geschrieben: %s", b)
	}
	// Zweiter Start derselben Version: keine weitere Sicherung
	if _, _, err := Open(dir, ""); err != nil {
		t.Fatal(err)
	}
	if baks, _ := filepath.Glob(filepath.Join(dir, "backup-*")); len(baks) != 1 {
		t.Errorf("weitere Sicherung: %v", baks)
	}
	// Nächstes Update (1.2.0 → 1.3.0): wieder eine Sicherung, jetzt mit der alten Version im Namen
	AppVersion = "1.3.0"
	if _, _, err := Open(dir, ""); err != nil {
		t.Fatal(err)
	}
	if baks, _ := filepath.Glob(filepath.Join(dir, "backup-1.2.0-*")); len(baks) != 1 {
		t.Errorf("keine Sicherung von 1.2.0: %v", baks)
	}
}

// Original-wolweb: ein Unicast-Ziel (einzelne IP:Port in einem anderen Netz) wird wörtlich übernommen.
func TestLegacyUnicastTargetKept(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "old.json")
	os.WriteFile(legacy, []byte(`{"devices":[{"name":"Server","mac":"02:00:00:00:00:03","ip":"10.0.0.5:7","interface":""}]}`), 0o644)
	s, _, err := Open(filepath.Join(dir, "data"), legacy)
	if err != nil {
		t.Fatal(err)
	}
	if devs, _ := s.Devices(); len(devs) != 1 || devs[0].Broadcast != "10.0.0.5:7" {
		t.Errorf("%+v", devs)
	}
}

// wolweb 1.0.x legte last_wake in devices.json ab; das wandert nach state.json.
func TestLastWakeMigratesToState(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "devices.json"), []byte(`{"version":2,"devices":[
		{"id":"abc","name":"NAS","mac":"00:11:22:33:44:55","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z","last_wake":"2026-09-01T10:00:00Z"}]}`), 0o644)
	s, _, err := Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := s.LastWake("abc"); !got.Equal(time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("last_wake nicht übernommen: %v", got)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "devices.json")); strings.Contains(string(b), "last_wake") {
		t.Error("last_wake steht noch in devices.json")
	}
	s2, _, _ := Open(dir, "")
	if s2.LastWake("abc").IsZero() {
		t.Error("last_wake nach Neustart verloren")
	}
}

func TestHistoryAndLastSeen(t *testing.T) {
	dir := t.TempDir()
	s, _, _ := Open(dir, "")
	d, _ := s.Create(Input{Name: "PC", MAC: "00:11:22:33:44:55"})
	for i := range HistoryMax + 5 {
		s.RecordWake(Event{ID: d.ID, Device: d.Name, Source: "Test", Success: i%2 == 0})
	}
	h := s.History(0)
	if len(h) != HistoryMax || !h[0].Time.After(h[len(h)-1].Time) && !h[0].Time.Equal(h[len(h)-1].Time) {
		t.Errorf("Verlauf: %d Einträge", len(h))
	}
	if s.LastWake(d.ID).IsZero() {
		t.Error("letzter Weckzeitpunkt fehlt")
	}
	seen := time.Now().UTC().Truncate(time.Second)
	s.SeenAt(d.ID, seen)
	if err := s.FlushState(); err != nil {
		t.Fatal(err)
	}
	s2, _, _ := Open(dir, "")
	if !s2.LastSeen(d.ID).Equal(seen) || len(s2.History(10)) != 10 || len(s2.Secret()) != 32 {
		t.Errorf("nach Neustart: zuletzt gesehen %v, Verlauf %d", s2.LastSeen(d.ID), len(s2.History(10)))
	}
	if !bytesEqual(s.Secret(), s2.Secret()) {
		t.Error("Geheimnis hat sich geändert")
	}
}

func bytesEqual(a, b []byte) bool { return string(a) == string(b) }

func TestExportImport(t *testing.T) {
	src, _, _ := Open(t.TempDir(), "")
	src.Create(Input{Name: "NAS", MAC: "00:11:22:33:44:55", IP: "192.168.1.5", Ports: []int{5000}, SecureOn: "01:02:03:04:05:06"})
	src.Create(Input{Name: "PC", MAC: "00:11:22:33:44:66"})
	b, err := src.Export()
	if err != nil {
		t.Fatal(err)
	}
	dst, _, _ := Open(t.TempDir(), "")
	dst.Create(Input{Name: "NAS", MAC: "00:AA:BB:CC:DD:EE"}) // gleicher Name, andere MAC
	dst.Create(Input{Name: "Alt", MAC: "00:11:22:33:44:66"}) // gleiche MAC
	res, err := dst.Import(b, "de")
	if err != nil || res.Added != 1 || res.Skipped != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	d, ok := dst.FindByName("NAS 2")
	if !ok || d.IP != "192.168.1.5" || len(d.Ports) != 1 || d.SecureOn != "01:02:03:04:05:06" {
		t.Errorf("importiert: %+v", d)
	}
	if _, err := dst.Import([]byte("kein json"), "de"); err == nil {
		t.Error("kaputte Datei angenommen")
	}
}

func TestGroupsAndSchedule(t *testing.T) {
	s, _, _ := Open(t.TempDir(), "")
	a, err := s.Create(Input{Name: "A", MAC: "00:00:00:00:00:01", Group: "Homelab", Schedule: []string{"mo-fr 7:30; wochenende 9:00"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Schedule) != 2 || a.Schedule[0] != "Mo-Fr 07:30" || a.Schedule[1] != "Sa,So 09:00" {
		t.Errorf("Zeitplan nicht vereinheitlicht: %q", a.Schedule)
	}
	s.Create(Input{Name: "B", MAC: "00:00:00:00:00:02", Group: "homelab"})
	s.Create(Input{Name: "C", MAC: "00:00:00:00:00:03", Group: "Büro"})
	s.Create(Input{Name: "D", MAC: "00:00:00:00:00:04"})
	if g := s.Groups(); len(g) != 2 || g[0] != "Homelab" || g[1] != "Büro" { // in der Reihenfolge des Anlegens
		t.Errorf("Gruppen: %q", g)
	}
	if m := s.GroupMembers("HOMELAB"); len(m) != 2 || m[0].Name != "A" || m[1].Name != "B" {
		t.Errorf("Mitglieder: %+v", m)
	}
}

func TestGroupDotsAndBrokenSecret(t *testing.T) {
	dir := t.TempDir()
	s, _, _ := Open(dir, "")
	for _, g := range []string{".", ".."} {
		if _, err := s.Create(Input{Name: "x" + g, MAC: "00:00:00:00:00:01", Group: g}); err == nil {
			t.Errorf("Gruppe %q angenommen", g)
		}
	}
	// Kaputtes Geheimnis wird ersetzt statt still als leerer Schlüssel benutzt.
	os.WriteFile(filepath.Join(dir, "state.json"), []byte(`{"version":1,"secret":"kein-hex"}`), 0o644)
	s2, _, err := Open(dir, "")
	if err != nil || len(s2.Secret()) != 32 {
		t.Fatalf("Geheimnis nach Reparatur: %d Byte, %v", len(s2.Secret()), err)
	}
}

// Nach einer Wiederherstellung müssen gespeicherte Wecklinks (Geräte und Gruppen) weiter gelten.
func TestImportKeepsWakeKeysAndSecret(t *testing.T) {
	src, _, _ := Open(t.TempDir(), "")
	d, _ := src.Create(Input{Name: "PC", MAC: "00:11:22:33:44:55", Group: "Büro"})
	b, _ := src.Export()
	dst, _, _ := Open(t.TempDir(), "")
	res, err := dst.Import(b, "de")
	if err != nil || res.Added != 1 || !res.SecretRestored {
		t.Fatalf("%+v %v", res, err)
	}
	got, _ := dst.FindByName("PC")
	if got.WakeKey != d.WakeKey || !bytesEqual(dst.Secret(), src.Secret()) {
		t.Error("Weckschlüssel oder Geheimnis nicht übernommen")
	}
	// In eine Installation mit Geräten: Geheimnis bleibt, Schlüssel nur ohne Kollision.
	other, _, _ := Open(t.TempDir(), "")
	other.Create(Input{Name: "Alt", MAC: "00:00:00:00:00:09"})
	before := other.Secret()
	res, _ = other.Import(b, "de")
	if res.SecretRestored || !bytesEqual(other.Secret(), before) {
		t.Error("Geheimnis einer bestehenden Installation überschrieben")
	}
}

// Alte Dateien: Jeder ungültige Eintrag wird gemeldet, ein ungültiges Ziel nie still durch den
// Standard ersetzt – beim ausdrücklichen Import wie beim automatischen beim Start.
func TestLegacyProblemsReported(t *testing.T) {
	old := []byte(`{"devices":[
		{"name":"Gut","mac":"02:00:00:00:00:01","ip":"10.0.0.255:9"},
		{"name":"Port","mac":"02:00:00:00:00:02","ip":"10.0.0.5:70000"},
		{"name":"Kaputt","mac":"xyz","ip":""}]}`)
	s, _, _ := Open(t.TempDir(), "")
	res, err := s.Import(old, i18n.DE)
	if err != nil {
		t.Fatal(err)
	}
	if res.Added != 1 || len(res.Errors) != 2 || !strings.Contains(res.Errors[0], "Kaputt") && !strings.Contains(res.Errors[1], "Kaputt") ||
		!strings.Contains(strings.Join(res.Errors, " "), "10.0.0.5:70000") {
		t.Errorf("%+v", res)
	}
	if devs, _ := s.Devices(); len(devs) != 1 || devs[0].Broadcast != "10.0.0.255:9" {
		t.Errorf("übernommen: %+v", devs)
	}

	dir := t.TempDir()
	legacy := filepath.Join(dir, "alt.json")
	os.WriteFile(legacy, old, 0o644)
	s2, notes, err := Open(filepath.Join(dir, "data"), legacy)
	if err != nil {
		t.Fatal(err)
	}
	all := strings.Join(notes, "\n")
	if !strings.Contains(all, "Port") || !strings.Contains(all, "70000") || !strings.Contains(all, "Kaputt") {
		t.Errorf("Hinweise: %s", all)
	}
	if devs, _ := s2.Devices(); len(devs) != 1 {
		t.Errorf("beim Start übernommen: %+v", devs)
	}
}

// Scheitert das Speichern des Verlaufs, holt FlushState es später nach.
func TestRecordWakeRetriedAfterSaveError(t *testing.T) {
	dir := t.TempDir()
	s, _, _ := Open(dir, "")
	d, _ := s.Create(Input{Name: "PC", MAC: "02:00:00:00:00:01"})
	path := filepath.Join(dir, "state.json")
	os.Remove(path)
	os.Mkdir(path, 0o755) // an der Stelle steht jetzt ein Ordner: Schreiben scheitert
	if err := s.RecordWake(Event{ID: d.ID, Device: d.Name, Source: "api", Success: true}); err == nil {
		t.Fatal("Speichern hätte scheitern müssen")
	}
	os.Remove(path)
	if err := s.FlushState(); err != nil {
		t.Fatal(err)
	}
	s2, _, err := Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if h := s2.History(0); len(h) != 1 || h[0].Device != "PC" || s2.LastWake(d.ID).IsZero() {
		t.Errorf("Verlauf verloren: %+v", h)
	}
}

// Frühere Namen: höchstens maxAliases, und Export/Import behält sie (nicht aber fremde).
func TestAliasesCappedAndImported(t *testing.T) {
	st, _, _ := Open(t.TempDir(), "")
	d, _ := st.Create(Input{Name: "PC0", MAC: "02:00:00:00:00:01"})
	for i := 1; i <= maxAliases+3; i++ {
		var err error
		if d, err = st.Update(d.ID, Input{Name: fmt.Sprintf("PC%d", i), MAC: d.MAC}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if len(d.Aliases) != maxAliases || d.Aliases[len(d.Aliases)-1] != fmt.Sprintf("PC%d", maxAliases+2) {
		t.Fatalf("%v", d.Aliases)
	}
	if got, ok := st.FindByName("PC5"); !ok || got.ID != d.ID {
		t.Error("alter Name nicht gefunden")
	}
	b, err := json.Marshal(map[string]any{"version": 2, "devices": []Device{d}})
	if err != nil {
		t.Fatal(err)
	}
	other, _, _ := Open(t.TempDir(), "")
	other.Create(Input{Name: "PC5", MAC: "02:00:00:00:00:09"}) // trägt einen der alten Namen schon
	if _, err := other.Import(b, i18n.DE); err != nil {
		t.Fatal(err)
	}
	got, _ := other.FindByName(d.Name)
	if len(got.Aliases) != maxAliases-1 || slices.Contains(got.Aliases, "PC5") {
		t.Errorf("importiert: %v", got.Aliases)
	}
}

// Ein importiertes Gerät mit dem früheren Namen eines vorhandenen bekommt einen freien Namen – sonst
// lenkte der Import alte Wecklinks unbemerkt auf das neue Gerät um.
func TestImportKeepsOldLinks(t *testing.T) {
	st, _, _ := Open(t.TempDir(), "")
	d, _ := st.Create(Input{Name: "NAS", MAC: "02:00:00:00:00:01"})
	d, _ = st.Update(d.ID, Input{Name: "Archiv", MAC: d.MAC}, nil)
	b, _ := json.Marshal(map[string]any{"version": 2, "devices": []Device{{Name: "NAS", MAC: "02:00:00:00:00:02"}}})
	res, err := st.Import(b, i18n.DE)
	if err != nil || res.Added != 1 || len(res.Items) != 1 || res.Items[0].NewName != "NAS 2" {
		t.Fatalf("%v %+v", err, res)
	}
	if got, _ := st.FindByName("NAS"); got.ID != d.ID {
		t.Error("Import hat den alten Link umgelenkt")
	}
}

// Das Importergebnis nennt jeden Eintrag: übernommen (ggf. umbenannt), schon vorhanden, fehlerhaft.
func TestImportListsEveryEntry(t *testing.T) {
	st, _, _ := Open(t.TempDir(), "")
	st.Create(Input{Name: "NAS", MAC: "02:00:00:00:00:01"})
	b := []byte(`{"version":2,"devices":[
		{"name":"Speicher","mac":"02:00:00:00:00:01"},
		{"name":"NAS","mac":"02:00:00:00:00:02"},
		{"name":"Kaputt","mac":"xyz"},
		{"name":"Drucker","mac":"02:00:00:00:00:03"}]}`)
	res, err := st.Import(b, i18n.DE)
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, it := range res.Items {
		got = append(got, it.Name+"|"+it.Result+"|"+it.NewName)
	}
	want := []string{"Speicher|skipped|", "NAS|added|NAS 2", "Kaputt|error|", "Drucker|added|"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("%v", got)
	}
	if res.Items[0].Detail != "NAS" || res.Items[2].Detail == "" || res.Added != 2 || res.Skipped != 1 || len(res.Errors) != 1 {
		t.Errorf("%+v", res)
	}
}

// Der Bestand der Suchtreffer bleibt begrenzt; ignorierte und angelegte Geräte bleiben dabei.
func TestFoundCapped(t *testing.T) {
	s, _, _ := Open(t.TempDir(), "")
	s.Create(Input{Name: "PC", MAC: "02:00:00:00:00:01"})
	old := time.Now().Add(-time.Hour)
	s.MergeFound([]Found{{MAC: "02:00:00:00:00:01", IP: "10.0.0.1", LastSeen: old}, {MAC: "02:00:00:00:00:02", IP: "10.0.0.2", LastSeen: old}})
	s.SetIgnored("02:00:00:00:00:02", true)
	var hits []Found
	for i := range MaxFound + 100 {
		hits = append(hits, Found{MAC: fmt.Sprintf("02:aa:%02x:%02x:%02x:00", i>>16&0xff, i>>8&0xff, i&0xff), IP: "10.0.1.1", LastSeen: time.Now()})
	}
	if _, err := s.MergeFound(hits); err != nil {
		t.Fatal(err)
	}
	if n := len(s.FoundList()); n > MaxFound {
		t.Fatalf("%d Treffer, erlaubt %d", n, MaxFound)
	}
	for _, mac := range []string{"02:00:00:00:00:01", "02:00:00:00:00:02"} {
		if _, ok := s.FoundByMAC(mac); !ok {
			t.Errorf("%s entfernt", mac)
		}
	}
}

// Ein angelegtes Gerät behält Netz und IP seines Suchtreffers, auch wenn dieselbe MAC kurz danach
// in einem anderen Netz antwortet; nach foundNetPin gilt der Wechsel.
func TestFoundNetworkPinnedForDevices(t *testing.T) {
	s, _, _ := Open(t.TempDir(), "")
	s.Create(Input{Name: "NAS", MAC: "02:00:00:00:00:05"})
	now := time.Now()
	s.MergeFound([]Found{{MAC: "02:00:00:00:00:05", IP: "192.168.50.20", Network: "192.168.50.0/24", LastSeen: now.Add(-time.Hour)}})
	s.MergeFound([]Found{{MAC: "02:00:00:00:00:05", IP: "10.0.20.66", Network: "10.0.20.0/24", LastSeen: now}})
	if f, _ := s.FoundByMAC("02:00:00:00:00:05"); f.IP != "192.168.50.20" {
		t.Fatalf("Netz gewechselt: %+v", f)
	}
	// Im selben Netz ändert sich die IP normal.
	s.MergeFound([]Found{{MAC: "02:00:00:00:00:05", IP: "192.168.50.21", Network: "192.168.50.0/24", LastSeen: now}})
	if f, _ := s.FoundByMAC("02:00:00:00:00:05"); f.IP != "192.168.50.21" {
		t.Fatalf("IP im selben Netz: %+v", f)
	}
	// Lange nicht mehr gesehen: der Wechsel gilt.
	s.MergeFound([]Found{{MAC: "02:00:00:00:00:05", IP: "10.0.20.66", Network: "10.0.20.0/24", LastSeen: now.Add(foundNetPin + time.Hour)}})
	if f, _ := s.FoundByMAC("02:00:00:00:00:05"); f.IP != "10.0.20.66" {
		t.Fatalf("Wechsel nach langer Zeit: %+v", f)
	}
}
