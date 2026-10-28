// Package web stellt Oberfläche, Weckseite und REST-API bereit.
package web

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"wolweb/internal/config"
	"wolweb/internal/discovery"
	"wolweb/internal/i18n"
	"wolweb/internal/notify"
	"wolweb/internal/status"
	"wolweb/internal/store"
	"wolweb/internal/wol"
)

//go:embed static templates
var assets embed.FS

type Server struct {
	cur      atomic.Pointer[live] // Einstellungen; die Einstellungsseite tauscht sie im Betrieb aus
	settings *config.Manager      // nil: Einstellungen nur aus config.json (Tests)
	applyMu  sync.Mutex           // nur eine Übernahme zur Zeit
	saveMu   sync.Mutex           // Speichern und Übernehmen der Einstellungen als ein Schritt
	store    *store.Store
	monitor  *status.Monitor
	scanner  *discovery.Scanner
	icmp     bool
	version  string
	retry    retries
	tmpl     *template.Template
	limiter  wakeLimiter
	rejected rejectedHosts // vom Schutz vor DNS-Rebinding abgelehnte Hostnamen
	schedRan ranSchedules  // ausgeführte Weckzeiten (Sommerzeit: dieselbe Uhrzeit zweimal)
	hub      hub

	wakesMu sync.Mutex
	wakes   map[[2]string]int // {Geräte-ID, "ok"/"error"} -> Anzahl seit dem Start (für /metrics)

	scanMu   sync.Mutex
	scanLast time.Time
}

// live sind die Einstellungen, mit denen der Server gerade arbeitet. Ein Satz wird nie verändert,
// sondern als Ganzes ersetzt – so sieht jede Anfrage einen stimmigen Stand.
type live struct {
	cfg                    config.Config
	wakeTimeout, wakeRetry time.Duration // Wiederholung 0 = keine
	lang                   i18n.Lang     // Sprache, wenn die Anfrage keine nennt (config language)
	notify                 *notify.Notifier
}

func (s *Server) newLive(cfg config.Config) *live {
	_, _, wt, wr := cfg.Durations()
	l, ok := i18n.Parse(cfg.Language)
	if !ok {
		l = i18n.EN
	}
	return &live{cfg: cfg, wakeTimeout: wt, wakeRetry: wr, lang: l,
		notify: notify.New(cfg.Notify.Webhook, cfg.Notify.Ntfy, cfg.Notify.Events, s.version, l)}
}

// rt liefert die aktuellen Einstellungen (nicht verändern).
func (s *Server) rt() *live { return s.cur.Load() }

// conf ist die aktuelle Konfiguration (nicht verändern).
func (s *Server) conf() *config.Config { return &s.cur.Load().cfg }

func New(cfg config.Config, st *store.Store, mon *status.Monitor, sc *discovery.Scanner, icmp bool, version string) *Server {
	s := &Server{
		store: st, monitor: mon, scanner: sc, icmp: icmp, version: version,
		tmpl:  template.Must(template.ParseFS(assets, "templates/*.html")),
		wakes: map[[2]string]int{},
	}
	s.cur.Store(s.newLive(cfg))
	// Live-Updates (und Benachrichtigungen) hängen an Monitor und Suche – daher vor deren Start.
	mon.OnChange = s.statusChanged
	sc.OnUpdate = s.scanChanged
	return s
}

