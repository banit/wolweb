package store

import (
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
