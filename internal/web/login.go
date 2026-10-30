package web

import (
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// --- Bremse für die Anmeldung (HTTP Basic, API-Token) ---
//
// Wie beim Passwort für Änderungen (attemptLimiter) soll niemand Zugangsdaten mit Netzgeschwindigkeit
// raten können. Darum werden Zugangsdaten nur nacheinander geprüft, und ein Fehlversuch hält die
// Prüfung loginFailDelay lang auf – höchstens zwei Fehlversuche pro Sekunde, egal von wie vielen
// Adressen und wie vielen gleichzeitigen Verbindungen. Warten schon loginMaxWaiting Anfragen, kommt
// sofort 429, ohne zu prüfen: Eine Antwort, die vor der Bremse käme, verriete sonst das Ergebnis.
//
// Damit ein Angriff niemanden aussperrt, prüft die Bremse Aufrufer (clientKey), die sich in den
// letzten loginGoodFor erfolgreich angemeldet haben, sofort; ein Fehlversuch nimmt ihnen das.

const (
	loginFailDelay  = 500 * time.Millisecond
	loginMaxWaiting = 32
	loginGoodFor    = 7 * 24 * time.Hour
	loginGoodMax    = 1024
)

type loginBrake struct {
	run     sync.Mutex // eine Prüfung zur Zeit
	waiting atomic.Int32

	mu   sync.Mutex
	good map[string]time.Time // Aufrufer -> letzte erfolgreiche Anmeldung

	logMu   sync.Mutex
	logged  time.Time
	pending int // Fehlversuche seit der letzten Logzeile
}

// check prüft die Zugangsdaten von r mit authorized. ok: angemeldet. busy: zu viele warten – abweisen,
// ohne geprüft zu haben. Ohne Authorization-Kopf gibt es nichts zu prüfen und nichts zu bremsen.
func (b *loginBrake) check(r *http.Request, authorized func(*http.Request) bool) (ok, busy bool) {
	if r.Header.Get("Authorization") == "" {
		return false, false
	}
	key := clientKey(r)
	if b.isGood(key) {
		if authorized(r) {
			b.markGood(key)
			return true, false
		}
		b.forget(key)
		b.note(key)
		time.Sleep(loginFailDelay)
		return false, false
	}
	if b.waiting.Add(1) > loginMaxWaiting {
		b.waiting.Add(-1)
		b.note(key)
		return false, true
	}
	defer b.waiting.Add(-1)
	b.run.Lock()
	defer b.run.Unlock()
	if authorized(r) {
		b.markGood(key)
		return true, false
	}
	b.note(key)
	time.Sleep(loginFailDelay)
	return false, false
}

func (b *loginBrake) isGood(key string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	t, ok := b.good[key]
	return ok && time.Since(t) < loginGoodFor
}

func (b *loginBrake) markGood(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.good == nil {
		b.good = map[string]time.Time{}
	}
	if _, ok := b.good[key]; !ok && len(b.good) >= loginGoodMax {
		oldest, first := "", true
		for k, t := range b.good {
			if time.Since(t) >= loginGoodFor {
				delete(b.good, k)
			} else if first || t.Before(b.good[oldest]) {
				oldest, first = k, false
			}
		}
		if len(b.good) >= loginGoodMax {
			delete(b.good, oldest)
		}
	}
	b.good[key] = time.Now()
}

func (b *loginBrake) forget(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.good, key)
}

// note schreibt höchstens eine Logzeile pro Minute (mit der Zahl der Fehlversuche seitdem).
func (b *loginBrake) note(key string) {
	b.logMu.Lock()
	defer b.logMu.Unlock()
	b.pending++
	if time.Since(b.logged) < time.Minute {
		return
	}
	log.Printf("Login failed from %s (%d failed logins since the last note)", key, b.pending)
	b.logged, b.pending = time.Now(), 0
}
