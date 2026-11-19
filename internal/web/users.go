package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"wolweb/internal/i18n"
	"wolweb/internal/status"
	"wolweb/internal/store"
)

// --- Benutzer und Anmeldung ---
//
// Ohne Benutzer bleibt alles wie bisher. Mit Benutzern (store/users.go) braucht die Oberfläche eine
// Anmeldung: Ein Cookie hält sie 30 Tage (fest ab der Anmeldung), signiert mit dem lokalen Geheimnis
// und an den Passwort-Hash des Benutzers gebunden – ein neues Passwort meldet alle Sitzungen ab.
// Administratoren dürfen alles (wie bisher "entsperrt"), alle anderen sehen und wecken nur die Geräte
// ihrer Benutzergruppen und ändern nur ihr eigenes Konto. Das API-Token zählt als Administrator.

const (
	sessionCookie = "wolweb_session"
	sessionMax    = 30 * 24 * time.Hour
)

func (s *Server) usersOn() bool { return s.store.HasUsers() }

func (s *Server) sessionSig(u store.User, exp string) string {
	m := hmac.New(sha256.New, s.store.LocalSecret())
	m.Write([]byte("session|" + u.ID + "|" + u.Password + "|" + exp))
	return hex.EncodeToString(m.Sum(nil))
}

func (s *Server) sessionValue(u store.User, until time.Time) string {
	exp := strconv.FormatInt(until.Unix(), 10)
	return u.ID + "." + exp + "." + s.sessionSig(u, exp)
}

// session liefert den angemeldeten Benutzer und das Ende der Sitzung (ok=false ohne gültige Sitzung).
func (s *Server) session(r *http.Request) (store.User, time.Time, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return store.User{}, time.Time{}, false
	}
	parts := strings.Split(c.Value, ".")
	if len(parts) != 3 {
		return store.User{}, time.Time{}, false
	}
	n, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return store.User{}, time.Time{}, false
	}
	until := time.Unix(n, 0)
	if time.Now().After(until) || time.Until(until) > sessionMax {
		return store.User{}, time.Time{}, false
	}
	u, ok := s.store.UserByID(parts[0])
	if !ok || !hmac.Equal([]byte(parts[2]), []byte(s.sessionSig(u, parts[1]))) {
		return store.User{}, time.Time{}, false
	}
	return u, until, true
}

