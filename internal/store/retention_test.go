package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Weckverlauf, „zuletzt geweckt“ und „zuletzt online“ verfallen nach der Aufbewahrung – in den
// Abfragen sofort, in state.json beim Einstellen und danach regelmäßig.
func TestRetentionDropsOldEntries(t *testing.T) {
	s, _, _ := Open(t.TempDir(), "")
	d, _ := s.Create(Input{Name: "PC", MAC: "02:00:00:00:00:01"})
	old, recent := time.Now().Add(-40*24*time.Hour).UTC(), time.Now().Add(-time.Hour).UTC()
	s.RecordWake(Event{ID: d.ID, Device: d.Name, Source: "api", Success: true, Time: old, Client: "192.168.1.50"},
		Event{ID: d.ID, Device: d.Name, Source: "api", Success: true, Time: recent})
	s.SeenAt(d.ID, old)
	if err := s.SetRecording(true, 30*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	if h := s.History(0); len(h) != 1 || !h[0].Time.Equal(recent) {
		t.Errorf("Verlauf: %+v", h)
	}
	if !s.LastWake(d.ID).Equal(recent) || !s.LastSeen(d.ID).IsZero() {
		t.Errorf("zuletzt geweckt %v, zuletzt online %v", s.LastWake(d.ID), s.LastSeen(d.ID))
	}
	b, _ := os.ReadFile(s.statePath())
	if strings.Contains(string(b), "192.168.1.50") || strings.Contains(string(b), old.Format("2006-01-02T15")) {
		t.Errorf("Abgelaufenes noch in state.json:\n%s", b)
	}
	// Was nach dem Einstellen abläuft, blenden die Abfragen sofort aus (gelöscht wird später).
	s.RecordWake(Event{ID: d.ID, Device: d.Name, Source: "api", Success: true, Time: old})
	if len(s.History(0)) != 1 {
		t.Error("abgelaufener Eintrag sichtbar")
	}
	s.mu.Lock()
	s.pruneLocked(time.Now())
	n := len(s.state.History)
	s.mu.Unlock()
	if n != 1 {
		t.Errorf("nicht gelöscht: %d Einträge", n)
	}
}

// Ausgeschaltet speichert wolweb nichts davon und löscht, was schon da ist.
func TestRecordingOff(t *testing.T) {
	dir := t.TempDir()
	s, _, _ := Open(dir, "")
	d, _ := s.Create(Input{Name: "PC", MAC: "02:00:00:00:00:01"})
	s.RecordWake(Event{ID: d.ID, Device: d.Name, Source: "api", Success: true, Client: "192.168.1.50"})
	s.SeenAt(d.ID, time.Now())
	if err := s.SetRecording(false, 30*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	s.RecordWake(Event{ID: d.ID, Device: d.Name, Source: "api", Success: true})
	s.SeenAt(d.ID, time.Now())
	if len(s.History(0)) != 0 || !s.LastWake(d.ID).IsZero() || !s.LastSeen(d.ID).IsZero() {
		t.Error("trotz ausgeschalteter Aufzeichnung gespeichert")
	}
	s.FlushState()
	if b, _ := os.ReadFile(s.statePath()); strings.Contains(string(b), "192.168.1.50") || strings.Contains(string(b), `"history"`) {
		t.Errorf("state.json enthält noch den Verlauf:\n%s", b)
	}
	// Wieder eingeschaltet wird ab jetzt aufgezeichnet.
	s.SetRecording(true, 30*24*time.Hour)
	s.RecordWake(Event{ID: d.ID, Device: d.Name, Source: "api", Success: true})
	if len(s.History(0)) != 1 || s.LastWake(d.ID).IsZero() {
		t.Error("nach dem Einschalten nicht aufgezeichnet")
	}
}

// Scheitert das Schreiben beim Abschalten, holt FlushState das Löschen nach – die alten Einträge
// bleiben nicht in state.json.
func TestSetRecordingRetriesAfterWriteError(t *testing.T) {
	dir := t.TempDir()
	s, _, _ := Open(dir, "")
	d, _ := s.Create(Input{Name: "PC", MAC: "02:00:00:00:00:01"})
	s.RecordWake(Event{ID: d.ID, Device: d.Name, Source: "api", Success: true, Client: "192.168.1.50"})
	path := filepath.Join(dir, "state.json")
	// Schreiben unmöglich machen: an der Stelle von state.json liegt kurz ein Ordner (das
	// Umbenennen der neuen Datei scheitert), die alte Datei bleibt daneben erhalten.
	os.Rename(path, path+".alt")
	os.MkdirAll(filepath.Join(path, "x"), 0o750)
	if err := s.SetRecording(false, 30*24*time.Hour); err == nil {
		t.Fatal("Schreibfehler nicht gemeldet")
	}
	os.RemoveAll(path)
	os.Rename(path+".alt", path) // die Platte ist wieder in Ordnung, die alte Datei noch da
	if err := s.FlushState(); err != nil {
		t.Fatal(err)
	}
	s2, _, err := Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); len(s2.History(0)) != 0 || strings.Contains(string(b), "192.168.1.50") {
		t.Errorf("Löschen nicht nachgeholt:\n%s", b)
	}
}

// Die Sicherungen beim Update enthalten state.json – auch dort gilt die Aufbewahrung.
func TestRetentionPrunesBackups(t *testing.T) {
	dir := t.TempDir()
	s, _, _ := Open(dir, "")
	bak := filepath.Join(dir, "backup-1.2.0-20261001-120000")
	os.MkdirAll(bak, 0o750)
	old, recent := time.Now().Add(-40*24*time.Hour).UTC(), time.Now().Add(-time.Hour).UTC()
	st := stateFile{Version: 1, Secret: "00112233445566778899aabbccddeeff",
		History:  []Event{{ID: "a", Device: "PC", Time: old, Client: "192.168.1.50", Success: true}, {ID: "a", Device: "PC", Time: recent, Client: "192.168.1.51", Success: true}},
		LastWake: map[string]time.Time{"a": recent}, LastSeen: map[string]time.Time{"a": old}}
	b, _ := json.Marshal(st)
	os.WriteFile(filepath.Join(bak, "state.json"), b, 0o600)
	read := func() string {
		b, _ := os.ReadFile(filepath.Join(bak, "state.json"))
		return string(b)
	}
	if err := s.SetRecording(true, 30*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	if got := read(); strings.Contains(got, "192.168.1.50") || !strings.Contains(got, "192.168.1.51") || strings.Contains(got, `"last_seen"`) || !strings.Contains(got, st.Secret) {
		t.Errorf("Sicherung nach 30 Tagen:\n%s", got)
	}
	if err := s.SetRecording(false, 30*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	if got := read(); strings.Contains(got, "192.168.1.51") || strings.Contains(got, `"history"`) || strings.Contains(got, `"last_wake"`) {
		t.Errorf("Sicherung ohne Aufzeichnung:\n%s", got)
	}
}

// blockWrite macht das Schreiben von name im Datenverzeichnis unmöglich (an seiner Stelle liegt kurz
// ein Ordner) und liefert die Funktion, die es wieder erlaubt – die alte Datei bleibt erhalten.
func blockWrite(t *testing.T, dir, name string) (unblock func()) {
	t.Helper()
	path := filepath.Join(dir, name)
	_, err := os.Stat(path)
	existed := err == nil
	if existed {
		os.Rename(path, path+".alt")
	}
	os.MkdirAll(filepath.Join(path, "x"), 0o750)
	return func() {
		os.RemoveAll(path)
		if existed {
			os.Rename(path+".alt", path)
		}
	}
}

// Scheitert beim Wiederherstellen einer Sicherung das Schreiben, lässt sich der Import wiederholen –
// samt Geheimnis für die Gruppen-Wecklinks, egal welcher der beiden Schritte scheiterte.
func TestImportRestoreRetryable(t *testing.T) {
	src, _, _ := Open(t.TempDir(), "")
	src.Create(Input{Name: "PC", MAC: "02:00:00:00:00:01", Group: "Büro"})
	b, _ := src.Export()
	for _, name := range []string{"state.json", "devices.json"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			dst, _, _ := Open(dir, "")
			unblock := blockWrite(t, dir, name)
			if _, err := dst.Import(b, "de"); err == nil {
				t.Fatal("Schreibfehler nicht gemeldet")
			}
			if devs, _ := dst.Devices(); len(devs) != 0 {
				t.Fatalf("halb wiederhergestellt: %d Geräte", len(devs))
			}
			unblock()
			res, err := dst.Import(b, "de")
			if err != nil || res.Added != 1 || !res.SecretRestored || !bytesEqual(dst.Secret(), src.Secret()) {
				t.Fatalf("Wiederholung: %+v %v", res, err)
			}
		})
	}
}
