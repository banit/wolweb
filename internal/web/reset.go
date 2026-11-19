package web

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"wolweb/internal/i18n"
	"wolweb/internal/mail"
	"wolweb/internal/store"
)

// --- Mailversand: Passwort vergessen, Einladungen ---
//
// Ist ein Mailserver eingetragen (Einstellungen, Abschnitt Benutzer), bekommt die Anmeldeseite
// "Passwort vergessen?": Wer Namen oder E-Mail eingibt, bekommt – sofern eine E-Mail hinterlegt ist –
// einen Link, der eine Stunde lang ein neues Passwort setzen lässt. Der Link ist signiert und an den
// Passwort-Hash gebunden: Einmal benutzt (oder das Passwort anders geändert), gilt er nicht mehr.
// Legt ein Administrator einen Benutzer mit E-Mail, aber ohne Passwort an, geht eine Einladung mit
// einem solchen Link raus (drei Tage gültig).

const (
	resetTTL    = time.Hour
	inviteTTL   = 72 * time.Hour
	resetPause  = 2 * time.Minute // je Benutzer höchstens eine Mail in dieser Zeit
	resetPerDay = 50              // insgesamt höchstens so viele Mails pro Tag
)

func (s *Server) mailReady() bool { return s.usersOn() && s.store.Mail().Ready() }

func (s *Server) mailServer() mail.Server {
	m := s.store.Mail()
	return mail.Server{Host: m.Host, Port: m.Port, Security: m.Security, Username: m.Username, Password: m.Password, From: m.From}
}

func (s *Server) resetSig(u store.User, exp string) string {
	m := hmac.New(sha256.New, s.store.LocalSecret())
	m.Write([]byte("reset|" + u.ID + "|" + u.Password + "|" + exp))
	return hex.EncodeToString(m.Sum(nil))
}

func (s *Server) resetToken(u store.User, ttl time.Duration) string {
	exp := strconv.FormatInt(time.Now().Add(ttl).Unix(), 10)
	return u.ID + "." + exp + "." + s.resetSig(u, exp)
}

// checkReset liefert den Benutzer eines gültigen Links (noch nicht abgelaufen, Passwort unverändert).
func (s *Server) checkReset(tok string) (store.User, bool) {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return store.User{}, false
	}
	n, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || time.Now().After(time.Unix(n, 0)) || time.Until(time.Unix(n, 0)) > inviteTTL {
		return store.User{}, false
	}
	u, ok := s.store.UserByID(parts[0])
	if !ok || !hmac.Equal([]byte(parts[2]), []byte(s.resetSig(u, parts[1]))) {
		return store.User{}, false
	}
	return u, true
}

// resetLimiter bremst Mails: je Benutzer eine pro resetPause, insgesamt resetPerDay pro Tag.
type resetLimiter struct {
	mu    sync.Mutex
	last  map[string]time.Time
	day   time.Time
	count int
}

func (l *resetLimiter) allow(id string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if l.last == nil {
		l.last = map[string]time.Time{}
	}
	if now.Sub(l.day) > 24*time.Hour {
		l.day, l.count = now, 0
		clear(l.last)
	}
	if now.Sub(l.last[id]) < resetPause || l.count >= resetPerDay {
		return false
	}
	l.last[id] = now
	l.count++
	return true
}

// sendLink verschickt im Hintergrund eine Mail mit Link (Zurücksetzen oder Einladung).
func (s *Server) sendLink(u store.User, l i18n.Lang, invite bool) {
	m := s.store.Mail()
	ttl, kind := resetTTL, "reset"
	if invite {
		ttl, kind = inviteTTL, "invite"
	}
	link := m.Link + "reset?token=" + s.resetToken(u, ttl)
	subject := i18n.T(l, "mail."+kind+".subject")
	body := i18n.T(l, "mail."+kind+".body", u.Name, link, m.Link)
	srv := s.mailServer()
	go func() {
		if err := mail.Send(context.Background(), srv, u.Email, subject, body); err != nil {
			log.Printf("Mail (%s) to user %s failed: %v", kind, u.Name, err)
			return
		}
		log.Printf("Mail (%s) sent to user %s", kind, u.Name)
	}()
}