func (s *Server) Handler() http.Handler {
	base := s.conf().VDir // "/" oder "/wolweb/"
	mux := http.NewServeMux()
	static, _ := fs.Sub(assets, "static")
	fileServer := http.StripPrefix(base+"static/", http.FileServer(http.FS(static)))
	mux.Handle("GET "+base+"static/", cacheControl(fileServer))
	if base != "/" {
		mux.HandleFunc("GET "+strings.TrimSuffix(base, "/"), func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, base, http.StatusFound)
		})
	}
	mux.HandleFunc("GET "+base+"{$}", s.index)
	mux.HandleFunc("GET "+base+"health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		io.WriteString(w, "alive")
	})

	// Feste Wecklinks
	mux.HandleFunc("GET "+base+"wake/{name}", s.wakeLink)
	mux.HandleFunc("GET "+base+"wake/{name}/{$}", s.wakeLink)
	mux.HandleFunc("POST "+base+"wake/{name}", s.wakeLinkPost)
	mux.HandleFunc("GET "+base+"wake/{name}/status", s.wakeLinkStatus)
	mux.HandleFunc("GET "+base+"wake-group/{name}", s.groupLink)
	mux.HandleFunc("POST "+base+"wake-group/{name}", s.groupLinkPost)
	mux.HandleFunc("GET "+base+"wake-group/{name}/status", s.groupLinkStatus)

	// API
	api := base + "api/v1/"
	mux.HandleFunc("GET "+api+"info", s.info)
	mux.HandleFunc("GET "+api+"events", s.events)
	mux.HandleFunc("GET "+api+"devices", s.listDevices)
	mux.HandleFunc("POST "+api+"devices", s.writable(s.createDevice))
	mux.HandleFunc("POST "+api+"devices/group", s.writable(s.setGroup))
	mux.HandleFunc("PUT "+api+"devices/{id}", s.writable(s.updateDevice))
	mux.HandleFunc("DELETE "+api+"devices/{id}", s.writable(s.deleteDevice))
	mux.HandleFunc("POST "+api+"devices/{id}/wake", s.wakeDevice)
	mux.HandleFunc("GET "+api+"devices/{id}/status", s.deviceStatus)
	mux.HandleFunc("POST "+api+"devices/{id}/wake-key", s.writable(s.rotateWakeKey))
	mux.HandleFunc("GET "+api+"schedule/preview", s.schedulePreview)
	mux.HandleFunc("GET "+api+"groups", s.listGroups)
	mux.HandleFunc("POST "+api+"groups/{name}/wake", s.wakeGroupAPI)
	mux.HandleFunc("GET "+api+"history", s.history)
	mux.HandleFunc("GET "+api+"export", s.export)
	mux.HandleFunc("POST "+api+"import", s.writable(s.importDevices))
	mux.HandleFunc("GET "+api+"discovery", s.discovery)
	mux.HandleFunc("POST "+api+"discovery/scan", s.startScan)
	mux.HandleFunc("POST "+api+"discovery/cancel", s.cancelScan)
	mux.HandleFunc("POST "+api+"discovery/{mac}/ignore", s.writable(s.ignoreFound))
	mux.HandleFunc("DELETE "+api+"discovery/{mac}", s.writable(s.forgetFound))
	mux.HandleFunc("POST "+api+"discovery/network", s.withSettings(s.writable(s.chooseNetwork)))
	mux.HandleFunc("GET "+base+"settings", s.withSettings(s.settingsPage))
	mux.HandleFunc("GET "+api+"settings", s.withSettings(s.getSettings))
	mux.HandleFunc("PUT "+api+"settings", s.withSettings(s.writable(s.putSettings)))
	mux.HandleFunc("DELETE "+api+"settings", s.withSettings(s.writable(s.resetSettings)))
	mux.HandleFunc("POST "+api+"settings/test-notify", s.withSettings(s.writable(s.testNotify)))

	// Kompatibel zum Original: Geräteliste im alten Format
	mux.HandleFunc("GET "+base+"data/get", s.legacyData)
	mux.HandleFunc("GET "+base+"metrics", s.metrics)

	return s.recoverer(s.hostGuard(s.auth(s.csrf(securityHeaders(mux)))))
}

// --- Middleware ---

func (s *Server) auth(next http.Handler) http.Handler {
	if !s.conf().AuthEnabled() {
		return next
	}
	base := s.conf().VDir
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if s.authorized(r) {
			next.ServeHTTP(w, r)
			return
		}
		if p == base+"health" || strings.HasPrefix(p, base+"static/") ||
			(s.conf().Auth.PublicWake && (strings.HasPrefix(p, base+"wake/") || strings.HasPrefix(p, base+"wake-group/"))) {
			next.ServeHTTP(w, markPublic(r)) // ohne Anmeldung: Schlüssel und Begrenzung greifen
			return
		}
		if s.conf().Auth.Username != "" {
			w.Header().Set("WWW-Authenticate", `Basic realm="wolweb", charset="UTF-8"`)
		}
		s.fail(w, r, http.StatusUnauthorized, "err.auth_required")
	})
}

func (s *Server) authorized(r *http.Request) bool {
	eq := func(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }
	if t := s.conf().Auth.APIToken; t != "" {
		if h, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok && eq(strings.TrimSpace(h), t) {
			return true
		}
	}
	if s.conf().Auth.Username != "" {
		if u, p, ok := r.BasicAuth(); ok && eq(u, s.conf().Auth.Username) && eq(p, s.conf().Auth.Password) {
			return true
		}
	}
	return false
}

