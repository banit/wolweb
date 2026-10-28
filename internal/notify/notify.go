// Package notify meldet Ereignisse (Gerät online/offline, geweckt, Wecken fehlgeschlagen) an einen
// Webhook (JSON) und/oder ein ntfy-Thema (Push aufs Handy).
package notify

import (
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"wolweb/internal/i18n"
)

// Ereignisarten
const (
	Online     = "online"
	Offline    = "offline"
	Wake       = "wake"
	WakeFailed = "wake_failed"
)

// Kinds sind alle Ereignisarten (für die Prüfung der Konfiguration).
var Kinds = []string{Online, Offline, Wake, WakeFailed}

type Event struct {
	Event   string    `json:"event"`
	Device  string    `json:"device"`
	ID      string    `json:"id,omitempty"`
	IP      string    `json:"ip,omitempty"`
	Source  string    `json:"source,omitempty"`
	Message string    `json:"message"`
	Time    time.Time `json:"time"`
}

type Notifier struct {
	webhook, ntfy string
	kinds         []string
	agent         string
	lang          i18n.Lang // Sprache der ntfy-Titel
	client        *http.Client
	queue         chan Event

	mu      sync.Mutex
	lastErr time.Time
}

// New liefert nil, wenn weder webhook noch ntfy gesetzt ist. kinds leer = alle Ereignisse.
// lang ist die Sprache der ntfy-Titel (die Meldungen selbst kommen fertig vom Aufrufer).
func New(webhook, ntfy string, kinds []string, version string, lang i18n.Lang) *Notifier {
	if webhook == "" && ntfy == "" {
		return nil
	}
	if len(kinds) == 0 {
		kinds = Kinds
	}
	n := &Notifier{webhook: webhook, ntfy: ntfy, kinds: kinds, agent: "wolweb/" + version, lang: lang,
		client: &http.Client{Timeout: 10 * time.Second}, queue: make(chan Event, 100)}
	go n.run()
	return n
}

// Send stellt ein Ereignis in die Warteschlange (blockiert nie; ist sie voll, entfällt es).
// Ein nil-Notifier tut nichts.
func (n *Notifier) Send(ev Event) {
	if n == nil || !slices.Contains(n.kinds, ev.Event) {
		return
	}
	if ev.Time.IsZero() {
		ev.Time = time.Now().UTC()
	}
	select {
	case n.queue <- ev:
	default:
		n.logErr("Warteschlange voll, Ereignis %s für %s verworfen", ev.Event, ev.Device)
	}
}

func (n *Notifier) run() {
	for ev := range n.queue {
		if n.webhook != "" {
			b, _ := json.Marshal(ev)
			n.post(n.webhook, "application/json", b)
		}
		if n.ntfy != "" {
			// Titel usw. als Parameter in der URL – in HTTP-Köpfen wären Umlaute nicht erlaubt.
			title, tag, prio := ntfyStyle(ev, n.lang)
			u, err := url.Parse(n.ntfy)
			if err != nil {
				n.logErr("ntfy-Adresse %q: %v", n.ntfy, err)
				continue
			}
			q := u.Query()
			q.Set("title", title)
			q.Set("tags", tag)
			q.Set("priority", prio)
			u.RawQuery = q.Encode()
			n.post(u.String(), "text/plain; charset=utf-8", []byte(ev.Message))
		}
	}
}

func ntfyStyle(ev Event, l i18n.Lang) (title, tag, priority string) {
	title = i18n.T(l, "notify.title."+ev.Event, ev.Device)
	switch ev.Event {
	case Online:
		return title, "green_circle", "default"
	case Offline:
		return title, "red_circle", "low"
	case Wake:
		return title, "zap", "low"
	default:
		return title, "warning", "high"
	}
}

func (n *Notifier) post(target, contentType string, body []byte) {
	req, err := http.NewRequest(http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		n.logErr("Benachrichtigung an %s: %v", redact(target), err)
		return
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("User-Agent", n.agent)
	resp, err := n.client.Do(req)
	if err != nil {
		var ue *url.Error // enthält die ganze URL samt Geheimnissen
		if errors.As(err, &ue) {
			err = ue.Err
		}
		n.logErr("Benachrichtigung an %s: %v", redact(target), err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		n.logErr("Benachrichtigung an %s: HTTP %d", redact(target), resp.StatusCode)
	}
}

// redact kürzt eine URL fürs Log auf Schema und Host (Pfad und Parameter können Geheimnisse
// enthalten, z. B. ein ntfy-Thema oder ein Webhook-Token).
func redact(target string) string {
	if u, err := url.Parse(target); err == nil && u.Host != "" {
		return u.Scheme + "://" + u.Host + "/…"
	}
	return "(Webhook)"
}

// logErr schreibt Fehler höchstens einmal pro Minute ins Log (ein ausgefallener Server soll das
// Log nicht fluten).
func (n *Notifier) logErr(format string, args ...any) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if time.Since(n.lastErr) < time.Minute {
		return
	}
	n.lastErr = time.Now()
	log.Printf(format, args...)
}

// ValidKind sagt, ob k eine bekannte Ereignisart ist.
func ValidKind(k string) bool { return slices.Contains(Kinds, strings.TrimSpace(k)) }
