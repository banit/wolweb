package config

// Accent ist eine wählbare Akzentfarbe: Name (Wert von "accent") und Farbe im hellen Modus – für
// <meta name="theme-color">. Die Farben für hell und dunkel stehen in app.css ([data-accent]); alle
// erreichen mindestens 4,5:1 zur Schrift auf Knöpfen und als Schrift auf den Flächen.
type Accent struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

// DefaultAccent ist die Farbe, die wolweb ohne Einstellung zeigt.
const DefaultAccent = "indigo"

var Accents = []Accent{
	{"indigo", "#4f46e5"}, {"blue", "#2563eb"}, {"sky", "#0369a1"}, {"cyan", "#0e7490"},
	{"teal", "#0f766e"}, {"emerald", "#047857"}, {"green", "#15803d"}, {"lime", "#477510"},
	{"amber", "#b45309"}, {"orange", "#c2410c"}, {"red", "#b91c1c"}, {"rose", "#be123c"},
	{"pink", "#be185d"}, {"fuchsia", "#a21caf"}, {"purple", "#9333ea"}, {"violet", "#7c3aed"},
	{"slate", "#475569"},
}

// AccentColor liefert die Farbe einer Akzentfarbe (unbekannt: die Standardfarbe).
func AccentColor(name string) string {
	for _, a := range Accents {
		if a.Name == name {
			return a.Color
		}
	}
	return Accents[0].Color
}

func accentNames() []string {
	out := make([]string, len(Accents))
	for i, a := range Accents {
		out[i] = a.Name
	}
	return out
}
