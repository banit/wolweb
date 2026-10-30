package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"log"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"
)

// --- Bremse für die Anmeldung (HTTP Basic, API-Token) ---
//
// Wie beim Passwort für Änderungen (attemptLimiter) soll niemand Zugangsdaten mit Netzgeschwindigkeit
// raten können. Darum werden Zugangsdaten nur nacheinander geprüft, und ein Fehlversuch hält die
// Prüfung loginFailDelay lang auf – höchstens zwei Fehlversuche pro Sekunde, egal von wie vielen
// Adressen und wie vielen gleichzeitigen Verbindungen. Warten schon loginMaxWaiting Anfragen oder
// loginPerSource von derselben Quelle, kommt sofort "busy", ohne zu prüfen: Eine Antwort, die vor der
// Bremse käme, verriete sonst das Ergebnis.
//
// Damit ein Angriff Angemeldete nicht aussperrt, haben sie Vorrang und werden sofort geprüft:
//   - Browser mit dem Nachweis einer Anmeldung in den letzten loginGoodFor (Cookie wolweb_login,
//     signiert mit dem lokalen Geheimnis, gebunden an die eingestellten Zugangsdaten und den
//     Aufrufer) – ohne Grenze, denn wer ihn hat, kannte die Zugangsdaten;
//   - Aufrufer ohne Cookies (Automationen), die sich von derselben Adresse (clientKey) in den letzten
//     loginGoodFor angemeldet haben – höchstens loginFastMax gleichzeitig; ein Fehlversuch nimmt den
//     Vorrang für loginCooldown. Wer sich die Adresse mit einem Angemeldeten teilt (NAT, Proxy ohne
//     X-Forwarded-For), rät so höchstens loginFastMax-mal pro Minute ungebremst.
// Wer sich zum ersten Mal anmeldet, hat keinen Vorrang: Während eines Angriffs wartet er bis zu
// loginMaxWaiting × loginFailDelay oder bekommt "gleich noch einmal versuchen". Das ist der Preis
// dafür, dass niemand ungebremst raten kann; eine Quelle belegt höchstens loginPerSource Plätze, und
// öffentliche Seiten (Weckseiten mit auth.public_wake, health, static) bleiben erreichbar.

const (
	loginFailDelay  = 500 * time.Millisecond
	loginMaxWaiting = 32
	loginPerSource  = 4
	loginGoodFor    = 7 * 24 * time.Hour
	loginFastMax    = 6
	loginCooldown   = time.Minute
	loginClientsMax = 1024
)

type loginClient struct {
	good     time.Time // letzte erfolgreiche Anmeldung
	failed   time.Time // letzter Fehlversuch
	inflight int       // laufende Sofortprüfungen
}

type loginBrake struct {
	run sync.Mutex // eine gebremste Prüfung zur Zeit

	mu      sync.Mutex
	clients map[string]*loginClient
	waiting int            // wartende gebremste Prüfungen
	sources map[string]int // Quelle -> davon wartend

	logMu   sync.Mutex
	logged  time.Time
	pending int // Fehlversuche seit der letzten Logzeile
}

// check prüft die Zugangsdaten von r mit authorized. proven: r trägt einen gültigen Nachweis (siehe
// oben). ok: angemeldet. busy: zu viele warten – abweisen, ohne geprüft zu haben. Ohne
// Authorization-Kopf gibt es nichts zu prüfen und nichts zu bremsen.
func (b *loginBrake) check(r *http.Request, authorized func(*http.Request) bool, proven bool) (ok, busy bool) {
	if r.Header.Get("Authorization") == "" {
		return false, false
	}
	key := clientKey(r)
	if proven {
		if ok = authorized(r); ok {
			b.markGood(key)
		} else {
			b.note(key)
			time.Sleep(loginFailDelay)
		}
		return ok, false
	}
	if b.claimFast(key) {
		ok := authorized(r)
		b.releaseFast(key, ok)
		if !ok {
			b.note(key)
			time.Sleep(loginFailDelay)
		}
		return ok, false
	}
	src := sourceKey(r)
	if !b.enqueue(src) {
		b.note(key)
		return false, true
	}
	defer b.dequeue(src)
	b.run.Lock()
	defer b.run.Unlock()
	if authorized(r) {
		b.markGood(key)
		return true, false
	}
	b.markFailed(key)
	b.note(key)
	time.Sleep(loginFailDelay)
	return false, false
}

// sourceKey: woher eine Anfrage kommt, für den Anteil an der Warteschlange – die Adresse der
// Verbindung (X-Forwarded-For kann jeder setzen), bei IPv6 das /64 (so viele Adressen hat jeder).
func sourceKey(r *http.Request) string {
	host := r.RemoteAddr
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		host = h
	}
	if a, err := netip.ParseAddr(host); err == nil && a.Is6() && !a.Is4In6() {
		p, _ := a.Prefix(64)
		return p.String()
	}
	return host
}

