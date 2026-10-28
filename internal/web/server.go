// Package web stellt Oberfläche, Weckseite und REST-API bereit.
package web

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"wolweb/internal/config"
	"wolweb/internal/discovery"
	"wolweb/internal/status"
	"wolweb/internal/store"
	"wolweb/internal/wol"
)

//go:embed static templates
var assets embed.FS

type Server struct {
	cfg         config.Config
	store       *store.Store
	monitor     *status.Monitor
	scanner     *discovery.Scanner
	icmp        bool
	version     string
	wakeTimeout time.Duration
	tmpl        *template.Template
	limiter     wakeLimiter
}

func New(cfg config.Config, st *store.Store, mon *status.Monitor, sc *discovery.Scanner, icmp bool, version string) *Server {
	wt, _ := config.ParseDuration(cfg.Status.WakeTimeout)
	if wt <= 0 {
		wt = 3 * time.Minute
	}
	return &Server{
		cfg: cfg, store: st, monitor: mon, scanner: sc, icmp: icmp, version: version, wakeTimeout: wt,
		tmpl: template.Must(template.ParseFS(assets, "templates/*.html")),
	}
}

func (s *Server) Handler() http.Handler {
	base := s.cfg.VDir // "/" oder "/wolweb/"
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
	mux.HandleFunc("GET "+api+"devices", s.listDevices)
	mux.HandleFunc("POST "+api+"devices", s.writable(s.createDevice))
	mux.HandleFunc("PUT "+api+"devices/{id}", s.writable(s.updateDevice))
	mux.HandleFunc("DELETE "+api+"devices/{id}", s.writable(s.deleteDevice))
	mux.HandleFunc("POST "+api+"devices/{id}/wake", s.wakeDevice)
	mux.HandleFunc("GET "+api+"devices/{id}/status", s.deviceStatus)
	mux.HandleFunc("POST "+api+"devices/{id}/wake-key", s.writable(s.rotateWakeKey))
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

	// Kompatibel zum Original: Geräteliste im alten Format
	mux.HandleFunc("GET "+base+"data/get", s.legacyData)

	return s.recoverer(s.hostGuard(s.auth(s.csrf(securityHeaders(mux)))))
}

// --- Middleware ---

func (s *Server) auth(next http.Handler) http.Handler {
	if !s.cfg.AuthEnabled() {
		return next
	}
	base := s.cfg.VDir
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if s.authorized(r) {
			next.ServeHTTP(w, r)
			return
		}
		if p == base+"health" || strings.HasPrefix(p, base+"static/") ||
			(s.cfg.Auth.PublicWake && (strings.HasPrefix(p, base+"wake/") || strings.HasPrefix(p, base+"wake-group/"))) {
			next.ServeHTTP(w, markPublic(r)) // ohne Anmeldung: Schlüssel und Begrenzung greifen
			return
		}
		if s.cfg.Auth.Username != "" {
			w.Header().Set("WWW-Authenticate", `Basic realm="wolweb", charset="UTF-8"`)
		}
		writeError(w, http.StatusUnauthorized, "Anmeldung erforderlich.")
	})
}

func (s *Server) authorized(r *http.Request) bool {
	eq := func(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }
	if t := s.cfg.Auth.APIToken; t != "" {
		if h, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok && eq(strings.TrimSpace(h), t) {
			return true
		}
	}
	if s.cfg.Auth.Username != "" {
		if u, p, ok := r.BasicAuth(); ok && eq(u, s.cfg.Auth.Username) && eq(p, s.cfg.Auth.Password) {
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
				writeError(w, http.StatusForbidden, `Anfrage abgelehnt: Kopfzeile "X-WolWeb: 1" fehlt.`)
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
				log.Printf("Fehler bei %s %s: %v", r.Method, r.URL.Path, v)
				writeError(w, http.StatusInternalServerError, "Interner Fehler.")
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
		if s.cfg.ReadOnly {
			writeError(w, http.StatusForbidden, "Nur-Lese-Modus: Änderungen sind abgeschaltet.")
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

func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return store.ValidationError{Msg: "Ungültige Daten: " + err.Error()}
	}
	if dec.More() {
		return store.ValidationError{Msg: "Ungültige Daten: mehr als ein JSON-Wert"}
	}
	return nil
}

func storeError(w http.ResponseWriter, err error) {
	var ve store.ValidationError
	switch {
	case errors.As(err, &ve):
		writeError(w, http.StatusBadRequest, ve.Msg)
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "Gerät nicht gefunden.")
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, err.Error())
	default:
		log.Printf("Speichern fehlgeschlagen: %v", err)
		writeError(w, http.StatusInternalServerError, "Speichern fehlgeschlagen: "+err.Error())
	}
}

