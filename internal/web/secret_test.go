package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"wolweb/internal/store"
)

// Eine Entsperrung, deren Prüfung noch das alte Passwort sah, darf nach einem gleichzeitigen
// Passwortwechsel nicht gelten (Prüfen -> Wechseln -> Ausstellen).
func TestUnlockBoundToCheckedPassword(t *testing.T) {
	s, h, _ := settingsServer(t, `{"port": 8089}`)
	owner := &browser{t: t, h: h}
	if rec := owner.do("PUT", "/api/v1/password", `{"password":"alt12345"}`); rec.Code != http.StatusOK {
		t.Fatalf("Passwort setzen: %d %s", rec.Code, rec.Body)
	}
	stamp, ok := s.store.CheckPasswordStamp("alt12345")
	if !ok {
		t.Fatal("altes Passwort nicht erkannt")
	}
	if err := s.store.SetPassword("neu123456"); err != nil {
		t.Fatal(err)
	}
	old := &browser{t: t, h: h, cookie: &http.Cookie{Name: unlockCookie, Value: s.unlockTokenFor(stamp, time.Now().Add(unlockIdle))}}
	if rec := old.do("POST", "/api/v1/devices", `{"name":"PC","mac":"02:00:00:00:00:02"}`); !lockedBody(t, rec) {
		t.Fatalf("Entsperrung mit altem Passwort gilt: %d %s", rec.Code, rec.Body)
	}
	// Die Entsperrung über die Route prüft den Stand nach der Prüfung: altes Passwort scheitert.
	if rec := (&browser{t: t, h: h}).do("POST", "/api/v1/unlock", `{"password":"alt12345"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("altes Passwort entsperrt: %d", rec.Code)
	}
}

// Zwei gleichzeitige Wechsel mit demselben bisherigen Passwort: nur einer gewinnt.
func TestSetPasswordIfRejectsStaleStamp(t *testing.T) {
	s, _, _ := settingsServer(t, `{"port": 8089}`)
	if _, err := s.store.SetPasswordIf("", "alt12345"); err != nil {
		t.Fatal(err)
	}
	stamp, _ := s.store.CheckPasswordStamp("alt12345")
	if _, err := s.store.SetPasswordIf(stamp, "erstes123"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.SetPasswordIf(stamp, "zweites123"); !errors.Is(err, store.ErrPasswordChanged) {
		t.Fatalf("veralteter Stand akzeptiert: %v", err)
	}
	if !s.store.CheckPassword("erstes123") {
		t.Fatal("erster Wechsel überschrieben")
	}
	// Ohne Passwort setzt nur, wer noch keins vorfindet.
	if _, err := s.store.SetPasswordIf("", "drittes123"); !errors.Is(err, store.ErrPasswordChanged) {
		t.Fatalf("Erstsetzen über bestehendes Passwort: %v", err)
	}
}

// Das exportierte Geheimnis reicht nicht, um watch-Werte oder Entsperrungen nachzubilden.
func TestExportedSecretCannotForgeWatch(t *testing.T) {
	s, h, d := privacyServer(t, "unlocked")
	admin := &browser{t: t, h: h}
	admin.do("PUT", "/api/v1/password", `{"password":"geheim123"}`)
	rec := admin.do("GET", "/api/v1/export", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("Export: %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "local_secret") {
		t.Fatal("lokales Geheimnis im Export")
	}
	var exp struct {
		Secret string `json:"secret"`
	}
	json.Unmarshal(rec.Body.Bytes(), &exp)
	key, err := hex.DecodeString(exp.Secret)
	if err != nil || len(key) == 0 {
		t.Fatalf("secret im Export: %q", exp.Secret)
	}
	until := strconv.FormatInt(time.Now().Add(10*time.Minute).Unix(), 10)
	m := hmac.New(sha256.New, key)
	m.Write([]byte("watch|device:" + strings.ToLower(d.ID) + "|" + until))
	forged := until + "." + hex.EncodeToString(m.Sum(nil))[:32]
	guest := &browser{t: t, h: h}
	if rec := guest.do("GET", "/wake/NAS/status?watch="+forged, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("watch-Wert aus dem Exportgeheimnis angenommen: %d %s", rec.Code, rec.Body)
	}
	// Ein echter watch-Wert gilt weiter.
	if rec := guest.do("GET", "/wake/NAS/status?watch="+s.watchToken("device:"+d.ID), ""); rec.Code != http.StatusOK {
		t.Fatalf("echter watch-Wert: %d", rec.Code)
	}
}
