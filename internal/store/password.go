package store

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Passwort für Änderungen (Bearbeitungsschutz, Einstellungsseite). Gespeichert wird nur ein Hash in
// state.json: PBKDF2-SHA256 mit zufälligem Salz, "pbkdf2-sha256$<Runden>$<Salz>$<Hash>" (hex).

// PasswordRounds ist die Zahl der PBKDF2-Runden für neue Passwörter (Tests setzen sie herab).
var PasswordRounds = 600_000

const passwordMin, passwordMax = 8, 128

func hashPassword(pw string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, pw, salt, PasswordRounds, 32)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("pbkdf2-sha256$%d$%x$%x", PasswordRounds, salt, key), nil
}

func checkPassword(stored, pw string) bool {
	parts := strings.Split(stored, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	rounds, err := strconv.Atoi(parts[1])
	salt, err1 := hex.DecodeString(parts[2])
	want, err2 := hex.DecodeString(parts[3])
	if err != nil || err1 != nil || err2 != nil || rounds < 1 || len(want) == 0 {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, pw, salt, rounds, len(want))
	return err == nil && subtle.ConstantTimeCompare(got, want) == 1
}

// HasPassword sagt, ob ein Passwort für Änderungen gesetzt ist.
func (s *Store) HasPassword() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state.Password != ""
}

// PasswordStamp ändert sich mit jedem neuen Passwort – daraus abgeleitete Entsperrungen gelten
// dann nicht mehr. Leer ohne Passwort.
func (s *Store) PasswordStamp() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state.Password
}

// CheckPassword prüft pw gegen das gespeicherte Passwort (false, wenn keins gesetzt ist).
func (s *Store) CheckPassword(pw string) bool {
	_, ok := s.CheckPasswordStamp(pw)
	return ok
}

// CheckPasswordStamp prüft pw und liefert den geprüften Stand (PasswordStamp). Wer daraus etwas
// ableitet (Entsperrung, Passwortwechsel), bindet es an genau diesen Stand – ein gleichzeitig
// gesetztes neues Passwort lässt sich so nicht mit dem alten erschleichen.
func (s *Store) CheckPasswordStamp(pw string) (stamp string, ok bool) {
	s.mu.RLock()
	stored := s.state.Password
	s.mu.RUnlock()
	return stored, stored != "" && checkPassword(stored, pw)
}

// ErrPasswordChanged meldet, dass sich das Passwort seit der Prüfung geändert hat.
var ErrPasswordChanged = errors.New("password changed meanwhile")

// SetPassword setzt das Passwort für Änderungen ohne Rücksicht auf den bisherigen Stand (für
// -reset-password); leer entfernt es. Gespeichert wird sofort.
func (s *Store) SetPassword(pw string) error {
	_, err := s.setPassword(pw, false, "")
	return err
}

// SetPasswordIf setzt das Passwort nur, wenn der Stand noch expect ist (vorher geprüft bzw. ""
// für "noch keins gesetzt"), und liefert den neuen Stand. Sonst ErrPasswordChanged.
func (s *Store) SetPasswordIf(expect, pw string) (stamp string, err error) {
	return s.setPassword(pw, true, expect)
}

func (s *Store) setPassword(pw string, check bool, expect string) (string, error) {
	hash := ""
	if pw != "" {
		if n := utf8.RuneCountInString(pw); n < passwordMin || n > passwordMax {
			return "", invalid("password", "val.password_length", passwordMin, passwordMax)
		}
		var err error
		if hash, err = hashPassword(pw); err != nil {
			return "", err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if check && s.state.Password != expect {
		return "", ErrPasswordChanged
	}
	prev := s.state.Password
	s.state.Password = hash
	if err := s.saveState(); err != nil {
		s.state.Password = prev
		return "", err
	}
	return hash, nil
}