// --- Seiten ---

type pageData struct {
	Base    string
	Version string
	Device  *deviceView
	Timeout int
	Missing string
	Key     string // Weckschlüssel, den die Weckseite bei ihren Aufrufen mitschickt
	Group   *groupView
}

func (s *Server) render(w http.ResponseWriter, code int, name string, d pageData) {
	d.Base, d.Version = s.cfg.VDir, s.version
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	if err := s.tmpl.ExecuteTemplate(w, name, d); err != nil {
		log.Printf("Vorlage %s: %v", name, err)
	}
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	s.render(w, http.StatusOK, "index.html", pageData{})
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
	}
	return s.defaultBroadcast()
}

// defaultBroadcast: eingestellte Adresse (bcastip), sonst die des einzigen Suchsegments, sonst
// 255.255.255.255 – das geht über die Schnittstelle der Standardroute hinaus. Früher war fest
// 192.168.1.255 voreingestellt; in jedem anderen Netz verschwand das Paket dann still.
func (s *Server) defaultBroadcast() string {
	if b, err := config.NormalizeBroadcast(s.cfg.BcastIP); err == nil && b != "" {
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
		storeError(w, err)
		return
	}
	d, err := s.store.Create(in)
	if err != nil {
		storeError(w, err)
		return
	}
	log.Printf("Gerät angelegt: %s (%s)", d.Name, d.MAC)
	go s.monitor.Check(context.Background(), d)
	writeJSON(w, http.StatusCreated, s.view(d))
}

func (s *Server) updateDevice(w http.ResponseWriter, r *http.Request) {
	var in struct {
		store.Input
		UpdatedAt *time.Time `json:"updated_at"`
	}
	if err := readJSON(r, &in); err != nil {
		storeError(w, err)
		return
	}
	d, err := s.store.Update(r.PathValue("id"), in.Input, in.UpdatedAt)
	if err != nil {
		storeError(w, err)
		return
	}
	log.Printf("Gerät geändert: %s (%s)", d.Name, d.MAC)
	go s.monitor.Check(context.Background(), d)
	writeJSON(w, http.StatusOK, s.view(d))
}

func (s *Server) deleteDevice(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Delete(r.PathValue("id")); err != nil {
		storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) rotateWakeKey(w http.ResponseWriter, r *http.Request) {
	d, err := s.store.RotateWakeKey(r.PathValue("id"))
	if err != nil {
		storeError(w, err)
		return
	}
	log.Printf("Neuer Weckschlüssel für %s", d.Name)
	writeJSON(w, http.StatusOK, s.view(d))
}

func (s *Server) deviceStatus(w http.ResponseWriter, r *http.Request) {
	d, ok := s.store.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "Gerät nicht gefunden.")
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
		client = clip(strings.TrimSpace(xff), 64) + " über " + client
	}
	if s.cfg.AuthEnabled() && s.authorized(r) {
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

// wake sendet das Magic Packet und trägt den Versuch in den Verlauf ein. r darf nil sein
// (Zeitplan); source sagt, woher der Auftrag kam.
func (s *Server) wake(r *http.Request, d store.Device, source string) (wakeResult, int) {
	target := s.wakeTarget(d)
	client, user := s.caller(r)
	ev := store.Event{ID: d.ID, Device: d.Name, Source: source, Client: client, User: user, Target: target}
	defer func() {
		if err := s.store.RecordWake(ev); err != nil {
			log.Printf("Verlauf speichern: %v", err)
		}
	}()
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
		log.Printf("Wecken von %s fehlgeschlagen: %v", d.Name, err)
		res.Message, res.Error = fmt.Sprintf("Magic Packet für %s konnte nicht gesendet werden.", d.Name), err.Error()
		ev.Error = err.Error()
		return res, http.StatusInternalServerError
	}
	log.Printf("Magic Packet an %s (%s) über %s gesendet (%s)", d.Name, d.MAC, target, source)
	ev.Success = true
	res.Success = true
	res.Message = fmt.Sprintf("Magic Packet an %s gesendet.", d.Name)
	return res, http.StatusOK
}

