// Package schedule liest Weckzeiten wie "Mo-Fr 07:30", "Sa,So 09:00", "werktags 6:45" oder
// "täglich 22:00" (Ortszeit, also TZ des Rechners bzw. Containers).
package schedule

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Entry ist eine Weckzeit an bestimmten Wochentagen.
type Entry struct {
	Days      [7]bool // Index wie time.Weekday: 0 = Sonntag
	Hour, Min int
}

// Kurznamen in der Reihenfolge von time.Weekday.
var short = [7]string{"So", "Mo", "Di", "Mi", "Do", "Fr", "Sa"}

// Montag zuerst – so werden Bereiche geschrieben.
var weekOrder = [7]time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday, time.Sunday}

var dayWords = map[string]time.Weekday{
	"so": time.Sunday, "sonntag": time.Sunday, "su": time.Sunday, "sun": time.Sunday,
	"mo": time.Monday, "montag": time.Monday, "mon": time.Monday,
	"di": time.Tuesday, "dienstag": time.Tuesday, "tu": time.Tuesday, "tue": time.Tuesday,
	"mi": time.Wednesday, "mittwoch": time.Wednesday, "we": time.Wednesday, "wed": time.Wednesday,
	"do": time.Thursday, "donnerstag": time.Thursday, "th": time.Thursday, "thu": time.Thursday,
	"fr": time.Friday, "freitag": time.Friday, "fri": time.Friday,
	"sa": time.Saturday, "samstag": time.Saturday, "sat": time.Saturday,
}

// Parse liest einen Eintrag "<Tage> <HH:MM>"; ohne Tage gilt er täglich.
func Parse(s string) (Entry, error) {
	var e Entry
	f := strings.Fields(strings.ToLower(strings.TrimSpace(s)))
	if len(f) == 0 || len(f) > 2 {
		return e, fmt.Errorf("%q: erwartet z. B. \"Mo-Fr 07:30\"", s)
	}
	clock := f[len(f)-1]
	h, m, ok := strings.Cut(clock, ":")
	hour, err1 := strconv.Atoi(h)
	min, err2 := strconv.Atoi(m)
	if !ok || err1 != nil || err2 != nil || hour < 0 || hour > 23 || min < 0 || min > 59 || len(m) != 2 {
		return e, fmt.Errorf("%q: Uhrzeit %q ungültig (HH:MM)", s, clock)
	}
	e.Hour, e.Min = hour, min
	if len(f) == 1 {
		e.Days = [7]bool{true, true, true, true, true, true, true}
		return e, nil
	}
	if err := e.parseDays(f[0]); err != nil {
		return e, fmt.Errorf("%q: %w", s, err)
	}
	return e, nil
}

func (e *Entry) parseDays(s string) error {
	switch s {
	case "täglich", "taeglich", "tgl", "*", "daily":
		e.Days = [7]bool{true, true, true, true, true, true, true}
		return nil
	case "werktags", "wochentags", "weekdays":
		s = "mo-fr"
	case "wochenende", "weekend":
		s = "sa,so"
	}
	for _, part := range strings.Split(s, ",") {
		from, to, isRange := strings.Cut(part, "-")
		a, ok := dayWords[strings.TrimSuffix(from, ".")]
		if !ok {
			return fmt.Errorf("unbekannter Wochentag %q", from)
		}
		if !isRange {
			e.Days[a] = true
			continue
		}
		b, ok := dayWords[strings.TrimSuffix(to, ".")]
		if !ok {
			return fmt.Errorf("unbekannter Wochentag %q", to)
		}
		for d := a; ; d = (d + 1) % 7 { // auch über das Wochenende hinweg, z. B. Fr-Mo
			e.Days[d] = true
			if d == b {
				break
			}
		}
	}
	return nil
}

// String liefert die einheitliche Schreibweise, z. B. "Mo-Fr 07:30" oder "täglich 22:00".
func (e Entry) String() string {
	clock := fmt.Sprintf("%02d:%02d", e.Hour, e.Min)
	if e.Days == [7]bool{true, true, true, true, true, true, true} {
		return "täglich " + clock
	}
	var parts []string
	for i := 0; i < 7; {
		if !e.Days[weekOrder[i]] {
			i++
			continue
		}
		j := i
		for j+1 < 7 && e.Days[weekOrder[j+1]] {
			j++
		}
		switch {
		case j == i:
			parts = append(parts, short[weekOrder[i]])
		case j == i+1:
			parts = append(parts, short[weekOrder[i]], short[weekOrder[j]])
		default:
			parts = append(parts, short[weekOrder[i]]+"-"+short[weekOrder[j]])
		}
		i = j + 1
	}
	return strings.Join(parts, ",") + " " + clock
}

// Due sagt, ob t (auf die Minute genau) eine Weckzeit dieses Eintrags ist.
func (e Entry) Due(t time.Time) bool {
	return e.Days[t.Weekday()] && t.Hour() == e.Hour && t.Minute() == e.Min
}

// Next liefert die nächste Weckzeit nach after (in dessen Zeitzone).
func (e Entry) Next(after time.Time) time.Time {
	day := time.Date(after.Year(), after.Month(), after.Day(), e.Hour, e.Min, 0, 0, after.Location())
	for i := 0; i < 8; i++ {
		t := day.AddDate(0, 0, i)
		if t.After(after) && e.Days[t.Weekday()] {
			return t
		}
	}
	return time.Time{}
}

// ParseList liest mehrere Einträge, getrennt durch ";" oder Zeilenumbruch.
func ParseList(items []string) ([]Entry, error) {
	var out []Entry
	for _, it := range items {
		for _, s := range strings.FieldsFunc(it, func(r rune) bool { return r == ';' || r == '\n' }) {
			if strings.TrimSpace(s) == "" {
				continue
			}
			e, err := Parse(s)
			if err != nil {
				return nil, err
			}
			out = append(out, e)
		}
	}
	if len(out) > 8 {
		return nil, errors.New("höchstens 8 Weckzeiten")
	}
	return out, nil
}

// NextOf liefert die früheste nächste Weckzeit aller Einträge (Null, wenn keine).
func NextOf(entries []Entry, after time.Time) time.Time {
	var best time.Time
	for _, e := range entries {
		if t := e.Next(after); !t.IsZero() && (best.IsZero() || t.Before(best)) {
			best = t
		}
	}
	return best
}
