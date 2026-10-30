package web

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"wolweb/internal/config"
	"wolweb/internal/store"
)

// Zugangsdaten werden nacheinander geprüft, ein Fehlversuch hält die Prüfung auf – auch gleichzeitig
// geschickte Versuche laufen einzeln. Wer den Nachweis einer Anmeldung hat, wartet währenddessen nicht;
// ein neuer Aufrufer wird erst geprüft, wenn er dran ist.
func TestLoginFailuresBraked(t *testing.T) {
	h, _ := newTestServer(t, func(c *config.Config) {
		c.Auth.Username, c.Auth.Password, c.Auth.APIToken = "admin", "geheim-lang-genug", "tok-lang-genug-1234567"
	})
	get := func(from string, cookie *http.Cookie, user, pass, token string) (*httptest.ResponseRecorder, time.Duration) {
		req := httptest.NewRequest("GET", "/api/v1/info", nil)
		req.RemoteAddr = from + ":40000"
		if user != "" {
			req.SetBasicAuth(user, pass)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if cookie != nil {
			req.AddCookie(cookie)
		}
		start := time.Now()
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec, time.Since(start)
	}
	rec, _ := get("192.0.2.10", nil, "admin", "geheim-lang-genug", "")
	var proof *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == loginCookie {
			proof = c
		}
	}
	if rec.Code != http.StatusOK || proof == nil {
		t.Fatalf("Anmeldung: %d, Nachweis %v", rec.Code, proof)
	}
	start := time.Now()
	var wg sync.WaitGroup
	for i := range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if i == 0 {
				get("192.0.2.66", nil, "", "", "falsch")
			} else {
				get("192.0.2.66", nil, "admin", "falsch", "")
			}
		}()
	}
	time.Sleep(50 * time.Millisecond)
	if rec, d := get("192.0.2.10", proof, "admin", "geheim-lang-genug", ""); rec.Code != http.StatusOK || d > 200*time.Millisecond {
		t.Errorf("mit Nachweis während der Bremse: %d nach %s", rec.Code, d)
	}
	if rec, d := get("192.0.2.67", nil, "", "", "tok-lang-genug-1234567"); rec.Code != http.StatusOK || d < 2*loginFailDelay {
		t.Errorf("neuer Aufrufer während der Bremse: %d nach %s – nicht eingereiht", rec.Code, d)
	}
	wg.Wait()
	if d := time.Since(start); d < 3*loginFailDelay-50*time.Millisecond {
		t.Errorf("3 Fehlversuche in %s – nicht gebremst", d)
	}
	// Ohne Authorization-Kopf (z. B. erster Aufruf vor dem Anmeldedialog) bremst nichts.
	if rec, d := get("192.0.2.68", nil, "", "", ""); rec.Code != http.StatusUnauthorized || d > 200*time.Millisecond {
		t.Errorf("ohne Zugangsdaten: %d nach %s", rec.Code, d)
	}
}

// Ist die Warteschlange voll – insgesamt oder für eine Quelle –, wird sofort abgewiesen, ohne die
// Zugangsdaten anzusehen; sonst verriete die schnelle Antwort, ob sie stimmten.
func TestLoginBrakeQueueLimited(t *testing.T) {
	req := func(from string) *http.Request {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = from
		r.Header.Set("Authorization", "Bearer x")
		return r
	}
	checked := false
	probe := func(*http.Request) bool { checked = true; return true }

	var b loginBrake
	b.waiting = loginMaxWaiting
	start := time.Now()
	if ok, busy := b.check(req("192.0.2.1:1"), probe, false); ok || !busy || checked {
		t.Fatalf("volle Warteschlange: ok=%v busy=%v geprüft=%v", ok, busy, checked)
	}
	if time.Since(start) > 100*time.Millisecond {
		t.Fatal("hat trotz voller Warteschlange gewartet")
	}

	var p loginBrake
	p.sources = map[string]int{"2001:db8:1:2::/64": loginPerSource}
	if _, busy := p.check(req("[2001:db8:1:2::99]:1"), probe, false); !busy || checked {
		t.Fatal("eine Quelle (IPv6 /64) belegt mehr als loginPerSource Plätze")
	}
	if ok, busy := p.check(req("192.0.2.2:1"), probe, false); !ok || busy {
		t.Fatalf("andere Quelle abgewiesen: ok=%v busy=%v", ok, busy)
	}
}

