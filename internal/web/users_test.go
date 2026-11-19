package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"wolweb/internal/config"
)

// session ist ein Browser mit Anmelde-Cookie (Benutzerverwaltung).
type session struct {
	t      *testing.T
	h      http.Handler
	cookie *http.Cookie
}

func (b *session) do(method, url, body string) *httptest.ResponseRecorder {
	b.t.Helper()
	req := httptest.NewRequest(method, url, strings.NewReader(body))
	req.Header.Set("X-WolWeb", "1")
	req.Header.Set("Accept", "application/json")
	if b.cookie != nil {
		req.AddCookie(b.cookie)
	}
	rec := httptest.NewRecorder()
	b.h.ServeHTTP(rec, req)
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie {
			if b.cookie = c; c.MaxAge < 0 {
				b.cookie = nil
			}
		}
	}
	return rec
}

func (b *session) want(code int, method, url, body string) []byte {
	b.t.Helper()
	rec := b.do(method, url, body)
	if rec.Code != code {
		b.t.Fatalf("%s %s: %d statt %d: %s", method, url, rec.Code, code, rec.Body)
	}
	return rec.Body.Bytes()
}

func idOf(t *testing.T, b []byte) string {
	t.Helper()
	var v struct{ ID string }
	if err := json.Unmarshal(b, &v); err != nil || v.ID == "" {
		t.Fatalf("keine ID in %s", b)
	}
	return v.ID
}

