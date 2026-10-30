package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"wolweb/internal/config"
	"wolweb/internal/store"
)

func init() { store.PasswordRounds = 1000 } // schnelle Tests; echte Passwörter bekommen 600 000 Runden

// browser schickt Anfragen wie die Oberfläche und merkt sich das Entsperr-Cookie.
type browser struct {
	t      *testing.T
	h      http.Handler
	cookie *http.Cookie
	setup  func(*http.Request)
}

func (b *browser) do(method, url, body string) *httptest.ResponseRecorder {
	b.t.Helper()
	req := httptest.NewRequest(method, url, strings.NewReader(body))
	req.Header.Set("X-WolWeb", "1")
	if b.cookie != nil {
		req.AddCookie(b.cookie)
	}
	if b.setup != nil {
		b.setup(req)
	}
	rec := httptest.NewRecorder()
	b.h.ServeHTTP(rec, req)
	for _, c := range rec.Result().Cookies() {
		if c.Name == unlockCookie {
			if b.cookie = c; c.MaxAge < 0 {
				b.cookie = nil
			}
		}
	}
	return rec
}

func lockedBody(t *testing.T, rec *httptest.ResponseRecorder) bool {
	t.Helper()
	var v struct {
		Locked bool `json:"locked"`
	}
	json.Unmarshal(rec.Body.Bytes(), &v)
	return rec.Code == http.StatusForbidden && v.Locked
}

func TestEditLock(t *testing.T) {
	_, h, _ := settingsServer(t, `{"port": 8089}`)
	admin := &browser{t: t, h: h}
	rec := admin.do("POST", "/api/v1/devices", `{"name":"NAS","mac":"02:00:00:00:00:01"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("ohne Passwort gesperrt: %d %s", rec.Code, rec.Body)
	}
	var nas struct{ ID string }
	json.Unmarshal(rec.Body.Bytes(), &nas)
	dev := "/api/v1/devices/" + nas.ID
	if rec := admin.do("PUT", "/api/v1/password", `{"password":"kurz"}`); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"field":"password"`) {
		t.Fatalf("zu kurzes Passwort: %d %s", rec.Code, rec.Body)
	}
	if rec := admin.do("PUT", "/api/v1/password", `{"password":"geheim123"}`); rec.Code != http.StatusOK || admin.cookie == nil {
		t.Fatalf("Passwort setzen: %d %s (Cookie %v)", rec.Code, rec.Body, admin.cookie)
	}

	// Ein anderer Browser darf ansehen und wecken, aber nichts ändern.
	guest := &browser{t: t, h: h}
	var info struct{ Password, Locked bool }
	json.Unmarshal(guest.do("GET", "/api/v1/info", "").Body.Bytes(), &info)
	if !info.Password || !info.Locked {
		t.Fatalf("info für Gast: %+v", info)
	}
	if rec := guest.do("GET", "/api/v1/devices", ""); rec.Code != http.StatusOK {
		t.Fatalf("Ansehen gesperrt: %d", rec.Code)
	}
	if rec := guest.do("POST", "/api/v1/groups/Keine/wake", ""); lockedBody(t, rec) {
		t.Fatal("Wecken gesperrt")
	}
	for _, c := range []struct{ method, url, body string }{
		{"POST", "/api/v1/devices", `{"name":"PC","mac":"02:00:00:00:00:02"}`},
		{"PUT", dev, `{"name":"NAS2","mac":"02:00:00:00:00:01"}`},
		{"DELETE", dev, ""},
		{"POST", "/api/v1/devices/group", `{"ids":["` + nas.ID + `"],"group":"Büro"}`},
		{"POST", "/api/v1/import", `[]`},
		{"GET", "/api/v1/export", ""},
		{"POST", "/api/v1/discovery/scan", ""},
		{"GET", "/api/v1/settings", ""},
		{"PUT", "/api/v1/settings", `{}`},
		{"DELETE", "/api/v1/settings", ""},
		{"PUT", "/api/v1/password", `{"current":"geheim123","password":""}`},
	} {
		if rec := guest.do(c.method, c.url, c.body); !lockedBody(t, rec) {
			t.Errorf("%s %s ohne Entsperren: %d %s", c.method, c.url, rec.Code, rec.Body)
		}
	}

	// Entsperren: falsches Passwort scheitert, richtiges gibt frei.
	if rec := guest.do("POST", "/api/v1/unlock", `{"password":"falsch"}`); rec.Code != http.StatusForbidden || guest.cookie != nil {
		t.Fatalf("falsches Passwort: %d %s", rec.Code, rec.Body)
	}
	if rec := guest.do("POST", "/api/v1/unlock", `{"password":"geheim123"}`); rec.Code != http.StatusOK || guest.cookie == nil {
		t.Fatalf("Entsperren: %d %s", rec.Code, rec.Body)
	}
	if rec := guest.do("POST", "/api/v1/devices", `{"name":"PC","mac":"02:00:00:00:00:02"}`); rec.Code != http.StatusCreated {
		t.Fatalf("entsperrt: %d %s", rec.Code, rec.Body)
	}
	if rec := guest.do("GET", "/api/v1/settings", ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"password":true`) {
		t.Fatalf("Einstellungen entsperrt: %d", rec.Code)
	}

	// Ändern verlangt das bisherige Passwort – auch entsperrt.
	if rec := guest.do("PUT", "/api/v1/password", `{"password":"anderes123"}`); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"field":"current"`) {
		t.Fatalf("ohne bisheriges Passwort: %d %s", rec.Code, rec.Body)
	}
	if rec := guest.do("PUT", "/api/v1/password", `{"current":"geheim123","password":"anderes123"}`); rec.Code != http.StatusOK {
		t.Fatalf("Passwort ändern: %d %s", rec.Code, rec.Body)
	}
	// Ein neues Passwort beendet die Entsperrung anderer Browser.
	if rec := admin.do("POST", "/api/v1/devices", `{"name":"TV","mac":"02:00:00:00:00:03"}`); !lockedBody(t, rec) {
		t.Fatalf("alte Entsperrung gilt noch: %d", rec.Code)
	}

	// Sperren wirkt sofort.
	guest.do("POST", "/api/v1/lock", "")
	if guest.cookie != nil {
		t.Fatal("Cookie nach dem Sperren noch da")
	}
	if rec := guest.do("DELETE", dev, ""); !lockedBody(t, rec) {
		t.Fatalf("nach dem Sperren: %d", rec.Code)
	}

	// Passwort entfernen gibt alles frei.
	guest.do("POST", "/api/v1/unlock", `{"password":"anderes123"}`)
	if rec := guest.do("PUT", "/api/v1/password", `{"current":"anderes123","password":""}`); rec.Code != http.StatusOK {
		t.Fatalf("Passwort entfernen: %d %s", rec.Code, rec.Body)
	}
	if rec := (&browser{t: t, h: h}).do("DELETE", dev, ""); rec.Code != http.StatusOK && rec.Code != http.StatusNoContent {
		t.Fatalf("ohne Passwort: %d %s", rec.Code, rec.Body)
	}
}

