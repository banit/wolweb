package store

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// Benutzer und Benutzergruppen (state.json, nie im Export).
//
// Solange es keinen Benutzer gibt, bleibt wolweb wie bisher (Ansehen und Wecken frei, optional das
// Passwort für Änderungen). Mit dem ersten Benutzer – er ist immer Administrator – braucht die
// Oberfläche eine Anmeldung, und das alte Passwort für Änderungen entfällt. Administratoren dürfen
// alles; alle anderen sehen und wecken nur die Geräte ihrer Benutzergruppen – einzeln zugewiesen oder
// über eine Gerätegruppe (dann auch Geräte, die später in die Gruppe kommen).

type User struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Email    string    `json:"email,omitempty"`
	Password string    `json:"password"` // Hash wie beim Passwort für Änderungen (password.go)
	Admin    bool      `json:"admin,omitempty"`
	Created  time.Time `json:"created"`
}

type UserGroup struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Users        []string `json:"users,omitempty"`         // Benutzer-IDs
	Devices      []string `json:"devices,omitempty"`       // Geräte-IDs
	DeviceGroups []string `json:"device_groups,omitempty"` // IDs der Gerätegruppen
}

// UserInput sind die Angaben aus der Benutzerverwaltung. Password leer = unverändert (beim Anlegen Pflicht).
type UserInput struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
	Admin    bool   `json:"admin"`
}

// UserGroupInput sind die Angaben aus dem Dialog einer Benutzergruppe.
type UserGroupInput struct {
	Name         string   `json:"name"`
	Users        []string `json:"users"`
	Devices      []string `json:"devices"`
	DeviceGroups []string `json:"device_groups"`
}

// ErrUserNotFound: Es gibt keinen Benutzer bzw. keine Benutzergruppe mit dieser ID.
var ErrUserNotFound = errors.New("user not found")

// ErrLastAdmin: Der letzte Administrator kann weder gelöscht noch herabgestuft werden.
var ErrLastAdmin = errors.New("last administrator")

const userNameMax, emailMax = 32, 254

func newSecretID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// RandomPassword erzeugt ein Startpasswort (für -reset-user).
func RandomPassword() string {
	b := make([]byte, 9)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func validUserName(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", invalid("name", "val.user_required")
	}
	if utf8.RuneCountInString(s) > userNameMax || strings.ContainsFunc(s, unicode.IsControl) {
		return "", invalid("name", "val.user_name", userNameMax)
	}
	return s, nil
}

func validEmail(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	at := strings.LastIndex(s, "@")
	if len(s) > emailMax || at < 1 || at == len(s)-1 || strings.ContainsFunc(s, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return "", invalid("email", "val.email")
	}
	return s, nil
}

func validNewPassword(pw string) error {
	if n := utf8.RuneCountInString(pw); n < passwordMin || n > passwordMax {
		return invalid("password", "val.password_length", passwordMin, passwordMax)
	}
	return nil
}

func userIndex(users []User, id string) int {
	return slices.IndexFunc(users, func(u User) bool { return u.ID == id })
}

// loginIndex findet einen Benutzer über Anmeldenamen oder E-Mail (ohne Groß-/Kleinschreibung).
func loginIndex(users []User, login string) int {
	login = strings.TrimSpace(login)
	if login == "" {
		return -1
	}
	if i := slices.IndexFunc(users, func(u User) bool { return strings.EqualFold(u.Name, login) }); i >= 0 {
		return i
	}
	return slices.IndexFunc(users, func(u User) bool { return u.Email != "" && strings.EqualFold(u.Email, login) })
}

// checkUnique: Name und E-Mail dürfen keinem anderen Benutzer gehören (auch nicht über Kreuz – beides
// taugt zum Anmelden).
func checkUnique(users []User, name, email, except string) error {
	for _, u := range users {
		if u.ID == except {
			continue
		}
		if strings.EqualFold(u.Name, name) || (u.Email != "" && strings.EqualFold(u.Email, name)) {
			return invalid("name", "val.user_taken", name)
		}
		if email != "" && (strings.EqualFold(u.Email, email) || strings.EqualFold(u.Name, email)) {
			return invalid("email", "val.email_taken", email)
		}
	}
	return nil
}

