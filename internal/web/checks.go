package web

import (
	"context"
	"fmt"
	"sync"
	"time"

	"wolweb/internal/status"
	"wolweb/internal/store"
)

// --- Gemeinsame Online-Prüfungen ---
//
// Status-Abfragen und ?wait= kommen auch ohne Anmeldung (öffentliche Wecklinks). Damit viele
// gleichzeitige Aufrufe nicht je eine eigene Prüfung (Ping + bis zu 16 Verbindungen) auslösen, teilen
// sie sich die laufende Prüfung eines Geräts, und es warten höchstens maxWaiters Aufrufe zugleich.

// maxWaiters begrenzt gleichzeitig laufende ?wait=-Schleifen; darüber wird einmal geprüft statt gewartet.
const maxWaiters = 64

type checkFlights struct {
	mu sync.Mutex
	m  map[string]*checkCall
}

type checkCall struct {
	done chan struct{}
	st   status.State
}

// sharedCheck prüft d jetzt – oder wartet auf eine schon laufende Prüfung desselben Geräts. Die Prüfung
// selbst hängt nicht am Aufrufer (sie ist durch das Zeitlimit der Prüfung begrenzt); ok ist false,
// wenn ctx vorher endet.
func (s *Server) sharedCheck(ctx context.Context, d store.Device) (st status.State, ok bool) {
	// Zusammengelegt wird nur, was dasselbe Ziel prüft: Ändert sich die IP (Gerät bearbeitet, neuer
	// Suchtreffer), bekäme ein späterer Aufruf sonst das Ergebnis des alten Ziels.
	ip, _ := s.monitor.Target(d)
	key := fmt.Sprint(d.ID, "|", ip, "|", d.Ports)
	f := &s.flights
	f.mu.Lock()
	if f.m == nil {
		f.m = map[string]*checkCall{}
	}
	c, running := f.m[key]
	if !running {
		c = &checkCall{done: make(chan struct{})}
		f.m[key] = c
		go func() {
			cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			c.st = s.monitor.Check(cctx, d)
			f.mu.Lock()
			delete(f.m, key)
			f.mu.Unlock()
			close(c.done)
		}()
	}
	f.mu.Unlock()
	select {
	case <-c.done:
		return c.st, true
	case <-ctx.Done():
		return status.State{}, false
	}
}

// waitOnline prüft alle 2 Sekunden, bis d erreichbar ist oder max abläuft. Warten schon maxWaiters
// Aufrufe, wird nur einmal geprüft.
func (s *Server) waitOnline(ctx context.Context, d store.Device, max time.Duration) bool {
	select {
	case s.waitSlots <- struct{}{}:
		defer func() { <-s.waitSlots }()
	default:
		st, ok := s.sharedCheck(ctx, d)
		return ok && st.State == "online"
	}
	ctx, cancel := context.WithTimeout(ctx, max)
	defer cancel()
	for {
		if st, ok := s.sharedCheck(ctx, d); ok && st.State == "online" {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(2 * time.Second):
		}
	}
}