func (b *loginBrake) enqueue(src string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.waiting >= loginMaxWaiting || b.sources[src] >= loginPerSource {
		return false
	}
	if b.sources == nil {
		b.sources = map[string]int{}
	}
	b.waiting++
	b.sources[src]++
	return true
}

func (b *loginBrake) dequeue(src string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.waiting--
	if b.sources[src]--; b.sources[src] <= 0 {
		delete(b.sources, src)
	}
}

// claimFast: key darf sofort geprüft werden (angemeldet, nicht gerade gescheitert, Platz frei).
func (b *loginBrake) claimFast(key string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	c := b.clients[key]
	if c == nil || c.good.IsZero() || time.Since(c.good) >= loginGoodFor || c.inflight >= loginFastMax {
		return false
	}
	c.inflight++
	return true
}

func (b *loginBrake) releaseFast(key string, ok bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	c := b.clients[key] // bleibt, solange inflight > 0
	c.inflight--
	b.settle(c, ok)
}

func (b *loginBrake) markGood(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if c := b.client(key); c != nil {
		b.settle(c, true)
	}
}

// markFailed merkt den Fehlversuch nur bei schon bekannten Aufrufern – ihnen nimmt er den Vorrang.
func (b *loginBrake) markFailed(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if c := b.clients[key]; c != nil {
		b.settle(c, false)
	}
}

// settle trägt das Ergebnis einer Prüfung ein (unter b.mu). Ein Erfolg kurz nach einem Fehlversuch
// gibt den Vorrang noch nicht zurück.
func (b *loginBrake) settle(c *loginClient, ok bool) {
	now := time.Now()
	switch {
	case !ok:
		c.good, c.failed = time.Time{}, now
	case now.Sub(c.failed) >= loginCooldown:
		c.good = now
	}
}

// client liefert den Eintrag von key und legt ihn bei Bedarf an (unter b.mu); die Liste bleibt
// höchstens loginClientsMax lang, notfalls fällt der am längsten nicht angemeldete weg.
func (b *loginBrake) client(key string) *loginClient {
	if c := b.clients[key]; c != nil {
		return c
	}
	if b.clients == nil {
		b.clients = map[string]*loginClient{}
	}
	if len(b.clients) >= loginClientsMax {
		var oldest string
		for k, c := range b.clients {
			if c.inflight > 0 {
				continue
			}
			if time.Since(c.good) >= loginGoodFor && time.Since(c.failed) >= loginCooldown {
				delete(b.clients, k)
			} else if oldest == "" || c.good.Before(b.clients[oldest].good) {
				oldest = k
			}
		}
		if len(b.clients) >= loginClientsMax {
			if oldest == "" {
				return nil
			}
			delete(b.clients, oldest)
		}
	}
	c := &loginClient{}
	b.clients[key] = c
	return c
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

// --- Nachweis einer Anmeldung (Cookie) ---

const loginCookie = "wolweb_login"

// loginKey hängt an den eingestellten Zugangsdaten – neue machen alle Nachweise ungültig – und am
// lokalen Geheimnis, das nie im Export steht.
func (s *Server) loginKey() []byte {
	a := s.conf().Auth
	m := hmac.New(sha256.New, s.store.LocalSecret())
	m.Write([]byte("login|" + a.Username + "\x00" + a.Password + "\x00" + a.APIToken))
	return m.Sum(nil)
}

// loginProof: Nachweis für den Aufrufer von r bis exp (Unix-Sekunden).
func (s *Server) loginProof(r *http.Request, exp string) string {
	m := hmac.New(sha256.New, s.loginKey())
	m.Write([]byte(exp + "|" + clientKey(r)))
	return exp + "." + hex.EncodeToString(m.Sum(nil))[:32]
}

// loginProven liefert das Ende eines gültigen Nachweises in r (Null, wenn keiner gilt).
func (s *Server) loginProven(r *http.Request) time.Time {
	c, err := r.Cookie(loginCookie)
	if err != nil {
		return time.Time{}
	}
	exp, _, ok := strings.Cut(c.Value, ".")
	n, err := strconv.ParseInt(exp, 10, 64)
	if !ok || err != nil {
		return time.Time{}
	}
	until := time.Unix(n, 0)
	if time.Now().After(until) || time.Until(until) > loginGoodFor || !hmac.Equal([]byte(c.Value), []byte(s.loginProof(r, exp))) {
		return time.Time{}
	}
	return until
}

// setLoginProof gibt nach einer Anmeldung den Nachweis aus (höchstens einmal am Tag neu).
func (s *Server) setLoginProof(w http.ResponseWriter, r *http.Request, until time.Time) {
	if time.Until(until) > loginGoodFor-24*time.Hour {
		return
	}
	until = time.Now().Add(loginGoodFor)
	http.SetCookie(w, &http.Cookie{Name: loginCookie, Value: s.loginProof(r, strconv.FormatInt(until.Unix(), 10)),
		Path: s.conf().VDir, Expires: until, HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"})
}