// csrf: Ändernde Anfragen brauchen den Kopf "X-WolWeb". Fremde Webseiten können ihn nicht ohne
// CORS-Freigabe setzen; Automationen mit Token sind ausgenommen.
func (s *Server) csrf(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if r.Header.Get("X-WolWeb") == "" && !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
				// Die Meldung ist für Menschen; wer die API anspricht, liest in "error", was fehlt.
				writeJSON(w, http.StatusForbidden, map[string]any{"success": false, "message": i18n.T(s.lang(r), "err.csrf"),
					"error": `header "X-WolWeb: 1" missing`})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				log.Printf("Error in %s %s: %v", r.Method, r.URL.Path, v)
				s.fail(w, r, http.StatusInternalServerError, "err.internal")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func cacheControl(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache") // ETag/Last-Modified reichen; nach Updates sofort neu
		h.ServeHTTP(w, r)
	})
}

func (s *Server) writable(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.conf().ReadOnly {
			s.fail(w, r, http.StatusForbidden, "err.read_only")
			return
		}
		h(w, r)
	}
}

// --- Hilfen ---

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(true)
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"success": false, "message": msg})
}

// fail antwortet mit einer Fehlermeldung in der Sprache der Anfrage.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, code int, key string, args ...any) {
	writeError(w, code, i18n.T(s.lang(r), key, args...))
}

// lang ist die Sprache einer Anfrage (?lang=, Cookie des Umschalters, Accept-Language,
// sonst die Einstellung language).
func (s *Server) lang(r *http.Request) i18n.Lang { return i18n.FromRequest(r, s.rt().lang) }

func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return store.Invalid("err.invalid_json", err.Error())
	}
	if dec.More() {
		return store.Invalid("err.json_multiple")
	}
	return nil
}

// storeError übersetzt Fehler des Store; Eingabefehler nennen das betroffene Feld ("field").
func (s *Server) storeError(w http.ResponseWriter, r *http.Request, err error) {
	var ve store.ValidationError
	switch {
	case errors.As(err, &ve):
		out := map[string]any{"success": false, "message": ve.In(s.lang(r)), "field": ve.Field}
		if ve.Confirm != "" {
			out["confirm"] = ve.Confirm // die Oberfläche fragt nach und schickt das Feld mit true erneut
		}
		writeJSON(w, http.StatusBadRequest, out)
	case errors.Is(err, store.ErrNotFound):
		s.fail(w, r, http.StatusNotFound, "err.device_not_found")
	case errors.Is(err, store.ErrConflict):
		s.fail(w, r, http.StatusConflict, "err.conflict")
	default:
		log.Printf("Saving failed: %v", err)
		s.fail(w, r, http.StatusInternalServerError, "err.save_failed", err)
	}
}

// --- Seiten ---

type pageData struct {
	Base    string
	Version string
	Device  *deviceView
	Timeout int
	Retry   int // Sekunden bis zur automatischen Wiederholung, 0 = keine
	Missing string
	Key     string // Weckschlüssel, den die Weckseite bei ihren Aufrufen mitschickt
	Host    string // abgelehnter Hostname (blocked.html)
	Group   *groupView

	Lang      i18n.Lang         // Sprache der Seite
	OtherLang i18n.Lang         // Ziel des Umschalters
	I18N      map[string]string // Katalog für die Skripte (als JSON in der Seite)
}

// T übersetzt in Vorlagen: {{.T "page.save"}}, {{.T "wake.title.starting" .Device.Name}}.
func (p pageData) T(key string, args ...any) string { return i18n.T(p.Lang, key, args...) }

// TN wählt Einzahl/Mehrzahl (key.one / key.other) nach n.
func (p pageData) TN(key string, n int) string {
	if n == 1 {
		return i18n.T(p.Lang, key+".one", n)
	}
	return i18n.T(p.Lang, key+".other", n)
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, code int, name string, d pageData) {
	d.Base, d.Version = s.conf().VDir, s.version
	d.Lang = s.lang(r)
	d.OtherLang = i18n.EN
	if d.Lang == i18n.EN {
		d.OtherLang = i18n.DE
	}
	d.I18N = i18n.Catalog(d.Lang)
	w.Header().Set("Content-Language", string(d.Lang))
	w.Header().Add("Vary", "Accept-Language, Cookie")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	if err := s.tmpl.ExecuteTemplate(w, name, d); err != nil {
		log.Printf("Template %s: %v", name, err)
	}
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "index.html", pageData{})
}

// --- Geräte ---

