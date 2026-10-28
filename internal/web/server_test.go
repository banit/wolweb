package web

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"wolweb/internal/config"
	"wolweb/internal/discovery"
	"wolweb/internal/i18n"
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
	// /health bleibt für Healthchecks immer erreichbar, static/ für die Fehlerseite selbst.
	h, _ := newTestServer(t, nil)
	for _, p := range []string{"/health", "/static/app.css"} {
		req := httptest.NewRequest("GET", p, nil)
		req.Host = "rebind.evil.example"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: %d", p, rec.Code)
		}
	}
}

// Update von 1.0.x hinter einem Reverse-Proxy: Die Ablehnung nennt Namen und Abhilfe, die
// Einstellungen bieten den Namen zum Erlauben an – und danach geht es ohne Neustart.
func TestHostGuardExplainsAndOffersHost(t *testing.T) {
	_, h, _ := settingsServer(t, `{"language": "de"}`)
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "wol.example.com"
	req.Header.Set("Accept", "text/html")
	rec := httptest.NewRecorder()
	h.(http.HandlerFunc)(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusForbidden || !strings.Contains(body, "wol.example.com") || !strings.Contains(body, "WOLWEB_ALLOWED_HOSTS=wol.example.com") ||
		!strings.Contains(body, `"allowed_hosts": ["wol.example.com"]`) {
		t.Fatalf("%d %s", rec.Code, body)
	}
	var v settingsView
	json.Unmarshal(call(h, "GET", "/api/v1/settings", "").Body.Bytes(), &v)
	if len(v.RejectedHosts) != 1 || v.RejectedHosts[0] != "wol.example.com" {
		t.Fatalf("angeboten: %v", v.RejectedHosts)
	}
	e := v.Settings
	e.AllowedHosts = []string{"wol.example.com"}
	b, _ := json.Marshal(e)
	if rec := call(h, "PUT", "/api/v1/settings", string(b)); rec.Code != 200 {
		t.Fatalf("erlauben: %d %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	h.(http.HandlerFunc)(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("nach dem Erlauben: %d", rec.Code)
	}
	json.Unmarshal(call(h, "GET", "/api/v1/settings", "").Body.Bytes(), &v)
	if len(v.RejectedHosts) != 0 {
		t.Errorf("erlaubter Name noch angeboten: %v", v.RejectedHosts)
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
	if c := get("/wake/NAS", true); c != http.StatusOK {
		t.Errorf("angemeldet ohne Schlüssel: %d", c)
	}
	// Neuer Schlüssel: der alte gilt nicht mehr.
	nd, _ := st.RotateWakeKey(d.ID)
	if nd.WakeKey == d.WakeKey || get("/wake/NAS/status?key="+d.WakeKey, false) != http.StatusNotFound {
		t.Error("alter Schlüssel gilt noch")
	}
}

func TestGroups(t *testing.T) {
	h, st := newTestServer(t, func(c *config.Config) {
		c.Auth.Username, c.Auth.Password, c.Auth.WakeKeys = "admin", "geheim", true
	})
	sink := udpSink(t)
	a, _ := st.Create(store.Input{Name: "A", MAC: "00:00:00:00:00:01", Group: "Homelab", Broadcast: sink})
	b, _ := st.Create(store.Input{Name: "B", MAC: "00:00:00:00:00:02", Group: "homelab", Broadcast: sink})
	st.Create(store.Input{Name: "C", MAC: "00:00:00:00:00:03", Broadcast: sink})
	do := func(method, path string, auth bool) (int, map[string]any) {
		req := httptest.NewRequest(method, path, nil)
		req.Header.Set("X-WolWeb", "1")
		if auth {
			req.SetBasicAuth("admin", "geheim")
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var body map[string]any
		json.Unmarshal(rec.Body.Bytes(), &body)
		return rec.Code, body
	}
	if c, body := do("POST", "/api/v1/groups/HOMELAB/wake", true); c != http.StatusOK || len(body["results"].([]any)) != 2 {
		t.Fatalf("Gruppe wecken: %d %v", c, body)
	}
	if st.LastWake(a.ID).IsZero() || st.LastWake(b.ID).IsZero() {
		t.Error("nicht alle Mitglieder geweckt")
	}
	// Liste mit abgeleitetem Schlüssel; öffentlicher Link nur mit diesem Schlüssel.
	req := httptest.NewRequest("GET", "/api/v1/groups", nil)
	req.SetBasicAuth("admin", "geheim")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var groups []struct{ Name, Key, Path string }
	if err := json.Unmarshal(rec.Body.Bytes(), &groups); err != nil || len(groups) != 1 || groups[0].Key == "" {
		t.Fatalf("Gruppenliste: %s", rec.Body)
	}
	if c, _ := do("GET", "/wake-group/Homelab", false); c != http.StatusNotFound {
		t.Errorf("öffentlich ohne Schlüssel: %d", c)
	}
	if c, _ := do("GET", "/wake-group/Homelab/status?key="+groups[0].Key, false); c != http.StatusOK {
		t.Errorf("Status mit Schlüssel: %d", c)
	}
	if c, _ := do("POST", "/wake-group/Homelab?key="+groups[0].Key, false); c != http.StatusOK {
		t.Errorf("öffentlich mit Schlüssel: %d", c)
	}
	if c, body := do("POST", "/wake-group/Homelab?key="+groups[0].Key, false); c != http.StatusOK || body["results"].([]any)[0].(map[string]any)["already"] != true {
		t.Errorf("sofort wiederholt: %d %v", c, body)
	}
	// Gruppenseite für Browser
	req = httptest.NewRequest("GET", "/wake-group/Homelab?key="+groups[0].Key, nil)
	req.Header.Set("Accept", "text/html")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `data-name="Homelab"`) {
		t.Errorf("Gruppenseite: %d", rec.Code)
	}
}

func TestScheduleWakesDueDevices(t *testing.T) {
	st, _, _ := store.Open(t.TempDir(), "")
	mon := status.New(st, probe.New(nil, 100*time.Millisecond), 0)
	s := New(config.Default(), st, mon, discovery.New(st, nil, 16), false, "test")
	sink := udpSink(t)
	st.Create(store.Input{Name: "Früh", MAC: "00:00:00:00:00:01", Schedule: []string{"Mo-Fr 07:30"}, Broadcast: sink})
	st.Create(store.Input{Name: "Spät", MAC: "00:00:00:00:00:02", Schedule: []string{"täglich 22:00"}, Broadcast: sink})
	fri := time.Date(2026, 10, 30, 7, 30, 0, 0, time.Local)
	if got := s.wakeDue(fri); len(got) != 1 || got[0] != "Früh" {
		t.Errorf("Freitag 07:30: %v", got)
	}
	if got := s.wakeDue(fri.AddDate(0, 0, 1)); len(got) != 0 { // Samstag
		t.Errorf("Samstag 07:30: %v", got)
	}
	if h := st.History(1); len(h) != 1 || h[0].Source != "schedule" || !h[0].Success {
		t.Errorf("Verlauf: %+v", h)
	}
}

func TestEventsStream(t *testing.T) {
	h, _ := newTestServer(t, nil)
	srv := httptest.NewServer(h)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/api/v1/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type %q", ct)
	}
	got := make(chan string, 10)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if ev, ok := strings.CutPrefix(sc.Text(), "event: "); ok {
				got <- ev
			}
		}
	}()
	time.Sleep(100 * time.Millisecond) // Abo steht
	req, _ := http.NewRequest("POST", srv.URL+"/api/v1/devices", strings.NewReader(`{"name":"PC","mac":"00:11:22:33:44:55","ip":"127.0.0.1"}`))
	req.Header.Set("X-WolWeb", "1")
	if r, err := http.DefaultClient.Do(req); err != nil || r.StatusCode != http.StatusCreated {
		t.Fatalf("anlegen: %v", err)
	}
	seen := map[string]bool{}
	timeout := time.After(5 * time.Second)
	for !seen["devices"] || !seen["status"] {
		select {
		case ev := <-got:
			seen[ev] = true
		case <-timeout:
			t.Fatalf("Ereignisse: %v", seen)
		}
	}
}