// Öffentliche Seiten bleiben bei voller Bremse erreichbar – aber ohne das ungeprüfte Token, das die
// Seite sonst ungebremst auswertete.
func TestLoginBrakeKeepsPublicPages(t *testing.T) {
	s, h, _ := settingsServer(t, `{"auth": {"api_token": "tok-lang-genug-1234567", "public_wake": true}, "privacy": {"online_status": "unlocked"}}`)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	if _, err := s.store.Create(store.Input{Name: "NAS", MAC: "02:00:00:00:00:01", IP: "127.0.0.1", Ports: []int{l.Addr().(*net.TCPAddr).Port}, Broadcast: udpSink(t)}); err != nil {
		t.Fatal(err)
	}
	status := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/wake/NAS/status", nil)
		req.Host = "192.168.1.10:8089"
		req.Header.Set("Authorization", "Bearer tok-lang-genug-1234567")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := status(); rec.Code != http.StatusOK {
		t.Fatalf("mit Token: %d %s", rec.Code, rec.Body)
	}
	s.logins.mu.Lock()
	s.logins.waiting, s.logins.clients = loginMaxWaiting, nil // voll, und niemand hat Vorrang
	s.logins.mu.Unlock()
	if rec := status(); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"hidden":true`) {
		t.Fatalf("bei voller Bremse: %d %s – erwartet die öffentliche Seite ohne Token", rec.Code, rec.Body)
	}
}

// Der Nachweis gilt nur für den Aufrufer, dem er ausgestellt wurde, und nur bis zu seinem Ende.
func TestLoginProofBoundToClient(t *testing.T) {
	s, _, _ := settingsServer(t, `{"auth": {"api_token": "tok-lang-genug-1234567"}}`)
	req := func(from, value string) *http.Request {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = from + ":40000"
		r.AddCookie(&http.Cookie{Name: loginCookie, Value: value})
		return r
	}
	exp := fmt.Sprint(time.Now().Add(time.Hour).Unix())
	value := s.loginProof(req("192.0.2.10", ""), exp)
	if s.loginProven(req("192.0.2.10", value)).IsZero() {
		t.Fatal("eigener Nachweis gilt nicht")
	}
	if !s.loginProven(req("192.0.2.11", value)).IsZero() {
		t.Fatal("Nachweis gilt für einen anderen Aufrufer")
	}
	old := fmt.Sprint(time.Now().Add(-time.Minute).Unix())
	if !s.loginProven(req("192.0.2.10", s.loginProof(req("192.0.2.10", ""), old))).IsZero() {
		t.Fatal("abgelaufener Nachweis gilt")
	}
}

// Ohne Cookie: Vorrang nach einer Anmeldung von derselben Adresse, höchstens loginFastMax gleichzeitig;
// ein Fehlversuch nimmt ihn für loginCooldown (auch ein Erfolg gleich danach gibt ihn nicht zurück).
func TestLoginGoodClients(t *testing.T) {
	var b loginBrake
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "192.0.2.10:40000"
	req.Header.Set("Authorization", "Bearer x")
	yes := func(*http.Request) bool { return true }
	if ok, _ := b.check(req, yes, false); !ok || !b.claimFast("192.0.2.10") {
		t.Fatal("Anmeldung nicht gemerkt")
	}
	b.releaseFast("192.0.2.10", true)
	for range loginFastMax {
		b.claimFast("192.0.2.10")
	}
	if b.claimFast("192.0.2.10") {
		t.Fatalf("mehr als %d gleichzeitige Sofortprüfungen", loginFastMax)
	}
	for range loginFastMax {
		b.releaseFast("192.0.2.10", true)
	}
	if ok, _ := b.check(req, func(*http.Request) bool { return false }, false); ok || b.claimFast("192.0.2.10") {
		t.Fatal("Fehlversuch behält Vorrang")
	}
	b.check(req, yes, false)
	if b.claimFast("192.0.2.10") {
		t.Fatal("Erfolg gleich nach einem Fehlversuch gibt den Vorrang zurück")
	}
	for i := range loginClientsMax + 10 {
		b.markGood(fmt.Sprintf("10.0.%d.%d", i/256, i%256))
	}
	if n := len(b.clients); n > loginClientsMax {
		t.Fatalf("%d gemerkte Aufrufer, erlaubt %d", n, loginClientsMax)
	}
}