func admins(users []User) int {
	n := 0
	for _, u := range users {
		if u.Admin {
			n++
		}
	}
	return n
}

// HasUsers sagt, ob die Benutzerverwaltung aktiv ist (mindestens ein Benutzer).
func (s *Store) HasUsers() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.state.Users) > 0
}

// Users liefert alle Benutzer nach Namen sortiert.
func (s *Store) Users() []User {
	s.mu.RLock()
	out := slices.Clone(s.state.Users)
	s.mu.RUnlock()
	slices.SortFunc(out, func(a, b User) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	return out
}

// UserByID liefert einen Benutzer.
func (s *Store) UserByID(id string) (User, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if i := userIndex(s.state.Users, id); i >= 0 {
		return s.state.Users[i], true
	}
	return User{}, false
}

// dummyHash dient dazu, bei unbekanntem Namen genauso lange zu rechnen wie bei einem bekannten –
// sonst verriete die Antwortzeit, welche Namen es gibt.
var (
	dummyOnce sync.Once
	dummyHash string
)

func dummyCheck(pw string) {
	dummyOnce.Do(func() { dummyHash, _ = hashPassword("wolweb-dummy-password") })
	checkPassword(dummyHash, pw)
}

// CheckLogin prüft Anmeldename (oder E-Mail) und Passwort.
func (s *Store) CheckLogin(login, pw string) (User, bool) {
	s.mu.RLock()
	i := loginIndex(s.state.Users, login)
	var u User
	if i >= 0 {
		u = s.state.Users[i]
	}
	s.mu.RUnlock()
	if i < 0 {
		dummyCheck(pw)
		return User{}, false
	}
	return u, checkPassword(u.Password, pw)
}

// CreateUser legt einen Benutzer an. Der erste ist immer Administrator; mit ihm entfällt das Passwort
// für Änderungen (im selben Schreibvorgang).
func (s *Store) CreateUser(in UserInput) (User, error) {
	name, err := validUserName(in.Name)
	if err != nil {
		return User{}, err
	}
	email, err := validEmail(in.Email)
	if err != nil {
		return User{}, err
	}
	if err := validNewPassword(in.Password); err != nil {
		return User{}, err
	}
	hash, err := hashPassword(in.Password)
	if err != nil {
		return User{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := checkUnique(s.state.Users, name, email, ""); err != nil {
		return User{}, err
	}
	u := User{ID: newSecretID(), Name: name, Email: email, Password: hash, Admin: in.Admin || len(s.state.Users) == 0,
		Created: time.Now().UTC()}
	prevUsers, prevPw := s.state.Users, s.state.Password
	s.state.Users = append(slices.Clone(s.state.Users), u)
	s.state.Password = ""
	if err := s.saveState(); err != nil {
		s.state.Users, s.state.Password = prevUsers, prevPw
		return User{}, err
	}
	return u, nil
}

// UpdateUser ändert einen Benutzer (Verwaltung durch einen Administrator). Ein leeres Passwort lässt
// es unverändert; ein neues meldet alle Sitzungen des Benutzers ab.
func (s *Store) UpdateUser(id string, in UserInput) (User, error) {
	name, err := validUserName(in.Name)
	if err != nil {
		return User{}, err
	}
	email, err := validEmail(in.Email)
	if err != nil {
		return User{}, err
	}
	hash := ""
	if in.Password != "" {
		if err := validNewPassword(in.Password); err != nil {
			return User{}, err
		}
		if hash, err = hashPassword(in.Password); err != nil {
			return User{}, err
		}
	}
	return s.changeUser(id, func(users []User, u *User) error {
		if err := checkUnique(users, name, email, id); err != nil {
			return err
		}
		if u.Admin && !in.Admin && admins(users) == 1 {
			return ErrLastAdmin
		}
		u.Name, u.Email, u.Admin = name, email, in.Admin
		if hash != "" {
			u.Password = hash
		}
		return nil
	})
}

// SetUserEmail ändert die E-Mail-Adresse (Mein Konto).
func (s *Store) SetUserEmail(id, email string) (User, error) {
	email, err := validEmail(email)
	if err != nil {
		return User{}, err
	}
	return s.changeUser(id, func(users []User, u *User) error {
		if err := checkUnique(users, u.Name, email, id); err != nil {
			return err
		}
		u.Email = email
		return nil
	})
}

// SetUserPasswordIf setzt ein neues Passwort, wenn der gespeicherte Hash noch expect ist (vorher mit
// dem aktuellen Passwort geprüft). Sonst ErrPasswordChanged.
func (s *Store) SetUserPasswordIf(id, expect, pw string) (User, error) {
	if err := validNewPassword(pw); err != nil {
		return User{}, err
	}
	hash, err := hashPassword(pw)
	if err != nil {
		return User{}, err
	}
	return s.changeUser(id, func(_ []User, u *User) error {
		if u.Password != expect {
			return ErrPasswordChanged
		}
		u.Password = hash
		return nil
	})
}

// ResetUserPassword setzt das Passwort eines Benutzers (Anmeldename oder E-Mail) ohne Rückfrage
// (für -reset-user bei gestopptem Server).
func (s *Store) ResetUserPassword(login, pw string) (User, error) {
	if err := validNewPassword(pw); err != nil {
		return User{}, err
	}
	hash, err := hashPassword(pw)
	if err != nil {
		return User{}, err
	}
	s.mu.RLock()
	i := loginIndex(s.state.Users, login)
	id := ""
	if i >= 0 {
		id = s.state.Users[i].ID
	}
	s.mu.RUnlock()
	if id == "" {
		return User{}, ErrUserNotFound
	}
	return s.changeUser(id, func(_ []User, u *User) error {
		u.Password = hash
		return nil
	})
}

func (s *Store) changeUser(id string, change func(users []User, u *User) error) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := userIndex(s.state.Users, id)
	if i < 0 {
		return User{}, ErrUserNotFound
	}
	next := slices.Clone(s.state.Users)
	if err := change(next, &next[i]); err != nil {
		return User{}, err
	}
	prev := s.state.Users
	s.state.Users = next
	if err := s.saveState(); err != nil {
		s.state.Users = prev
		return User{}, err
	}
	return next[i], nil
}

// DeleteUser löscht einen Benutzer und nimmt ihn aus allen Benutzergruppen.
func (s *Store) DeleteUser(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := userIndex(s.state.Users, id)
	if i < 0 {
		return ErrUserNotFound
	}
	if s.state.Users[i].Admin && admins(s.state.Users) == 1 {
		return ErrLastAdmin
	}
	prevU, prevG := s.state.Users, s.state.UserGroups
	s.state.Users = slices.Delete(slices.Clone(s.state.Users), i, i+1)
	groups := slices.Clone(s.state.UserGroups)
	for j := range groups {
		groups[j].Users = slices.DeleteFunc(slices.Clone(groups[j].Users), func(x string) bool { return x == id })
	}
	s.state.UserGroups = groups
	if err := s.saveState(); err != nil {
		s.state.Users, s.state.UserGroups = prevU, prevG
		return err
	}
	return nil
}

// --- Benutzergruppen ---

// UserGroups liefert alle Benutzergruppen nach Namen sortiert. Verweise auf gelöschte Benutzer,
// Geräte und Gerätegruppen fallen dabei weg.
func (s *Store) UserGroups() []UserGroup {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]UserGroup, 0, len(s.state.UserGroups))
	for _, g := range s.state.UserGroups {
		out = append(out, s.cleanUserGroup(g))
	}
	slices.SortFunc(out, func(a, b UserGroup) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	return out
}

// cleanUserGroup lässt nur Verweise stehen, die es (noch) gibt. Unter s.mu aufrufen.
func (s *Store) cleanUserGroup(g UserGroup) UserGroup {
	g.Users = slices.DeleteFunc(slices.Clone(g.Users), func(id string) bool { return userIndex(s.state.Users, id) < 0 })
	g.Devices = slices.DeleteFunc(slices.Clone(g.Devices), func(id string) bool {
		return !slices.ContainsFunc(s.devices, func(d Device) bool { return d.ID == id })
	})
	g.DeviceGroups = slices.DeleteFunc(slices.Clone(g.DeviceGroups), func(id string) bool {
		return !slices.ContainsFunc(s.groups, func(x Group) bool { return x.ID == id })
	})
	return g
}

func validUserGroupName(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", invalid("name", "val.ugroup_required")
	}
	if utf8.RuneCountInString(s) > userNameMax || strings.ContainsFunc(s, unicode.IsControl) {
		return "", invalid("name", "val.ugroup_name", userNameMax)
	}
	return s, nil
}