type deviceView struct {
	store.Device
	Status     *status.State `json:"status,omitempty"`
	WakeTarget string        `json:"wake_target"`
	WakePath   string        `json:"wake_path"`
	CheckIP    string        `json:"check_ip,omitempty"`
	LastWake   *time.Time    `json:"last_wake,omitempty"`
	NextWake   *time.Time    `json:"next_wake,omitempty"` // nächste Weckzeit laut Zeitplan
}

func (s *Server) view(d store.Device) deviceView {
	v := deviceView{Device: d, WakeTarget: s.wakeTarget(d), WakePath: "wake/" + pathEscape(d.Name)}
	if ip, _ := s.monitor.Target(d); ip.IsValid() {
		v.CheckIP = ip.String()
	}
	if t := s.store.LastWake(d.ID); !t.IsZero() {
		v.LastWake = &t
	}
	v.NextWake = nextWake(d)
	if st, ok := s.monitor.Get(d.ID); ok {
		v.Status = &st
	}
	return v
}

func pathEscape(s string) string {
	return strings.ReplaceAll(template.URLQueryEscaper(s), "+", "%20")
}

// wakeTarget: eingetragene Broadcast-Adresse, sonst die des Netzes der Geräte-IP, sonst der Standard.
func (s *Server) wakeTarget(d store.Device) string {
	if d.Broadcast != "" {
		return d.Broadcast
	}
	ip, _ := s.monitor.Target(d)
	if ip.IsValid() {
		if b := discovery.BroadcastFor(ip.String()); b != "" {
			return b
		}
		// Eingetragenes Netz ohne eigene Schnittstelle (anderes VLAN): gerichteter Broadcast. Ein
		// Router kann ihn weiterleiten; 255.255.255.255 käme dort nie an.
		for _, n := range s.conf().Discovery.Networks {
			if p, err := netip.ParsePrefix(n.CIDR); err == nil && p.Bits() <= 30 && p.Contains(ip) {
				return discovery.BroadcastOf(p)
			}
		}
	}
	return s.defaultBroadcast()
}

// defaultBroadcast: eingestellte Adresse (bcastip), sonst die des einzigen Suchsegments, sonst
// 255.255.255.255 – das geht über die Schnittstelle der Standardroute hinaus. Früher war fest
// 192.168.1.255 voreingestellt; in jedem anderen Netz verschwand das Paket dann still.
func (s *Server) defaultBroadcast() string {
	if b, err := config.NormalizeBroadcast(s.conf().BcastIP); err == nil && b != "" {
		return b
	}
	if b := s.scanner.Broadcast(); b != "" {
		return b
	}
	return "255.255.255.255:9"
}

