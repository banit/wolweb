package web

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/netip"

	"wolweb/internal/config"
	"wolweb/internal/discovery"
	"wolweb/internal/i18n"
	"wolweb/internal/notify"
)

// --- Einstellungen ---

// SetSettings schaltet die Einstellungsseite frei. Ohne Manager gelten nur config.json und
// Umgebung (so in den meisten Tests).
func (s *Server) SetSettings(m *config.Manager) { s.settings = m }

// Apply übernimmt geänderte Einstellungen im laufenden Betrieb: Sprache, Wartezeiten,
// Benachrichtigungen, Online-Prüfung und Suche. Adresse, Port, Pfad und Zugangsdaten kann die
// Oberfläche nicht ändern; sie bleiben, wie sie beim Start waren.
func (s *Server) Apply(cfg config.Config) {
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	old := s.rt()
	s.cur.Store(s.newLive(cfg))
	old.notify.Close()
	scan, check, _, _ := cfg.Durations()
	s.monitor.SetPorts(cfg.Status.Ports)
	s.monitor.SetInterval(check)
	if err := s.scanner.SetNetworks(cfg.Discovery.ScanCIDRs()); err != nil {
		log.Printf("Einstellungen: Suchsegmente: %v", err) // geprüft – sollte nie vorkommen
	}
	s.scanner.SetInterval(scan)
	log.Printf("Einstellungen übernommen")
	s.hub.publish("settings", struct{}{}) // offene Fenster laden Info und Geräte neu
	go s.monitor.CheckAll(context.Background())
}

type networkView struct {
	CIDR      string              `json:"cidr"`
	Name      string              `json:"name"`
	VLAN      int                 `json:"vlan"`
	Scan      bool                `json:"scan"`
	Label     string              `json:"label"`
	Broadcast string              `json:"broadcast"`
	Local     *discovery.LocalNet `json:"local,omitempty"` // Schnittstelle dieses Rechners im Netz
	Devices   int                 `json:"devices"`
	Found     int                 `json:"found"`
}

type interfaceView struct {
	discovery.LocalNet
	Configured bool `json:"configured"` // das Netz steht schon in der Liste
}

type settingsView struct {
	Settings     config.Editable   `json:"settings"`
	Locked       map[string]string `json:"locked"` // Schlüssel -> Umgebungsvariable
	Stored       []string          `json:"stored"` // in der Oberfläche geändert
	File         string            `json:"file"`
	FileFound    bool              `json:"file_found"`
	SettingsFile string            `json:"settings_file"`
	ReadOnly     bool              `json:"read_only"`
	Auth         bool              `json:"auth"`
	Networks     []networkView     `json:"networks"`
	Interfaces   []interfaceView   `json:"interfaces"`
	Events       []string          `json:"events"` // alle Ereignisarten
}

// networks beschreibt die eingestellten Netze: Schnittstelle des Rechners darin (ohne die kann
// wolweb dort weder suchen noch per Broadcast wecken), Geräte und Suchtreffer.
func (s *Server) networks(cfg *config.Config) ([]networkView, []interfaceView) {
	devices, _ := s.store.Devices()
	found := s.store.FoundList()
	nets := []networkView{}
	var prefixes []netip.Prefix
	for _, n := range cfg.Discovery.Networks {
		p, err := netip.ParsePrefix(n.CIDR)
		if err != nil {
			continue
		}
		prefixes = append(prefixes, p)
		v := networkView{CIDR: n.CIDR, Name: n.Name, VLAN: n.VLAN, Scan: n.Scan, Label: n.Label(), Broadcast: discovery.BroadcastOf(p)}
		if ln, ok := discovery.LocalNetFor(p); ok {
			v.Local = &ln
			v.Broadcast = ln.Broadcast // so weckt wolweb dort (Netz der Schnittstelle)
		}
		for _, d := range devices {
			if ip, _ := s.monitor.Target(d); ip.IsValid() && p.Contains(ip) {
				v.Devices++
			}
		}
		for _, f := range found {
			if a, err := netip.ParseAddr(f.IP); err == nil && p.Contains(a) {
				v.Found++
			}
		}
		nets = append(nets, v)
	}
	ifs := []interfaceView{}
	for _, ln := range discovery.LocalNets() {
		iv := interfaceView{LocalNet: ln}
		for _, p := range prefixes {
			if p.Overlaps(ln.Prefix) {
				iv.Configured = true
			}
		}
		ifs = append(ifs, iv)
	}
	return nets, ifs
}

