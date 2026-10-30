package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"wolweb/internal/config"
)

// Zugangsdaten werden nacheinander geprüft, ein Fehlversuch hält die Prüfung auf – auch gleichzeitig
// geschickte Versuche laufen einzeln. Wer sich schon angemeldet hat, wartet währenddessen nicht.
func TestLoginFailuresBraked(t *testing.T) {
	h, _ := newTestServer(t, func(c *config.Config) {
		c.Auth.Username, c.Auth.Password, c.Auth.APIToken = "admin", "geheim-lang-genug", "tok-lang-genug-1234567"
	})
	get := func(from, user, pass, token string) (int, time.Duration) {
		req := httptest.NewRequest("GET", "/api/v1/info", nil)
		req.RemoteAddr = from + ":40000"
		if user != "" {
			req.SetBasicAuth(user, pass)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		start := time.Now()
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, time.Since(start)
	}
	if code, _ := get("192.0.2.10", "admin", "geheim-lang-genug", ""); code != http.StatusOK {
		t.Fatalf("Anmeldung: %d", code)
	}
	start := time.Now()
	var wg sync.WaitGroup
	for i := range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if i == 0 {
				get("192.0.2.66", "", "", "falsch")
			} else {
				get("192.0.2.66", "admin", "falsch", "")
			}
		}()
	}
	time.Sleep(50 * time.Millisecond)
	if code, d := get("192.0.2.10", "admin", "geheim-lang-genug", ""); code != http.StatusOK || d > 200*time.Millisecond {
		t.Errorf("angemeldeter Aufrufer während der Bremse: %d nach %s", code, d)
	}
	// Ein neuer Aufrufer wird erst geprüft, wenn er dran ist – auch mit richtigen Zugangsdaten.
	if code, d := get("192.0.2.67", "", "", "tok-lang-genug-1234567"); code != http.StatusOK || d < 2*loginFailDelay {
		t.Errorf("neuer Aufrufer während der Bremse: %d nach %s – nicht eingereiht", code, d)
	}
	wg.Wait()
	if d := time.Since(start); d < 3*loginFailDelay-50*time.Millisecond {
		t.Errorf("3 Fehlversuche in %s – nicht gebremst", d)
	}
	// Ohne Authorization-Kopf (z. B. erster Aufruf vor dem Anmeldedialog) bremst nichts.
	if code, d := get("192.0.2.68", "", "", ""); code != http.StatusUnauthorized || d > 200*time.Millisecond {
		t.Errorf("ohne Zugangsdaten: %d nach %s", code, d)
	}
}

// Warten schon zu viele, wird sofort abgewiesen, ohne die Zugangsdaten anzusehen – sonst verriete die
// schnelle Antwort, ob sie stimmten.
func TestLoginBrakeQueueLimited(t *testing.T) {
	var b loginBrake
	b.waiting.Store(loginMaxWaiting)
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer x")
	checked := false
	start := time.Now()
	ok, busy := b.check(req, func(*http.Request) bool { checked = true; return true })
	if ok || !busy || checked {
		t.Fatalf("volle Warteschlange: ok=%v busy=%v geprüft=%v", ok, busy, checked)
	}
	if time.Since(start) > 100*time.Millisecond {
		t.Fatal("hat trotz voller Warteschlange gewartet")
	}
}

// Die Bremse gilt auch auf öffentlichen Weckseiten: Ist sie voll, kommt 429 statt der Seite (die das
// Token sonst ungebremst auswertete).
func TestLoginBrakeCoversPublicPages(t *testing.T) {
	s, h, _ := settingsServer(t, `{"auth": {"api_token": "tok-lang-genug-1234567", "public_wake": true}}`)
	s.logins.waiting.Store(loginMaxWaiting)
	req := httptest.NewRequest("GET", "/wake/NAS/status", nil)
	req.Host = "192.168.1.10:8089"
	req.Header.Set("Authorization", "Bearer rate")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("öffentliche Seite bei voller Bremse: %d %s", rec.Code, rec.Body)
	}
}

// Ein Fehlversuch nimmt einem angemeldeten Aufrufer den Vorrang; die Liste bleibt begrenzt.
func TestLoginGoodClients(t *testing.T) {
	var b loginBrake
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "192.0.2.10:40000"
	req.Header.Set("Authorization", "Bearer x")
	if ok, _ := b.check(req, func(*http.Request) bool { return true }); !ok || !b.isGood("192.0.2.10") {
		t.Fatal("Anmeldung nicht gemerkt")
	}
	if ok, _ := b.check(req, func(*http.Request) bool { return false }); ok || b.isGood("192.0.2.10") {
		t.Fatal("Fehlversuch behält Vorrang")
	}
	for i := range loginGoodMax + 10 {
		b.markGood(fmt.Sprintf("10.0.%d.%d", i/256, i%256))
	}
	if n := len(b.good); n > loginGoodMax {
		t.Fatalf("%d gemerkte Aufrufer, erlaubt %d", n, loginGoodMax)
	}
}
