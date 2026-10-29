package web

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"wolweb/internal/i18n"
	"wolweb/internal/status"
	"wolweb/internal/store"
)

// privacyServer startet einen Server mit Einstellung online_status und einem Gerät, das online ist
// (es antwortet auf einem lokalen Port).
func privacyServer(t *testing.T, mode string) (*Server, http.Handler, store.Device) {
	t.Helper()
	s, h, _ := settingsServer(t, `{"privacy": {"online_status": "`+mode+`"}}`)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	port := l.Addr().(*net.TCPAddr).Port
	d, err := s.store.Create(store.Input{Name: "NAS", MAC: "02:00:00:00:00:01", IP: "127.0.0.1", Ports: []int{port}, Broadcast: udpSink(t), Group: "Büro"})
	if err != nil {
		t.Fatal(err)
	}
	if st := s.monitor.Check(context.Background(), d); st.State != "online" {
		t.Fatalf("Testgerät nicht online: %+v", st)
	}
	return s, h, d
}

// seesStatus prüft alle Stellen, die den Online-Status zeigen, aus Sicht von b.
func seesStatus(t *testing.T, b *browser, d store.Device) map[string]bool {
	t.Helper()
	got := map[string]bool{}
	got["devices"] = strings.Contains(b.do("GET", "/api/v1/devices", "").Body.String(), `"status"`)
	got["groups"] = strings.Contains(b.do("GET", "/api/v1/groups", "").Body.String(), `"status"`)
	got["device status"] = b.do("GET", "/api/v1/devices/"+d.ID+"/status", "").Code == http.StatusOK
	got["link status"] = b.do("GET", "/wake/NAS/status", "").Code == http.StatusOK
	got["group status"] = b.do("GET", "/wake-group/B%C3%BCro/status", "").Code == http.StatusOK
	got["metrics"] = strings.Contains(b.do("GET", "/metrics", "").Body.String(), "wolweb_device_up{")
	var info struct {
		StatusHidden bool `json:"status_hidden"`
	}
	json.Unmarshal(b.do("GET", "/api/v1/info", "").Body.Bytes(), &info)
	got["info"] = !info.StatusHidden
	page := b.do("GET", "/wake/NAS?format=html", "").Body.String()
	got["wake page"] = strings.Contains(page, `data-has-ip="1"`) && !strings.Contains(page, `data-status-hidden="1"`)
	return got
}

func allSame(t *testing.T, who string, got map[string]bool, want bool) {
	t.Helper()
	for k, v := range got {
		if v != want {
			t.Errorf("%s, %s: sieht Status = %v, erwartet %v", who, k, v, want)
		}
	}
}

