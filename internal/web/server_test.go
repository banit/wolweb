package web

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"wolweb/internal/config"
	"wolweb/internal/discovery"
	"wolweb/internal/probe"
	"wolweb/internal/status"
	"wolweb/internal/store"
)

func newTestServer(t *testing.T, mutate func(*config.Config)) (http.Handler, *store.Store) {
	t.Helper()
	cfg := config.Default()
	if mutate != nil {
		mutate(&cfg)
	}
	st, _, err := store.Open(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	mon := status.New(st, probe.New(nil, 100*time.Millisecond), 0)
	sc := discovery.New(st, nil, 16)
	h := New(cfg, st, mon, sc, false, "test").Handler()
	// httptest.NewRequest setzt den Host "example.com" – den lehnt der Schutz vor DNS-Rebinding
	// ab. Tests ohne eigenen Host laufen daher unter einer IP-Adresse.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host == "example.com" {
			r.Host = "192.168.1.10:8089"
		}
		h.ServeHTTP(w, r)
	}), st
}

func TestWakeLinkCrossSiteDoesNotWake(t *testing.T) {
	h, st := newTestServer(t, nil)
	d, err := st.Create(store.Input{Name: "NAS", MAC: "00:11:22:33:44:55"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/wake/NAS?format=json", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("fremde Seite bekam %q statt der Weckseite", rec.Header().Get("Content-Type"))
	}
	if !st.LastWake(d.ID).IsZero() {
		t.Fatal("Aufruf einer fremden Seite hat geweckt")
	}
}

func TestMutationsNeedHeader(t *testing.T) {
	h, _ := newTestServer(t, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/devices", strings.NewReader(`{"name":"x","mac":"00:11:22:33:44:55"}`)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("ohne X-WolWeb: %d", rec.Code)
	}
	req := httptest.NewRequest("POST", "/api/v1/devices", strings.NewReader(`{"name":"x","mac":"00:11:22:33:44:55"}`))
	req.Header.Set("X-WolWeb", "1")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("mit X-WolWeb: %d %s", rec.Code, rec.Body)
	}
}

func TestAuth(t *testing.T) {
	h, _ := newTestServer(t, func(c *config.Config) {
		c.Auth.Username, c.Auth.Password, c.Auth.APIToken = "admin", "geheim", "tok"
	})
	for _, tc := range []struct {
		path string
		set  func(*http.Request)
		want int
	}{
		{"/api/v1/devices", func(*http.Request) {}, http.StatusUnauthorized},
		{"/api/v1/devices", func(r *http.Request) { r.SetBasicAuth("admin", "falsch") }, http.StatusUnauthorized},
		{"/api/v1/devices", func(r *http.Request) { r.SetBasicAuth("admin", "geheim") }, http.StatusOK},
		{"/api/v1/devices", func(r *http.Request) { r.Header.Set("Authorization", "Bearer tok") }, http.StatusOK},
		{"/health", func(*http.Request) {}, http.StatusOK},
		{"/wake/gibtsnicht", func(*http.Request) {}, http.StatusNotFound}, // öffentlich, aber unbekannt
	} {
		req := httptest.NewRequest("GET", tc.path, nil)
		tc.set(req)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("%s: %d statt %d", tc.path, rec.Code, tc.want)
		}
	}
}

func TestVDir(t *testing.T) {
	h, _ := newTestServer(t, func(c *config.Config) { c.VDir = "/wolweb/" })
	for path, want := range map[string]int{"/wolweb": http.StatusFound, "/wolweb/": http.StatusOK, "/wolweb/health": http.StatusOK, "/health": http.StatusNotFound} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != want {
			t.Errorf("%s: %d statt %d", path, rec.Code, want)
		}
	}
}