func (s *Server) setSession(w http.ResponseWriter, r *http.Request, u *store.User) {
	c := &http.Cookie{Name: sessionCookie, Path: s.conf().VDir, HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"}
	if u == nil {
		c.MaxAge = -1
	} else {
		until := time.Now().Add(sessionMax)
		c.Value, c.Expires = s.sessionValue(*u, until), until
	}
	http.SetCookie(w, c)
}

// isAdmin: API-Token oder angemeldeter Administrator (nur mit Benutzern).
func (s *Server) isAdmin(r *http.Request) bool {
	if s.tokenAuthorized(r) {
		return true
	}
	u, _, ok := s.session(r)
	return ok && u.Admin
}

// limited: Die Anfrage kommt von einem angemeldeten Benutzer ohne Administratorrechte – sie sieht nur
// die Geräte ihrer Benutzergruppen und keine technischen Details.
func (s *Server) limited(r *http.Request) bool {
	if r == nil || !s.usersOn() || isPublic(r) || s.tokenAuthorized(r) {
		return false
	}
	u, _, ok := s.session(r)
	return !ok || !u.Admin
}

// access sagt, welche Geräte eine Anfrage sehen und wecken darf.
type access struct {
	all bool
	ids map[string]bool
}

func (a access) can(id string) bool { return a.all || a.ids[id] }

func (s *Server) access(r *http.Request) access {
	if r == nil || !s.limited(r) {
		return access{all: true}
	}
	u, _, ok := s.session(r)
	if !ok {
		return access{}
	}
	return access{ids: s.store.Access(u.ID)}
}

func (a access) filter(devs []store.Device) []store.Device {
	if a.all {
		return devs
	}
	return slices.DeleteFunc(slices.Clone(devs), func(d store.Device) bool { return !a.ids[d.ID] })
}

// accounts verlangt mit Benutzern eine Anmeldung. Frei bleiben /health, static/, die Anmeldung selbst
// und – wenn "Wecklinks ohne Anmeldung" an ist – die Wecklinks, dann immer mit Schlüssel (needsKey).
func (s *Server) accounts(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.usersOn() || isPublic(r) || s.tokenAuthorized(r) {
			next.ServeHTTP(w, r)
			return
		}
		if _, _, ok := s.session(r); ok {
			next.ServeHTTP(w, r)
			return
		}
		base, p := s.conf().VDir, r.URL.Path
		if p == base+"health" || strings.HasPrefix(p, base+"static/") || p == base+"login" || p == base+"api/v1/login" || p == base+"api/v1/logout" ||
			p == base+"reset" || p == base+"api/v1/password-reset" || p == base+"api/v1/password-reset/confirm" {
			next.ServeHTTP(w, r)
			return
		}
		if s.conf().Auth.PublicWake && (strings.HasPrefix(p, base+"wake/") || strings.HasPrefix(p, base+"wake-group/")) {
			next.ServeHTTP(w, markPublic(r))
			return
		}
		if r.Method == http.MethodGet && wantsHTML(r) && !strings.HasPrefix(p, base+"api/") {
			to := base + "login"
			if p != base {
				to += "?next=" + url.QueryEscape(r.URL.RequestURI())
			}
			http.Redirect(w, r, to, http.StatusSeeOther)
			return
		}
		writeJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "login": true,
			"message": i18n.T(s.lang(r), "err.login_required")})
	})
}

// limitStatus nimmt für Benutzer ohne Administratorrechte die Netzdetails aus einem Prüfergebnis.
func (s *Server) limitStatus(r *http.Request, st status.State) status.State {
	if s.limited(r) {
		st.Conflict, st.IPSource, st.Note = "", "", ""
	}
	return st
}

// --- Anmelden, Abmelden ---

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	if !s.usersOn() {
		http.Redirect(w, r, s.conf().VDir, http.StatusSeeOther)
		return
	}
	s.render(w, r, http.StatusOK, "login.html", pageData{MailReset: s.mailReady()})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &in); err != nil {
		s.storeError(w, r, err)
		return
	}
	if !s.usersOn() {
		writeJSON(w, http.StatusOK, map[string]any{"success": true})
		return
	}
	var u store.User
	ok, wait := s.attempts.try(clientKey(r), func() bool {
		var good bool
		u, good = s.store.CheckLogin(in.Name, in.Password)
		return good
	})
	if wait > 0 {
		s.fail(w, r, http.StatusTooManyRequests, "err.unlock_wait", int(wait.Seconds()+0.999))
		return
	}
	if !ok {
		log.Printf("Login failed from %s", clientKey(r))
		writeJSON(w, http.StatusForbidden, map[string]any{"success": false, "field": "password",
			"message": i18n.T(s.lang(r), "err.login_wrong")})
		return
	}
	log.Printf("Login: %s", u.Name)
	s.setSession(w, r, &u)
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	s.setSession(w, r, nil)
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// --- Mein Konto ---

type userView struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Email  string   `json:"email"`
	Admin  bool     `json:"admin"`
	Groups []string `json:"groups"` // Namen der Benutzergruppen
}

func (s *Server) userView(u store.User, groups []store.UserGroup) userView {
	v := userView{ID: u.ID, Name: u.Name, Email: u.Email, Admin: u.Admin, Groups: []string{}}
	for _, g := range groups {
		if slices.Contains(g.Users, u.ID) {
			v.Groups = append(v.Groups, g.Name)
		}
	}
	return v
}