func TestOnlineStatusForUnlockedOnly(t *testing.T) {
	_, h, d := privacyServer(t, "unlocked")
	// Ohne Passwort für Änderungen ist niemand entsperrt: niemand sieht den Status.
	allSame(t, "ohne Passwort", seesStatus(t, &browser{t: t, h: h}, d), false)

	admin := &browser{t: t, h: h}
	admin.do("PUT", "/api/v1/password", `{"password":"geheim123"}`)
	allSame(t, "entsperrt", seesStatus(t, admin, d), true)
	guest := &browser{t: t, h: h}
	allSame(t, "Gast", seesStatus(t, guest, d), false)

	// Wer weckt, startet das Gerät ohnehin: ?wait sagt auch Gästen, ob es hochkommt.
	rec := guest.do("GET", "/wake/NAS?wait=5", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"online":true`) {
		t.Fatalf("Wecken mit ?wait: %d %s", rec.Code, rec.Body)
	}
	// Verlauf ohne Angaben zum Start
	var hist struct {
		Events       []store.Event `json:"events"`
		StatusHidden bool          `json:"status_hidden"`
	}
	json.Unmarshal(guest.do("GET", "/api/v1/history", "").Body.Bytes(), &hist)
	if !hist.StatusHidden || len(hist.Events) == 0 || hist.Events[0].Checkable {
		t.Fatalf("Verlauf für Gäste: %+v", hist)
	}
	json.Unmarshal(admin.do("GET", "/api/v1/history", "").Body.Bytes(), &hist)
	if hist.StatusHidden || !hist.Events[0].Checkable {
		t.Fatalf("Verlauf für Entsperrte: %+v", hist)
	}
	// Suchtreffer: nur für Entsperrte
	var found struct{ Hidden bool }
	json.Unmarshal(guest.do("GET", "/api/v1/discovery", "").Body.Bytes(), &found)
	if !found.Hidden {
		t.Error("Suchtreffer für Gäste sichtbar")
	}
	json.Unmarshal(admin.do("GET", "/api/v1/discovery", "").Body.Bytes(), &found)
	if found.Hidden {
		t.Error("Suchtreffer für Entsperrte ausgeblendet")
	}
}

func TestOnlineStatusNone(t *testing.T) {
	s, h, d := privacyServer(t, "none")
	admin := &browser{t: t, h: h}
	admin.do("PUT", "/api/v1/password", `{"password":"geheim123"}`)
	allSame(t, "entsperrt", seesStatus(t, admin, d), false)
	allSame(t, "Gast", seesStatus(t, &browser{t: t, h: h}, d), false)
	// Die Suchtreffer braucht, wer Geräte übernimmt: Entsperrte sehen sie weiter.
	var found struct{ Hidden bool }
	json.Unmarshal(admin.do("GET", "/api/v1/discovery", "").Body.Bytes(), &found)
	if found.Hidden {
		t.Error("Suchtreffer auch für Entsperrte ausgeblendet")
	}
	// Geprüft wird trotzdem (Wiederholung, Benachrichtigungen).
	if st, ok := s.monitor.Get(d.ID); !ok || st.State != "online" {
		t.Errorf("Prüfung abgeschaltet: %+v", st)
	}
}

func TestOnlineStatusAll(t *testing.T) {
	_, h, d := privacyServer(t, "all")
	allSame(t, "Gast", seesStatus(t, &browser{t: t, h: h}, d), true)
}

// Live-Ereignisse zum Status gehen nur an Verbindungen, die ihn sehen dürfen – nach der Einstellung
// zum Zeitpunkt des Ereignisses.
func TestStatusEventsFiltered(t *testing.T) {
	s, _, d := privacyServer(t, "unlocked")
	guest, stopG := s.hub.subscribe(i18n.DE, false)
	defer stopG()
	admin, stopA := s.hub.subscribe(i18n.DE, true)
	defer stopA()
	got := func(ch chan []byte) bool { // kam ein Statusereignis? (andere Ereignisse zählen nicht)
		for {
			select {
			case m := <-ch:
				if strings.HasPrefix(string(m), "event: status") {
					return true
				}
			case <-time.After(200 * time.Millisecond):
				return false
			}
		}
	}
	s.statusChanged(d, status.State{State: "offline"}, true, status.State{State: "online"})
	if got(guest) || !got(admin) {
		t.Fatal("unlocked: Gast bekommt Status oder Entsperrter nicht")
	}
	cfg := *s.conf()
	cfg.Privacy.OnlineStatus = "all"
	s.Apply(cfg)
	s.statusChanged(d, status.State{State: "online"}, true, status.State{State: "offline"})
	if !got(guest) {
		t.Fatal("all: Gast bekommt keinen Status")
	}
}

// Wer über die Weckseite weckt, bekommt einen Schlüssel, mit dem er den Start verfolgen darf – nur
// für dieses Gerät (bzw. diese Gruppe) und nur, wenn der Status sonst ausgeblendet ist.
func TestWakeLinkWatchToken(t *testing.T) {
	_, h, d := privacyServer(t, "none")
	guest := &browser{t: t, h: h}
	watchOf := func(rec *httptest.ResponseRecorder) string {
		var v struct{ Watch string }
		json.Unmarshal(rec.Body.Bytes(), &v)
		return v.Watch
	}
	tok := watchOf(guest.do("POST", "/wake/NAS", ""))
	if tok == "" {
		t.Fatal("kein Schlüssel in der Weckantwort")
	}
	if rec := guest.do("GET", "/wake/NAS/status", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("ohne Schlüssel: %d", rec.Code)
	}
	if rec := guest.do("GET", "/wake/NAS/status?watch="+tok, ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"state":"online"`) {
		t.Fatalf("mit Schlüssel: %d %s", rec.Code, rec.Body)
	}
	if rec := guest.do("GET", "/wake/NAS/status?watch="+flipLast(tok), ""); rec.Code != http.StatusForbidden {
		t.Fatalf("veränderter Schlüssel: %d", rec.Code)
	}
	// Der Schlüssel gilt nicht für die Liste und nicht für die Gruppe.
	if strings.Contains(guest.do("GET", "/api/v1/devices?watch="+tok, "").Body.String(), `"status"`) {
		t.Fatal("Schlüssel öffnet die Geräteliste")
	}
	if rec := guest.do("GET", "/wake-group/B%C3%BCro/status?watch="+tok, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("Geräteschlüssel für die Gruppe: %d", rec.Code)
	}
	// Gruppe: eigener Schlüssel
	gtok := watchOf(guest.do("POST", "/wake-group/B%C3%BCro", ""))
	if gtok == "" {
		t.Fatal("kein Schlüssel in der Gruppenantwort")
	}
	if rec := guest.do("GET", "/wake-group/B%C3%BCro/status?watch="+gtok, ""); rec.Code != http.StatusOK {
		t.Fatalf("Gruppe mit Schlüssel: %d %s", rec.Code, rec.Body)
	}
	if rec := guest.do("GET", "/api/v1/devices/"+d.ID+"/status?watch="+gtok, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("Gruppenschlüssel für das Gerät: %d", rec.Code)
	}
}

