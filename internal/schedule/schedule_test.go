package schedule

import (
	"testing"
	"time"
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