func TestMetrics(t *testing.T) {
	h, st := newTestServer(t, nil)
	d, _ := st.Create(store.Input{Name: `NAS "1"`, MAC: "00:11:22:33:44:55", Broadcast: udpSink(t)})
	req := httptest.NewRequest("POST", "/api/v1/devices/"+d.ID+"/wake", nil)
	req.Header.Set("X-WolWeb", "1")
	h.ServeHTTP(httptest.NewRecorder(), req)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()
	for _, want := range []string{
		`wolweb_info{version="test"} 1`,
		`wolweb_wakes_total{id="` + d.ID + `",name="NAS \"1\"",mac="00:11:22:33:44:55",result="ok"} 1`,
		`wolweb_device_last_wake_timestamp_seconds{`,
		`wolweb_discovery_found{kind="new"} 0`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("fehlt: %s\n%s", want, body)
		}
	}
}

func TestStatusChangeNotifies(t *testing.T) {
	got := make(chan string, 4)
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ev map[string]any
		json.NewDecoder(r.Body).Decode(&ev)
		got <- ev["event"].(string)
	}))
	defer hook.Close()
	st, _, _ := store.Open(t.TempDir(), "")
	mon := status.New(st, probe.New(nil, 100*time.Millisecond), 0)
	cfg := config.Default()
	cfg.Notify.Webhook = hook.URL
	s := New(cfg, st, mon, discovery.New(st, nil, 16), false, "test")
	d := store.Device{ID: "x", Name: "PC"}
	s.statusChanged(d, status.State{}, false, status.State{State: "online"})                 // erstes Ergebnis: still
	s.statusChanged(d, status.State{State: "online"}, true, status.State{State: "online"})   // keine Änderung
	s.statusChanged(d, status.State{State: "online"}, true, status.State{State: "offline"})  // Wechsel
	s.statusChanged(d, status.State{State: "offline"}, true, status.State{State: "unknown"}) // keine IP mehr: still
	select {
	case ev := <-got:
		if ev != "offline" {
			t.Errorf("Ereignis %q", ev)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("keine Benachrichtigung")
	}
	select {
	case ev := <-got:
		t.Errorf("unerwartet: %q", ev)
	case <-time.After(300 * time.Millisecond):
	}
}

