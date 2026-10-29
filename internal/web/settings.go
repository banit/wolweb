package web

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/netip"
	"slices"

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
	s.applyRecording(cfg)
	scan, check, _, _ := cfg.Durations()
	s.monitor.SetPorts(cfg.Status.Ports)
	s.monitor.SetInterval(check)
	if err := s.scanner.Configure(cfg.Discovery.ScanCIDRs(), len(cfg.Discovery.Networks) > 0); err != nil {
		log.Printf("Settings: scan networks: %v", err) // geprüft – sollte nie vorkommen
	}
	s.scanner.SetInterval(scan)
	log.Print("Settings applied")
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
	Password     bool              `json:"password"` // Passwort für Änderungen gesetzt
	Networks     []networkView     `json:"networks"`
	Interfaces   []interfaceView   `json:"interfaces"`
	Events       []string          `json:"events"`  // alle Ereignisarten
	Accents      []config.Accent   `json:"accents"` // wählbare Akzentfarben
	// RejectedHosts sind Namen, unter denen wolweb zuletzt aufgerufen und abgewiesen wurde (Schutz
	// vor DNS-Rebinding) – meist der eigene Reverse-Proxy, der nur noch erlaubt werden muss.
	RejectedHosts []string `json:"rejected_hosts"`
	// AutoNetwork: Kein Netz eingetragen, wolweb nutzt das erkannte Heimnetz (sonst leer).
	AutoNetwork string `json:"auto_network,omitempty"`
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
		ReadOnly: cfg.ReadOnly, Auth: cfg.AuthEnabled(), Password: s.store.HasPassword(), Networks: nets, Interfaces: ifs, Events: notify.Kinds, Accents: config.Accents,
		RejectedHosts: s.rejected.list(selfHost(), cfg.AllowedHosts),
		AutoNetwork:   autoNetwork(s),
	}
}

func autoNetwork(s *Server) string {
	if nets := s.scanner.Networks(); s.scanner.Auto() && len(nets) == 1 {
		return nets[0]
	}
	return ""
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
	if err := s.commit(func() (config.Config, error) { return s.settings.Save(e) }); err != nil {
		s.settingsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.settingsState())
}

func (s *Server) resetSettings(w http.ResponseWriter, r *http.Request) {
	if err := s.commit(s.settings.Reset); err != nil {
		s.settingsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.settingsState())
}

// commit speichert (oder setzt zurück) und übernimmt in einem Schritt. Ohne gemeinsame Sperre könnte
// eine ältere Anfrage ihren Stand nach einer neueren übernehmen – settings.json und der laufende
// Betrieb (etwa erlaubte Hostnamen) liefen dann auseinander.
func (s *Server) commit(change func() (config.Config, error)) error {
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	cfg, err := change()
	if err != nil {
		return err
	}
	s.Apply(cfg)
	return nil
}

// settingsError: Eingabefehler nennen das Feld (wie bei Geräten), alles andere ist ein Speicherfehler.
func (s *Server) settingsError(w http.ResponseWriter, r *http.Request, err error) {
	var fe config.FieldError
	if errors.As(err, &fe) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": i18n.Text(s.lang(r), fe.Err), "field": fe.Field})
		return
	}
	log.Printf("Saving settings: %v", err)
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
		// Nur „nicht zugestellt“: Fehlerart und HTTP-Status verrieten sonst, welche internen Adressen
		// und Ports antworten (wolweb hängt oft in mehreren Netzen). Die Einzelheiten stehen im Log.
		log.Printf("Test message: %v", err)
		writeJSON(w, http.StatusBadGateway, map[string]any{"success": false, "message": i18n.T(l, "msg.notify_failed")})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": i18n.T(l, "msg.notify_sent")})
}

// networkChoices: Ist kein Netz eingestellt und gibt es nicht genau ein Heimnetz, stehen die
// Heimnetze zur Wahl ("Wähle das Netzwerk, in dem dein Rechner steht").
func (s *Server) networkChoices() []discovery.LocalNet {
	if s.scanner.Configured() || s.scanner.Auto() {
		return []discovery.LocalNet{}
	}
	return append([]discovery.LocalNet{}, discovery.HomeNets(s.conf().Discovery.MaxHosts)...)
}

// chooseNetwork speichert das in der Suche gewählte Heimnetz (wie in den Einstellungen).
func (s *Server) chooseNetwork(w http.ResponseWriter, r *http.Request) {
	var in struct {
		CIDR  string   `json:"cidr"`
		CIDRs []string `json:"cidrs"` // „Alle erkannten Netze durchsuchen“
	}
	if err := readJSON(r, &in); err != nil {
		s.storeError(w, r, err)
		return
	}
	if in.CIDR == "" && !slices.ContainsFunc(in.CIDRs, func(c string) bool { return c != "" }) {
		s.fail(w, r, http.StatusBadRequest, "set.err.cidr_missing") // leer hieße: alle Netze löschen
		return
	}
	if _, env := s.settings.Locked()["discovery.networks"]; env {
		s.fail(w, r, http.StatusConflict, "err.networks_locked")
		return
	}
	err := s.commit(func() (config.Config, error) {
		e := s.settings.Config().Editable() // unter der Sperre lesen, sonst ginge eine gleichzeitige Änderung verloren
		e.Discovery.Networks = nil
		for _, c := range append(in.CIDRs, in.CIDR) {
			if c != "" {
				e.Discovery.Networks = append(e.Discovery.Networks, config.Network{CIDR: c, Scan: true})
			}
		}
		return s.settings.Save(e)
	})
	if err != nil {
		s.settingsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"networks": s.scanner.Networks()})
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
