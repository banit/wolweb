package web

import (
	"context"
	"crypto/subtle"
	"log"
	"math"
	"net"
	"net/http"
	"net/netip"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"wolweb/internal/i18n"
	"wolweb/internal/store"
)

// --- Schutz vor DNS-Rebinding ---
//
// Eine fremde Webseite kann ihren Namen kurzzeitig auf die IP von wolweb zeigen lassen; der
// Browser hält ihre Skripte dann für "gleiche Herkunft" und lässt sie die API benutzen – ohne
// Anmeldung also alles. Darum werden nur Hostnamen angenommen, die keine fremde Seite haben kann:
// IP-Adressen, Namen ohne Punkt, lokale Endungen und was in allowed_hosts steht.

// lanSuffixes sind Endungen, die es im öffentlichen DNS nicht gibt (oder nur für den Router).
var lanSuffixes = []string{".local", ".lan", ".home", ".internal", ".intranet", ".localdomain", ".home.arpa", ".fritz.box"}

// Eine sichere Automatik gibt es hier nicht: Der Name eines echten Reverse-Proxys und der einer
// Angriffsseite sehen für wolweb gleich aus (öffentliche Domain, Anfrage aus dem LAN), und
// X-Forwarded-Host kann das Skript der Angriffsseite selbst setzen. Deshalb nennt die Ablehnung den
// Namen samt der Zeile zum Eintragen, und die Einstellungsseite – über die IP geöffnet – bietet die
// zuletzt abgelehnten Namen zum Erlauben an. Ein Klick dort braucht X-WolWeb und die vertrauenswürdige
// Adresse, eine fremde Seite kann ihn nicht auslösen.

func (s *Server) hostGuard(next http.Handler) http.Handler {
	self := selfHost()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := s.conf() // bei jeder Anfrage: allowed_hosts lässt sich im Betrieb ändern
		// Mit Anmeldung schützt bereits die: Der Browser schickt die Zugangsdaten nicht an die fremde
		// Seite. Ohne eigene Liste bleibt dann alles wie gehabt (z. B. Reverse-Proxy mit eigener Domain).
		if (c.AuthEnabled() && len(c.AllowedHosts) == 0) || slices.Contains(c.AllowedHosts, "*") {
			next.ServeHTTP(w, r)
			return
		}
		host := hostOnly(r.Host)
		// /health für Healthchecks, static/ für die Fehlerseite selbst (CSS, Symbole – nichts Geheimes).
		if r.URL.Path == c.VDir+"health" || strings.HasPrefix(r.URL.Path, c.VDir+"static/") || hostAllowed(host, self, c.AllowedHosts) {
			next.ServeHTTP(w, r)
			return
		}
		if s.rejected.add(host) {
			log.Printf("Aufruf unter dem Hostnamen %q abgelehnt (Schutz vor DNS-Rebinding). Gewollt, z. B. hinter "+
				"einem Reverse-Proxy? Dann in den Einstellungen unter Sicherheit erlauben oder eintragen: "+
				`"allowed_hosts": [%q] bzw. WOLWEB_ALLOWED_HOSTS=%s`, host, host, host)
		}
		if strings.HasPrefix(r.URL.Path, c.VDir+"api/") || !wantsHTML(r) {
			writeError(w, http.StatusForbidden, i18n.T(s.lang(r), "err.host_not_allowed", host))
			return
		}
		s.render(w, r, http.StatusForbidden, "blocked.html", pageData{Host: host})
	})
}

// rejectedHosts merkt sich die zuletzt abgelehnten Hostnamen (für das Log und die Einstellungen).
type rejectedHosts struct {
	mu   sync.Mutex
	last map[string]time.Time
}

// add merkt sich host und sagt, ob er neu ist (dann einmal ins Log).
func (h *rejectedHosts) add(host string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.last == nil {
		h.last = map[string]time.Time{}
	}
	_, seen := h.last[host]
	h.last[host] = time.Now()
	for len(h.last) > 20 { // nur die neuesten behalten – Namen kann sich jeder ausdenken
		oldest := ""
		for k, t := range h.last {
			if oldest == "" || t.Before(h.last[oldest]) {
				oldest = k
			}
		}
		delete(h.last, oldest)
	}
	return !seen
}

