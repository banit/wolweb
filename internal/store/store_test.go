package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLegacyImport(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "old.json")
	os.WriteFile(legacy, []byte(`{"devices":[
		{"name":"NAS","mac":"28-c6-8e-36-dc-38","ip":"192.168.1.255:9","interface":""},
		{"name":"NAS","mac":"28:c6:8e:36:dc:39","ip":"10.0.0.255"},
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
	if devs[0].MAC != "28:C6:8E:36:DC:38" || devs[0].Broadcast != "192.168.1.255:9" || devs[1].Broadcast != "10.0.0.255:9" {
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
	orig := []byte(`{"devices":[{"name":"NAS","mac":"28:c6:8e:36:dc:38","ip":"192.168.1.255:9"}]}`)
	os.WriteFile(filepath.Join(dir, "devices.json"), orig, 0o644)
	if _, _, err := Open(dir, ""); err != nil {
		t.Fatal(err)
	}
	baks, _ := filepath.Glob(filepath.Join(dir, "devices.json.alt-*"))
	if len(baks) != 1 {
		t.Fatalf("Sicherungen: %v", baks)
	}
	if b, _ := os.ReadFile(baks[0]); string(b) != string(orig) {
		t.Errorf("Sicherung weicht ab: %s", b)
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
	res, err := dst.Import(b)
	if err != nil || res.Added != 1 || res.Skipped != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	d, ok := dst.FindByName("NAS 2")
	if !ok || d.IP != "192.168.1.5" || len(d.Ports) != 1 || d.SecureOn != "01:02:03:04:05:06" {
		t.Errorf("importiert: %+v", d)
	}
	if _, err := dst.Import([]byte("kein json")); err == nil {
		t.Error("kaputte Datei angenommen")
	}
}