// Jeder Schlüssel, den Vorlagen und Skripte benutzen, muss im Katalog stehen.
func TestUIKeysExist(t *testing.T) {
	cat := i18n.Catalog(i18n.DE)
	use := regexp.MustCompile(`(?:\bt|\btn|\.T|\.TN|\$\.T)\(?\s*"((?:ui|page|wake|group|set|blocked)\.[a-z_.]+)"`)
	files, _ := fs.Glob(assets, "*/*")
	for _, f := range files {
		b, _ := assets.ReadFile(f)
		for _, m := range use.FindAllStringSubmatch(string(b), -1) {
			k := m[1]
			if strings.HasSuffix(k, ".") { // dynamisch zusammengesetzt, z. B. "ui.icon." + key
				continue
			}
			_, ok := cat[k]
			_, one := cat[k+".one"]
			if !ok && !one {
				t.Errorf("%s: Schlüssel %q fehlt im Katalog", f, k)
			}
		}
	}
	for _, prefix := range []string{"ui.icon.desktop", "ui.src.schedule", "ui.src.group_link", "ui.scan.method.arp", "ui.scan.method.neighbors", "set.event.online", "set.event.wake_failed"} {
		if _, ok := cat[prefix]; !ok {
			t.Errorf("dynamischer Schlüssel %q fehlt", prefix)
		}
	}
}

