package status

import (
	"context"
	"testing"
	"time"

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
