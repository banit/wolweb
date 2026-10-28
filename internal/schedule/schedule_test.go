package schedule

import (
	"testing"
	"time"
	_ "time/tzdata" // Zeitzonen auch ohne Systemdaten (Windows, schlanke Container)
)

func TestParseAndString(t *testing.T) {
	for in, want := range map[string]string{
		"Mo-Fr 07:30":     "Mo-Fr 07:30",
		"mo-fr 7:30":      "Mo-Fr 07:30",
		"Sa,So 09:00":     "Sa,So 09:00",
		"Wochenende 9:00": "Sa,So 09:00",
		"werktags 06:45":  "Mo-Fr 06:45",
		"täglich 22:00":   "täglich 22:00",
		"22:00":           "täglich 22:00",
		"Mo,Mi,Fr 06:00":  "Mo,Mi,Fr 06:00",
		"Fr-Mo 08:15":     "Mo,Fr-So 08:15",
		"Mon,Tue 23:59":   "Mo,Di 23:59",
		"Mo-So 00:00":     "täglich 00:00",
	} {
		e, err := Parse(in)
		if err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		if got := e.String(); got != want {
			t.Errorf("%q → %q statt %q", in, got, want)
		}
	}
	for _, in := range []string{"", "Mo", "Mo 25:00", "Mo 7:5", "Xy 07:00", "Mo-Fr 07:30 extra", "Mo 07"} {
		if _, err := Parse(in); err == nil {
			t.Errorf("%q sollte ungültig sein", in)
		}
	}
}

func TestDueAndNext(t *testing.T) {
	e, _ := Parse("Mo-Fr 07:30")
	loc := time.FixedZone("CET", 3600)
	fri := time.Date(2026, 10, 30, 7, 30, 20, 0, loc) // Freitag
	if !e.Due(fri) || e.Due(fri.Add(time.Minute)) || e.Due(fri.AddDate(0, 0, 1)) {
		t.Error("Due falsch")
	}
	if next := e.Next(fri); !next.Equal(time.Date(2026, 11, 2, 7, 30, 0, 0, loc)) { // Montag
		t.Errorf("Next nach Freitag: %v", next)
	}
	if next := e.Next(fri.Add(-time.Hour)); !next.Equal(time.Date(2026, 10, 30, 7, 30, 0, 0, loc)) {
		t.Errorf("Next am selben Tag: %v", next)
	}
	list, err := ParseList([]string{"Mo-Fr 07:30; Sa,So 09:00"})
	if err != nil || len(list) != 2 {
		t.Fatalf("%v %v", list, err)
	}
	if n := NextOf(list, fri); !n.Equal(time.Date(2026, 10, 31, 9, 0, 0, 0, loc)) { // Samstag
		t.Errorf("NextOf: %v", n)
	}
}

// minutes liefert alle Minuten zwischen from und to (als echte Zeitpunkte, also auch die doppelte
// Stunde der Rückstellung zweimal).
func minutes(from, to time.Time) []time.Time {
	var out []time.Time
	for t := from; t.Before(to); t = t.Add(time.Minute) {
		out = append(out, t)
	}
	return out
}

func berlin(t *testing.T) *time.Location {
	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skip("keine Zeitzonendaten:", err)
	}
	return loc
}

// Rückstellung (25.10.2026): 02:30 gibt es zweimal – geweckt wird einmal, beim ersten Mal.
func TestFallBackOnce(t *testing.T) {
	loc := berlin(t)
	e, _ := Parse("täglich 02:30")
	var due []time.Time
	for _, m := range minutes(time.Date(2026, 10, 24, 22, 0, 0, 0, time.UTC), time.Date(2026, 10, 25, 4, 0, 0, 0, time.UTC)) {
		if e.Due(m.In(loc)) {
			due = append(due, m)
		}
	}
	if len(due) != 1 || !due[0].Equal(time.Date(2026, 10, 25, 0, 30, 0, 0, time.UTC)) {
		t.Fatalf("geweckt: %v", due)
	}
	if n := e.Next(time.Date(2026, 10, 24, 12, 0, 0, 0, loc)); !n.Equal(due[0]) {
		t.Errorf("Next zeigt %v statt %v", n, due[0])
	}
	if n := e.Next(due[0].In(loc)); !n.Equal(time.Date(2026, 10, 26, 2, 30, 0, 0, loc)) || n.Hour() != 2 {
		t.Errorf("danach: %v", n)
	}
}

// Umstellung auf Sommerzeit (29.03.2026): 02:30 gibt es nicht – geweckt wird einmal um 03:00, am
// nächsten Tag wieder um 02:30 (nicht 03:30).
func TestSpringForwardNextValidMinute(t *testing.T) {
	loc := berlin(t)
	e, _ := Parse("täglich 02:30")
	var due []time.Time
	for _, m := range minutes(time.Date(2026, 3, 28, 23, 0, 0, 0, time.UTC), time.Date(2026, 3, 29, 3, 0, 0, 0, time.UTC)) {
		if e.Due(m.In(loc)) {
			due = append(due, m.In(loc))
		}
	}
	if len(due) != 1 || due[0].Hour() != 3 || due[0].Minute() != 0 {
		t.Fatalf("geweckt: %v", due)
	}
	next := e.Next(time.Date(2026, 3, 28, 12, 0, 0, 0, loc))
	if !next.Equal(due[0]) {
		t.Errorf("Next zeigt %v statt %v", next, due[0])
	}
	for i, want := range []int{30, 31, 1} { // die folgenden Tage
		next = e.Next(next)
		if next.Hour() != 2 || next.Minute() != 30 || next.Day() != want {
			t.Errorf("Tag %d danach: %v", i+1, next)
		}
	}
	// Ein Eintrag nur werktags trifft den Sonntag der Umstellung gar nicht.
	w, _ := Parse("Mo-Fr 02:30")
	if n := w.Next(time.Date(2026, 3, 28, 12, 0, 0, 0, loc)); n.Day() != 30 || n.Hour() != 2 || n.Minute() != 30 {
		t.Errorf("werktags: %v", n)
	}
}
