package i18n

import (
	"errors"
	"net/http/httptest"
	"regexp"
	"slices"
	"testing"
)

var placeholder = regexp.MustCompile(`\{\d+\}`)

// Beide Kataloge müssen dieselben Schlüssel mit denselben Platzhaltern haben.
func TestCatalogsMatch(t *testing.T) {
	de, en := Catalog(DE), Catalog(EN)
	if len(de) < 100 {
		t.Fatalf("nur %d Texte geladen", len(de))
	}
	for k, v := range de {
		w, ok := en[k]
		if !ok {
			t.Errorf("en.json: %q fehlt", k)
			continue
		}
		a, b := placeholder.FindAllString(v, -1), placeholder.FindAllString(w, -1)
		slices.Sort(a)
		slices.Sort(b)
		if !slices.Equal(a, b) {
			t.Errorf("%q: Platzhalter %v (de) gegen %v (en)", k, a, b)
		}
	}
	for k := range en {
		if _, ok := de[k]; !ok {
			t.Errorf("de.json: %q fehlt", k)
		}
	}
}

func TestT(t *testing.T) {
	if got := T(EN, "msg.wake_sent", "NAS"); got != "Wake-up call sent to NAS." {
		t.Errorf("EN: %q", got)
	}
	if got := T(DE, "msg.group_sent", 2, 3, "Homelab"); got != "Weckruf an 2 von 3 Geräten der Gruppe Homelab gesendet." {
		t.Errorf("DE: %q", got)
	}
	if got := T(EN, "gibt.es.nicht"); got != "gibt.es.nicht" {
		t.Errorf("fehlender Schlüssel: %q", got)
	}
	// Verschachtelte Meldung wird mitübersetzt, Fehler bleiben mit errors.Is erreichbar.
	inner := errors.New("connection refused")
	m := M("wol.send", "192.168.1.255:9", inner)
	if got := m.In(EN); got != "Sending to 192.168.1.255:9: connection refused" {
		t.Errorf("verschachtelt: %q", got)
	}
	if !errors.Is(m, inner) {
		t.Error("errors.Is durch die Meldung hindurch")
	}
	if got := T(EN, "val.schedule", M("sched.max")); got != "Schedule: at most 8 wake times" {
		t.Errorf("Meldung als Argument: %q", got)
	}
	if got := ErrorText(EN, errors.Join(M("scan.cancelled"), errors.New("x"))); got != "cancelled\nx" {
		t.Errorf("Join: %q", got)
	}
}

func TestFromRequest(t *testing.T) {
	for _, tc := range []struct {
		url, cookie, accept string
		want                Lang
	}{
		{"/", "", "", DE},
		{"/", "", "en-US,en;q=0.9", EN},
		{"/", "", "fr-FR,en;q=0.8,de;q=0.5", EN}, // erste unterstützte Sprache
		{"/", "de", "en-US", DE},                 // Umschalter vor Browser
		{"/?lang=en", "de", "de", EN},            // Parameter vor allem
		{"/", "xx", "", DE},
	} {
		r := httptest.NewRequest("GET", tc.url, nil)
		if tc.cookie != "" {
			r.Header.Set("Cookie", CookieName+"="+tc.cookie)
		}
		if tc.accept != "" {
			r.Header.Set("Accept-Language", tc.accept)
		}
		if got := FromRequest(r, DE); got != tc.want {
			t.Errorf("%+v: %s", tc, got)
		}
	}
}
