package store

import (
	"errors"
	"os"
	"path/filepath"
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