func (s *Server) accountPage(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.session(r); !ok {
		http.Redirect(w, r, s.conf().VDir, http.StatusSeeOther)
		return
	}
	s.render(w, r, http.StatusOK, "account.html", pageData{})
}

// me liefert den angemeldeten Benutzer; ohne Sitzung 401.
func (s *Server) me(w http.ResponseWriter, r *http.Request) (store.User, bool) {
	u, _, ok := s.session(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "login": true,
			"message": i18n.T(s.lang(r), "err.login_required")})
	}
	return u, ok
}

func (s *Server) getAccount(w http.ResponseWriter, r *http.Request) {
	if u, ok := s.me(w, r); ok {
		writeJSON(w, http.StatusOK, s.userView(u, s.store.UserGroups()))
	}
}

func (s *Server) putAccount(w http.ResponseWriter, r *http.Request) {
	u, ok := s.me(w, r)
	if !ok {
		return
	}
	var in struct {
		Email string `json:"email"`
	}
	if err := readJSON(r, &in); err != nil {
		s.storeError(w, r, err)
		return
	}
	u, err := s.store.SetUserEmail(u.ID, in.Email)
	if err != nil {
		s.userError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.userView(u, s.store.UserGroups()))
}

// putAccountPassword ändert das eigene Passwort; das bisherige muss mit.
func (s *Server) putAccountPassword(w http.ResponseWriter, r *http.Request) {
	u, ok := s.me(w, r)
	if !ok {
		return
	}
	var in struct {
		Current  string `json:"current"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &in); err != nil {
		s.storeError(w, r, err)
		return
	}
	var checked store.User
	good, wait := s.attempts.try(clientKey(r), func() bool {
		var ok bool
		checked, ok = s.store.CheckLogin(u.Name, in.Current)
		return ok && checked.ID == u.ID
	})
	if wait > 0 {
		s.fail(w, r, http.StatusTooManyRequests, "err.unlock_wait", int(wait.Seconds()+0.999))
		return
	}
	if !good {
		writeJSON(w, http.StatusForbidden, map[string]any{"success": false, "field": "current",
			"message": i18n.T(s.lang(r), "err.password_wrong")})
		return
	}
	nu, err := s.store.SetUserPasswordIf(u.ID, checked.Password, in.Password)
	if errors.Is(err, store.ErrPasswordChanged) {
		writeJSON(w, http.StatusForbidden, map[string]any{"success": false, "field": "current",
			"message": i18n.T(s.lang(r), "err.password_wrong")})
		return
	}
	if err != nil {
		s.userError(w, r, err)
		return
	}
	log.Printf("Password changed: %s", nu.Name)
	s.setSession(w, r, &nu) // andere Geräte sind damit abgemeldet, dieses bleibt angemeldet
	s.hub.dropPrivileged()
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// --- Benutzerverwaltung (Administratoren) ---

func (s *Server) userError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, store.ErrUserNotFound):
		s.fail(w, r, http.StatusNotFound, "err.user_not_found")
	case errors.Is(err, store.ErrLastAdmin):
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "field": "admin",
			"message": i18n.T(s.lang(r), "err.last_admin")})
	default:
		s.storeError(w, r, err)
	}
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	groups := s.store.UserGroups()
	out := []userView{}
	for _, u := range s.store.Users() {
		out = append(out, s.userView(u, groups))
	}
	writeJSON(w, http.StatusOK, out)
}

// usersChanged: Rechte können sich geändert haben – offene Verbindungen neu einstufen, Fenster neu laden.
func (s *Server) usersChanged(modeSwitch bool) {
	if modeSwitch {
		s.hub.dropAll() // alle bisherigen Verbindungen liefen ohne Anmeldung
	} else {
		s.hub.dropPrivileged()
	}
	s.hub.publish("settings", struct{}{})
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var in store.UserInput
	if err := readJSON(r, &in); err != nil {
		s.storeError(w, r, err)
		return
	}
	// Ohne Passwort, mit E-Mail und Mailserver: Einladung – der Benutzer setzt sein Passwort selbst.
	invite := in.Password == "" && strings.TrimSpace(in.Email) != "" && s.mailReady()
	if invite {
		in.Password = store.RandomPassword()
	}
	u, first, err := s.store.CreateUserFirst(in, !s.isAdmin(r)) // ohne Admin-Anmeldung nur als erster Benutzer
	if err != nil {
		s.userError(w, r, err)
		return
	}
	log.Printf("User created: %s (admin: %v)", u.Name, u.Admin)
	if first {
		// Wer den ersten Benutzer anlegt, ist es selbst: gleich angemeldet bleiben.
		log.Print("User accounts active: login required, password for changes removed")
		s.setUnlockCookie(w, r, "", true)
		s.setSession(w, r, &u)
	}
	if invite && !first {
		s.sendLink(u, s.lang(r), true)
	}
	s.usersChanged(first)
	v := s.userView(u, s.store.UserGroups())
	writeJSON(w, http.StatusCreated, struct {
		userView
		Invited bool `json:"invited,omitempty"`
	}{v, invite && !first})
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	var in store.UserInput
	if err := readJSON(r, &in); err != nil {
		s.storeError(w, r, err)
		return
	}
	me, _, loggedIn := s.session(r) // vor der Änderung: ein neues Passwort macht das Cookie ungültig
	u, err := s.store.UpdateUser(r.PathValue("id"), in)
	if err != nil {
		s.userError(w, r, err)
		return
	}
	log.Printf("User changed: %s (admin: %v)", u.Name, u.Admin)
	if loggedIn && me.ID == u.ID && in.Password != "" {
		s.setSession(w, r, &u) // eigenes Passwort neu gesetzt: dieser Browser bleibt angemeldet
	}
	s.usersChanged(false)
	writeJSON(w, http.StatusOK, s.userView(u, s.store.UserGroups()))
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteUser(r.PathValue("id")); err != nil {
		s.userError(w, r, err)
		return
	}
	log.Printf("User deleted: %s", r.PathValue("id"))
	s.usersChanged(false)
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// --- Benutzergruppen ---

func (s *Server) listUserGroups(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.store.UserGroups())
}

func (s *Server) createUserGroup(w http.ResponseWriter, r *http.Request) {
	var in store.UserGroupInput
	if err := readJSON(r, &in); err != nil {
		s.storeError(w, r, err)
		return
	}
	g, err := s.store.CreateUserGroup(in)
	if err != nil {
		s.userError(w, r, err)
		return
	}
	log.Printf("User group created: %s", g.Name)
	s.usersChanged(false)
	writeJSON(w, http.StatusCreated, g)
}

func (s *Server) updateUserGroup(w http.ResponseWriter, r *http.Request) {
	var in store.UserGroupInput
	if err := readJSON(r, &in); err != nil {
		s.storeError(w, r, err)
		return
	}
	g, err := s.store.UpdateUserGroup(r.PathValue("id"), in)
	if err != nil {
		s.userError(w, r, err)
		return
	}
	log.Printf("User group changed: %s", g.Name)
	s.usersChanged(false)
	writeJSON(w, http.StatusOK, g)
}

func (s *Server) deleteUserGroup(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteUserGroup(r.PathValue("id")); err != nil {
		s.userError(w, r, err)
		return
	}
	log.Printf("User group deleted: %s", r.PathValue("id"))
	s.usersChanged(false)
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// adminOnly: mit Benutzern nur für Administratoren (sonst 403), ohne Benutzer wie bisher.
func (s *Server) adminOnly(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.usersOn() && !s.isAdmin(r) {
			s.fail(w, r, http.StatusForbidden, "err.admin_only")
			return
		}
		h(w, r)
	}
}

// legacyLock: Das Passwort für Änderungen gibt es mit Benutzern nicht mehr.
func (s *Server) legacyLock(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.usersOn() {
			s.fail(w, r, http.StatusConflict, "err.users_active")
			return
		}
		h(w, r)
	}
}