func TestUsersAndUserGroups(t *testing.T) {
	sink := udpSink(t)
	h, st := newTestServer(t, func(c *config.Config) { c.Auth.PublicWake = true })
	admin := &session{t: t, h: h}

	// Ohne Benutzer wie bisher: Geräte anlegen geht ohne Anmeldung; ein altes Passwort für Änderungen
	// wird mit dem ersten Benutzer entfernt.
	a := idOf(t, admin.want(http.StatusCreated, "POST", "/api/v1/devices", `{"name":"A","mac":"02:00:00:00:00:01","group":"Büro","broadcast":"`+sink+`"}`))
	admin.want(http.StatusCreated, "POST", "/api/v1/devices", `{"name":"B","mac":"02:00:00:00:00:02","group":"Büro","broadcast":"`+sink+`"}`)
	c := idOf(t, admin.want(http.StatusCreated, "POST", "/api/v1/devices", `{"name":"C","mac":"02:00:00:00:00:03","broadcast":"`+sink+`"}`))
	if err := st.SetPassword("altes-passwort"); err != nil {
		t.Fatal(err)
	}
	// Den ersten Benutzer legt an, wer ändern darf – mit altem Passwort also nur entsperrt.
	if rec := admin.do("POST", "/api/v1/users", `{"name":"chef","password":"chef-passwort"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("erster Benutzer ohne Entsperren: %d", rec.Code)
	}
	st.SetPassword("")
	body := admin.want(http.StatusCreated, "POST", "/api/v1/users", `{"name":"chef","email":"chef@example.org","password":"chef-passwort"}`)
	if !strings.Contains(string(body), `"admin":true`) || admin.cookie == nil {
		t.Fatalf("erster Benutzer: %s (Cookie %v)", body, admin.cookie)
	}
	if st.HasPassword() || !st.HasUsers() {
		t.Fatal("Benutzerverwaltung nicht aktiv")
	}

	// Ohne Anmeldung: API 401, Seiten leiten zur Anmeldung, /health und Anmeldeseite frei.
	anon := &session{t: t, h: h}
	anon.want(http.StatusUnauthorized, "GET", "/api/v1/devices", "")
	req := httptest.NewRequest("GET", "/settings", nil)
	req.Header.Set("Accept", "text/html")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), "/login") {
		t.Fatalf("Seite ohne Anmeldung: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	anon.want(http.StatusOK, "GET", "/health", "")
	anon.want(http.StatusOK, "GET", "/login", "")
	admin.want(http.StatusConflict, "POST", "/api/v1/unlock", `{"password":"x"}`)
	// Wecklinks ohne Anmeldung nur mit Schlüssel.
	anon.want(http.StatusNotFound, "GET", "/wake/A", "")
	dev, _ := st.Get(a)
	anon.want(http.StatusOK, "GET", "/wake/A?key="+dev.WakeKey, "")

	// Benutzer bob bekommt die Gerätegruppe "Büro" freigegeben.
	bob := idOf(t, admin.want(http.StatusCreated, "POST", "/api/v1/users", `{"name":"bob","password":"bob-passwort"}`))
	var groups []struct{ ID, Name string }
	json.Unmarshal(admin.want(http.StatusOK, "GET", "/api/v1/groups", ""), &groups)
	if len(groups) != 1 || groups[0].ID == "" {
		t.Fatalf("Gerätegruppen: %+v", groups)
	}
	ug := idOf(t, admin.want(http.StatusCreated, "POST", "/api/v1/user-groups",
		`{"name":"Familie","users":["`+bob+`"],"device_groups":["`+groups[0].ID+`"]}`))

	user := &session{t: t, h: h}
	user.want(http.StatusForbidden, "POST", "/api/v1/login", `{"name":"bob","password":"falsch-falsch"}`)
	user.want(http.StatusOK, "POST", "/api/v1/login", `{"name":"BOB","password":"bob-passwort"}`)
	if user.cookie == nil {
		t.Fatal("kein Anmelde-Cookie")
	}
	list := string(user.want(http.StatusOK, "GET", "/api/v1/devices", ""))
	if !strings.Contains(list, `"name":"A"`) || !strings.Contains(list, `"name":"B"`) || strings.Contains(list, `"name":"C"`) {
		t.Fatalf("Geräte für bob: %s", list)
	}
	if strings.Contains(list, "wake_key") || strings.Contains(list, "02:00:00") {
		t.Fatalf("technische Details für bob: %s", list)
	}
	if g := string(user.want(http.StatusOK, "GET", "/api/v1/groups", "")); !strings.Contains(g, "Büro") || strings.Contains(g, `"key"`) {
		t.Fatalf("Gruppen für bob: %s", g)
	}
	user.want(http.StatusOK, "POST", "/api/v1/devices/"+a+"/wake", "")
	user.want(http.StatusNotFound, "POST", "/api/v1/devices/"+c+"/wake", "")
	user.want(http.StatusNotFound, "GET", "/api/v1/devices/"+c+"/status", "")
	user.want(http.StatusNotFound, "GET", "/wake/C", "")
	admin.want(http.StatusOK, "POST", "/api/v1/devices/"+c+"/wake", "")
	if hist := string(user.want(http.StatusOK, "GET", "/api/v1/history", "")); strings.Contains(hist, `"device":"C"`) || !strings.Contains(hist, `"device":"A"`) {
		t.Fatalf("Verlauf für bob: %s", hist)
	}
	for _, r := range [][2]string{{"POST", "/api/v1/devices"}, {"POST", "/api/v1/discovery/scan"}, {"GET", "/api/v1/users"},
		{"GET", "/api/v1/events"}, {"GET", "/metrics"}, {"GET", "/api/v1/export"}, {"PUT", "/api/v1/users/" + bob}} {
		if rec := user.do(r[0], r[1], `{}`); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s für bob: %d", r[0], r[1], rec.Code)
		}
	}
	var info struct{ Users, Admin, Locked, Live bool }
	json.Unmarshal(user.want(http.StatusOK, "GET", "/api/v1/info", ""), &info)
	if !info.Users || info.Admin || !info.Locked || info.Live {
		t.Fatalf("info für bob: %+v", info)
	}

	// Umbenennen der Gerätegruppe ändert nichts an der Freigabe; gelöscht und gleichnamig neu angelegt
	// gibt es keine Freigabe mehr.
	admin.want(http.StatusOK, "PUT", "/api/v1/groups/B%C3%BCro", `{"name":"Arbeit"}`)
	if l := string(user.want(http.StatusOK, "GET", "/api/v1/devices", "")); !strings.Contains(l, `"name":"A"`) {
		t.Fatalf("nach dem Umbenennen: %s", l)
	}
	admin.want(http.StatusOK, "DELETE", "/api/v1/groups/Arbeit", "")
	admin.want(http.StatusCreated, "POST", "/api/v1/groups", `{"name":"Arbeit","members":["`+a+`"]}`)
	if l := string(user.want(http.StatusOK, "GET", "/api/v1/devices", "")); l != "[]\n" {
		t.Fatalf("neue gleichnamige Gruppe gibt frei: %s", l)
	}
	admin.want(http.StatusOK, "PUT", "/api/v1/user-groups/"+ug, `{"name":"Familie","users":["`+bob+`"],"devices":["`+c+`"]}`)
	if l := string(user.want(http.StatusOK, "GET", "/api/v1/devices", "")); !strings.Contains(l, `"name":"C"`) {
		t.Fatalf("einzelnes Gerät: %s", l)
	}

	// Mein Konto: E-Mail und Passwort; ein neues Passwort meldet andere Sitzungen ab.
	user.want(http.StatusOK, "PUT", "/api/v1/account", `{"email":"bob@example.org"}`)
	user.want(http.StatusBadRequest, "PUT", "/api/v1/account", `{"email":"chef@example.org"}`)
	other := &session{t: t, h: h, cookie: user.cookie}
	user.want(http.StatusForbidden, "PUT", "/api/v1/account/password", `{"current":"falsch-falsch","password":"neu-passwort"}`)
	user.want(http.StatusOK, "PUT", "/api/v1/account/password", `{"current":"bob-passwort","password":"neu-passwort"}`)
	user.want(http.StatusOK, "GET", "/api/v1/account", "")
	other.want(http.StatusUnauthorized, "GET", "/api/v1/account", "")
	(&session{t: t, h: h}).want(http.StatusOK, "POST", "/api/v1/login", `{"name":"bob@example.org","password":"neu-passwort"}`)

	// Den letzten Administrator gibt es nicht zu löschen oder herabzustufen.
	var me struct{ ID string }
	json.Unmarshal(admin.want(http.StatusOK, "GET", "/api/v1/account", ""), &me)
	admin.want(http.StatusBadRequest, "DELETE", "/api/v1/users/"+me.ID, "")
	admin.want(http.StatusBadRequest, "PUT", "/api/v1/users/"+me.ID, `{"name":"chef","admin":false}`)
	admin.want(http.StatusOK, "PUT", "/api/v1/users/"+bob, `{"name":"bob","admin":true}`)
	user.want(http.StatusOK, "GET", "/api/v1/users", "")
	admin.want(http.StatusOK, "DELETE", "/api/v1/users/"+bob, "")
	user.want(http.StatusUnauthorized, "GET", "/api/v1/devices", "")
	user.want(http.StatusOK, "POST", "/api/v1/logout", "")
}
