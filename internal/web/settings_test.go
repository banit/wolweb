package web

import (
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"wolweb/internal/config"
	"wolweb/internal/discovery"
	"wolweb/internal/i18n"
	"wolweb/internal/probe"
	"wolweb/internal/status"
	"wolweb/internal/store"
)

// settingsServer startet einen Server mit Einstellungsseite (config.json und Datenverzeichnis im
// Testordner).
func settingsServer(t *testing.T, cfgJSON string) (*Server, http.Handler, *config.Manager) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	dataDir, _ := json.Marshal(filepath.Join(dir, "data")) // als JSON-Text: Windows-Pfade haben Backslashes
	cfgJSON = strings.Replace(cfgJSON, "{", `{"data_dir": `+string(dataDir)+`, `, 1)
	os.WriteFile(path, []byte(cfgJSON), 0o644)
	m, err := config.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg := m.Config()
	st, _, err := store.Open(cfg.DataDir, "")
	if err != nil {
		t.Fatal(err)
	}
	mon := status.New(st, probe.New(cfg.Status.Ports, 100*time.Millisecond), 0)
	sc := discovery.New(st, cfg.Discovery.ScanCIDRs(), 16)
	s := New(cfg, st, mon, sc, false, "test")
	s.SetSettings(m)
	h := s.Handler()
	return s, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host == "example.com" { // Vorgabe von httptest – der Schutz vor DNS-Rebinding lehnte sie ab
			r.Host = "192.168.1.10:8089"
		}
		h.ServeHTTP(w, r)
	}), m
}

func call(h http.Handler, method, url, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, url, strings.NewReader(body))
	req.Header.Set("X-WolWeb", "1")
	req.Header.Set("Accept-Language", "de")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestSettingsAPI(t *testing.T) {
	s, h, _ := settingsServer(t, `{"discovery": {"networks": ["192.168.1.0/24"]}, "status": {"wake_retry": "45s"}}`)
	rec := call(h, "GET", "/api/v1/settings", "")
	var v settingsView
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil || rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if len(v.Networks) != 1 || v.Networks[0].Broadcast != "192.168.1.255:9" || v.Auth || len(v.Events) != 4 {
		t.Errorf("Ansicht: %+v", v)
	}

	// Ändern: gilt sofort, ohne Neustart
	e := v.Settings
	e.Status.WakeRetry = "10s"
	e.Discovery.Networks = append(e.Discovery.Networks, config.Network{CIDR: "10.0.0.0/24", Name: "IoT", VLAN: 20, Scan: true})
	e.Language = "de"
	b, _ := json.Marshal(e)
	rec = call(h, "PUT", "/api/v1/settings", string(b))
	if rec.Code != 200 {
		t.Fatalf("PUT: %d %s", rec.Code, rec.Body)
	}
	json.Unmarshal(rec.Body.Bytes(), &v)
	if s.rt().wakeRetry != 10*time.Second || s.rt().lang != "de" || len(s.scanner.Networks()) != 2 {
		t.Errorf("nicht übernommen: %v %v %v", s.rt().wakeRetry, s.rt().lang, s.scanner.Networks())
	}
	if len(v.Stored) != 3 || v.Networks[1].Label != "IoT (VLAN 20)" || v.Networks[1].Local != nil {
		t.Errorf("Antwort: %v %+v", v.Stored, v.Networks)
	}
	// Ein Gerät im IoT-Netz ohne eigene Schnittstelle bekommt den gerichteten Broadcast.
	if got := s.wakeTarget(store.Device{MAC: "00:11:22:33:44:55", IP: "10.0.0.7"}); got != "10.0.0.255:9" {
		t.Errorf("Ziel im fremden VLAN: %s", got)
	}
	var info map[string]any
	json.Unmarshal(call(h, "GET", "/api/v1/info", "").Body.Bytes(), &info)
	if segs, _ := info["segments"].([]any); len(segs) != 2 || info["settings"] != true || info["wake_retry"].(float64) != 10 {
		t.Errorf("info: %v", info)
	}

	// Fehler nennen das Feld
	e.Status.Interval = "1s"
	b, _ = json.Marshal(e)
	rec = call(h, "PUT", "/api/v1/settings", string(b))
	var fe map[string]any
	json.Unmarshal(rec.Body.Bytes(), &fe)
	if rec.Code != 400 || fe["field"] != "status.interval" || !strings.Contains(fe["message"].(string), "mindestens") {
		t.Errorf("Fehler: %d %s", rec.Code, rec.Body)
	}
	// Unbekannte Felder (etwa ein Passwort) werden abgewiesen
	if rec := call(h, "PUT", "/api/v1/settings", `{"auth":{"password":"x"}}`); rec.Code != 400 {
		t.Errorf("Passwort angenommen: %d %s", rec.Code, rec.Body)
	}

	// Zurücksetzen
	rec = call(h, "DELETE", "/api/v1/settings", "")
	json.Unmarshal(rec.Body.Bytes(), &v)
	if rec.Code != 200 || len(v.Stored) != 0 || s.rt().wakeRetry != 45*time.Second || len(s.scanner.Networks()) != 1 {
		t.Errorf("zurückgesetzt: %d %v %v", rec.Code, v.Stored, s.rt().wakeRetry)
	}
	// Ohne X-WolWeb keine Änderung
	req := httptest.NewRequest("DELETE", "/api/v1/settings", nil)
	req.Host = "192.168.1.10:8089"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("ohne Kopfzeile: %d", rr.Code)
	}
	// Die Seite selbst
	if rec := call(h, "GET", "/settings", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), "settings.js") {
		t.Errorf("Seite: %d", rec.Code)
	}
}

