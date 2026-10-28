package web

import (
	"context"
	"log"
	"sync"
	"time"

	"wolweb/internal/status"
)

// --- Wiederholung ---
//
// Antwortet ein Gerät eine Weile nach dem Wecken nicht (status.wake_retry, Standard 45 s), schickt
// wolweb das Magic Packet einmal erneut. Manche Netzwerkkarten verpassen das erste Paket, etwa
// solange der Switch-Port nach dem Herunterfahren noch im Energiesparmodus ist. Das geschieht im
// Server und nicht in der Weckseite, damit es für Wecklinks, Zeitpläne und Automationen genauso gilt
// wie für einen Browser, dessen Tab längst geschlossen ist.

type retries struct {
	mu     sync.Mutex
	gen    map[string]uint64 // Geräte-ID -> Nummer des Weckens, dessen Wiederholung aussteht
	timers map[uint64]*time.Timer
	n      uint64
	closed bool
}

// scheduleRetry merkt erfolgreiche Weckversuche von Geräten mit IP für eine Wiederholung vor –
// alle eines Aufrufs (Gruppe, Zeitplan) gemeinsam. Ein neues Wecken desselben Geräts ersetzt die
// ausstehende Wiederholung, Wiederholungen selbst werden nicht wiederholt.
func (s *Server) scheduleRetry(as []wakeAttempt) {
	if s.wakeRetry <= 0 {
		return
	}
	var due []wakeAttempt
	for _, a := range as {
		if ip, _ := s.monitor.Target(a.d); a.ev.Success && !a.ev.Retry && ip.IsValid() {
			due = append(due, a)
		}
	}
	if len(due) == 0 {
		return
	}
	q := &s.retry
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	if q.gen == nil {
		q.gen, q.timers = map[string]uint64{}, map[uint64]*time.Timer{}
	}
	q.n++
	g := q.n
	for _, a := range due {
		q.gen[a.d.ID] = g
	}
	q.timers[g] = time.AfterFunc(s.wakeRetry, func() { s.runRetry(g, due) })
}

// runRetry prüft die Geräte eines Weckens und schickt denen, die noch nicht antworten, das Paket
// noch einmal.
func (s *Server) runRetry(g uint64, as []wakeAttempt) {
	q := &s.retry
	q.mu.Lock()
	delete(q.timers, g)
	var mine []wakeAttempt
	for _, a := range as {
		if q.gen[a.d.ID] == g { // sonst wurde das Gerät inzwischen neu geweckt
			delete(q.gen, a.d.ID)
			mine = append(mine, a)
		}
	}
	closed := q.closed
	q.mu.Unlock()
	if closed || len(mine) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	states := make([]status.State, len(mine))
	var wg sync.WaitGroup
	for i := range mine {
		d, ok := s.store.Get(mine[i].d.ID) // aktuelle Daten – oder gelöscht
		if !ok {
			continue
		}
		mine[i].d = d
		wg.Add(1)
		go func() {
			defer wg.Done()
			states[i] = s.freshStatus(ctx, d)
		}()
	}
	wg.Wait()
	var again []wakeAttempt
	for i, a := range mine {
		if states[i].State != "offline" { // online, gelöscht oder nicht mehr prüfbar
			continue
		}
		log.Printf("%s antwortet %v nach dem Wecken nicht – Magic Packet erneut", a.d.Name, s.wakeRetry)
		_, _, b := s.sendWake(nil, a.d, a.ev.Source, a.ev.Group)
		b.ev.Client, b.ev.User, b.ev.Retry = a.ev.Client, a.ev.User, true
		again = append(again, b)
	}
	s.recordWakes(again...)
}

// stopRetries verwirft ausstehende Wiederholungen (beim Beenden).
func (s *Server) stopRetries() {
	q := &s.retry
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	for g, t := range q.timers {
		t.Stop()
		delete(q.timers, g)
	}
}