// buildUserGroup prüft die Eingabe gegen die vorhandenen Benutzer, Geräte und Gerätegruppen. Unter s.mu.
func (s *Store) buildUserGroup(in UserGroupInput, except string) (UserGroup, error) {
	name, err := validUserGroupName(in.Name)
	if err != nil {
		return UserGroup{}, err
	}
	for _, g := range s.state.UserGroups {
		if g.ID != except && strings.EqualFold(g.Name, name) {
			return UserGroup{}, invalid("name", "val.ugroup_taken", name)
		}
	}
	g := UserGroup{Name: name}
	for _, id := range in.Users {
		if userIndex(s.state.Users, id) < 0 {
			return UserGroup{}, Invalid("val.stale")
		}
		if !slices.Contains(g.Users, id) {
			g.Users = append(g.Users, id)
		}
	}
	for _, id := range in.Devices {
		if !slices.ContainsFunc(s.devices, func(d Device) bool { return d.ID == id }) {
			return UserGroup{}, Invalid("val.stale")
		}
		if !slices.Contains(g.Devices, id) {
			g.Devices = append(g.Devices, id)
		}
	}
	for _, id := range in.DeviceGroups {
		if !slices.ContainsFunc(s.groups, func(x Group) bool { return x.ID == id }) {
			return UserGroup{}, Invalid("val.stale")
		}
		if !slices.Contains(g.DeviceGroups, id) {
			g.DeviceGroups = append(g.DeviceGroups, id)
		}
	}
	return g, nil
}

