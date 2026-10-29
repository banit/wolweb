package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
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
// Ausnahme Wecklinks: Wer über einen Weck- oder Gruppenlink weckt, startet das Gerät ohnehin – er
// darf sehen, ob es hochkommt (siehe watchToken, ?wait=). Nur die Vorabprüfung „läuft es schon?“
// entfällt, denn die verriete den Status, ohne zu wecken.

// privileged: Die Anfrage kommt von jemandem, der entsperrt hat (mit gesetztem Passwort für
// Änderungen) oder das API-Token nutzt. Ohne Passwort trifft das außer dem Token niemanden – wer
// den Status nur Entsperrten zeigen will, braucht ein Passwort. Die Anmeldung per HTTP Basic zählt
// nicht, wie beim Bearbeitungsschutz.
func (s *Server) privileged(r *http.Request) bool {
	return s.tokenAuthorized(r) || (s.store.HasPassword() && !s.unlockUntil(r).IsZero())
}

// statusShown: Wer (privileged oder nicht) sieht den Online-Status bei dieser Einstellung?
func statusShown(mode string, privileged bool) bool {
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
	return statusShown(s.conf().Privacy.OnlineStatus, s.privileged(r))
}

// foundVisible: Die Anfrage darf die Suchtreffer sehen. Auch sie verraten, wer gerade im Netz ist
// („zuletzt gesehen“); wer Geräte aus der Suche übernimmt, braucht sie aber – daher sieht sie auch
// bei „niemand“ noch, wer entsperrt hat.
func (s *Server) foundVisible(r *http.Request) bool {
	return s.conf().Privacy.OnlineStatus == config.StatusAll || s.privileged(r)
}

// watchTTL: So lange darf, wer geweckt hat, den Start verfolgen (Wartezeit der Weckseite und
// „Nochmals prüfen“ danach).
const watchTTL = 15 * time.Minute

// watchToken bekommt, wer über einen Wecklink geweckt hat: Mit ihm zeigt …/status den Start des
// Geräts (target "device:<ID>") bzw. der Gruppe ("group:<Name>"), auch wenn der Online-Status sonst
// ausgeblendet ist. Signiert mit dem Geheimnis der Installation, gültig für watchTTL.
func (s *Server) watchToken(target string) string {
	exp := strconv.FormatInt(time.Now().Add(watchTTL).Unix(), 10)
	return exp + "." + s.watchSig(target, exp)
}

func (s *Server) watchSig(target, exp string) string {
	m := hmac.New(sha256.New, s.store.Secret())
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

// statusHidden antwortet, dass der Online-Status für diese Anfrage ausgeblendet ist.
func (s *Server) statusHidden(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusForbidden, map[string]any{"success": false, "hidden": true,
		"message": i18n.T(s.lang(r), "err.status_hidden")})
}
