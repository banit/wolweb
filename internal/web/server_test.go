package web

import (
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
	return New(cfg, st, mon, sc, false, "test").Handler(), st
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
	if got, _ := st.Get(d.ID); got.LastWake != nil {
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
