package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"wolweb/internal/i18n"
)

func names(devs []Device) []string {
	out := []string{}
	for _, d := range devs {
		out = append(out, d.Name)
	}
	return out
}

func TestGroupsFrom12AreCreatedAlphabetically(t *testing.T) {
	dir := t.TempDir()
	// devices.json von 1.2: Gruppen nur als Name am Gerät, in verschiedener Schreibweise
	os.WriteFile(filepath.Join(dir, "devices.json"), []byte(`{"version":2,"writer":"1.2.0","devices":[
		{"id":"a","name":"A","mac":"02:00:00:00:00:01","group":"Homelab","wake_key":"0123456789abcdef"},
		{"id":"b","name":"B","mac":"02:00:00:00:00:02","group":"büro","wake_key":"0123456789abcdee"},
		{"id":"c","name":"C","mac":"02:00:00:00:00:03","group":"homelab","wake_key":"0123456789abcded"}]}`), 0o600)
	s, _, err := Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if g := s.Groups(); !slices.Equal(g, []string{"büro", "Homelab"}) {
		t.Fatalf("Gruppen: %q", g)
	}
	if m := s.GroupMembers("HOMELAB"); !slices.Equal(names(m), []string{"A", "C"}) || m[1].Group != "Homelab" {
		t.Fatalf("Mitglieder: %+v", m)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "devices.json"))
	var f devicesFile
	json.Unmarshal(b, &f)
	if len(f.Groups) != 2 {
		t.Fatalf("Gruppen nicht gespeichert: %s", b)
	}
}

