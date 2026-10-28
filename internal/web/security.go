package web

import (
	"context"
	"crypto/subtle"
	"fmt"
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

func (s *Server) hostGuard(next http.Handler) http.Handler {
	// Mit Anmeldung schützt bereits die: Der Browser schickt die Zugangsdaten nicht an die fremde
	// Seite. Ohne eigene Liste bleibt dann alles wie gehabt (z. B. Reverse-Proxy mit eigener Domain).
	if (s.cfg.AuthEnabled() && len(s.cfg.AllowedHosts) == 0) || slices.Contains(s.cfg.AllowedHosts, "*") {
		return next
	}
	self, _ := os.Hostname()
	self = strings.ToLower(self)
	var mu sync.Mutex
	logged := map[string]bool{}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := hostOnly(r.Host)
		if r.URL.Path == s.cfg.VDir+"health" || hostAllowed(host, self, s.cfg.AllowedHosts) {
			next.ServeHTTP(w, r)
			return
		}
		mu.Lock()
		if !logged[host] && len(logged) < 100 {
			logged[host] = true
			log.Printf("Aufruf unter dem unbekannten Hostnamen %q abgelehnt (Schutz vor DNS-Rebinding) – "+
				"gewollt? Dann in allowed_hosts bzw. WOLWEB_ALLOWED_HOSTS eintragen.", host)
		}
		mu.Unlock()
		msg := i18n.T(s.lang(r), "err.host_not_allowed", host)
		if strings.HasPrefix(r.URL.Path, s.cfg.VDir+"api/") {
			writeError(w, http.StatusForbidden, msg)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprintln(w, msg)
	})
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
func (s *Server) needsKey(r *http.Request) bool { return s.cfg.Auth.WakeKeys && isPublic(r) }

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
// mit true. Angemeldete Aufrufe sind nicht begrenzt.
func (s *Server) limitPublic(w http.ResponseWriter, r *http.Request, d store.Device) bool {
	if !isPublic(r) {
		return false
	}
	wait, ok := s.limiter.allow(d.ID)
	if ok {
		return false
	}
	secs := int(math.Ceil(wait.Seconds()))
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	writeJSON(w, http.StatusTooManyRequests, wakeResult{Device: d.Name, Error: "rate limited",
		Message: i18n.T(s.lang(r), "err.rate_limited", d.Name, secs)})
	return true
}
