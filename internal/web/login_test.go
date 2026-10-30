package web

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"wolweb/internal/config"
)

// Gescheiterte Anmeldungen laufen gebremst nacheinander; richtige Zugangsdaten warten dabei nicht.
func TestLoginFailuresBraked(t *testing.T) {
	h, _ := newTestServer(t, func(c *config.Config) {
		c.Auth.Username, c.Auth.Password, c.Auth.APIToken = "admin", "geheim-lang-genug", "tok-lang-genug-1234567"
	})
	get := func(user, pass, token string) (int, time.Duration) {
		req := httptest.NewRequest("GET", "/api/v1/info", nil)
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
	start := time.Now()
	var wg sync.WaitGroup
	for i := range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if i == 0 {
				get("", "", "falsch")
			} else {
				get("admin", "falsch", "")
			}
		}()
	}
	time.Sleep(50 * time.Millisecond)
	if code, d := get("admin", "geheim-lang-genug", ""); code != http.StatusOK || d > 200*time.Millisecond {
		t.Errorf("richtige Anmeldung während der Bremse: %d nach %s", code, d)
	}
	wg.Wait()
	if d := time.Since(start); d < 3*loginFailDelay-50*time.Millisecond {
		t.Errorf("3 Fehlversuche in %s – nicht gebremst", d)
	}
	// Ohne Authorization-Kopf (z. B. erster Aufruf vor dem Anmeldedialog) bremst nichts.
	if code, d := get("", "", ""); code != http.StatusUnauthorized || d > 200*time.Millisecond {
		t.Errorf("ohne Zugangsdaten: %d nach %s", code, d)
	}
}

// Warten schon zu viele Fehlversuche, wird sofort abgewiesen.
func TestLoginBrakeQueueLimited(t *testing.T) {
	var b loginBrake
	b.waiting.Store(loginMaxWaiting)
	start := time.Now()
	if b.fail("192.0.2.1") {
		t.Fatal("volle Warteschlange angenommen")
	}
	if time.Since(start) > 100*time.Millisecond {
		t.Fatal("hat trotz voller Warteschlange gewartet")
	}
}