func (s *Server) listDevices(w http.ResponseWriter, r *http.Request) {
	devices, _ := s.store.Devices()
	out := make([]deviceView, 0, len(devices))
	for _, d := range devices {
		out = append(out, s.view(d))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createDevice(w http.ResponseWriter, r *http.Request) {
	var in store.Input
	if err := readJSON(r, &in); err != nil {
		s.storeError(w, r, err)
		return
	}
	d, err := s.store.Create(in)
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	log.Printf("Device added: %s (%s)", d.Name, d.MAC)
	s.devicesChanged()
	go s.monitor.Check(context.Background(), d)
	writeJSON(w, http.StatusCreated, s.view(d))
}

func (s *Server) updateDevice(w http.ResponseWriter, r *http.Request) {
	var in struct {
		store.Input
		UpdatedAt *time.Time `json:"updated_at"`
	}
	if err := readJSON(r, &in); err != nil {
		s.storeError(w, r, err)
		return
	}
	d, err := s.store.Update(r.PathValue("id"), in.Input, in.UpdatedAt)
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	log.Printf("Device changed: %s (%s)", d.Name, d.MAC)
	s.devicesChanged()
	go s.monitor.Check(context.Background(), d)
	writeJSON(w, http.StatusOK, s.view(d))
}

// setGroup ordnet mehrere Geräte auf einmal einer Gruppe zu (Mehrfachauswahl in der Übersicht);
// eine leere Gruppe nimmt sie aus ihrer Gruppe.
func (s *Server) setGroup(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDs   []string `json:"ids"`
		Group string   `json:"group"`
	}
	if err := readJSON(r, &in); err != nil {
		s.storeError(w, r, err)
		return
	}
	if len(in.IDs) == 0 {
		s.storeError(w, r, store.Invalid("err.none_selected"))
		return
	}
	devs, err := s.store.SetGroup(in.IDs, in.Group)
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	group := in.Group
	if len(devs) > 0 {
		group = devs[0].Group // Schreibweise einer schon vorhandenen Gruppe
	}
	log.Printf("Group %q set for %d devices", group, len(devs))
	s.devicesChanged()
	out := make([]deviceView, 0, len(devs))
	for _, d := range devs {
		out = append(out, s.view(d))
	}
	writeJSON(w, http.StatusOK, map[string]any{"group": group, "devices": out})
}

func (s *Server) deleteDevice(w http.ResponseWriter, r *http.Request) {
	s.supersedeRetry(r.PathValue("id")) // keine Wiederholung mehr an ein gelöschtes Gerät
	if err := s.store.Delete(r.PathValue("id")); err != nil {
		s.storeError(w, r, err)
		return
	}
	s.devicesChanged()
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) rotateWakeKey(w http.ResponseWriter, r *http.Request) {
	d, err := s.store.RotateWakeKey(r.PathValue("id"))
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	log.Printf("New wake key for %s", d.Name)
	s.devicesChanged()
	writeJSON(w, http.StatusOK, s.view(d))
}

func (s *Server) deviceStatus(w http.ResponseWriter, r *http.Request) {
	d, ok := s.store.Get(r.PathValue("id"))
	if !ok {
		s.fail(w, r, http.StatusNotFound, "err.device_not_found")
		return
	}
	writeJSON(w, http.StatusOK, s.freshStatus(r.Context(), d))
}

// freshStatus prüft sofort, außer die letzte Prüfung ist jünger als eine Sekunde.
func (s *Server) freshStatus(ctx context.Context, d store.Device) status.State {
	if st, ok := s.monitor.Current(d, time.Second); ok {
		return st
	}
	return s.monitor.Check(ctx, d)
}

type wakeResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Error   string `json:"error,omitempty"`
	Device  string `json:"device,omitempty"`
	ID      string `json:"id,omitempty"`
	Target  string `json:"target,omitempty"`
	Online  *bool  `json:"online,omitempty"`
	Already bool   `json:"already,omitempty"` // eben erst geweckt – kein neuer Weckruf, der Start läuft
}

// caller beschreibt den Aufrufer für den Verlauf: IP-Adresse (hinter einem Reverse-Proxy samt
// X-Forwarded-For) und, falls angemeldet, den Benutzer.
func (s *Server) caller(r *http.Request) (client, user string) {
	if r == nil {
		return "", ""
	}
	client = r.RemoteAddr
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		client = h
	}
	if xff, _, _ := strings.Cut(r.Header.Get("X-Forwarded-For"), ","); strings.TrimSpace(xff) != "" {
		client = clip(strings.TrimSpace(xff), 64) + " via " + client
	}
	if s.conf().AuthEnabled() && s.authorized(r) {
		if u, _, ok := r.BasicAuth(); ok {
			user = u
		} else {
			user = "Token"
		}
	}
	return client, user
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// Quellen eines Weckauftrags (Schlüssel im Verlauf, die Oberfläche übersetzt sie als ui.src.*).
const (
	srcUI        = "ui"         // Übersicht
	srcPage      = "page"       // Weckseite
	srcLink      = "link"       // Wecklink (GET, Automationen)
	srcAPI       = "api"        // REST-API mit Token
	srcSchedule  = "schedule"   // Zeitplan
	srcGroup     = "group"      // "Gruppe wecken" in der Übersicht bzw. API
	srcGroupPage = "group_page" // Gruppenseite
	srcGroupLink = "group_link" // Gruppen-Wecklink
)

// wake sendet das Magic Packet und trägt den Versuch in den Verlauf ein. r darf nil sein
// (Zeitplan); source sagt, woher der Auftrag kam, group nennt bei Gruppen deren Namen.
// Wurde d eben erst geweckt, geht kein weiterer Weckruf raus (siehe wakeDedupWindow).
func (s *Server) wake(r *http.Request, d store.Device, source, group string) (wakeResult, int) {
	if res, busy := s.alreadyWaking(r, d); busy {
		return res, http.StatusOK
	}
	s.supersedeRetry(d.ID)
	res, code, a := s.sendWake(r, d, source, group)
	if !res.Success {
		s.limiter.release(d.ID)
	}
	s.recordWakes(a)
	return res, code
}

// wakeAttempt ist ein Weckversuch, der noch eingetragen und gemeldet werden muss.
type wakeAttempt struct {
	ev store.Event
	d  store.Device
}

