// Package status prüft regelmäßig, welche Geräte online sind.
package status

import (
	"context"
	"log"
	"net/netip"
	"strings"
	"sync"
	"time"

	"wolweb/internal/discovery"
	"wolweb/internal/i18n"
	"wolweb/internal/probe"
	"wolweb/internal/store"
)

type State struct {
	State     string    `json:"state"` // "online", "offline", "unknown" (keine IP bekannt)
	IP        string    `json:"ip,omitempty"`
	IPSource  string    `json:"ip_source,omitempty"` // "device" oder "discovery"
	Method    string    `json:"method,omitempty"`
	RTTMillis int64     `json:"rtt_ms,omitempty"`
	CheckedAt time.Time `json:"checked_at,omitzero"`
	LastSeen  time.Time `json:"last_seen,omitzero"`
	Note      string    `json:"note,omitempty"`         // Hinweis auf Deutsch (API wie bisher)
	Conflict  string    `json:"conflict_mac,omitempty"` // MAC des Geräts, das stattdessen unter der IP antwortet

	started time.Time // Beginn der Prüfung – ein älteres Ergebnis überschreibt kein neueres
}

type Monitor struct {
	store    *store.Store
	prober   *probe.Prober
	interval time.Duration // unter mu; 0 = keine regelmäßige Prüfung
	reset    chan struct{} // SetInterval weckt Run
	mu       sync.RWMutex
	states   map[string]State

	// OnChange wird nach jeder Änderung (Zustand, IP, Methode, Hinweis) aufgerufen – unter der
	// Sperre des Monitors, also kurz halten und nicht zurückrufen. hadOld ist false beim ersten
	// Ergebnis eines Geräts. Vor Run setzen.
	OnChange func(d store.Device, old State, hadOld bool, st State)
}

func New(s *store.Store, p *probe.Prober, interval time.Duration) *Monitor {
	return &Monitor{store: s, prober: p, interval: interval, reset: make(chan struct{}, 1), states: map[string]State{}}
}

// Run prüft alle Geräte im eingestellten Abstand, bis ctx endet. Ein neuer Abstand (SetInterval)
// gilt sofort; bei 0 ruht die regelmäßige Prüfung, Einzelprüfungen laufen weiter.
func (m *Monitor) Run(ctx context.Context) {
	for {
		var tick <-chan time.Time
		var t *time.Timer
		if iv := m.Interval(); iv > 0 {
			m.CheckAll(ctx)
			t = time.NewTimer(iv)
			tick = t.C
		}
		select {
		case <-ctx.Done():
		case <-tick:
		case <-m.reset:
		}
		if t != nil {
			t.Stop()
		}
		if ctx.Err() != nil {
			return
		}
	}
}

func (m *Monitor) Interval() time.Duration {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.interval
}

// SetInterval ändert den Abstand der regelmäßigen Prüfung (Einstellungsseite).
func (m *Monitor) SetInterval(d time.Duration) {
	m.mu.Lock()
	changed := m.interval != d
	m.interval = d
	m.mu.Unlock()
	if changed {
		select {
		case m.reset <- struct{}{}:
		default:
		}
	}
}

// SetPorts ersetzt die Standardports der Online-Prüfung.
func (m *Monitor) SetPorts(ports []int) { m.prober.SetPorts(ports) }

// maxFoundAge: ältere Suchtreffer liefern keine IP mehr (DHCP kann sie längst neu vergeben haben).
const maxFoundAge = 7 * 24 * time.Hour

// Target liefert die IP, unter der ein Gerät geprüft wird: eingetragen oder aus der Netzwerksuche.
func (m *Monitor) Target(d store.Device) (netip.Addr, string) {
	if a, err := netip.ParseAddr(d.IP); err == nil {
		return a, "device"
	}
	if f, ok := m.store.FoundByMAC(d.MAC); ok && time.Since(f.LastSeen) < maxFoundAge {
		if a, err := netip.ParseAddr(f.IP); err == nil {
			return a, "discovery"
		}
	}
	return netip.Addr{}, ""
}

func (m *Monitor) CheckAll(ctx context.Context) {
	devices, _ := m.store.Devices()
	sem := make(chan struct{}, 32)
	var wg sync.WaitGroup
	for _, d := range devices {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			m.Check(ctx, d)
		}()
	}
	wg.Wait()
	m.prune(devices)
}

