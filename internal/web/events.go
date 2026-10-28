package web

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"wolweb/internal/discovery"
	"wolweb/internal/status"
	"wolweb/internal/store"
)

// hub verteilt Ereignisse an alle offenen Server-Sent-Events-Verbindungen der Oberfläche.
type hub struct {
	mu     sync.Mutex
	subs   map[chan []byte]struct{}
	closed bool
}

func (h *hub) subscribe() (chan []byte, func()) {
	ch := make(chan []byte, 32)
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		close(ch)
		return ch, func() {}
	}
	if h.subs == nil {
		h.subs = map[chan []byte]struct{}{}
	}
	h.subs[ch] = struct{}{}
	return ch, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if _, ok := h.subs[ch]; ok {
			delete(h.subs, ch)
			close(ch)
		}
	}
}

// publish schickt ein Ereignis an alle. Wer nicht mitkommt, verpasst es – die Oberfläche lädt
// ohnehin regelmäßig nach.
func (h *hub) publish(event string, data any) {
	b, err := json.Marshal(data)
	if err != nil {
		log.Printf("Ereignis %s: %v", event, err)
		return
	}
	msg := []byte(fmt.Sprintf("event: %s\ndata: %s\n\n", event, b))
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- msg:
		default:
		}
	}
}

func (h *hub) close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	for ch := range h.subs {
		delete(h.subs, ch)
		close(ch)
	}
}

// Close beendet alle offenen Ereignis-Verbindungen (für http.Server.RegisterOnShutdown).
func (s *Server) Close() { s.hub.close() }

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	// Die Verbindung bleibt offen: keine Schreibfrist (WriteTimeout) und keine Lesefrist – sonst
	// bricht net/http sie nach ReadTimeout ab.
	_ = rc.SetWriteDeadline(time.Time{})
	_ = rc.SetReadDeadline(time.Time{})
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no") // nginx: nicht puffern
	w.WriteHeader(http.StatusOK)
	ch, unsubscribe := s.hub.subscribe()
	defer unsubscribe()
	io.WriteString(w, "retry: 5000\n\n")
	if rc.Flush() != nil {
		return
	}
	ping := time.NewTicker(25 * time.Second) // hält Proxys und NAT-Tabellen wach
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			if _, err := w.Write(msg); err != nil {
				return
			}
		case <-ping.C:
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
				return
			}
		}
		if rc.Flush() != nil {
			return
		}
	}
}

// devicesChanged meldet der Oberfläche, dass sie die Geräteliste neu laden soll.
func (s *Server) devicesChanged() { s.hub.publish("devices", struct{}{}) }

// statusChanged wird vom Monitor nach jeder Statusänderung aufgerufen.
func (s *Server) statusChanged(d store.Device, old status.State, hadOld bool, st status.State) {
	s.hub.publish("status", map[string]any{"id": d.ID, "status": st})
}

// scanChanged gibt den Fortschritt der Suche weiter – höchstens viermal pro Sekunde, Anfang und
// Ende immer.
func (s *Server) scanChanged(j discovery.Job) {
	s.scanMu.Lock()
	if j.Running && j.Done > 0 && time.Since(s.scanLast) < 250*time.Millisecond {
		s.scanMu.Unlock()
		return
	}
	s.scanLast = time.Now()
	s.scanMu.Unlock()
	s.hub.publish("scan", j)
}
