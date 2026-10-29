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
// Verbindung hat ihre Sprache (für übersetzte Inhalte wie Fehler der Suche) und weiß, ob sie beim
// Verbinden entsperrt war (Privatsphäre: Online-Status nur für Entsperrte).
type hub struct {
	mu     sync.Mutex
	subs   map[chan []byte]subscriber
	closed bool
}

type subscriber struct {
	lang       i18n.Lang
	privileged bool
}

func (h *hub) subscribe(l i18n.Lang, privileged bool) (chan []byte, func()) {
	ch := make(chan []byte, 32)
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		close(ch)
		return ch, func() {}
	}
	if h.subs == nil {
		h.subs = map[chan []byte]subscriber{}
	}
	h.subs[ch] = subscriber{l, privileged}
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
	h.publishTo(event, data, nil)
}

// publishTo wie publishLocalized, aber nur an Empfänger, für die to (falls gesetzt) zutrifft.
func (h *hub) publishTo(event string, data func(i18n.Lang) any, to func(subscriber) bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	msgs := map[i18n.Lang][]byte{}
	for ch, sub := range h.subs {
		if to != nil && !to(sub) {
			continue
		}
		l := sub.lang
		msg, ok := msgs[l]
		if !ok {
			b, err := json.Marshal(data(l))
			if err != nil {
				log.Printf("Event %s: %v", event, err)
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

// dropPrivileged beendet alle Verbindungen, die beim Verbinden entsperrt waren (neues oder
// entferntes Passwort, ein Browser hat gesperrt). Die Browser verbinden sich von selbst neu und
// werden dabei mit ihrem aktuellen Cookie neu eingestuft.
func (h *hub) dropPrivileged() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch, sub := range h.subs {
		if sub.privileged {
			delete(h.subs, ch)
			close(ch)
		}
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
	privileged := s.privileged(r)
	ch, unsubscribe := s.hub.subscribe(s.lang(r), privileged)
	defer unsubscribe()
	// Entsperrt per Cookie: Die Verbindung endet, wenn die Entsperrung abläuft – sonst bekäme sie
	// den Online-Status weiter. Beim Neuverbinden zählt das dann gültige Cookie (eine inzwischen
	// verlängerte Entsperrung gilt also weiter).
	var expired <-chan time.Time
	if until := s.unlockUntil(r); privileged && !s.tokenAuthorized(r) && !until.IsZero() {
		t := time.NewTimer(time.Until(until))
		defer t.Stop()
		expired = t.C
	}
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
		case <-expired:
			return
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
	// nur an Verbindungen, die den Online-Status sehen dürfen (Einstellung zum Zeitpunkt des Ereignisses)
	data := map[string]any{"id": d.ID, "status": st}
	s.hub.publishTo("status", func(i18n.Lang) any { return data }, func(sub subscriber) bool {
		return shownTo(s.conf().Privacy.OnlineStatus, sub.privileged)
	})
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