// recordWakes trägt Weckversuche mit einem Schreibvorgang in den Verlauf ein, meldet die Änderung
// einmal an die Oberfläche, benachrichtigt je Versuch und merkt die Wiederholung vor.
func (s *Server) recordWakes(as ...wakeAttempt) {
	if len(as) == 0 {
		return
	}
	evs := make([]store.Event, len(as))
	for i, a := range as {
		evs[i] = a.ev
		// Für den Verlauf in Worten: Ließ sich der Start prüfen, lief das Gerät schon?
		if ip, _ := s.monitor.Target(a.d); ip.IsValid() {
			evs[i].Checkable = true
			if st, ok := s.monitor.Get(a.d.ID); ok && st.State == "online" && !a.ev.Retry {
				evs[i].WasOnline = true
			}
		}
	}
	if err := s.store.RecordWake(evs...); err != nil {
		log.Printf("Saving history: %v", err)
	}
	s.devicesChanged() // "zuletzt geweckt" in allen offenen Fenstern
	for _, a := range as {
		ev := a.ev
		if ev.Success {
			s.limiter.mark(ev.ID) // auch Zeitplan und Wiederholung: ein Tippen gleich danach weckt nicht nochmal
		}
		// Benachrichtigungen in der eingestellten Sprache – sie gehen an niemand Bestimmtes.
		src := i18n.T(s.rt().lang, "ui.src."+ev.Source, ev.Group)
		result, kind, msg := "ok", notify.Wake, i18n.T(s.rt().lang, "notify.wake", ev.Device, src)
		if !ev.Success {
			result, kind, msg = "error", notify.WakeFailed, i18n.T(s.rt().lang, "notify.wake_failed", ev.Device, ev.Localized(s.rt().lang).Error)
		}
		s.wakesMu.Lock()
		s.wakes[[2]string{ev.ID, result}]++
		s.wakesMu.Unlock()
		if ev.Retry && ev.Success {
			continue // "geweckt" kam schon beim ersten Mal
		}
		s.rt().notify.Send(notify.Event{Event: kind, Device: ev.Device, ID: ev.ID, IP: a.d.IP, Source: ev.Source, Message: msg})
	}
	s.scheduleRetry(as)
}

// sendWake verschickt das Magic Packet, ohne den Versuch schon einzutragen.
func (s *Server) sendWake(r *http.Request, d store.Device, source, group string) (wakeResult, int, wakeAttempt) {
	l := s.lang(r)
	target := s.wakeTarget(d)
	client, user := s.caller(r)
	a := wakeAttempt{ev: store.Event{ID: d.ID, Device: d.Name, Source: source, Group: group, Client: client, User: user, Target: target}, d: d}
	res := wakeResult{Device: d.Name, ID: d.ID, Target: target}
	hw, err := wol.ParseMAC(d.MAC)
	var pass []byte
	if err == nil {
		pass, err = wol.ParseSecureOn(d.SecureOn)
	}
	if err == nil {
		err = wol.Send(hw, target, d.Interface, pass)
	}
	if err != nil {
		log.Printf("Waking %s failed: %v", d.Name, err)
		res.Message, res.Error = i18n.T(l, "msg.wake_failed", d.Name), i18n.Text(l, err)
		a.ev.SetError(l, err) // im Verlauf später in der Sprache des Betrachters
		return res, http.StatusInternalServerError, a
	}
	log.Printf("Magic packet to %s (%s) sent via %s (%s)", d.Name, d.MAC, target, source)
	a.ev.Success = true
	res.Success = true
	res.Message = i18n.T(l, "msg.wake_sent", d.Name)
	return res, http.StatusOK, a
}

func (s *Server) wakeDevice(w http.ResponseWriter, r *http.Request) {
	d, ok := s.store.Get(r.PathValue("id"))
	if !ok {
		s.fail(w, r, http.StatusNotFound, "err.device_not_found")
		return
	}
	// Die Oberfläche schickt X-WolWeb ohne Token; Automationen nutzen meist ein Token.
	source := srcAPI
	if r.Header.Get("X-WolWeb") != "" && !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		source = srcUI
	}
	res, code := s.wake(r, d, source, "")
	writeJSON(w, code, res)
}

// --- Feste Wecklinks ---