func TestGroupCRUD(t *testing.T) {
	s, _, _ := Open(t.TempDir(), "")
	a, _ := s.Create(Input{Name: "A", MAC: "02:00:00:00:00:01", Group: "Büro"})
	b, _ := s.Create(Input{Name: "B", MAC: "02:00:00:00:00:02"})
	c, _ := s.Create(Input{Name: "C", MAC: "02:00:00:00:00:03", Group: "Büro"})

	// Anlegen: leer oder mit Mitgliedern (auch aus einer anderen Gruppe)
	if _, err := s.CreateGroup("büro", nil, false); err == nil {
		t.Fatal("doppelter Name angenommen")
	}
	var ve ValidationError
	if _, err := s.CreateGroup("a/b", nil, false); !errors.As(err, &ve) || ve.Field != "name" {
		t.Fatalf("ungültiger Name: %v", err)
	}
	if _, err := s.CreateGroup("Leer", nil, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateGroup("Homelab", []string{b.ID, c.ID}, false); err != nil {
		t.Fatal(err)
	}
	if g := s.Groups(); !slices.Equal(g, []string{"Büro", "Leer", "Homelab"}) {
		t.Fatalf("Gruppen: %q", g)
	}
	if m := s.GroupMembers("Homelab"); !slices.Equal(names(m), []string{"B", "C"}) {
		t.Fatalf("Homelab: %q", names(m))
	}
	if _, err := s.CreateGroup("X", []string{"gibtsnicht"}, false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unbekanntes Gerät: %v", err)
	}

	// Umbenennen: alter Name bleibt als früherer Name; Mitglieder setzen
	g, err := s.UpdateGroup("büro", "Office", []string{a.ID, b.ID}, false)
	if err != nil || g.Name != "Office" || !slices.Equal(g.Aliases, []string{"Büro"}) {
		t.Fatalf("umbenennen: %+v %v", g, err)
	}
	if m := s.GroupMembers("Office"); !slices.Equal(names(m), []string{"A", "B"}) {
		t.Fatalf("Office: %q", names(m))
	}
	if f, ok := s.FindGroup("BÜRO"); !ok || f.Name != "Office" {
		t.Fatalf("früherer Name: %+v %v", f, ok)
	}
	if _, err := s.UpdateGroup("Office", "homelab", nil, false); err == nil {
		t.Fatal("Umbenennen auf vergebenen Namen angenommen")
	}
	// Den früheren Namen einer anderen Gruppe gibt es nur mit Bestätigung – sonst wechselte das Ziel
	// alter Links unbemerkt.
	if _, err := s.CreateGroup("büro", nil, false); !errors.As(err, &ve) || ve.Confirm != "take_over_name" {
		t.Fatalf("ohne Bestätigung: %v", err)
	}
	if f, _ := s.FindGroup("Büro"); f.Name != "Office" {
		t.Fatalf("früherer Name schon vergeben: %+v", f)
	}
	if _, err := s.CreateGroup("Büro", nil, true); err != nil {
		t.Fatal(err)
	}
	if f, _ := s.FindGroup("Büro"); f.Name != "Büro" {
		t.Fatalf("früherer Name nicht freigegeben: %+v", f)
	}
	if g, _ := s.FindGroup("Office"); len(g.Aliases) != 0 {
		t.Fatalf("Office behält den Namen: %+v", g)
	}

	// Löschen: Geräte bleiben, ohne Gruppe
	if err := s.DeleteGroup("office"); err != nil {
		t.Fatal(err)
	}
	if d, _ := s.Get(a.ID); d.Group != "" {
		t.Fatalf("Gerät noch in der Gruppe: %q", d.Group)
	}
	if err := s.DeleteGroup("Office"); !errors.Is(err, ErrGroupNotFound) {
		t.Fatalf("zweimal gelöscht: %v", err)
	}
	// Nach dem Neustart unverändert
	s2, _, _ := Open(s.dir, "")
	if g := s2.Groups(); !slices.Equal(g, []string{"Leer", "Homelab", "Büro"}) {
		t.Fatalf("nach dem Neustart: %q", g)
	}
}

func TestArrange(t *testing.T) {
	s, _, _ := Open(t.TempDir(), "")
	var ids []string
	for _, in := range []Input{{Name: "Anna", Group: "G1"}, {Name: "Bert", Group: "G1"}, {Name: "Carl", Group: "G1"}, {Name: "Dora", Group: "G2"}, {Name: "Emil"}} {
		in.MAC = "02:00:00:00:00:0" + string(rune('1'+len(ids)))
		d, err := s.Create(in)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, d.ID)
	}
	// Unsortiert: nach Name
	if m := s.GroupMembers("G1"); !slices.Equal(names(m), []string{"Anna", "Bert", "Carl"}) {
		t.Fatalf("vorher: %q", names(m))
	}
	// Gruppen tauschen, Carl nach vorn, Bert zu G2, Emil bleibt ohne Gruppe
	err := s.Arrange([]string{"g2"}, []Placement{
		{Group: "G1", IDs: []string{ids[2], ids[0]}},
		{Group: "G2", IDs: []string{ids[1], ids[3]}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if g := s.Groups(); !slices.Equal(g, []string{"G2", "G1"}) {
		t.Fatalf("Gruppen: %q", g)
	}
	if m := s.GroupMembers("G1"); !slices.Equal(names(m), []string{"Carl", "Anna"}) {
		t.Fatalf("G1: %q", names(m))
	}
	if m := s.GroupMembers("G2"); !slices.Equal(names(m), []string{"Bert", "Dora"}) {
		t.Fatalf("G2: %q", names(m))
	}
	// Neue Geräte kommen hinter die sortierten.
	s.Create(Input{Name: "Aaron", MAC: "02:00:00:00:00:09", Group: "G1"})
	if m := s.GroupMembers("G1"); !slices.Equal(names(m), []string{"Carl", "Anna", "Aaron"}) {
		t.Fatalf("neu: %q", names(m))
	}
	// Bearbeiten behält den Platz
	d, _ := s.Get(ids[0])
	in := inputOf(d)
	in.Note = "x"
	s.Update(d.ID, in, nil)
	if m := s.GroupMembers("G1"); !slices.Equal(names(m), []string{"Carl", "Anna", "Aaron"}) {
		t.Fatalf("nach dem Bearbeiten: %q", names(m))
	}
	// Fehler ändern nichts
	if err := s.Arrange([]string{"Gibtsnicht"}, nil); err == nil {
		t.Fatal("unbekannte Gruppe angenommen")
	}
	if err := s.Arrange(nil, []Placement{{Group: "G1", IDs: []string{ids[0], ids[0]}}}); err == nil {
		t.Fatal("doppelte ID angenommen")
	}
	if err := s.Arrange(nil, []Placement{{Group: "", IDs: []string{"gibtsnicht"}}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unbekanntes Gerät: %v", err)
	}
	if g := s.Groups(); !slices.Equal(g, []string{"G2", "G1"}) {
		t.Fatalf("nach Fehlern: %q", g)
	}
}

func TestExportImportKeepsGroups(t *testing.T) {
	s, _, _ := Open(t.TempDir(), "")
	a, _ := s.Create(Input{Name: "A", MAC: "02:00:00:00:00:01", Group: "Zeta"})
	b, _ := s.Create(Input{Name: "B", MAC: "02:00:00:00:00:02", Group: "Alpha"})
	s.CreateGroup("Leer", nil, false)
	s.UpdateGroup("Zeta", "Zeta2", nil, false)
	s.Arrange([]string{"Alpha"}, []Placement{{Group: "Zeta2", IDs: []string{a.ID}}, {Group: "Alpha", IDs: []string{b.ID}}})
	exp, err := s.Export()
	if err != nil {
		t.Fatal(err)
	}
	// Wiederherstellen in eine leere Installation
	r, _, _ := Open(t.TempDir(), "")
	if _, err := r.Import(exp, i18n.DE); err != nil {
		t.Fatal(err)
	}
	if g := r.Groups(); !slices.Equal(g, []string{"Alpha", "Zeta2", "Leer"}) {
		t.Fatalf("Gruppen: %q", g)
	}
	if g, ok := r.FindGroup("zeta"); !ok || g.Name != "Zeta2" {
		t.Fatalf("früherer Name: %+v", g)
	}
	if d := r.GroupMembers("Alpha"); len(d) != 1 || d[0].Order != 1 {
		t.Fatalf("Reihenfolge: %+v", d)
	}
}

// Ein früherer Gruppenname im Gerätedialog, in der Mehrfachauswahl oder im Import meint die Gruppe, die
// ihn trägt – ohne Rückfrage entsteht keine neue Gruppe, und kein alter Link wechselt das Ziel.
func TestFormerGroupNameMeansThatGroup(t *testing.T) {
	s, _, _ := Open(t.TempDir(), "")
	s.Create(Input{Name: "A", MAC: "02:00:00:00:00:01", Group: "Büro"})
	s.UpdateGroup("Büro", "Office", nil, false)
	b, err := s.Create(Input{Name: "B", MAC: "02:00:00:00:00:02", Group: "büro"})
	if err != nil || b.Group != "Office" {
		t.Fatalf("Gerätedialog: %q %v", b.Group, err)
	}
	c, _ := s.Create(Input{Name: "C", MAC: "02:00:00:00:00:03"})
	if devs, err := s.SetGroup([]string{c.ID}, "Büro"); err != nil || devs[0].Group != "Office" {
		t.Fatalf("Mehrfachauswahl: %+v %v", devs, err)
	}
	// Import mit einer Gruppe „Büro“: Ihre Geräte kommen zu Office, der frühere Name bleibt dort.
	_, err = s.Import([]byte(`{"version":2,"groups":[{"name":"Büro"}],"devices":[
		{"name":"D","mac":"02:00:00:00:00:04","group":"Büro"}]}`), i18n.DE)
	if err != nil {
		t.Fatal(err)
	}
	if g := s.Groups(); !slices.Equal(g, []string{"Office"}) {
		t.Fatalf("Gruppen: %q", g)
	}
	if g, _ := s.FindGroup("Büro"); g.Name != "Office" {
		t.Fatalf("Import hat den früheren Namen genommen: %+v", g)
	}
	if m := s.GroupMembers("Office"); len(m) != 4 {
		t.Fatalf("Mitglieder: %q", names(m))
	}
}
