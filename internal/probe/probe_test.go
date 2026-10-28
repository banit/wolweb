package probe

import (
	"context"
	"net"
	"net/netip"
	"strconv"
	"testing"
	"time"
)

var loopback = netip.MustParseAddr("127.0.0.1")

// freePort liefert einen Port, auf dem gerade niemand lauscht (Verbindung wird abgelehnt).
func freePort(t *testing.T) int {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

func TestOpenPortIsOnline(t *testing.T) {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port
	// Ohne Ping (pinger nil); hinter einem Router zählt eine angenommene Verbindung trotzdem.
	p := &Prober{ports: []int{port}, Timeout: time.Second, OnLink: func(netip.Addr) bool { return false }}
	if r := p.Check(context.Background(), loopback, nil, ""); !r.Online || r.Method == "" {
		t.Errorf("offener Port: %+v", r)
	}
}

func TestRefusedCountsOnlyOnLink(t *testing.T) {
	// Windows meldet eine Ablehnung auf localhost erst nach etwa zwei Sekunden (Wiederholungen).
	p := &Prober{ports: []int{freePort(t)}, Timeout: 5 * time.Second}
	if r := p.Check(context.Background(), loopback, nil, ""); !r.Online {
		t.Error("Ablehnung im eigenen Netz sollte als online gelten")
	}
	p.OnLink = func(netip.Addr) bool { return false }
	if r := p.Check(context.Background(), loopback, nil, ""); r.Online {
		t.Errorf("Ablehnung hinter einem Router zählt als online: %+v", r)
	}
}

func TestHintedPortFirst(t *testing.T) {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	open := l.Addr().(*net.TCPAddr).Port
	// Der abgelehnte Port wäre ohne Hinweis genauso schnell; mit Hinweis muss der offene gewinnen.
	p := &Prober{ports: []int{freePort(t), open}, Timeout: time.Second}
	want := "tcp/" + strconv.Itoa(open)
	if r := p.Check(context.Background(), loopback, nil, want); r.Method != want {
		t.Errorf("Methode %q statt %q", r.Method, want)
	}
	// Eigene Ports ersetzen die Standardliste.
	if r := p.Check(context.Background(), loopback, []int{open}, ""); r.Method != want {
		t.Errorf("eigene Ports: %q", r.Method)
	}
}

func TestHintPort(t *testing.T) {
	for in, want := range map[string]int{"tcp/22": 22, "tcp/445 (abgelehnt)": 445, "icmp": 0, "": 0, "tcp/x": 0} {
		if got := hintPort(in); got != want {
			t.Errorf("%q: %d statt %d", in, got, want)
		}
	}
}