func TestUnlockCookieTampered(t *testing.T) {
	s, h, _ := settingsServer(t, `{"port": 8089}`)
	b := &browser{t: t, h: h}
	b.do("PUT", "/api/v1/password", `{"password":"geheim123"}`)
	for name, value := range map[string]string{
		"verändert":   flipLast(b.cookie.Value),
		"abgelaufen":  s.unlockTokenFor(s.store.PasswordStamp(), time.Now().Add(-time.Minute)),
		"zu lang":     s.unlockTokenFor(s.store.PasswordStamp(), time.Now().Add(unlockIdle+time.Hour)),
		"kein Format": "abc",
	} {
		c := &browser{t: t, h: h, cookie: &http.Cookie{Name: unlockCookie, Value: value}}
		if rec := c.do("POST", "/api/v1/devices", `{"name":"PC","mac":"02:00:00:00:00:02"}`); !lockedBody(t, rec) {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	// Abfragen im Hintergrund (eine offene Übersicht) verlängern eine Entsperrung nicht …
	soon := s.unlockTokenFor(s.store.PasswordStamp(), time.Now().Add(time.Hour))
	c := &browser{t: t, h: h, cookie: &http.Cookie{Name: unlockCookie, Value: soon}}
	for _, path := range []string{"/api/v1/info", "/api/v1/devices", "/api/v1/groups", "/"} {
		c.do("GET", path, "")
	}
	if c.cookie == nil || c.cookie.Value != soon {
		t.Fatal("Entsperrung durch bloßes Ansehen verlängert")
	}
	// … Arbeiten damit schon.
	c.do("GET", "/api/v1/settings", "")
	if c.cookie == nil || s.unlockUntil(httptestWithCookie(c.cookie)).Before(time.Now().Add(unlockIdle-time.Minute)) {
		t.Fatal("Entsperrung nicht verlängert")
	}
}

func flipLast(v string) string {
	if strings.HasSuffix(v, "0") {
		return v[:len(v)-1] + "1"
	}
	return v[:len(v)-1] + "0"
}

func httptestWithCookie(c *http.Cookie) *http.Request {
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(c)
	return r
}

func TestUnlockAttemptsLimited(t *testing.T) {
	_, h, _ := settingsServer(t, `{"port": 8089}`)
	b := &browser{t: t, h: h}
	b.do("PUT", "/api/v1/password", `{"password":"geheim123"}`)
	b.cookie = nil
	for i := range attemptsFree {
		if rec := b.do("POST", "/api/v1/unlock", `{"password":"falsch"}`); rec.Code != http.StatusForbidden {
			t.Fatalf("Versuch %d: %d", i+1, rec.Code)
		}
	}
	// Jetzt hilft auch das richtige Passwort erst nach der Wartezeit.
	if rec := b.do("POST", "/api/v1/unlock", `{"password":"geheim123"}`); rec.Code != http.StatusTooManyRequests || b.cookie != nil {
		t.Fatalf("nach %d Fehlversuchen: %d %s", attemptsFree, rec.Code, rec.Body)
	}
}

// Gleichzeitig geschickte Versuche zählen einzeln: Mehr als attemptsFree falsche Passwörter werden
// nicht geprüft, auch wenn sie alle auf einmal kommen.
func TestUnlockAttemptsConcurrent(t *testing.T) {
	_, h, _ := settingsServer(t, `{"port": 8089}`)
	(&browser{t: t, h: h}).do("PUT", "/api/v1/password", `{"password":"geheim123"}`)
	codes := make(chan int, 40)
	var wg sync.WaitGroup
	for range 40 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes <- call(h, "POST", "/api/v1/unlock", `{"password":"falsch"}`).Code
		}()
	}
	wg.Wait()
	close(codes)
	checked := 0
	for c := range codes {
		if c == http.StatusForbidden {
			checked++
		} else if c != http.StatusTooManyRequests {
			t.Fatalf("unerwartet: %d", c)
		}
	}
	if checked != attemptsFree {
		t.Fatalf("%d Passwörter geprüft statt %d", checked, attemptsFree)
	}
}

