package status

import (
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
