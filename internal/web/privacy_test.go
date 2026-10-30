package web

import (
	"bufio"
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

// seesWakes prüft alle Stellen, die zeigen, wann geweckt wurde, aus Sicht von b.
func seesWakes(t *testing.T, b *browser) map[string]bool {
	t.Helper()
	got := map[string]bool{}
	got["devices"] = strings.Contains(b.do("GET", "/api/v1/devices", "").Body.String(), `"last_wake"`)
	got["groups"] = strings.Contains(b.do("GET", "/api/v1/groups", "").Body.String(), `"last_wake"`)
	got["history"] = b.do("GET", "/api/v1/history", "").Code == http.StatusOK
	m := b.do("GET", "/metrics", "").Body.String()
	got["metrics last wake"] = strings.Contains(m, "wolweb_device_last_wake_timestamp_seconds{")
	got["metrics wakes"] = strings.Contains(m, "wolweb_wakes_total{")
	var info struct {
		HistoryHidden bool `json:"history_hidden"`
	}
	json.Unmarshal(b.do("GET", "/api/v1/info", "").Body.Bytes(), &info)
	got["info"] = !info.HistoryHidden
	return got
}

func TestWakeHistoryPrivacy(t *testing.T) {
	for _, c := range []struct {
		mode         string
		guest, admin bool
	}{{"all", true, true}, {"unlocked", false, true}, {"none", false, false}} {
		t.Run(c.mode, func(t *testing.T) {
			s, h, _ := settingsServer(t, `{"privacy": {"wake_history": "`+c.mode+`"}}`)
			d, err := s.store.Create(store.Input{Name: "NAS", MAC: "02:00:00:00:00:01", Broadcast: udpSink(t), Group: "Büro"})
			if err != nil {
				t.Fatal(err)
			}
			admin := &browser{t: t, h: h}
			admin.do("PUT", "/api/v1/password", `{"password":"geheim123"}`)
			guest := &browser{t: t, h: h}
			// Wecken darf jeder, und wer weckt, bekommt seine Antwort wie gewohnt.
			if rec := guest.do("POST", "/api/v1/devices/"+d.ID+"/wake", ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"success":true`) {
				t.Fatalf("Wecken: %d %s", rec.Code, rec.Body)
			}
			for who, want := range map[*browser]bool{guest: c.guest, admin: c.admin} {
				name := map[bool]string{true: "entsperrt", false: "Gast"}[who == admin]
				for k, v := range seesWakes(t, who) {
					if v != want {
						t.Errorf("%s, %s: sieht Weckzeitpunkte = %v, erwartet %v", name, k, v, want)
					}
				}
			}
			if rec := guest.do("GET", "/api/v1/history", ""); !c.guest && (rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"hidden":true`)) {
				t.Errorf("Verlauf ausgeblendet: %d %s", rec.Code, rec.Body)
			}
			// Aufgezeichnet wird trotzdem.
			if len(s.store.History(10)) != 1 || s.store.LastWake(d.ID).IsZero() {
				t.Error("Weckruf nicht aufgezeichnet")
			}
		})
	}
}

