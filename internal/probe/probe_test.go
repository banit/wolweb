package probe

import (
	"context"
	"net"
	"net/netip"
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
	p := &Prober{Ports: []int{port}, Timeout: time.Second, OnLink: func(netip.Addr) bool { return false }}
	if r := p.Check(context.Background(), loopback); !r.Online || r.Method == "" {
		t.Errorf("offener Port: %+v", r)
	}
}

func TestRefusedCountsOnlyOnLink(t *testing.T) {
	p := &Prober{Ports: []int{freePort(t)}, Timeout: time.Second}
	if r := p.Check(context.Background(), loopback); !r.Online {
		t.Error("Ablehnung im eigenen Netz sollte als online gelten")
	}
	p.OnLink = func(netip.Addr) bool { return false }
	if r := p.Check(context.Background(), loopback); r.Online {
		t.Errorf("Ablehnung hinter einem Router zählt als online: %+v", r)
	}
}