func wantsHTML(r *http.Request) bool {
	if f := r.URL.Query().Get("format"); f != "" {
		return f == "html"
	}
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

// wakeLink: Browser bekommen die Weckseite (sie weckt per POST und verfolgt den Start),
// Automationen (curl, Home Assistant …) werden sofort geweckt und bekommen JSON.
// ?wait=<Sekunden> wartet zusätzlich, bis das Gerät erreichbar ist.
func (s *Server) wakeLink(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	d, ok := s.findForLink(r)
	// Browser schicken Sec-Fetch-Site mit. Kommt der Aufruf von einer fremden Seite (z. B. als
	// eingebettetes Bild), wird nicht direkt geweckt, sondern nur die Weckseite ausgeliefert –
	// sie weckt dann selbst per POST. curl, Home Assistant & Co. senden den Kopf nicht.
	// HEAD (Linkprüfer, Vorschauen in Messengern, curl -I) darf nie wecken – Go beantwortet
	// HEAD über die GET-Route, daher hier ausdrücklich nur die Seite.
	if sfs := r.Header.Get("Sec-Fetch-Site"); r.Method == http.MethodHead || wantsHTML(r) || sfs == "cross-site" || sfs == "same-site" {
		if !ok {
			s.render(w, r, http.StatusNotFound, "wake.html", pageData{Missing: name})
			return
		}
		// Alter Name nach dem Umbenennen: zur neuen Adresse weiterleiten (Schlüssel & Co. bleiben dran),
		// damit das Lesezeichen sichtbar die neue Adresse zeigt. Ohne Browser gilt der alte Name direkt.
		if store.IsAlias(d, name) {
			to := s.conf().VDir + "wake/" + pathEscape(d.Name)
			if r.URL.RawQuery != "" {
				to += "?" + r.URL.RawQuery
			}
			http.Redirect(w, r, to, http.StatusTemporaryRedirect) // nicht dauerhaft: der Name kann neu vergeben werden
			return
		}
		v := s.view(d)
		pd := pageData{Device: &v, Timeout: int(s.rt().wakeTimeout.Seconds()), Retry: int(s.rt().wakeRetry.Seconds())}
		if s.needsKey(r) {
			pd.Key = d.WakeKey
		}
		s.render(w, r, http.StatusOK, "wake.html", pd)
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, wakeResult{Message: i18n.T(s.lang(r), "err.device_not_found_name", name), Error: "not found"})
		return
	}
	res, code := s.wake(r, d, srcLink, "")
	if wait, _ := strconv.Atoi(r.URL.Query().Get("wait")); res.Success && wait > 0 {
		if ip, _ := s.monitor.Target(d); !ip.IsValid() {
			// Ohne IP lässt sich nichts prüfen – "online" bleibt weg statt eines falschen false.
			res.Message = i18n.T(s.lang(r), "msg.wake_no_ip", d.Name)
		} else {
			online := s.waitOnline(r.Context(), d, time.Duration(min(wait, 600))*time.Second)
			res.Online = &online
			if online {
				res.Message = i18n.T(s.lang(r), "msg.wake_online", d.Name)
			} else {
				res.Message = i18n.T(s.lang(r), "msg.wake_no_answer", d.Name)
			}
		}
	}
	writeJSON(w, code, res)
}

func (s *Server) waitOnline(ctx context.Context, d store.Device, max time.Duration) bool {
	ctx, cancel := context.WithTimeout(ctx, max)
	defer cancel()
	for {
		if s.monitor.Check(ctx, d).State == "online" {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(2 * time.Second):
		}
	}
}

func (s *Server) wakeLinkPost(w http.ResponseWriter, r *http.Request) {
	d, ok := s.findForLink(r)
	if !ok {
		s.fail(w, r, http.StatusNotFound, "err.device_not_found")
		return
	}
	res, code := s.wake(r, d, srcPage, "")
	writeJSON(w, code, res)
}

func (s *Server) wakeLinkStatus(w http.ResponseWriter, r *http.Request) {
	d, ok := s.findForLink(r)
	if !ok {
		s.fail(w, r, http.StatusNotFound, "err.device_not_found")
		return
	}
	writeJSON(w, http.StatusOK, s.freshStatus(r.Context(), d))
}

// --- Verlauf, Export, Import ---

