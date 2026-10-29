package store

import (
	"os"
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