func TestEnglish(t *testing.T) {
	h, st := newTestServer(t, nil)
	st.Create(store.Input{Name: "NAS", MAC: "00:11:22:33:44:55"})
	// API: Fehlermeldung auf Englisch, mit betroffenem Feld
	req := httptest.NewRequest("POST", "/api/v1/devices", strings.NewReader(`{"name":"PC","mac":"kaputt"}`))
	req.Header.Set("X-WolWeb", "1")
	req.Header.Set("Accept-Language", "en-GB,en;q=0.9")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var res struct{ Message, Field string }
	json.Unmarshal(rec.Body.Bytes(), &res)
	if rec.Code != http.StatusBadRequest || res.Field != "mac" || !strings.HasPrefix(res.Message, "The MAC address is invalid") {
		t.Errorf("%d %+v", rec.Code, res)
	}
	// Seite: Cookie des Umschalters schlägt den Browser
	req = httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept-Language", "de-DE")
	req.Header.Set("Cookie", "wolweb_lang=en")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, `<html lang="en">`) || !strings.Contains(body, "Scan network") || !strings.Contains(body, `data-lang="de"`) {
		t.Errorf("Seite nicht englisch:\n%.400s", body)
	}
	// Katalog als JSON in der Seite
	i := strings.Index(body, `<script id="i18n" type="application/json">`)
	j := strings.Index(body[i:], "</script>")
	var cat map[string]string
	if i < 0 || json.Unmarshal([]byte(body[i+len(`<script id="i18n" type="application/json">`):i+j]), &cat) != nil || cat["ui.wake"] != "Wake" {
		t.Error("Katalog in der Seite fehlt oder ist kein JSON")
	}
	// Wecklink ohne Gerät, ?lang=en
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/wake/Gibtsnicht?lang=en", nil))
	if !strings.Contains(rec.Body.String(), `Device \"Gibtsnicht\" not found.`) {
		t.Errorf("Wecklink: %s", rec.Body)
	}
}

func TestScheduleMinuteDue(t *testing.T) {
	at := func(h, m, s int) time.Time { return time.Date(2026, 10, 30, h, m, s, 0, time.Local) }
	next := at(7, 30, 0)
	for _, tc := range []struct {
		name      string
		now, last time.Time
		want      bool
	}{
		{"pünktlich", at(7, 30, 0), at(7, 29, 0), true},
		{"etwas später", at(7, 30, 2), at(7, 29, 0), true},
		{"nach Ruhezustand", at(12, 0, 20), at(7, 29, 0), false},
		{"Uhr zurückgestellt, Minute schon dran", at(7, 30, 1), at(7, 30, 0), false},
		{"Wanduhr noch davor", at(7, 29, 58), at(7, 29, 0), false},
	} {
		if got := scheduleMinuteDue(next, tc.now, tc.last); got != tc.want {
			t.Errorf("%s: %v", tc.name, got)
		}
	}
}