func (s *Server) history(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > store.HistoryMax {
		limit = 200
	}
	events := s.store.History(limit)
	for i := range events {
		events[i] = events[i].Localized(s.lang(r))
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

func (s *Server) export(w http.ResponseWriter, r *http.Request) {
	b, err := s.store.Export()
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="wolweb-geraete-`+time.Now().Format("2006-01-02")+`.json"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(b)
}

func (s *Server) importDevices(w http.ResponseWriter, r *http.Request) {
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "err.file_too_large")
		return
	}
	res, err := s.store.Import(b, s.lang(r))
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	log.Printf("Import: %d device(s) added, %d already present, %d invalid", res.Added, res.Skipped, len(res.Errors))
	if res.Added > 0 {
		s.devicesChanged()
		go s.monitor.CheckAll(context.Background())
	}
	writeJSON(w, http.StatusOK, res)
}

// --- Suche ---

func (s *Server) info(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"version":           s.version,
		"read_only":         s.conf().ReadOnly,
		"networks":          s.scanner.Networks(),
		"networks_auto":     s.scanner.Auto(),    // ohne Einstellung erkannt (genau ein Heimnetz)
		"network_choices":   s.networkChoices(),  // ohne Einstellung und ohne eindeutiges Heimnetz: zur Wahl
		"scan_off":          s.scanner.ScanOff(), // Netze eingetragen, aber keins wird durchsucht
		"segments":          s.segments(),
		"settings":          s.settings != nil,
		"local_networks":    discovery.LocalNets(),
		"icmp":              s.icmp,
		"default_broadcast": s.defaultBroadcast(),
		"auth":              s.conf().AuthEnabled(),
		"public_wake":       s.conf().Auth.PublicWake,
		"wake_timeout":      int(s.rt().wakeTimeout.Seconds()),
		"wake_retry":        int(s.rt().wakeRetry.Seconds()),
		"ports":             s.conf().Status.Ports,
		"wake_keys":         s.conf().Auth.WakeKeys && s.conf().AuthEnabled(),
		"timezone":          localZone(), // Zeitpläne laufen in der Ortszeit des Servers
	})
}

type foundView struct {
	store.FoundEntry
	Broadcast string `json:"broadcast"`
}

func (s *Server) discovery(w http.ResponseWriter, r *http.Request) {
	list := s.store.FoundList()
	out := make([]foundView, 0, len(list))
	for _, f := range list {
		out = append(out, foundView{FoundEntry: f, Broadcast: discovery.BroadcastFor(f.IP)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": s.scanner.Job().Localized(s.lang(r)), "found": out})
}

func (s *Server) startScan(w http.ResponseWriter, r *http.Request) {
	switch err := s.scanner.Start(false); {
	case errors.Is(err, discovery.ErrBusy):
		s.fail(w, r, http.StatusConflict, "scan.busy")
	case errors.Is(err, discovery.ErrNoNetworks):
		s.fail(w, r, http.StatusBadRequest, "scan.no_networks")
	case err != nil:
		writeError(w, http.StatusBadRequest, i18n.Text(s.lang(r), err))
	default:
		writeJSON(w, http.StatusAccepted, s.scanner.Job().Localized(s.lang(r)))
	}
}

func (s *Server) cancelScan(w http.ResponseWriter, r *http.Request) {
	s.scanner.Cancel()
	writeJSON(w, http.StatusOK, s.scanner.Job().Localized(s.lang(r)))
}

func macParam(r *http.Request) (string, error) {
	hw, err := wol.ParseMAC(r.PathValue("mac"))
	if err != nil {
		return "", store.Invalid("val.mac_param", r.PathValue("mac"))
	}
	return wol.FormatMAC(hw), nil
}

func (s *Server) ignoreFound(w http.ResponseWriter, r *http.Request) {
	mac, err := macParam(r)
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	var in struct {
		Ignored bool `json:"ignored"`
	}
	if err := readJSON(r, &in); err != nil {
		s.storeError(w, r, err)
		return
	}
	if err := s.store.SetIgnored(mac, in.Ignored); err != nil {
		s.storeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) forgetFound(w http.ResponseWriter, r *http.Request) {
	mac, err := macParam(r)
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	if err := s.store.ForgetFound(mac); err != nil {
		s.storeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) legacyData(w http.ResponseWriter, r *http.Request) {
	devices, _ := s.store.Devices()
	type legacy struct {
		Name      string `json:"name"`
		MAC       string `json:"mac"`
		IP        string `json:"ip"`
		Interface string `json:"interface"`
	}
	out := struct {
		Devices []legacy `json:"devices"`
	}{Devices: []legacy{}}
	for _, d := range devices {
		out.Devices = append(out.Devices, legacy{d.Name, d.MAC, s.wakeTarget(d), d.Interface})
	}
	writeJSON(w, http.StatusOK, out)
}
