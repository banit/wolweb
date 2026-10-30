package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"wolweb/internal/config"
	"wolweb/internal/store"
)

func TestGroupAPI(t *testing.T) {
	h, st := newTestServer(t, func(c *config.Config) {
		c.Auth.Username, c.Auth.Password, c.Auth.WakeKeys = "admin", "geheim", true
	})
	sink := udpSink(t)
	a, _ := st.Create(store.Input{Name: "A", MAC: "02:00:00:00:00:01", Broadcast: sink})
	b, _ := st.Create(store.Input{Name: "B", MAC: "02:00:00:00:00:02", Broadcast: sink})
	do := func(method, path, body string, auth bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("X-WolWeb", "1")
		req.Header.Set("Accept-Language", "de")
		if auth {
			req.SetBasicAuth("admin", "geheim")
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	groups := func() []groupView {
		var out []groupView
		json.Unmarshal(do("GET", "/api/v1/groups", "", true).Body.Bytes(), &out)
		return out
	}

	// Leere Gruppe anlegen, dann eine mit Mitgliedern
	if rec := do("POST", "/api/v1/groups", `{"name":"Leer"}`, true); rec.Code != http.StatusCreated {
		t.Fatalf("anlegen: %d %s", rec.Code, rec.Body)
	}
	if rec := do("POST", "/api/v1/groups", `{"name":"leer"}`, true); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "schon eine Gruppe") {
		t.Fatalf("doppelt: %d %s", rec.Code, rec.Body)
	}
	if rec := do("POST", "/api/v1/groups", `{"name":"Büro","members":["`+a.ID+`","`+b.ID+`"]}`, true); rec.Code != http.StatusCreated {
		t.Fatalf("mit Mitgliedern: %d %s", rec.Code, rec.Body)
	}
	g := groups()
	if len(g) != 2 || g[0].Name != "Leer" || len(g[0].Members) != 0 || g[1].Name != "Büro" || len(g[1].Members) != 2 {
		t.Fatalf("Liste: %+v", g)
	}
	oldKey := g[1].Key

	// Umbenennen: der alte Link (mit seinem Schlüssel) weckt weiter
	if rec := do("PUT", "/api/v1/groups/B%C3%BCro", `{"name":"Office","members":["`+a.ID+`"]}`, true); rec.Code != http.StatusOK {
		t.Fatalf("umbenennen: %d %s", rec.Code, rec.Body)
	}
	if rec := do("POST", "/wake-group/B%C3%BCro?key="+oldKey, "", false); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"group":"Office"`) {
		t.Fatalf("alter Link: %d %s", rec.Code, rec.Body)
	}
	if rec := do("POST", "/wake-group/Office?key="+oldKey, "", false); rec.Code != http.StatusNotFound {
		t.Fatalf("neuer Name mit altem Schlüssel: %d", rec.Code)
	}
	if rec := do("PUT", "/api/v1/groups/Gibtsnicht", `{"name":"X"}`, true); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "Gruppe nicht gefunden") {
		t.Fatalf("unbekannt: %d %s", rec.Code, rec.Body)
	}

	// Reihenfolge: Gruppen tauschen, B in die Gruppe Leer
	if rec := do("PUT", "/api/v1/order", `{"groups":["Office","Leer"],"devices":[{"group":"Leer","ids":["`+b.ID+`"]}]}`, true); rec.Code != http.StatusOK {
		t.Fatalf("Reihenfolge: %d %s", rec.Code, rec.Body)
	}
	g = groups()
	if g[0].Name != "Office" || g[1].Name != "Leer" || len(g[1].Members) != 1 || g[1].Members[0].Order != 1 {
		t.Fatalf("nach dem Sortieren: %+v", g)
	}
	if rec := do("PUT", "/api/v1/order", `{"groups":["Weg"]}`, true); rec.Code != http.StatusBadRequest {
		t.Fatalf("unbekannte Gruppe: %d %s", rec.Code, rec.Body)
	}

	// Löschen: Geräte bleiben
	if rec := do("DELETE", "/api/v1/groups/Leer", "", true); rec.Code != http.StatusOK {
		t.Fatalf("löschen: %d %s", rec.Code, rec.Body)
	}
	if d, _ := st.Get(b.ID); d.Group != "" {
		t.Fatalf("B noch in der Gruppe %q", d.Group)
	}
	if len(groups()) != 1 {
		t.Fatalf("Gruppe nicht gelöscht: %+v", groups())
	}
}

func TestGroupAPILocked(t *testing.T) {
	_, h, _ := settingsServer(t, `{"port": 8089}`)
	b := &browser{t: t, h: h}
	b.do("PUT", "/api/v1/password", `{"password":"geheim123"}`)
	guest := &browser{t: t, h: h}
	for _, c := range []struct{ method, url, body string }{
		{"POST", "/api/v1/groups", `{"name":"X"}`},
		{"PUT", "/api/v1/groups/X", `{"name":"Y"}`},
		{"DELETE", "/api/v1/groups/X", ""},
		{"PUT", "/api/v1/order", `{"groups":[]}`},
	} {
		if rec := guest.do(c.method, c.url, c.body); !lockedBody(t, rec) {
			t.Errorf("%s %s: %d", c.method, c.url, rec.Code)
		}
	}
	if rec := b.do("POST", "/api/v1/groups", `{"name":"X"}`); rec.Code != http.StatusCreated {
		t.Fatalf("entsperrt: %d %s", rec.Code, rec.Body)
	}
}

// Eine gelöschte Gruppe nimmt ihre Wecklinks mit: Eine neue gleichnamige Gruppe hat einen anderen
// Schlüssel, und "Schlüssel erneuern" macht bisherige Links ungültig.
func TestGroupKeyBoundToGroup(t *testing.T) {
	h, st := newTestServer(t, func(c *config.Config) {
		c.Auth.Username, c.Auth.Password, c.Auth.WakeKeys = "admin", "geheim", true
	})
	sink := udpSink(t)
	a, _ := st.Create(store.Input{Name: "A", MAC: "02:00:00:00:00:01", Broadcast: sink})
	b, _ := st.Create(store.Input{Name: "B", MAC: "02:00:00:00:00:02", Broadcast: sink})
	do := func(method, path, body string, auth bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("X-WolWeb", "1")
		if auth {
			req.SetBasicAuth("admin", "geheim")
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	key := func(name string) string {
		var out []groupView
		json.Unmarshal(do("GET", "/api/v1/groups", "", true).Body.Bytes(), &out)
		for _, g := range out {
			if strings.EqualFold(g.Name, name) {
				return g.Key
			}
		}
		t.Fatalf("Gruppe %s fehlt", name)
		return ""
	}
	do("POST", "/api/v1/groups", `{"name":"Gast","members":["`+a.ID+`"]}`, true)
	old := key("Gast")
	if rec := do("POST", "/wake-group/Gast?key="+old, "", false); rec.Code != http.StatusOK {
		t.Fatalf("Link vor dem Löschen: %d %s", rec.Code, rec.Body)
	}
	do("DELETE", "/api/v1/groups/Gast", "", true)
	if rec := do("POST", "/api/v1/groups", `{"name":"gast","members":["`+b.ID+`"]}`, true); rec.Code != http.StatusCreated {
		t.Fatalf("neu anlegen: %d %s", rec.Code, rec.Body)
	}
	if key("gast") == old {
		t.Fatal("neue gleichnamige Gruppe hat den alten Schlüssel")
	}
	if rec := do("POST", "/wake-group/Gast?key="+old, "", false); rec.Code != http.StatusNotFound {
		t.Fatalf("alter Link weckt die neue Gruppe: %d %s", rec.Code, rec.Body)
	}
	// Erneuern: neuer Schlüssel, alter gilt nicht mehr.
	cur := key("gast")
	if rec := do("POST", "/api/v1/groups/gast/wake-key", "", true); rec.Code != http.StatusOK {
		t.Fatalf("erneuern: %d %s", rec.Code, rec.Body)
	}
	if next := key("gast"); next == cur || next == "" {
		t.Fatalf("Schlüssel nicht erneuert: %q", next)
	}
	if rec := do("POST", "/wake-group/gast?key="+cur, "", false); rec.Code != http.StatusNotFound {
		t.Fatalf("alter Schlüssel nach dem Erneuern: %d", rec.Code)
	}
	if rec := do("POST", "/api/v1/groups/gast/wake-key", "", false); rec.Code != http.StatusUnauthorized {
		t.Fatalf("erneuern ohne Anmeldung: %d", rec.Code)
	}
}
