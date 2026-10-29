package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPassword(t *testing.T) {
	PasswordRounds = 1000
	dir := t.TempDir()
	s, _, err := Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if s.HasPassword() || s.CheckPassword("") {
		t.Fatal("neuer Speicher hat ein Passwort")
	}
	var fe ValidationError
	if err := s.SetPassword("kurz"); !errors.As(err, &fe) || fe.Field != "password" {
		t.Fatalf("zu kurz: %v", err)
	}
	if err := s.SetPassword("geheim123"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "state.json"))
	if strings.Contains(string(b), "geheim123") || !strings.Contains(string(b), "pbkdf2-sha256$1000$") {
		t.Fatalf("state.json: %s", b)
	}
	// Nach dem Neustart gilt es weiter.
	s, _, err = Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if !s.CheckPassword("geheim123") || s.CheckPassword("geheim12") || s.CheckPassword("") {
		t.Fatal("Prüfung nach dem Neustart falsch")
	}
	stamp := s.PasswordStamp()
	s.SetPassword("geheim123") // gleiches Passwort, neues Salz
	if s.PasswordStamp() == stamp {
		t.Fatal("neues Passwort ohne neuen Stempel")
	}
	if err := s.SetPassword(""); err != nil || s.HasPassword() || s.CheckPassword("geheim123") {
		t.Fatalf("entfernen: %v", err)
	}
}