// Check prüft ein Gerät sofort und speichert das Ergebnis.
func (m *Monitor) Check(ctx context.Context, d store.Device) State {
	started := time.Now()
	ip, src := m.Target(d)
	if !ip.IsValid() {
		st := State{State: "unknown", CheckedAt: time.Now().UTC(), started: started}
		m.set(d, st)
		return st
	}
	m.mu.RLock()
	prev := m.states[d.ID]
	m.mu.RUnlock()
	hint := ""
	if prev.State == "online" { // zuerst mit der Methode prüfen, die zuletzt geklappt hat
		hint = prev.Method
	}
	var ports []int // nil = Standardliste
	if len(d.Ports) > 0 {
		ports = d.Ports
	}
	r := m.prober.Check(ctx, ip, ports, hint)
	st := State{State: "offline", IP: ip.String(), IPSource: src, CheckedAt: time.Now().UTC(), started: started}
	st.LastSeen = prev.LastSeen
	if st.LastSeen.IsZero() { // nach einem Neustart: gespeicherter Wert
		st.LastSeen = m.store.LastSeen(d.ID)
	}
	if r.Online {
		// Antwortet unter der IP ein anderes Gerät (andere MAC in der Nachbartabelle), zählt das nicht.
		if mac := discovery.NeighborMAC(ip); mac != nil && !strings.EqualFold(mac.String(), d.MAC) {
			st.Conflict = strings.ToUpper(mac.String())
			st.Note = i18n.T(i18n.DE, "notify.conflict", ip.String(), st.Conflict)
		} else {
			st.State, st.Method, st.RTTMillis, st.LastSeen = "online", r.Method, r.RTT.Milliseconds(), st.CheckedAt
			m.store.SeenAt(d.ID, st.CheckedAt)
		}
	}
	// Nur speichern, wenn das Gerät inzwischen nicht geändert wurde (sonst überschreibt eine
	// langsame Prüfung der alten IP das Ergebnis der neuen).
	if cur, ok := m.store.Get(d.ID); ctx.Err() == nil && ok {
		if curIP, _ := m.Target(cur); curIP == ip {
			m.set(cur, st)
		}
	}
	return st
}

// Current liefert den gespeicherten Status nur, wenn er zur aktuellen Ziel-IP des Geräts passt.
func (m *Monitor) Current(d store.Device, maxAge time.Duration) (State, bool) {
	st, ok := m.Get(d.ID)
	if !ok || time.Since(st.CheckedAt) > maxAge {
		return State{}, false
	}
	ip, _ := m.Target(d)
	if (ip.IsValid() && st.IP != ip.String()) || (!ip.IsValid() && st.IP != "") {
		return State{}, false
	}
	return st, true
}

// set speichert das Ergebnis – außer eine später begonnene Prüfung ist schon fertig. Sonst
// überschriebe z. B. die langsame Routineprüfung, die während des Starts lief, das "online"
// der Weckseite.
func (m *Monitor) set(d store.Device, st State) {
	m.mu.Lock()
	old, ok := m.states[d.ID]
	if ok && old.started.After(st.started) {
		m.mu.Unlock()
		return
	}
	if ok && old.State != st.State && old.State != "" {
		log.Printf("Status: %s %s → %s", d.Name, old.State, st.State)
	}
	m.states[d.ID] = st
	// Unter der Sperre melden: Sonst können zwei gleichzeitige Prüfungen ihre Ereignisse in
	// anderer Reihenfolge abliefern, als sie gespeichert wurden, und die Oberfläche zeigte den
	// älteren Zustand. OnChange darf deshalb nicht in den Monitor zurückrufen.
	if m.OnChange != nil && (!ok || old.State != st.State || old.IP != st.IP || old.Method != st.Method || old.Note != st.Note) {
		m.OnChange(d, old, ok, st)
	}
	m.mu.Unlock()
}

func (m *Monitor) prune(devices []store.Device) {
	keep := map[string]bool{}
	for _, d := range devices {
		keep[d.ID] = true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for id := range m.states {
		if !keep[id] {
			delete(m.states, id)
		}
	}
}

func (m *Monitor) Get(id string) (State, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.states[id]
	return s, ok
}

func (m *Monitor) All() map[string]State {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]State, len(m.states))
	for k, v := range m.states {
		out[k] = v
	}
	return out
}