func TestWakeRetry(t *testing.T) {
	st, _, _ := store.Open(t.TempDir(), "")
	mon := status.New(st, probe.New(nil, 100*time.Millisecond), 0)
	cfg := config.Default()
	cfg.Status.WakeRetry = "100ms"
	s := New(cfg, st, mon, discovery.New(st, nil, 16), false, "test")
	sink := udpSink(t)
	// Antwortet auf TCP – gilt also als online.
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	off, _ := st.Create(store.Input{Name: "Aus", MAC: "00:00:00:00:00:01", IP: "192.0.2.1", Ports: []int{9}, Broadcast: sink})
	on, _ := st.Create(store.Input{Name: "An", MAC: "00:00:00:00:00:02", IP: "127.0.0.1", Ports: []int{port}, Broadcast: sink})
	noIP, _ := st.Create(store.Input{Name: "OhneIP", MAC: "00:00:00:00:00:03", Broadcast: sink})

	s.wake(nil, off, srcAPI, "")
	s.limiter.release(off.ID)    // sonst gälte der zweite Aufruf als Nachfassen
	s.wake(nil, off, srcAPI, "") // ersetzt die ausstehende Wiederholung, statt eine zweite anzulegen
	s.wakeGroup(nil, "", []store.Device{on, noIP}, srcGroup)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && len(st.History(0)) < 5 {
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(500 * time.Millisecond) // falls doch noch mehr käme
	h := st.History(0)
	var retried []string
	for _, ev := range h {
		if ev.Retry {
			retried = append(retried, ev.Device)
			if ev.Source != srcAPI || !ev.Success {
				t.Errorf("Wiederholung: %+v", ev)
			}
		}
	}
	if len(h) != 5 || fmt.Sprint(retried) != "[Aus]" {
		t.Fatalf("erwartet 4 Weckversuche und eine Wiederholung für Aus, Verlauf: %+v", h)
	}

	// Nach Close kommt keine Wiederholung mehr.
	s.limiter.release(off.ID)
	s.wake(nil, off, srcAPI, "")
	s.Close()
	time.Sleep(400 * time.Millisecond)
	if n := len(st.History(0)); n != 6 {
		t.Errorf("nach Close: %d Einträge statt 6", n)
	}
}

// Ohne Anmeldung wie mit: Ein eben geweckter Rechner bekommt keinen zweiten Weckruf – über keinen
// Einstieg, auch nicht als Teil einer Gruppe. Nachfassen ist kein Fehler, sondern "wird bereits geweckt".
func TestWakeDedupEverywhere(t *testing.T) {
	st, _, _ := store.Open(t.TempDir(), "")
	mon := status.New(st, probe.New(nil, 100*time.Millisecond), 0)
	s := New(config.Default(), st, mon, discovery.New(st, nil, 16), false, "test")
	h := s.Handler()
	sink := udpSink(t)
	nas, _ := st.Create(store.Input{Name: "NAS", MAC: "02:00:00:00:00:01", Group: "Keller", Broadcast: sink})
	st.Create(store.Input{Name: "PC", MAC: "02:00:00:00:00:02", Group: "Keller", Broadcast: sink})
	do := func(method, path string) map[string]any {
		req := httptest.NewRequest(method, path, nil)
		req.Host = "192.168.1.10:8089"
		req.Header.Set("X-WolWeb", "1")
		req.Header.Set("Accept-Language", "de")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var m map[string]any
		json.Unmarshal(rec.Body.Bytes(), &m)
		if rec.Code != http.StatusOK {
			t.Errorf("%s %s: %d %s", method, path, rec.Code, rec.Body)
		}
		return m
	}
	if m := do("GET", "/wake/NAS"); m["success"] != true || m["already"] != nil {
		t.Fatalf("erster Aufruf: %v", m)
	}
	for _, c := range [][2]string{{"GET", "/wake/NAS"}, {"POST", "/wake/NAS"}, {"POST", "/api/v1/devices/" + nas.ID + "/wake"}} {
		if m := do(c[0], c[1]); m["success"] != true || m["already"] != true || !strings.Contains(m["message"].(string), "bereits geweckt") {
			t.Errorf("%s %s: %v", c[0], c[1], m)
		}
	}
	// Gruppe: NAS wurde eben geweckt, nur PC bekommt einen Weckruf
	g := do("POST", "/api/v1/groups/Keller/wake")
	res := g["results"].([]any)
	if res[0].(map[string]any)["already"] != true || res[1].(map[string]any)["already"] != nil || g["success"] != true {
		t.Errorf("Gruppe: %v", g)
	}
	if m := do("POST", "/wake-group/Keller"); m["results"].([]any)[1].(map[string]any)["already"] != true {
		t.Errorf("Gruppenlink gleich danach: %v", m)
	}
	if n := len(st.History(0)); n != 2 {
		t.Errorf("%d Verlaufseinträge statt 2 – Nachfassen darf nichts auftürmen", n)
	}

	// Ein gescheiterter Weckruf blockiert nichts: gleich nochmal versuchen sendet wieder.
	bad, err := st.Create(store.Input{Name: "Kaputt", MAC: "02:00:00:00:00:03", Interface: "gibtsnicht0"})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		req := httptest.NewRequest("POST", "/api/v1/devices/"+bad.ID+"/wake", nil)
		req.Host, req.Header["X-Wolweb"] = "192.168.1.10:8089", []string{"1"}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "already") {
			t.Errorf("Versuch %d: %d %s", i+1, rec.Code, rec.Body)
		}
	}

	// Ein Zeitplan weckt immer, zählt aber mit: der Wecklink gleich danach sendet nicht nochmal.
	pc, _ := st.FindByName("PC")
	s.limiter.release(pc.ID)
	st.Update(pc.ID, store.Input{Name: "PC", MAC: pc.MAC, Broadcast: sink, Schedule: []string{"täglich 07:30"}}, nil)
	s.wakeDue(time.Date(2026, 10, 30, 7, 30, 0, 0, time.Local))
	if m := do("GET", "/wake/PC"); m["already"] != true {
		t.Errorf("nach dem Zeitplan: %v", m)
	}
}
