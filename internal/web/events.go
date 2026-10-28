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
	"wolweb/internal/i18n"
	"wolweb/internal/notify"
	"wolweb/internal/status"
	"wolweb/internal/store"
)

// hub verteilt Ereignisse an alle offenen Server-Sent-Events-Verbindungen der Oberfläche. Jede
// Verbindung hat ihre Sprache (für übersetzte Inhalte wie Fehler der Suche).
type hub struct {
	mu     sync.Mutex
	subs   map[chan []byte]i18n.Lang
	closed bool
}

func (h *hub) subscribe(l i18n.Lang) (chan []byte, func()) {
	ch := make(chan []byte, 32)
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		close(ch)
		return ch, func() {}
	}
	if h.subs == nil {
		h.subs = map[chan []byte]i18n.Lang{}
	}
	h.subs[ch] = l
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
	h.publishLocalized(event, func(i18n.Lang) any { return data })
}

// publishLocalized baut den Inhalt je Sprache der Empfänger (höchstens einmal pro Sprache).
func (h *hub) publishLocalized(event string, data func(i18n.Lang) any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	msgs := map[i18n.Lang][]byte{}
	for ch, l := range h.subs {
		msg, ok := msgs[l]
		if !ok {
			b, err := json.Marshal(data(l))
			if err != nil {
				log.Printf("Ereignis %s: %v", event, err)
				return
			}
			msg = []byte(fmt.Sprintf("event: %s\ndata: %s\n\n", event, b))
			msgs[l] = msg
		}
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

// Close beendet alle offenen Ereignis-Verbindungen und verwirft ausstehende Wiederholungen (für
// http.Server.RegisterOnShutdown).
func (s *Server) Close() {
	s.hub.close()
	s.stopRetries()
}

// sseWriteTimeout: So lange darf ein einzelnes Ereignis zum Empfänger brauchen. Liest er nicht
// (eingefrorener Tab, abgerissene Verbindung ohne FIN), wird die Verbindung beendet, statt ewig in
// Write zu hängen; der Browser verbindet sich bei Bedarf selbst neu.
var sseWriteTimeout = 10 * time.Second

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	// Die Verbindung bleibt offen: keine Lesefrist (sonst bricht net/http sie nach ReadTimeout ab)
	// und statt der allgemeinen Schreibfrist eine kurze je Ereignis.
	_ = rc.SetReadDeadline(time.Time{})
	send := func(write func() error) bool {
		_ = rc.SetWriteDeadline(time.Now().Add(sseWriteTimeout))
		return write() == nil && rc.Flush() == nil
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no") // nginx: nicht puffern
	ch, unsubscribe := s.hub.subscribe(s.lang(r))
	defer unsubscribe()
	if !send(func() error {
		w.WriteHeader(http.StatusOK)
		_, err := io.WriteString(w, "retry: 5000\n\n")
		return err
	}) {
		return
	}
	ping := time.NewTicker(25 * time.Second) // hält Proxys und NAT-Tabellen wach
	defer ping.Stop()
	for {
		var ok bool
		select {
		case <-r.Context().Done():
			return
		case msg, open := <-ch:
			if !open {
				return
			}
			ok = send(func() error { _, err := w.Write(msg); return err })
		case <-ping.C:
			ok = send(func() error { _, err := io.WriteString(w, ": ping\n\n"); return err })
		}
		if !ok {
			return // Frist überschritten oder Verbindung weg: abmelden (defer)
		}
	}
}

// subscribers zählt die offenen Ereignis-Verbindungen (für Tests und /metrics).
func (h *hub) subscribers() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

// devicesChanged meldet der Oberfläche, dass sie die Geräteliste neu laden soll.
func (s *Server) devicesChanged() { s.hub.publish("devices", struct{}{}) }

// statusChanged wird vom Monitor nach jeder Statusänderung aufgerufen.
func (s *Server) statusChanged(d store.Device, old status.State, hadOld bool, st status.State) {
	s.hub.publish("status", map[string]any{"id": d.ID, "status": st})
	// Erreichbar geworden: offene Weckrufe im Verlauf gelten als „Start bestätigt“ – auch wenn der
	// Start länger dauerte als die Wartezeit der Weckseite.
	if st.State == "online" && (!hadOld || old.State != "online") {
		s.store.ConfirmStart(d.ID, time.Now(), max(2*s.rt().wakeTimeout, 5*time.Minute))
	}
	// Benachrichtigen nur bei echten Wechseln online ↔ offline – nicht beim ersten Ergebnis nach
	// dem Start und nicht bei "unbekannt" (keine IP).
	if hadOld && old.State != st.State && (old.State == "online" || old.State == "offline") &&
		(st.State == "online" || st.State == "offline") {
		kind, msg := notify.Online, i18n.T(s.rt().lang, "notify.online", d.Name)
		if st.State == "offline" {
			kind, msg = notify.Offline, i18n.T(s.rt().lang, "notify.offline", d.Name)
		}
		if st.Method != "" {
			msg += " (" + st.Method + ")"
		}
		if st.Conflict != "" {
			msg += " – " + i18n.T(s.rt().lang, "notify.conflict", st.IP, st.Conflict)
		}
		s.rt().notify.Send(notify.Event{Event: kind, Device: d.Name, ID: d.ID, IP: st.IP, Message: msg})
	}
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
	s.hub.publishLocalized("scan", func(l i18n.Lang) any { return j.Localized(l) })
}
