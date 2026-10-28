package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wolweb/internal/config"
	"wolweb/internal/discovery"
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
	cfgJSON = strings.Replace(cfgJSON, "{", `{"data_dir": "`+filepath.Join(dir, "data")+`", `, 1)
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
		r.Host = "192.168.1.10:8089"
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
	e.Discovery.Networks = append(e.Discovery.Networks, config.Network{CIDR: "10.9.20.0/24", Name: "IoT", VLAN: 20, Scan: true})
	e.Language = "en"
	b, _ := json.Marshal(e)
	rec = call(h, "PUT", "/api/v1/settings", string(b))
	if rec.Code != 200 {
		t.Fatalf("PUT: %d %s", rec.Code, rec.Body)
	}
	json.Unmarshal(rec.Body.Bytes(), &v)
	if s.rt().wakeRetry != 10*time.Second || s.rt().lang != "en" || len(s.scanner.Networks()) != 2 {
		t.Errorf("nicht übernommen: %v %v %v", s.rt().wakeRetry, s.rt().lang, s.scanner.Networks())
	}
	if len(v.Stored) != 3 || v.Networks[1].Label != "IoT (VLAN 20)" || v.Networks[1].Local != nil {
		t.Errorf("Antwort: %v %+v", v.Stored, v.Networks)
	}
	// Ein Gerät im IoT-Netz ohne eigene Schnittstelle bekommt den gerichteten Broadcast.
	if got := s.wakeTarget(store.Device{MAC: "00:11:22:33:44:55", IP: "10.9.20.7"}); got != "10.9.20.255:9" {
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
	if rec := call(h, "POST", "/api/v1/settings/test-notify", `{"webhook":"`+broken.URL+`/geheim","ntfy":""}`); rec.Code != http.StatusBadGateway || strings.Contains(rec.Body.String(), "geheim") {
		t.Errorf("Fehler: %d %s", rec.Code, rec.Body)
	}
	if rec := call(h, "POST", "/api/v1/settings/test-notify", `{"webhook":"ftp://x","ntfy":""}`); rec.Code != 400 || !strings.Contains(rec.Body.String(), "notify.webhook") {
		t.Errorf("ungültige Adresse: %d %s", rec.Code, rec.Body)
	}
}