func (s *Store) CreateUserGroup(in UserGroupInput) (UserGroup, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, err := s.buildUserGroup(in, "")
	if err != nil {
		return UserGroup{}, err
	}
	g.ID = newSecretID()
	prev := s.state.UserGroups
	s.state.UserGroups = append(slices.Clone(s.state.UserGroups), g)
	if err := s.saveState(); err != nil {
		s.state.UserGroups = prev
		return UserGroup{}, err
	}
	return g, nil
}

func (s *Store) UpdateUserGroup(id string, in UserGroupInput) (UserGroup, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.state.UserGroups, func(g UserGroup) bool { return g.ID == id })
	if i < 0 {
		return UserGroup{}, ErrUserNotFound
	}
	g, err := s.buildUserGroup(in, id)
	if err != nil {
		return UserGroup{}, err
	}
	g.ID = id
	prev := s.state.UserGroups
	next := slices.Clone(s.state.UserGroups)
	next[i] = g
	s.state.UserGroups = next
	if err := s.saveState(); err != nil {
		s.state.UserGroups = prev
		return UserGroup{}, err
	}
	return g, nil
}

func (s *Store) DeleteUserGroup(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.state.UserGroups, func(g UserGroup) bool { return g.ID == id })
	if i < 0 {
		return ErrUserNotFound
	}
	prev := s.state.UserGroups
	s.state.UserGroups = slices.Delete(slices.Clone(s.state.UserGroups), i, i+1)
	if err := s.saveState(); err != nil {
		s.state.UserGroups = prev
		return err
	}
	return nil
}

// Access liefert die IDs der Geräte, die ein Benutzer sehen und wecken darf: einzeln zugewiesene und
// alle Geräte der zugewiesenen Gerätegruppen.
func (s *Store) Access(userID string) map[string]bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := map[string]bool{}
	groups := map[string]bool{} // Namen der Gerätegruppen (klein)
	for _, g := range s.state.UserGroups {
		if !slices.Contains(g.Users, userID) {
			continue
		}
		for _, id := range g.Devices {
			ids[id] = true
		}
		for _, gid := range g.DeviceGroups {
			if i := slices.IndexFunc(s.groups, func(x Group) bool { return x.ID == gid }); i >= 0 {
				groups[strings.ToLower(s.groups[i].Name)] = true
			}
		}
	}
	for _, d := range s.devices {
		if d.Group != "" && groups[strings.ToLower(d.Group)] {
			ids[d.ID] = true
		}
	}
	return ids
}
