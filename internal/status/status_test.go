package status

import (
	"context"
	"net"
	"testing"
	"time"

	"wolweb/internal/probe"
	"wolweb/internal/store"
)

func TestOlderCheckDoesNotOverwriteNewer(t *testing.T) {
	m := New(nil, nil, 0)
	d := store.Device{ID: "x", Name: "PC"}
	now := time.Now()
	m.set(d, State{State: "online", started: now})
	m.set(d, State{State: "offline", started: now.Add(-time.Second)}) // früher begonnen, später fertig
	if st, _ := m.Get(d.ID); st.State != "online" {
		t.Errorf("ältere Prüfung hat überschrieben: %s", st.State)
	}
	m.set(d, State{State: "offline", started: now.Add(time.Second)})
	if st, _ := m.Get(d.ID); st.State != "offline" {
		t.Errorf("neuere Prüfung nicht übernommen: %s", st.State)
	}
}

// Run folgt einem neuen Abstand ohne Neustart; bei 0 ruht die regelmäßige Prüfung.
func TestRunFollowsInterval(t *testing.T) {
	st, _, _ := store.Open(t.TempDir(), "")
	d, _ := st.Create(store.Input{Name: "PC", MAC: "00:11:22:33:44:55"}) // ohne IP: sofort "unbekannt"
	m := New(st, nil, 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)
	time.Sleep(50 * time.Millisecond)
	if _, ok := m.Get(d.ID); ok {
		t.Fatal("ohne Abstand geprüft")
	}
	m.SetInterval(20 * time.Millisecond)
	wait := func(ok func() bool) bool {
		for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
			if ok() {
				return true
			}
		}
		return false
	}
	var first State
	if !wait(func() bool { var ok bool; first, ok = m.Get(d.ID); return ok }) {
		t.Fatal("nach dem Einschalten nicht geprüft")
	}
	if !wait(func() bool { again, _ := m.Get(d.ID); return again.CheckedAt.After(first.CheckedAt) }) {
		t.Error("keine weitere Prüfung im neuen Abstand")
	}
}

// Wird während der Routineprüfung ein Gerät angelegt und gleich geprüft, darf das Aufräumen am
// Ende der Runde dessen Status nicht wegwerfen – sonst zeigte die Karte bis zur nächsten Runde
// (30 s) „kein Status“ (in der CI als Browsertest-Fehler sichtbar).
func TestCheckAllKeepsDevicesAddedMeanwhile(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	st, _, _ := store.Open(t.TempDir(), "")
	// Antwortet nie (Dokumentationsnetz): hält die Runde bis zum Timeout auf.
	st.Create(store.Input{Name: "NAS", MAC: "02:00:00:00:00:01", IP: "198.51.100.1", Ports: []int{9}})
	m := New(st, probe.New(nil, 400*time.Millisecond), 0)
	done := make(chan struct{})
	go func() { m.CheckAll(context.Background()); close(done) }()
	time.Sleep(50 * time.Millisecond) // die Runde läuft schon
	d, _ := st.Create(store.Input{Name: "Proxmox", MAC: "02:00:00:00:00:02", IP: "127.0.0.1", Ports: []int{port}})
	if s := m.Check(context.Background(), d); s.State != "online" {
		t.Fatalf("Prüfung: %s", s.State)
	}
	<-done
	if s, ok := m.Get(d.ID); !ok || s.State != "online" {
		t.Errorf("Status nach der Runde weg: %+v %v", s, ok)
	}
	// Gelöschte Geräte räumt die Runde weiterhin auf.
	nas, _ := st.Devices()
	st.Delete(nas[0].ID)
	m.CheckAll(context.Background())
	if _, ok := m.Get(nas[0].ID); ok {
		t.Error("Status eines gelöschten Geräts blieb")
	}
}