func (s *Server) wakeDevice(w http.ResponseWriter, r *http.Request) {
	d, ok := s.store.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "Gerät nicht gefunden.")
		return
	}
	// Die Oberfläche schickt X-WolWeb ohne Token; Automationen nutzen meist ein Token.
	source := "API"
	if r.Header.Get("X-WolWeb") != "" && !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		source = "Übersicht"
	}
	res, code := s.wake(r, d, source)
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
			s.render(w, http.StatusNotFound, "wake.html", pageData{Missing: name})
			return
		}
		v := s.view(d)
		pd := pageData{Device: &v, Timeout: int(s.wakeTimeout.Seconds())}
		if s.needsKey(r) {
			pd.Key = d.WakeKey
		}
		s.render(w, http.StatusOK, "wake.html", pd)
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, wakeResult{Message: fmt.Sprintf("Gerät %q nicht gefunden.", name), Error: "not found"})
		return
	}
	if s.limitPublic(w, r, d) {
		return
	}
	res, code := s.wake(r, d, "Wecklink")
	if wait, _ := strconv.Atoi(r.URL.Query().Get("wait")); res.Success && wait > 0 {
		if ip, _ := s.monitor.Target(d); !ip.IsValid() {
			// Ohne IP lässt sich nichts prüfen – "online" bleibt weg statt eines falschen false.
			res.Message = fmt.Sprintf("Magic Packet an %s gesendet. Ohne bekannte IP-Adresse lässt sich der Start nicht prüfen.", d.Name)
		} else {
			online := s.waitOnline(r.Context(), d, time.Duration(min(wait, 600))*time.Second)
			res.Online = &online
			if online {
				res.Message = fmt.Sprintf("%s ist online.", d.Name)
			} else {
				res.Message = fmt.Sprintf("Magic Packet an %s gesendet, das Gerät hat sich aber noch nicht gemeldet.", d.Name)
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
		writeError(w, http.StatusNotFound, "Gerät nicht gefunden.")
		return
	}
	if s.limitPublic(w, r, d) {
		return
	}
	res, code := s.wake(r, d, "Weckseite")
	writeJSON(w, code, res)
}

func (s *Server) wakeLinkStatus(w http.ResponseWriter, r *http.Request) {
	d, ok := s.findForLink(r)
	if !ok {
		writeError(w, http.StatusNotFound, "Gerät nicht gefunden.")
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
	writeJSON(w, http.StatusOK, map[string]any{"events": s.store.History(limit)})
}

func (s *Server) export(w http.ResponseWriter, r *http.Request) {
	b, err := s.store.Export()
	if err != nil {
		storeError(w, err)
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
		writeError(w, http.StatusBadRequest, "Datei zu groß oder unvollständig.")
		return
	}
	res, err := s.store.Import(b)
	if err != nil {
		storeError(w, err)
		return
	}
	log.Printf("Import: %d Gerät(e) übernommen, %d schon vorhanden, %d fehlerhaft", res.Added, res.Skipped, len(res.Errors))
	if res.Added > 0 {
		go s.monitor.CheckAll(context.Background())
	}
	writeJSON(w, http.StatusOK, res)
}

// --- Suche ---

func (s *Server) info(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"version":           s.version,
		"read_only":         s.cfg.ReadOnly,
		"networks":          s.scanner.Networks(),
		"local_networks":    discovery.LocalNets(),
		"icmp":              s.icmp,
		"default_broadcast": s.defaultBroadcast(),
		"auth":              s.cfg.AuthEnabled(),
		"public_wake":       s.cfg.Auth.PublicWake,
		"wake_timeout":      int(s.wakeTimeout.Seconds()),
		"ports":             s.cfg.Status.Ports,
		"wake_keys":         s.cfg.Auth.WakeKeys && s.cfg.AuthEnabled(),
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
	writeJSON(w, http.StatusOK, map[string]any{"job": s.scanner.Job(), "found": out})
}

func (s *Server) startScan(w http.ResponseWriter, r *http.Request) {
	switch err := s.scanner.Start(false); {
	case errors.Is(err, discovery.ErrBusy):
		writeError(w, http.StatusConflict, err.Error())
	case err != nil:
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeJSON(w, http.StatusAccepted, s.scanner.Job())
	}
}

func (s *Server) cancelScan(w http.ResponseWriter, r *http.Request) {
	s.scanner.Cancel()
	writeJSON(w, http.StatusOK, s.scanner.Job())
}

func macParam(r *http.Request) (string, error) {
	hw, err := wol.ParseMAC(r.PathValue("mac"))
	if err != nil {
		return "", store.ValidationError{Msg: err.Error()}
	}
	return wol.FormatMAC(hw), nil
}

func (s *Server) ignoreFound(w http.ResponseWriter, r *http.Request) {
	mac, err := macParam(r)
	if err != nil {
		storeError(w, err)
		return
	}
	var in struct {
		Ignored bool `json:"ignored"`
	}
	if err := readJSON(r, &in); err != nil {
		storeError(w, err)
		return
	}
	if err := s.store.SetIgnored(mac, in.Ignored); err != nil {
		storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) forgetFound(w http.ResponseWriter, r *http.Request) {
	mac, err := macParam(r)
	if err != nil {
		storeError(w, err)
		return
	}
	if err := s.store.ForgetFound(mac); err != nil {
		storeError(w, err)
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
