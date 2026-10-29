package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"wolweb/internal/i18n"
)

// --- Bearbeitungsschutz ---
//
// Ist auf der Einstellungsseite ein Passwort gesetzt, bleiben Ansehen und Wecken frei; ändern lässt
// sich erst, wenn der Browser mit dem Passwort entsperrt wurde. Das Entsperren gilt, bis der
// Browser geschlossen wird (Sitzungs-Cookie) oder 12 Stunden lang nichts bearbeitet wird
// (gleitende Frist im Cookie, vom Server signiert). Automationen mit API-Token gelten als
// entsperrt; die Anmeldung per HTTP Basic nicht – die kennen oft alle im Haushalt, das Passwort
// für Änderungen nicht.

const (
	unlockCookie = "wolweb_unlock"
	unlockIdle   = 12 * time.Hour
	unlockRenew  = 10 * time.Minute // so alt darf die Frist werden, bevor sie verlängert wird
)

// lockKey ist der Schlüssel für Entsperr-Cookies. Er hängt am Passwort: Ein neues (oder
// entferntes) Passwort macht alle bisherigen Entsperrungen ungültig.
func (s *Server) lockKey() []byte {
	m := hmac.New(sha256.New, s.store.Secret())
	m.Write([]byte("unlock|" + s.store.PasswordStamp()))
	return m.Sum(nil)
}

func (s *Server) unlockToken(until time.Time) string {
	exp := strconv.FormatInt(until.Unix(), 10)
	m := hmac.New(sha256.New, s.lockKey())
	m.Write([]byte(exp))
	return exp + "." + hex.EncodeToString(m.Sum(nil))
}

// unlockUntil liefert das Ende der Entsperrung aus dem Cookie (Null, wenn keins gilt).
func (s *Server) unlockUntil(r *http.Request) time.Time {
	c, err := r.Cookie(unlockCookie)
	if err != nil {
		return time.Time{}
	}
	exp, sig, ok := strings.Cut(c.Value, ".")
	n, err := strconv.ParseInt(exp, 10, 64)
	if !ok || err != nil {
		return time.Time{}
	}
	m := hmac.New(sha256.New, s.lockKey())
	m.Write([]byte(exp))
	want := hex.EncodeToString(m.Sum(nil))
	until := time.Unix(n, 0)
	if !hmac.Equal([]byte(sig), []byte(want)) || time.Now().After(until) || time.Until(until) > unlockIdle {
		return time.Time{}
	}
	return until
}

// unlocked sagt, ob die Anfrage ändern darf: kein Passwort gesetzt, API-Token oder gültige
// Entsperrung.
func (s *Server) unlocked(r *http.Request) bool {
	if !s.store.HasPassword() || s.tokenAuthorized(r) {
		return true
	}
	return !s.unlockUntil(r).IsZero()
}

func (s *Server) setUnlockCookie(w http.ResponseWriter, r *http.Request, value string, remove bool) {
	c := &http.Cookie{Name: unlockCookie, Value: value, Path: s.conf().VDir, HttpOnly: true,
		SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"}
	if remove {
		c.MaxAge = -1
	}
	http.SetCookie(w, c) // ohne Ablaufdatum: endet mit dem Browser
}

// renewUnlock verlängert eine gültige Entsperrung, wenn damit gearbeitet wird (gleitende 12 Stunden).
// Nur bei geschützten Aufrufen, nicht bei jeder Anfrage: Eine offene Übersicht fragt im Hintergrund
// ständig nach – das hielte die Entsperrung sonst unbegrenzt am Leben.
func (s *Server) renewUnlock(w http.ResponseWriter, r *http.Request) {
	if until := s.unlockUntil(r); !until.IsZero() && time.Until(until) < unlockIdle-unlockRenew {
		s.setUnlockCookie(w, r, s.unlockToken(time.Now().Add(unlockIdle)), false)
	}
}

// editable lässt nur entsperrte Anfragen durch; sonst 403 mit "locked": true, damit die Oberfläche
// gleich nach dem Passwort fragen kann.
func (s *Server) editable(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.unlocked(r) {
			writeJSON(w, http.StatusForbidden, map[string]any{"success": false, "locked": true,
				"message": i18n.T(s.lang(r), "err.locked")})
			return
		}
		s.renewUnlock(w, r)
		h(w, r)
	}
}

// change: ändernde Aufrufe – nicht im Nur-Lesen-Betrieb und nur entsperrt.
func (s *Server) change(h http.HandlerFunc) http.HandlerFunc { return s.writable(s.editable(h)) }

func (s *Server) unlock(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Password string `json:"password"`
	}
	if err := readJSON(r, &in); err != nil {
		s.storeError(w, r, err)
		return
	}
	if !s.store.HasPassword() {
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "locked": false})
		return
	}
	key := clientKey(r)
	ok, wait := s.attempts.try(key, func() bool { return s.store.CheckPassword(in.Password) })
	if wait > 0 {
		s.fail(w, r, http.StatusTooManyRequests, "err.unlock_wait", int(wait.Seconds()+0.999))
		return
	}
	if !ok {
		log.Printf("Unlock failed from %s", key)
		writeJSON(w, http.StatusForbidden, map[string]any{"success": false, "field": "password",
			"message": i18n.T(s.lang(r), "err.password_wrong")})
		return
	}
	s.setUnlockCookie(w, r, s.unlockToken(time.Now().Add(unlockIdle)), false)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "locked": false})
}

