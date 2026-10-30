package web

import (
	"log"
	"sync"
	"sync/atomic"
	"time"
)

// --- Bremse für die Anmeldung (HTTP Basic, API-Token) ---
//
// Wie beim Passwort für Änderungen (attemptLimiter) soll niemand Zugangsdaten mit Netzgeschwindigkeit
// raten können. Gescheiterte Anmeldungen laufen deshalb nacheinander und dauern je loginFailDelay –
// höchstens zwei Versuche pro Sekunde, egal von wie vielen Adressen. Richtige Zugangsdaten warten nie,
// darum sperrt die Bremse niemanden aus. Warten schon loginMaxWaiting Fehlversuche, kommt sofort 429.

const (
	loginFailDelay  = 500 * time.Millisecond
	loginMaxWaiting = 32
)

type loginBrake struct {
	run     sync.Mutex // eine gescheiterte Anmeldung zur Zeit
	waiting atomic.Int32

	logMu   sync.Mutex
	logged  time.Time
	pending int // Fehlversuche seit der letzten Logzeile
}

// fail bremst eine gescheiterte Anmeldung von key und protokolliert gedrosselt. false: Es warten
// schon zu viele – dann ohne Wartezeit abweisen.
func (b *loginBrake) fail(key string) bool {
	b.note(key)
	if b.waiting.Add(1) > loginMaxWaiting {
		b.waiting.Add(-1)
		return false
	}
	defer b.waiting.Add(-1)
	b.run.Lock()
	time.Sleep(loginFailDelay)
	b.run.Unlock()
	return true
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