// Gleichzeitige Änderungen: Was gespeichert ist, muss auch gelten. Ohne gemeinsame Sperre für
// Speichern und Übernehmen konnte eine ältere Anfrage ihren Stand nach einer neueren übernehmen.
func TestSettingsConcurrentSaves(t *testing.T) {
	s, h, m := settingsServer(t, `{"status": {"wake_retry": "45s"}}`)
	var wg sync.WaitGroup
	for i := range 40 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e := m.Config().Editable()
			e.Status.WakeRetry = []string{"10s", "20s", "30s", "40s"}[i%4]
			b, _ := json.Marshal(e)
			if rec := call(h, "PUT", "/api/v1/settings", string(b)); rec.Code != 200 {
				t.Errorf("PUT: %d %s", rec.Code, rec.Body)
			}
		}()
	}
	wg.Wait()
	_, _, _, want := m.Config().Durations()
	if got := s.rt().wakeRetry; got != want {
		t.Errorf("gilt %v, gespeichert %v", got, want)
	}
}

func TestSettingsReadOnlyAndUnavailable(t *testing.T) {
	_, h, _ := settingsServer(t, `{"read_only": true}`)
	if rec := call(h, "PUT", "/api/v1/settings", `{}`); rec.Code != http.StatusForbidden {
		t.Errorf("Nur-Lesen: %d", rec.Code)
	}
	if rec := call(h, "GET", "/api/v1/settings", ""); rec.Code != 200 {
		t.Errorf("Ansehen im Nur-Lesen-Modus: %d", rec.Code)
	}
	plain, _ := newTestServer(t, nil) // ohne Manager
	if rec := call(plain, "GET", "/api/v1/settings", ""); rec.Code != http.StatusNotFound {
		t.Errorf("ohne Manager: %d", rec.Code)
	}
}

func TestSettingsTestNotify(t *testing.T) {
	_, h, _ := settingsServer(t, `{"language": "de"}`)
	got := make(chan string, 1)
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- string(b)
	}))
	defer hook.Close()
	rec := call(h, "POST", "/api/v1/settings/test-notify", `{"webhook":"`+hook.URL+`/x","ntfy":""}`)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if b := <-got; !strings.Contains(b, `"event":"test"`) {
		t.Errorf("Webhook bekam %s", b)
	}
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer broken.Close()
	if rec := call(h, "POST", "/api/v1/settings/test-notify", `{"webhook":"`+broken.URL+`/geheim","ntfy":""}`); rec.Code != http.StatusBadGateway || strings.Contains(rec.Body.String(), "geheim") || strings.Contains(rec.Body.String(), "500") {
		t.Errorf("Fehler: %d %s", rec.Code, rec.Body)
	}
	if rec := call(h, "POST", "/api/v1/settings/test-notify", `{"webhook":"ftp://x","ntfy":""}`); rec.Code != 400 || !strings.Contains(rec.Body.String(), "notify.webhook") {
		t.Errorf("ungültige Adresse: %d %s", rec.Code, rec.Body)
	}
}