func (s *Server) lock(w http.ResponseWriter, r *http.Request) {
	if !s.unlockUntil(r).IsZero() {
		s.hub.dropPrivileged() // auch andere Fenster dieses Browsers bekommen den Status nicht mehr
	}
	s.setUnlockCookie(w, r, "", true)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "locked": s.store.HasPassword()})
}

// setPassword setzt, ändert oder entfernt das Passwort für Änderungen. Ist schon eins gesetzt,
// muss es mitgeschickt werden – auch wenn der Browser entsperrt ist.
func (s *Server) setPassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Current  string `json:"current"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &in); err != nil {
		s.storeError(w, r, err)
		return
	}
	if s.store.HasPassword() {
		ok, wait := s.attempts.try(clientKey(r), func() bool { return s.store.CheckPassword(in.Current) })
		if wait > 0 {
			s.fail(w, r, http.StatusTooManyRequests, "err.unlock_wait", int(wait.Seconds()+0.999))
			return
		}
		if !ok {
			writeJSON(w, http.StatusForbidden, map[string]any{"success": false, "field": "current",
				"message": i18n.T(s.lang(r), "err.password_wrong")})
			return
		}
	}
	if err := s.store.SetPassword(in.Password); err != nil {
		s.storeError(w, r, err)
		return
	}
	if in.Password == "" {
		log.Print("Password for changes removed")
		s.setUnlockCookie(w, r, "", true)
	} else {
		log.Print("Password for changes set")
		// Wer es eben gesetzt hat, bleibt entsperrt (der Schlüssel hängt am neuen Passwort).
		s.setUnlockCookie(w, r, s.unlockToken(time.Now().Add(unlockIdle)), false)
	}
	s.hub.dropPrivileged()                // bisherige Entsperrungen gelten nicht mehr
	s.hub.publish("settings", struct{}{}) // offene Fenster fragen neu, ob sie ändern dürfen
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "password": in.Password != ""})
}

// clientKey unterscheidet Aufrufer für die Bremse: die Adresse und, hinter einem Reverse-Proxy, der
// letzte Eintrag von X-Forwarded-For (den hängt der Proxy selbst an) – sonst teilten sich alle im
// Haushalt einen Zähler, und wer sich vertippt, sperrte alle aus. Ohne Proxy kann ein Aufrufer den
// Kopf frei setzen; dagegen hilft die gemeinsame Obergrenze für alle (attemptsFreeAll).
func clientKey(r *http.Request) string {
	key := r.RemoteAddr
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		key = h
	}
	if f := r.Header.Values("X-Forwarded-For"); len(f) > 0 {
		hops := strings.Split(f[len(f)-1], ",")
		if last := strings.TrimSpace(hops[len(hops)-1]); last != "" {
			key += " via " + last[:min(len(last), 64)]
		}
	}
	return key
}

// attemptLimiter bremst das Raten: Nach 5 Fehlversuchen je Aufrufer heißt es warten, zuerst 30 s,
// dann jeweils doppelt so lange (höchstens 15 Minuten); nach 30 Fehlversuchen insgesamt gilt das für
// alle. Ein Treffer setzt zurück.
type attemptLimiter struct {
	run  sync.Mutex // eine Prüfung zur Zeit
	mu   sync.Mutex
	seen map[string]*attempts
}

const allClients = "" // Schlüssel der gemeinsamen Zählung (clientKey ist nie leer)

// try prüft ein Passwort unter der Bremse. Es läuft immer nur eine Prüfung (PBKDF2 kostet Rechenzeit),
// und die Wartezeit wird erst unter dieser Sperre angesehen – so zählen auch gleichzeitig geschickte
// Versuche einzeln. wait > 0: abgewiesen, ohne zu prüfen.
func (l *attemptLimiter) try(key string, check func() bool) (ok bool, wait time.Duration) {
	l.run.Lock()
	defer l.run.Unlock()
	if wait = max(l.wait(key), l.wait(allClients)); wait > 0 {
		return false, wait
	}
	if check() {
		l.reset(key)
		l.reset(allClients)
		return true, 0
	}
	l.failed(key, attemptsFree)
	l.failed(allClients, attemptsFreeAll)
	return false, 0
}

type attempts struct {
	failures int
	last     time.Time // letzter Fehlversuch
	until    time.Time // so lange abweisen
}

const (
	attemptsFree    = 5
	attemptsFreeAll = 30
	attemptsWait    = 30 * time.Second
	attemptsMax     = 15 * time.Minute
)

func (l *attemptLimiter) wait(key string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	if a := l.seen[key]; a != nil {
		return time.Until(a.until)
	}
	return 0
}

func (l *attemptLimiter) failed(key string, free int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.seen == nil {
		l.seen = map[string]*attempts{}
	}
	for k, a := range l.seen { // nach einer Stunde Ruhe vergessen
		if time.Since(a.last) > time.Hour && time.Now().After(a.until) {
			delete(l.seen, k)
		}
	}
	a := l.seen[key]
	if a == nil {
		a = &attempts{}
		l.seen[key] = a
	}
	a.failures++
	a.last = time.Now()
	if n := a.failures - free; n >= 0 {
		d := attemptsWait << min(n, 10)
		a.until = time.Now().Add(min(d, attemptsMax))
	}
}

func (l *attemptLimiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.seen, key)
}
