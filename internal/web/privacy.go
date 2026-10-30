package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"wolweb/internal/config"
	"wolweb/internal/i18n"
)

// --- Privatsphäre: wer sieht den Online-Status ---
//
// privacy.online_status legt fest, wer sieht, ob Geräte laufen: alle, nur wer entsperrt hat
// (Passwort für Änderungen, API-Token) oder niemand. Der Server lässt den Status dann überall weg –
// in Geräteliste und Gruppen, bei Live-Ereignissen, auf den Weckseiten, im Verlauf und in /metrics –,
// nicht nur in der Oberfläche. Geprüft wird trotzdem (automatische Wiederholung, Benachrichtigungen).
//
// Ausnahme Wecken: Wer ein Gerät weckt (Wecklink, Gruppenlink, Knopf in der Übersicht), startet es
// ohnehin – er darf sehen, ob es hochkommt (siehe watchToken, ?wait=). Nur die Vorabprüfung „läuft es
// schon?“ entfällt, denn die verriete den Status, ohne zu wecken.
//
// privacy.wake_history (dieselben Stufen, eigens einzuschalten) blendet aus, wann Geräte geweckt
// wurden: „Zuletzt geweckt“ (last_wake), den Verlauf und die Metriken dazu. Wer selbst weckt,
// verfolgt seinen Weckruf trotzdem – der Verlauf wird weiter geschrieben, nur nicht gezeigt.

// privileged: Die Anfrage kommt von jemandem, der entsperrt hat (mit gesetztem Passwort für
// Änderungen) oder das API-Token nutzt. Ohne Passwort trifft das außer dem Token niemanden – wer
// den Status nur Entsperrten zeigen will, braucht ein Passwort. Die Anmeldung per HTTP Basic zählt
// nicht, wie beim Bearbeitungsschutz.
func (s *Server) privileged(r *http.Request) bool {
	return s.tokenAuthorized(r) || (s.store.HasPassword() && !s.unlockUntil(r).IsZero())
}

// shownTo: Sieht jemand (privileged oder nicht) bei dieser Stufe etwas (all, unlocked, none)?
func shownTo(mode string, privileged bool) bool {
	switch mode {
	case config.StatusNone:
		return false
	case config.StatusUnlocked:
		return privileged
	}
	return true
}

// statusVisible: Die Anfrage darf den Online-Status sehen.
func (s *Server) statusVisible(r *http.Request) bool {
	return shownTo(s.conf().Privacy.OnlineStatus, s.privileged(r))
}

// checkVisible: wie statusVisible, für die Routen, die bei jeder Abfrage frisch prüfen (…/status). Das
// Entsperr-Cookie zählt dort nur zusammen mit X-WolWeb (das senden alle eigenen Seiten): Ein Cookie
// geht auch an Anfragen anderer Seiten derselben Site mit (anderer Port, Schwester-Subdomain), und
// deren Antwortzeit verriete den ausgeblendeten Status – online antwortet schnell, offline erst nach
// dem Zeitlimit der Prüfung.
func (s *Server) checkVisible(r *http.Request) bool {
	priv := s.tokenAuthorized(r) || (r.Header.Get("X-WolWeb") != "" && s.store.HasPassword() && !s.unlockUntil(r).IsZero())
	return shownTo(s.conf().Privacy.OnlineStatus, priv)
}

// visibility: was eine Anfrage von den Geräten sehen darf.
type visibility struct {
	status bool // Online-Status (privacy.online_status)
	wakes  bool // wann zuletzt geweckt, Verlauf (privacy.wake_history)
}

func (s *Server) visibility(r *http.Request) visibility {
	p, pv := s.privileged(r), s.conf().Privacy
	return visibility{status: shownTo(pv.OnlineStatus, p), wakes: shownTo(pv.WakeHistory, p)}
}

// foundVisible: Die Anfrage darf die Suchtreffer sehen. Sie sind ein Werkzeug zum Anlegen von
// Geräten und verraten obendrein, wer gerade im Netz ist („zuletzt gesehen“) – sehen darf sie, wer
// bearbeiten darf: ohne Passwort für Änderungen alle, mit Passwort nur Entsperrte (und das
// API-Token), unabhängig von der Privatsphäre-Einstellung.
func (s *Server) foundVisible(r *http.Request) bool {
	return s.unlocked(r)
}

// watchTTL: So lange darf, wer geweckt hat, den Start verfolgen (Wartezeit der Weckseite und
// „Nochmals prüfen“ danach).
const watchTTL = 15 * time.Minute

// watchToken bekommt, wer eben geweckt hat: Mit ihm zeigt …/status den Start des
// Geräts (target "device:<ID>") bzw. der Gruppe ("group:<Name>"), auch wenn der Online-Status sonst
// ausgeblendet ist. Signiert mit dem lokalen Geheimnis der Installation (nicht im Export), gültig für watchTTL.
func (s *Server) watchToken(target string) string {
	exp := strconv.FormatInt(time.Now().Add(watchTTL).Unix(), 10)
	return exp + "." + s.watchSig(target, exp)
}

func (s *Server) watchSig(target, exp string) string {
	m := hmac.New(sha256.New, s.store.LocalSecret())
	m.Write([]byte("watch|" + strings.ToLower(target) + "|" + exp))
	return hex.EncodeToString(m.Sum(nil))[:32]
}

// watching: Die Anfrage trägt einen gültigen watchToken für target (Parameter "watch").
func (s *Server) watching(r *http.Request, target string) bool {
	exp, sig, ok := strings.Cut(r.URL.Query().Get("watch"), ".")
	n, err := strconv.ParseInt(exp, 10, 64)
	if !ok || err != nil || time.Now().After(time.Unix(n, 0)) || time.Until(time.Unix(n, 0)) > watchTTL {
		return false
	}
	return hmac.Equal([]byte(sig), []byte(s.watchSig(target, exp)))
}

// recording: wolweb speichert Weckverlauf, „zuletzt geweckt“ und „zuletzt online“
// (privacy.recording). Ausgeschaltet bleibt der Verlauf leer, und der Knopf dazu entfällt.
func (s *Server) recording() bool { return s.conf().Privacy.Recording != config.RecordingOff }

// applyRecording gibt Aufzeichnung und Aufbewahrung an den Speicher weiter; der löscht sofort, was
// nicht mehr aufbewahrt wird.
func (s *Server) applyRecording(cfg config.Config) {
	keep := time.Duration(cfg.Privacy.RetentionDays) * 24 * time.Hour
	if err := s.store.SetRecording(cfg.Privacy.Recording != config.RecordingOff, keep); err != nil {
		log.Printf("Saving runtime data: %v", err)
	}
}

// hidden antwortet, dass diese Angaben für die Anfrage ausgeblendet sind (key: die Meldung dazu).
func (s *Server) hidden(w http.ResponseWriter, r *http.Request, key string) {
	writeJSON(w, http.StatusForbidden, map[string]any{"success": false, "hidden": true,
		"message": i18n.T(s.lang(r), key)})
}