func (s *Server) settingsState() settingsView {
	cfg := s.settings.Config()
	nets, ifs := s.networks(&cfg)
	return settingsView{
		Settings: cfg.Editable(), Locked: s.settings.Locked(), Stored: s.settings.Stored(),
		File: s.settings.Path(), FileFound: s.settings.FileFound(), SettingsFile: s.settings.SettingsPath(),
		ReadOnly: cfg.ReadOnly, Auth: cfg.AuthEnabled(), Networks: nets, Interfaces: ifs, Events: notify.Kinds,
	}
}

// withSettings antwortet 404, wenn die Einstellungsseite nicht freigeschaltet ist.
func (s *Server) withSettings(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.settings == nil {
			s.fail(w, r, http.StatusNotFound, "err.settings_unavailable")
			return
		}
		h(w, r)
	}
}

func (s *Server) settingsPage(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "settings.html", pageData{})
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.settingsState())
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var e config.Editable
	if err := readJSON(r, &e); err != nil {
		s.storeError(w, r, err)
		return
	}
	cfg, err := s.settings.Save(e)
	if err != nil {
		s.settingsError(w, r, err)
		return
	}
	s.Apply(cfg)
	writeJSON(w, http.StatusOK, s.settingsState())
}

func (s *Server) resetSettings(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.settings.Reset()
	if err != nil {
		s.settingsError(w, r, err)
		return
	}
	s.Apply(cfg)
	writeJSON(w, http.StatusOK, s.settingsState())
}

// settingsError: Eingabefehler nennen das Feld (wie bei Geräten), alles andere ist ein Speicherfehler.
func (s *Server) settingsError(w http.ResponseWriter, r *http.Request, err error) {
	var fe config.FieldError
	if errors.As(err, &fe) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": i18n.Text(s.lang(r), fe.Err), "field": fe.Field})
		return
	}
	log.Printf("Einstellungen speichern: %v", err)
	s.fail(w, r, http.StatusInternalServerError, "err.save_failed", err)
}

// testNotify schickt eine Probe an die eingegebenen (noch nicht gespeicherten) Adressen.
func (s *Server) testNotify(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Webhook string `json:"webhook"`
		Ntfy    string `json:"ntfy"`
	}
	if err := readJSON(r, &in); err != nil {
		s.storeError(w, r, err)
		return
	}
	for _, f := range []struct{ key, val string }{{"notify.webhook", in.Webhook}, {"notify.ntfy", in.Ntfy}} {
		if err := config.CheckURL(f.key, f.val); err != nil {
			s.settingsError(w, r, err)
			return
		}
	}
	l := s.lang(r)
	n := notify.New(in.Webhook, in.Ntfy, nil, s.version, l)
	if n == nil {
		s.fail(w, r, http.StatusBadRequest, "err.notify_empty")
		return
	}
	defer n.Close()
	ev := notify.Event{Event: notify.Test, Device: "wolweb", Message: i18n.T(l, "notify.test")}
	if err := n.Test(ev); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"success": false, "message": i18n.T(l, "msg.notify_failed", err)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": i18n.T(l, "msg.notify_sent")})
}

// segments sind die eingestellten Netze für die Oberfläche (Broadcast-Vorschläge im Gerätedialog).
func (s *Server) segments() []map[string]any {
	out := []map[string]any{}
	for _, n := range s.conf().Discovery.Networks {
		p, err := netip.ParsePrefix(n.CIDR)
		if err != nil {
			continue
		}
		_, local := discovery.LocalNetFor(p)
		out = append(out, map[string]any{"cidr": n.CIDR, "label": n.Label(), "broadcast": discovery.BroadcastOf(p), "local": local})
	}
	return out
}