// --- Seiten und API ---

func (s *Server) resetPage(w http.ResponseWriter, r *http.Request) {
	if !s.usersOn() {
		http.Redirect(w, r, s.conf().VDir, http.StatusSeeOther)
		return
	}
	s.render(w, r, http.StatusOK, "reset.html", pageData{MailReset: s.mailReady()})
}

// requestReset: Antwortet immer gleich – ob es den Namen gibt und ob eine E-Mail hinterlegt ist,
// erfährt der Aufrufer nicht.
func (s *Server) requestReset(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &in); err != nil {
		s.storeError(w, r, err)
		return
	}
	if !s.mailReady() {
		s.fail(w, r, http.StatusConflict, "err.mail_off")
		return
	}
	u, ok := s.store.UserByLogin(in.Name)
	if ok && u.Email != "" && s.resets.allow(u.ID) {
		s.sendLink(u, s.lang(r), false)
	} else if ok {
		log.Printf("Password reset for %s not sent (no e-mail or too soon)", u.Name)
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// confirmReset setzt mit einem gültigen Link das neue Passwort und meldet gleich an.
func (s *Server) confirmReset(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &in); err != nil {
		s.storeError(w, r, err)
		return
	}
	u, ok := s.checkReset(in.Token)
	if !ok {
		s.fail(w, r, http.StatusForbidden, "err.reset_invalid")
		return
	}
	nu, err := s.store.SetUserPasswordIf(u.ID, u.Password, in.Password)
	if errors.Is(err, store.ErrPasswordChanged) {
		s.fail(w, r, http.StatusForbidden, "err.reset_invalid")
		return
	}
	if err != nil {
		s.userError(w, r, err)
		return
	}
	log.Printf("Password reset by mail link: %s", nu.Name)
	s.hub.dropPrivileged()
	s.setSession(w, r, &nu)
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// --- Einstellungen des Mailservers (Administratoren) ---

type mailView struct {
	store.MailSettings
	PasswordSet bool `json:"password_set"`
}

func viewMail(m store.MailSettings) mailView {
	v := mailView{MailSettings: m, PasswordSet: m.Password != ""}
	v.Password = "" // geht nie an die Oberfläche zurück
	return v
}

func (s *Server) getMail(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, viewMail(s.store.Mail()))
}

func (s *Server) putMail(w http.ResponseWriter, r *http.Request) {
	var in store.MailSettings
	if err := readJSON(r, &in); err != nil {
		s.storeError(w, r, err)
		return
	}
	m, err := s.store.SetMail(in)
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	log.Printf("Mail server set: %q", m.Host)
	writeJSON(w, http.StatusOK, viewMail(m))
}

// testMail schickt eine Probe an die eigene Adresse und wartet auf das Ergebnis.
func (s *Server) testMail(w http.ResponseWriter, r *http.Request) {
	me, _, ok := s.session(r)
	if !ok || me.Email == "" {
		s.fail(w, r, http.StatusBadRequest, "err.mail_no_own")
		return
	}
	if !s.store.Mail().Ready() {
		s.fail(w, r, http.StatusConflict, "err.mail_off")
		return
	}
	l := s.lang(r)
	err := mail.Send(r.Context(), s.mailServer(), me.Email, i18n.T(l, "mail.test.subject"), i18n.T(l, "mail.test.body", s.store.Mail().Link))
	if err != nil {
		log.Printf("Test mail failed: %v", err)
		writeJSON(w, http.StatusBadGateway, map[string]any{"success": false, "message": i18n.T(l, "err.mail_failed", err.Error())})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "to": me.Email})
}
