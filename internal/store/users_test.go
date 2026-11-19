package store

import (
	"errors"
	"testing"

	"wolweb/internal/i18n"
)

func TestUsersStore(t *testing.T) {
	PasswordRounds = 1000
	dir := t.TempDir()
	s, _, err := Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	a, _ := s.Create(Input{Name: "A", MAC: "02:00:00:00:00:01", Group: "Büro"})
	b, _ := s.Create(Input{Name: "B", MAC: "02:00:00:00:00:02"})
	if err := s.SetPassword("altes-passwort"); err != nil {
		t.Fatal(err)
	}
	chef, err := s.CreateUser(UserInput{Name: "chef", Password: "chef-passwort"})
	if err != nil || !chef.Admin {
		t.Fatalf("erster Benutzer: %+v %v", chef, err)
	}
	if s.HasPassword() {
		t.Fatal("Passwort für Änderungen nicht entfernt")
	}
	if _, err := s.CreateUser(UserInput{Name: "CHEF", Password: "irgendwas1"}); err == nil {
		t.Fatal("doppelter Name angenommen")
	}
	if _, err := s.CreateUser(UserInput{Name: "x", Password: "kurz"}); err == nil {
		t.Fatal("zu kurzes Passwort angenommen")
	}
	bob, err := s.CreateUser(UserInput{Name: "bob", Email: "bob@example.org", Password: "bob-passwort"})
	if err != nil || bob.Admin {
		t.Fatalf("bob: %+v %v", bob, err)
	}
	if u, ok := s.CheckLogin("Bob@Example.org", "bob-passwort"); !ok || u.ID != bob.ID {
		t.Fatal("Anmeldung per E-Mail")
	}
	if _, ok := s.CheckLogin("niemand", "bob-passwort"); ok {
		t.Fatal("unbekannter Name angemeldet")
	}
	if err := s.DeleteUser(chef.ID); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("letzter Administrator gelöscht: %v", err)
	}

	g, _ := s.FindGroup("Büro")
	if g.ID == "" {
		t.Fatal("Gerätegruppe ohne ID")
	}
	ug, err := s.CreateUserGroup(UserGroupInput{Name: "Familie", Users: []string{bob.ID}, DeviceGroups: []string{g.ID}, Devices: []string{b.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if acc := s.Access(bob.ID); !acc[a.ID] || !acc[b.ID] || len(acc) != 2 {
		t.Fatalf("Freigaben: %v", acc)
	}
	if acc := s.Access(chef.ID); len(acc) != 0 {
		t.Fatalf("chef ohne Gruppe: %v", acc)
	}

	// Nach einem Neustart: dieselben IDs, Benutzer und Freigaben.
	s2, _, err := Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if g2, _ := s2.FindGroup("Büro"); g2.ID != g.ID {
		t.Fatalf("ID der Gerätegruppe nach Neustart: %q statt %q", g2.ID, g.ID)
	}
	if acc := s2.Access(bob.ID); !acc[a.ID] {
		t.Fatal("Freigabe nach Neustart weg")
	}
	// Ein Import bringt eigene IDs mit – Freigaben hängen nie an einer importierten Gruppe.
	exp, _ := s2.Export()
	other, _, _ := Open(t.TempDir(), "")
	if _, err := other.Import(exp, i18n.DE); err != nil {
		t.Fatal(err)
	}
	if gi, _ := other.FindGroup("Büro"); gi.ID == "" || gi.ID == g.ID {
		t.Fatalf("importierte Gruppe: ID %q", gi.ID)
	}

	// Gelöschter Benutzer verschwindet aus der Gruppe.
	if err := s2.DeleteUser(bob.ID); err != nil {
		t.Fatal(err)
	}
	for _, x := range s2.UserGroups() {
		if x.ID == ug.ID && len(x.Users) != 0 {
			t.Fatalf("gelöschter Benutzer noch Mitglied: %+v", x)
		}
	}
	if _, err := s2.ResetUserPassword("chef", "neues-passwort"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s2.CheckLogin("chef", "neues-passwort"); !ok {
		t.Fatal("zurückgesetztes Passwort gilt nicht")
	}
}