// Ohne eingetragenes Netz: Die Wahl in der Suche landet in den Einstellungen und gilt sofort.
func TestChooseNetwork(t *testing.T) {
	s, h, m := settingsServer(t, `{"language": "de"}`)
	if s.scanner.Configured() {
		t.Fatal("es sollte kein Netz eingetragen sein")
	}
	rec := call(h, "POST", "/api/v1/discovery/network", `{"cidr":"10.0.0.7/24"}`)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if !s.scanner.Configured() || s.scanner.Networks()[0] != "10.0.0.0/24" || m.Stored()[0] != "discovery.networks" {
		t.Errorf("nicht übernommen: %v %v", s.scanner.Networks(), m.Stored())
	}
	if rec := call(h, "POST", "/api/v1/discovery/network", `{"cidr":"kein-netz"}`); rec.Code != 400 {
		t.Errorf("ungültiges Netz: %d", rec.Code)
	}
	for _, body := range []string{`{}`, `{"cidr":""}`, `{"cidrs":[]}`, `{"cidrs":[""]}`} {
		if rec := call(h, "POST", "/api/v1/discovery/network", body); rec.Code != 400 || len(s.scanner.Networks()) != 1 {
			t.Errorf("leere Auswahl %s: %d, Netze %v", body, rec.Code, s.scanner.Networks())
		}
	}
	// „Alle erkannten Netze durchsuchen“
	if rec := call(h, "POST", "/api/v1/discovery/network", `{"cidrs":["10.0.0.0/24","10.1.0.0/24"]}`); rec.Code != 200 || len(s.scanner.Networks()) != 2 {
		t.Errorf("alle: %d %v", rec.Code, s.scanner.Networks())
	}
	// Netze eingetragen, keins durchsucht: info meldet „ausgeschaltet“, keine Auswahl
	e := m.Config().Editable()
	for i := range e.Discovery.Networks {
		e.Discovery.Networks[i].Scan = false
	}
	if _, err := m.Save(e); err != nil {
		t.Fatal(err)
	}
	s.Apply(m.Config())
	var info map[string]any
	json.Unmarshal(call(h, "GET", "/api/v1/info", "").Body.Bytes(), &info)
	if info["scan_off"] != true || len(info["networks"].([]any)) != 0 || len(info["network_choices"].([]any)) != 0 {
		t.Errorf("info: %v %v %v", info["scan_off"], info["networks"], info["network_choices"])
	}
}

// Jede wählbare Akzentfarbe hat Farben in app.css und einen Namen in beiden Sprachen; die Seiten
// tragen die gewählte als data-accent und theme-color.
func TestAccents(t *testing.T) {
	css, _ := assets.ReadFile("static/app.css")
	for _, a := range config.Accents {
		if !strings.Contains(string(css), `[data-accent="`+a.Name+`"] { --a-l: `+a.Color+`;`) {
			t.Errorf("app.css: keine Farben für %q (hell %s)", a.Name, a.Color)
		}
		for _, l := range []i18n.Lang{i18n.DE, i18n.EN} {
			if _, ok := i18n.Catalog(l)["set.accent."+a.Name]; !ok {
				t.Errorf("%s: set.accent.%s fehlt", l, a.Name)
			}
		}
	}
	// Kontrast nach WCAG AA (4,5:1) mit jeder Akzentfarbe: Schrift auf Knöpfen (--accent-contrast) und
	// die Farbe als Schrift auf Hintergrund und Flächen, hell und dunkel (Werte wie in app.css).
	tokens := regexp.MustCompile(`\[data-accent="(\w+)"\] \{ --a-l: (#\w{6}); --a-lh: (#\w{6}); --a-d: (#\w{6}); --a-dh: (#\w{6}); \}`)
	found := tokens.FindAllStringSubmatch(string(css), -1)
	if len(found) != len(config.Accents) {
		t.Errorf("app.css: %d Akzentfarben erkannt, erwartet %d", len(found), len(config.Accents))
	}
	for _, m := range found {
		l, lh, d, dh := m[2], m[3], m[4], m[5]
		for _, p := range [][2]string{
			{"#ffffff", l}, {"#ffffff", lh}, {l, "#f4f5f8"}, // hell: Knopfschrift, Text auf --bg/--surface
			{"#0c0f14", d}, {"#0c0f14", dh}, {d, "#151a22"}, // dunkel: Knopfschrift, Text auf --surface/--bg
		} {
			if c := contrast(p[0], p[1]); c < 4.5 {
				t.Errorf("%s: Kontrast %s auf %s nur %.2f:1", m[1], p[0], p[1], c)
			}
		}
	}
	_, h, _ := settingsServer(t, `{"accent": "teal"}`)
	for _, path := range []string{"/", "/settings", "/wake/gibtsnicht?format=html"} {
		body := call(h, "GET", path, "").Body.String()
		if !strings.Contains(body, `data-accent="teal"`) || !strings.Contains(body, `<meta name="theme-color" content="#0f766e">`) {
			t.Errorf("%s: Akzentfarbe fehlt", path)
		}
	}
	if rec := call(h, "PUT", "/api/v1/settings", `{"accent":"neon"}`); rec.Code != 400 || !strings.Contains(rec.Body.String(), `"field":"accent"`) {
		t.Errorf("ungültige Farbe: %d %s", rec.Code, rec.Body)
	}
}

// contrast: Kontrastverhältnis zweier Farben (#rrggbb) nach WCAG.
func contrast(a, b string) float64 {
	lum := func(hex string) float64 {
		var c [3]float64
		for i := range c {
			v, _ := strconv.ParseUint(hex[1+2*i:3+2*i], 16, 8)
			x := float64(v) / 255
			if x <= 0.03928 {
				c[i] = x / 12.92
			} else {
				c[i] = math.Pow((x+0.055)/1.055, 2.4)
			}
		}
		return 0.2126*c[0] + 0.7152*c[1] + 0.0722*c[2]
	}
	x, y := lum(a), lum(b)
	return (math.Max(x, y) + 0.05) / (math.Min(x, y) + 0.05)
}
