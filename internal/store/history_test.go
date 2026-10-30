package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Wiederholungen zählen nicht zur Verlaufsgrenze: Sie verdrängen keine anderen Einträge.
func TestHistoryTrimIgnoresRetries(t *testing.T) {
	s, _, _ := Open(t.TempDir(), "")
	at := time.Now().Add(-time.Hour)
	var evs []Event
	for i := range HistoryMax {
		evs = append(evs, Event{Time: at.Add(time.Duration(i) * time.Second), ID: "a", Device: "A", Success: true})
		if i%2 == 0 {
			evs = append(evs, Event{Time: at.Add(time.Duration(i)*time.Second + 500*time.Millisecond), ID: "a", Device: "A", Success: true, Retry: true})
		}
	}
	if err := s.RecordWake(evs...); err != nil {
		t.Fatal(err)
	}
	normal := 0
	for _, ev := range s.History(0) {
		if !ev.Retry {
			normal++
		}
	}
	if normal != HistoryMax {
		t.Fatalf("%d normale Einträge statt %d – Wiederholungen haben verdrängt", normal, HistoryMax)
	}
	s.RecordWake(Event{Time: time.Now(), ID: "a", Device: "A", Success: true})
	h := s.History(0)
	if last := h[len(h)-1]; last.Retry || !last.Time.Equal(evs[1].Time) && !last.Time.Equal(evs[2].Time) {
		t.Fatalf("ältester Eintrag nach dem Kürzen: %+v", last)
	}
}

// Ein "zuletzt geweckt" aus einer Wiederholung (ältere Versionen) fällt beim Laden auf den letzten
// anderen erfolgreichen Weckruf zurück.
func TestLoadFixesRetryLastWake(t *testing.T) {
	dir := t.TempDir()
	first := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	retry := first.Add(45 * time.Second)
	b, _ := json.Marshal(map[string]any{
		"version":   1,
		"last_wake": map[string]time.Time{"a": retry, "b": retry, "c": first},
		"history": []Event{
			{Time: first, ID: "a", Device: "A", Success: true},
			{Time: retry, ID: "a", Device: "A", Success: true, Retry: true},
			{Time: retry, ID: "b", Device: "B", Success: true, Retry: true},
			{Time: first, ID: "c", Device: "C", Success: true},
		},
	})
	os.WriteFile(filepath.Join(dir, "state.json"), b, 0o600)
	s, _, err := Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := s.LastWake("a"); !got.Equal(first) {
		t.Errorf("a: %v statt %v", got, first)
	}
	if got := s.LastWake("b"); !got.IsZero() {
		t.Errorf("b: %v statt leer", got)
	}
	if got := s.LastWake("c"); !got.Equal(first) {
		t.Errorf("c: %v statt %v", got, first)
	}
}