// Hinter einem Reverse-Proxy (alle Anfragen von derselben Adresse) sperrt ein Browser, der sich
// vertippt, nicht die anderen aus; viele Fehlversuche insgesamt bremsen aber alle.
func TestUnlockAttemptsBehindProxy(t *testing.T) {
	_, h, _ := settingsServer(t, `{"port": 8089}`)
	(&browser{t: t, h: h}).do("PUT", "/api/v1/password", `{"password":"geheim123"}`)
	via := func(client string) *browser {
		return &browser{t: t, h: h, setup: func(r *http.Request) {
			r.RemoteAddr = "127.0.0.1:40000"
			r.Header.Set("X-Forwarded-For", client)
		}}
	}
	kid, owner := via("192.168.1.30"), via("192.168.1.20")
	for range attemptsFree {
		kid.do("POST", "/api/v1/unlock", `{"password":"falsch"}`)
	}
	if rec := kid.do("POST", "/api/v1/unlock", `{"password":"geheim123"}`); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("Kind nicht gebremst: %d", rec.Code)
	}
	if rec := owner.do("POST", "/api/v1/unlock", `{"password":"geheim123"}`); rec.Code != http.StatusOK || owner.cookie == nil {
		t.Fatalf("Besitzer ausgesperrt: %d %s", rec.Code, rec.Body)
	}
	// Wechselnde Absender (gefälschter Kopf ohne Proxy): nach attemptsFreeAll ist für alle Schluss.
	for i := range attemptsFreeAll {
		via(fmt.Sprintf("10.0.0.%d", i+1)).do("POST", "/api/v1/unlock", `{"password":"falsch"}`)
	}
	if rec := via("192.168.1.21").do("POST", "/api/v1/unlock", `{"password":"geheim123"}`); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("gemeinsame Obergrenze greift nicht: %d", rec.Code)
	}
}

func TestEditLockTokenAndBasic(t *testing.T) {
	h, st := newTestServer(t, func(c *config.Config) {
		c.Auth.Username, c.Auth.Password, c.Auth.APIToken = "admin", "login123", "tok"
	})
	if err := st.SetPassword("geheim123"); err != nil {
		t.Fatal(err)
	}
	body := `{"name":"NAS","mac":"02:00:00:00:00:01"}`
	// Die Anmeldung per Basic kennt oft der ganze Haushalt – sie entsperrt nicht.
	basic := &browser{t: t, h: h, setup: func(r *http.Request) { r.SetBasicAuth("admin", "login123") }}
	if rec := basic.do("POST", "/api/v1/devices", body); !lockedBody(t, rec) {
		t.Fatalf("Basic: %d %s", rec.Code, rec.Body)
	}
	// Automationen mit Token dürfen weiter ändern.
	token := &browser{t: t, h: h, setup: func(r *http.Request) { r.Header.Set("Authorization", "Bearer tok") }}
	if rec := token.do("POST", "/api/v1/devices", body); rec.Code != http.StatusCreated {
		t.Fatalf("Token: %d %s", rec.Code, rec.Body)
	}
}
