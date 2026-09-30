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
	Note      string    `json:"note,omitempty"`
}

type Monitor struct {
	store    *store.Store
	prober   *probe.Prober
	interval time.Duration
	mu       sync.RWMutex
	states   map[string]State
}

func New(s *store.Store, p *probe.Prober, interval time.Duration) *Monitor {
	return &Monitor{store: s, prober: p, interval: interval, states: map[string]State{}}
}

func (m *Monitor) Run(ctx context.Context) {
	if m.interval <= 0 {
		return
	}
	t := time.NewTicker(m.interval)
	defer t.Stop()
	for {
		m.CheckAll(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

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
	ip, src := m.Target(d)
	if !ip.IsValid() {
		st := State{State: "unknown", CheckedAt: time.Now().UTC()}
		m.set(d.ID, st)
		return st
	}
	r := m.prober.Check(ctx, ip)
	st := State{State: "offline", IP: ip.String(), IPSource: src, CheckedAt: time.Now().UTC()}
	m.mu.RLock()
	st.LastSeen = m.states[d.ID].LastSeen
	m.mu.RUnlock()
	if r.Online {
		// Antwortet unter der IP ein anderes Gerät (andere MAC in der Nachbartabelle), zählt das nicht.
		if mac := discovery.NeighborMAC(ip); mac != nil && !strings.EqualFold(mac.String(), d.MAC) {
			st.Note = "Unter " + ip.String() + " antwortet ein anderes Gerät (" + strings.ToUpper(mac.String()) + ")"
		} else {
			st.State, st.Method, st.RTTMillis, st.LastSeen = "online", r.Method, r.RTT.Milliseconds(), st.CheckedAt
		}
	}
	// Nur speichern, wenn das Gerät inzwischen nicht geändert wurde (sonst überschreibt eine
	// langsame Prüfung der alten IP das Ergebnis der neuen).
	if cur, ok := m.store.Get(d.ID); ctx.Err() == nil && ok {
		if curIP, _ := m.Target(cur); curIP == ip {
			m.set(d.ID, st)
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

func (m *Monitor) set(id string, st State) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if old, ok := m.states[id]; ok && old.State != st.State && old.State != "" {
		log.Printf("Status: Gerät %s %s → %s", id, old.State, st.State)
	}
	m.states[id] = st
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