func TestDefaultBroadcast(t *testing.T) {
	st, _, err := store.Open(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	mon := status.New(st, probe.New(nil, 100*time.Millisecond), 0)
	for _, tc := range []struct {
		bcast string
		nets  []string
		want  string
	}{
		{"", nil, "255.255.255.255:9"},
		{"", []string{"10.9.8.0/24"}, "10.9.8.255:9"},
		{"", []string{"10.9.8.0/24", "10.9.9.0/24"}, "255.255.255.255:9"},
		{"192.168.5.255", []string{"10.9.8.0/24"}, "192.168.5.255:9"},
	} {
		cfg := config.Default()
		cfg.BcastIP = tc.bcast
		s := New(cfg, st, mon, discovery.New(st, tc.nets, 1024), false, "test")
		if got := s.wakeTarget(store.Device{MAC: "00:11:22:33:44:55"}); got != tc.want {
			t.Errorf("bcastip=%q Netze=%v: %s statt %s", tc.bcast, tc.nets, got, tc.want)
		}
	}
}

// udpSink nimmt Magic Packets entgegen, damit das Wecken im Test gelingt.
func udpSink(t *testing.T) string {
	t.Helper()
	l, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return l.LocalAddr().String()
}

func TestWakeLinkHeadDoesNotWake(t *testing.T) {
	h, st := newTestServer(t, nil)
	d, err := st.Create(store.Input{Name: "NAS", MAC: "00:11:22:33:44:55", Broadcast: udpSink(t)})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("HEAD", "/wake/NAS", nil))
	if !st.LastWake(d.ID).IsZero() || rec.Code != http.StatusOK {
		t.Fatalf("HEAD hat geweckt (Status %d)", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/wake/NAS", nil))
	if st.LastWake(d.ID).IsZero() {
		t.Fatalf("GET hat nicht geweckt: %d %s", rec.Code, rec.Body)
	}
}

func TestWakeWaitWithoutIP(t *testing.T) {
	h, st := newTestServer(t, nil)
	if _, err := st.Create(store.Input{Name: "NAS", MAC: "00:11:22:33:44:55", Broadcast: udpSink(t)}); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/wake/NAS?wait=30", nil))
	var res map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil || res["success"] != true {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if _, ok := res["online"]; ok || time.Since(start) > 5*time.Second {
		t.Errorf("ohne IP: online=%v nach %s", res["online"], time.Since(start))
	}
}

func TestHostGuard(t *testing.T) {
	for _, tc := range []struct {
		host    string
		allowed []string
		auth    bool
		want    int
	}{
		{"192.168.1.10:8089", nil, false, http.StatusOK},
		{"[::1]:8089", nil, false, http.StatusOK},
		{"nas:8089", nil, false, http.StatusOK},
		{"wol.fritz.box", nil, false, http.StatusOK},
		{"wol.home.arpa", nil, false, http.StatusOK},
		{"localhost:8089", nil, false, http.StatusOK},
		{"rebind.evil.example", nil, false, http.StatusForbidden},
		{"wol.example.com", []string{"wol.example.com"}, false, http.StatusOK},
		{"a.example.com", []string{"*.example.com"}, false, http.StatusOK},
		{"evil.example", []string{"*"}, false, http.StatusOK},
		{"wol.example.com", nil, true, http.StatusUnauthorized},                    // mit Anmeldung keine Prüfung …
		{"wol.example.com", []string{"x.example.com"}, true, http.StatusForbidden}, // … außer mit eigener Liste
	} {
		h, _ := newTestServer(t, func(c *config.Config) {
			c.AllowedHosts = tc.allowed
			if tc.auth {
				c.Auth.Username, c.Auth.Password = "admin", "geheim"
			}
		})
		req := httptest.NewRequest("GET", "/api/v1/devices", nil)
		req.Host = tc.host
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("%s %v: %d statt %d", tc.host, tc.allowed, rec.Code, tc.want)
		}
	}
	// /health bleibt für Healthchecks immer erreichbar.
	h, _ := newTestServer(t, nil)
	req := httptest.NewRequest("GET", "/health", nil)
	req.Host = "rebind.evil.example"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("/health: %d", rec.Code)
	}
}

func TestWakeKeysAndRateLimit(t *testing.T) {
	h, st := newTestServer(t, func(c *config.Config) {
		c.Auth.Username, c.Auth.Password, c.Auth.WakeKeys = "admin", "geheim", true
	})
	d, err := st.Create(store.Input{Name: "NAS", MAC: "00:11:22:33:44:55", Broadcast: udpSink(t)})
	if err != nil || d.WakeKey == "" {
		t.Fatalf("%v, Schlüssel %q", err, d.WakeKey)
	}
	get := func(path string, auth bool) int {
		req := httptest.NewRequest("GET", path, nil)
		if auth {
			req.SetBasicAuth("admin", "geheim")
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if c := get("/wake/NAS", false); c != http.StatusNotFound {
		t.Errorf("ohne Schlüssel: %d", c)
	}
	if c := get("/wake/NAS?key=falsch", false); c != http.StatusNotFound {
		t.Errorf("falscher Schlüssel: %d", c)
	}
	if c := get("/wake/NAS?key="+d.WakeKey, false); c != http.StatusOK {
		t.Errorf("richtiger Schlüssel: %d", c)
	}
	if c := get("/wake/NAS?key="+d.WakeKey, false); c != http.StatusTooManyRequests {
		t.Errorf("sofort wiederholt: %d statt 429", c)
	}
	if c := get("/wake/NAS", true); c != http.StatusOK {
		t.Errorf("angemeldet ohne Schlüssel und ohne Begrenzung: %d", c)
	}
	// Neuer Schlüssel: der alte gilt nicht mehr.
	nd, _ := st.RotateWakeKey(d.ID)
	if nd.WakeKey == d.WakeKey || get("/wake/NAS/status?key="+d.WakeKey, false) != http.StatusNotFound {
		t.Error("alter Schlüssel gilt noch")
	}
}
