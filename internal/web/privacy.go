package web

import (
	"net/http"

	"wolweb/internal/config"
	"wolweb/internal/i18n"
)

// --- Privatsphäre: wer sieht den Online-Status ---
//
// privacy.online_status legt fest, wer sieht, ob Geräte laufen: alle, nur wer entsperrt hat
// (Passwort für Änderungen, API-Token) oder niemand. Der Server lässt den Status dann überall weg –
// in Geräteliste und Gruppen, bei Live-Ereignissen, auf den Weckseiten, im Verlauf und in /metrics –,
// nicht nur in der Oberfläche. Geprüft wird trotzdem (automatische Wiederholung, Benachrichtigungen).

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

// statusHidden antwortet, dass der Online-Status für diese Anfrage ausgeblendet ist.
func (s *Server) statusHidden(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusForbidden, map[string]any{"success": false, "hidden": true,
		"message": i18n.T(s.lang(r), "err.status_hidden")})
}