// list liefert die abgelehnten Namen, neueste zuerst, ohne die inzwischen erlaubten.
func (h *rejectedHosts) list(self string, allowed []string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := []string{}
	for k := range h.last {
		if !hostAllowed(k, self, allowed) {
			out = append(out, k)
		}
	}
	slices.SortFunc(out, func(a, b string) int { return h.last[b].Compare(h.last[a]) })
	return out
}

func selfHost() string {
	h, _ := os.Hostname()
	return strings.ToLower(h)
}

func hostOnly(hostport string) string {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	return strings.ToLower(strings.TrimSuffix(strings.Trim(host, "[]"), "."))
}

func hostAllowed(host, self string, allowed []string) bool {
	if host == "" || host == self || host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return true
	}
	if !strings.Contains(host, ".") {
		return true
	}
	for _, suf := range lanSuffixes {
		if strings.HasSuffix(host, suf) {
			return true
		}
	}
	for _, a := range allowed {
		a = strings.ToLower(strings.TrimSpace(a))
		if a == host || (strings.HasPrefix(a, "*.") && strings.HasSuffix(host, a[1:])) {
			return true
		}
	}
	return false
}

// --- Öffentliche Wecklinks ---

type ctxKey int

const publicAccess ctxKey = 1

// isPublic: Die Anfrage kam ohne Anmeldung durch, weil der Pfad öffentlich ist (Wecklinks).
func isPublic(r *http.Request) bool {
	v, _ := r.Context().Value(publicAccess).(bool)
	return v
}

func markPublic(r *http.Request) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), publicAccess, true))
}

// needsKey: Ohne Anmeldung verlangt auth.wake_keys den Schlüssel des Geräts.
func (s *Server) needsKey(r *http.Request) bool { return s.conf().Auth.WakeKeys && isPublic(r) }

// findForLink sucht das Gerät eines Wecklinks. Fehlt der nötige Schlüssel oder ist er falsch,
// gilt das Gerät als nicht vorhanden – so lassen sich Namen nicht durchprobieren.
func (s *Server) findForLink(r *http.Request) (store.Device, bool) {
	d, ok := s.store.FindByName(r.PathValue("name"))
	if !ok || !s.needsKey(r) {
		return d, ok
	}
	if key := r.URL.Query().Get("key"); d.WakeKey == "" || subtle.ConstantTimeCompare([]byte(key), []byte(d.WakeKey)) != 1 {
		return store.Device{}, false
	}
	return d, true
}

// publicWakeInterval: So oft darf ein Gerät über einen öffentlichen Wecklink geweckt werden.
const publicWakeInterval = 10 * time.Second

type wakeLimiter struct {
	mu   sync.Mutex
	last map[string]time.Time
}

func (l *wakeLimiter) allow(id string) (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.last == nil {
		l.last = map[string]time.Time{}
	}
	if wait := publicWakeInterval - time.Since(l.last[id]); wait > 0 {
		return wait, false
	}
	l.last[id] = time.Now()
	if len(l.last) > 1000 {
		for k, t := range l.last {
			if time.Since(t) > publicWakeInterval {
				delete(l.last, k)
			}
		}
	}
	return 0, true
}

// limitPublic beantwortet zu schnell wiederholte öffentliche Weckaufrufe mit 429 und meldet das
// mit true. key trennt Geräte und Gruppen, msgKey/name ergeben die Meldung. Angemeldete Aufrufe
// sind nicht begrenzt.
func (s *Server) limitPublic(w http.ResponseWriter, r *http.Request, key, msgKey, name string) bool {
	if !isPublic(r) {
		return false
	}
	wait, ok := s.limiter.allow(key)
	if ok {
		return false
	}
	secs := int(math.Ceil(wait.Seconds()))
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	writeJSON(w, http.StatusTooManyRequests, wakeResult{Device: name, Error: "rate limited",
		Message: i18n.T(s.lang(r), msgKey, name, secs)})
	return true
}