func TestWakeLinkNoWatchWhenVisible(t *testing.T) {
	_, h, _ := privacyServer(t, "all")
	if body := (&browser{t: t, h: h}).do("POST", "/wake/NAS", "").Body.String(); strings.Contains(body, `"watch"`) {
		t.Fatalf("Schlüssel, obwohl der Status sichtbar ist: %s", body)
	}
}

// Auch der Knopf in der Übersicht (API) gibt dem, der weckt, einen Schlüssel – je Gerät, auch beim
// Wecken einer Gruppe.
func TestOverviewWakeWatchToken(t *testing.T) {
	_, h, d := privacyServer(t, "unlocked")
	guest := &browser{t: t, h: h}
	var res struct{ Watch string }
	json.Unmarshal(guest.do("POST", "/api/v1/devices/"+d.ID+"/wake", "").Body.Bytes(), &res)
	if res.Watch == "" {
		t.Fatal("kein Schlüssel in der Weckantwort")
	}
	status := "/api/v1/devices/" + d.ID + "/status"
	if rec := guest.do("GET", status, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("ohne Schlüssel: %d", rec.Code)
	}
	if rec := guest.do("GET", status+"?watch="+res.Watch, ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"state":"online"`) {
		t.Fatalf("mit Schlüssel: %d %s", rec.Code, rec.Body)
	}
	// Die Liste bleibt ohne Status.
	if strings.Contains(guest.do("GET", "/api/v1/devices", "").Body.String(), `"status"`) {
		t.Fatal("Status in der Liste")
	}
	var grp struct{ Results []struct{ ID, Watch string } }
	json.Unmarshal(guest.do("POST", "/api/v1/groups/B%C3%BCro/wake", "").Body.Bytes(), &grp)
	if len(grp.Results) != 1 || grp.Results[0].Watch == "" {
		t.Fatalf("Gruppe: %+v", grp)
	}
	if rec := guest.do("GET", "/api/v1/devices/"+grp.Results[0].ID+"/status?watch="+grp.Results[0].Watch, ""); rec.Code != http.StatusOK {
		t.Fatalf("Gruppenmitglied mit Schlüssel: %d", rec.Code)
	}
}

// Suchtreffer sieht, wer bearbeiten darf: mit Passwort nur Entsperrte – auch wenn der Online-Status
// für alle sichtbar ist –, ohne Passwort alle (auch bei ausgeblendetem Status: ohne Passwort ließe
// sich ohnehin alles umstellen, und Geräte aus der Suche übernehmen soll weiter gehen).
func TestFoundVisibleForEditorsOnly(t *testing.T) {
	hidden := func(b *browser) bool {
		var v struct{ Hidden bool }
		json.Unmarshal(b.do("GET", "/api/v1/discovery", "").Body.Bytes(), &v)
		return v.Hidden
	}
	_, h, _ := privacyServer(t, "all")
	if hidden(&browser{t: t, h: h}) {
		t.Fatal("ohne Passwort ausgeblendet")
	}
	admin := &browser{t: t, h: h}
	admin.do("PUT", "/api/v1/password", `{"password":"geheim123"}`)
	if !hidden(&browser{t: t, h: h}) || hidden(admin) {
		t.Fatal("mit Passwort: Gast sieht Treffer oder Entsperrter nicht")
	}
	var info struct {
		FoundHidden bool `json:"found_hidden"`
	}
	json.Unmarshal((&browser{t: t, h: h}).do("GET", "/api/v1/info", "").Body.Bytes(), &info)
	if !info.FoundHidden {
		t.Fatal("info nennt die ausgeblendeten Treffer nicht")
	}
	_, h, _ = privacyServer(t, "none")
	if hidden(&browser{t: t, h: h}) {
		t.Fatal("ohne Passwort bei „niemand“ ausgeblendet – dann könnte niemand Geräte aus der Suche übernehmen")
	}
}