// Die Einstellungen sind unabhängig: Weckzeitpunkte ausblenden lässt den Online-Status stehen.
func TestWakeHistoryIndependentOfStatus(t *testing.T) {
	_, h, _ := settingsServer(t, `{"privacy": {"online_status": "all", "wake_history": "none"}}`)
	guest := &browser{t: t, h: h}
	var info struct {
		StatusHidden  bool `json:"status_hidden"`
		HistoryHidden bool `json:"history_hidden"`
	}
	json.Unmarshal(guest.do("GET", "/api/v1/info", "").Body.Bytes(), &info)
	if info.StatusHidden || !info.HistoryHidden {
		t.Errorf("info: %+v", info)
	}
	if rec := guest.do("PUT", "/api/v1/settings", `{"privacy": {"online_status": "all", "wake_history": "später"}}`); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"field":"privacy.wake_history"`) {
		t.Errorf("ungültiger Wert: %d %s", rec.Code, rec.Body)
	}
}

// Aufzeichnung aus: kein Verlauf, kein „zuletzt geweckt“, kein „zuletzt online“ – Wecken und
// Startbestätigung laufen weiter.
func TestRecordingOff(t *testing.T) {
	s, h, _ := settingsServer(t, `{"privacy": {"recording": "off"}}`)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	d, _ := s.store.Create(store.Input{Name: "NAS", MAC: "02:00:00:00:00:01", IP: "127.0.0.1", Ports: []int{l.Addr().(*net.TCPAddr).Port}, Broadcast: udpSink(t)})
	b := &browser{t: t, h: h}
	if rec := b.do("GET", "/wake/NAS?wait=5", ""); !strings.Contains(rec.Body.String(), `"online":true`) {
		t.Fatalf("Wecken mit Startbestätigung: %d %s", rec.Code, rec.Body)
	}
	if st := s.monitor.Check(context.Background(), d); st.State != "online" || !st.LastSeen.IsZero() {
		t.Errorf("Status: %+v", st)
	}
	devs := b.do("GET", "/api/v1/devices", "").Body.String()
	if strings.Contains(devs, `"last_wake"`) || strings.Contains(devs, `"last_seen"`) {
		t.Errorf("Geräteliste: %s", devs)
	}
	var hist struct{ Events []store.Event }
	json.Unmarshal(b.do("GET", "/api/v1/history", "").Body.Bytes(), &hist)
	var info struct {
		HistoryHidden bool `json:"history_hidden"`
	}
	json.Unmarshal(b.do("GET", "/api/v1/info", "").Body.Bytes(), &info)
	if len(hist.Events) != 0 || !info.HistoryHidden {
		t.Errorf("Verlauf %+v, ausgeblendet %v", hist.Events, info.HistoryHidden)
	}
	if m := b.do("GET", "/metrics", "").Body.String(); strings.Contains(m, "wolweb_wakes_total{") || strings.Contains(m, "wolweb_device_last_seen_timestamp_seconds{") {
		t.Errorf("Kennzahlen:\n%s", m)
	}
	// Ein fehlendes Feld schaltet nicht ab: leer heißt „an“.
	if rec := b.do("PUT", "/api/v1/settings", `{"privacy": {"online_status": "all"}}`); rec.Code != http.StatusOK || s.conf().Privacy.Recording != "on" || s.conf().Privacy.RetentionDays != 30 {
		t.Errorf("Standard: %d %+v", rec.Code, s.conf().Privacy)
	}
}

// sse öffnet eine echte Live-Verbindung (mit cookie, falls gesetzt). events liefert die Namen der
// Ereignisse, closed schließt sich, wenn der Server die Verbindung beendet.
func sse(t *testing.T, s *Server, url string, cookie *http.Cookie) (events chan string, closed chan struct{}) {
	t.Helper()
	before := s.hub.subscribers()
	req, _ := http.NewRequest("GET", url+"/api/v1/events", nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	events, closed = make(chan string, 16), make(chan struct{})
	go func() {
		defer close(closed)
		sc := bufio.NewScanner(res.Body)
		for sc.Scan() {
			if name, ok := strings.CutPrefix(sc.Text(), "event: "); ok {
				events <- name
			}
		}
	}()
	for deadline := time.Now().Add(2 * time.Second); s.hub.subscribers() == before; {
		if time.Now().After(deadline) {
			t.Fatal("Live-Verbindung nicht angemeldet")
		}
		time.Sleep(5 * time.Millisecond)
	}
	return events, closed
}

// Eine offene Live-Verbindung behält den Online-Status nicht über die Entsperrung hinaus: Ablauf,
// neues Passwort und Sperren beenden sie (der Browser verbindet sich neu und wird neu eingestuft).
func TestStatusEventsFollowUnlock(t *testing.T) {
	s, h, d := privacyServer(t, "unlocked")
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close) // läuft nach dem Schließen der Verbindungen (Cleanup rückwärts)
	admin := &browser{t: t, h: h}
	admin.do("PUT", "/api/v1/password", `{"password":"geheim123"}`)
	statusEvent := func(events chan string) bool {
		s.statusChanged(d, status.State{State: "offline"}, true, status.State{State: "online"})
		for {
			select {
			case e := <-events:
				if e == "status" {
					return true
				}
			case <-time.After(300 * time.Millisecond):
				return false
			}
		}
	}
	isClosed := func(closed chan struct{}, within time.Duration) bool {
		select {
		case <-closed:
			return true
		case <-time.After(within):
			return false
		}
	}
	_, guestClosed := sse(t, s, srv.URL, nil)

	// Neues Passwort (aus einem anderen Browser): Die entsperrte Verbindung endet.
	events, closed := sse(t, s, srv.URL, admin.cookie)
	if !statusEvent(events) {
		t.Fatal("entsperrt: kein Status")
	}
	other := &browser{t: t, h: h}
	other.do("POST", "/api/v1/unlock", `{"password":"geheim123"}`)
	if rec := other.do("PUT", "/api/v1/password", `{"current":"geheim123","password":"neu-geheim1"}`); rec.Code != 200 {
		t.Fatalf("Passwort ändern: %d %s", rec.Code, rec.Body)
	}
	if !isClosed(closed, 2*time.Second) {
		t.Fatal("nach neuem Passwort weiter verbunden")
	}

	// Ablauf der Entsperrung beendet die Verbindung.
	soon := &http.Cookie{Name: unlockCookie, Value: s.unlockToken(time.Now().Add(1500 * time.Millisecond))}
	events, closed = sse(t, s, srv.URL, soon)
	if !statusEvent(events) {
		t.Fatal("kurz entsperrt: kein Status")
	}
	if !isClosed(closed, 3*time.Second) {
		t.Fatal("nach Ablauf der Entsperrung weiter verbunden")
	}

	// Sperren beendet sie ebenso.
	_, closed = sse(t, s, srv.URL, other.cookie)
	other.do("POST", "/api/v1/lock", "")
	if !isClosed(closed, 2*time.Second) {
		t.Fatal("nach dem Sperren weiter verbunden")
	}
	if isClosed(guestClosed, 0) {
		t.Error("Verbindung ohne Entsperrung wurde beendet")
	}
}

// Bei ausgeblendetem Online-Status verrät der Verlauf keine automatischen Wiederholungen (es gibt sie
// nur, wenn das Gerät offline blieb), und "zuletzt geweckt" bleibt beim ersten Weckruf.
func TestHistoryHidesRetries(t *testing.T) {
	s, h, d := privacyServer(t, "unlocked")
	first := time.Now().Add(-time.Minute).UTC().Truncate(time.Second)
	s.store.RecordWake(store.Event{Time: first, ID: d.ID, Device: d.Name, Source: "link", Success: true},
		store.Event{Time: first.Add(45 * time.Second), ID: d.ID, Device: d.Name, Source: "link", Success: true, Retry: true})
	guest := &browser{t: t, h: h}
	body := guest.do("GET", "/api/v1/history", "").Body.String()
	if strings.Contains(body, `"retry"`) || strings.Count(body, `"device":"NAS"`) != 1 {
		t.Fatalf("Verlauf für Gäste: %s", body)
	}
	if lw := s.store.LastWake(d.ID); !lw.Equal(first) {
		t.Fatalf("zuletzt geweckt %v statt %v", lw, first)
	}
	// Wer den Status sehen darf, sieht die Wiederholung.
	admin := &browser{t: t, h: h}
	admin.do("PUT", "/api/v1/password", `{"password":"geheim123"}`)
	if body := admin.do("GET", "/api/v1/history", "").Body.String(); !strings.Contains(body, `"retry":true`) {
		t.Fatalf("Verlauf für Entsperrte: %s", body)
	}
}

// Das Live-Ereignis nach einem Weckruf geht nur an Verbindungen, die Weckzeiten sehen dürfen.
func TestWakeEventsFollowWakeHistory(t *testing.T) {
	s, _, _ := settingsServer(t, `{"privacy": {"wake_history": "unlocked"}}`)
	d, _ := s.store.Create(store.Input{Name: "NAS", MAC: "02:00:00:00:00:01", Broadcast: udpSink(t)})
	guest, stopG := s.hub.subscribe(i18n.DE, false)
	defer stopG()
	priv, stopP := s.hub.subscribe(i18n.DE, true)
	defer stopP()
	s.recordWakes(wakeAttempt{ev: store.Event{ID: d.ID, Device: d.Name, Source: "link", Success: true}, d: d})
	select {
	case m := <-priv:
		if !strings.HasPrefix(string(m), "event: devices") {
			t.Fatalf("privilegiert: %s", m)
		}
	case <-time.After(time.Second):
		t.Fatal("privilegierte Verbindung bekam kein Ereignis")
	}
	select {
	case m := <-guest:
		t.Fatalf("Gast bekam %s", m)
	case <-time.After(200 * time.Millisecond):
	}
}
