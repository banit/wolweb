package wol

import (
	"bytes"
	"net"
	"testing"
	"time"
)

func TestParseMAC(t *testing.T) {
	for _, in := range []string{"00:11:22:aa:bb:cc", "00-11-22-AA-BB-CC", "0011.22aa.bbcc", "001122aabbcc", " 00:11:22:AA:BB:CC "} {
		hw, err := ParseMAC(in)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if got := FormatMAC(hw); got != "00:11:22:AA:BB:CC" {
			t.Errorf("%q → %s", in, got)
		}
	}
	for _, in := range []string{"", "00:11:22:33:44", "zz:11:22:33:44:55", "00:11:22:33:44:55:66:77"} {
		if _, err := ParseMAC(in); err == nil {
			t.Errorf("%q sollte ungültig sein", in)
		}
	}
}

func TestPacket(t *testing.T) {
	hw, _ := ParseMAC("01:23:45:67:89:ab")
	p := Packet(hw)
	if len(p) != 102 {
		t.Fatalf("Länge %d", len(p))
	}
	if !bytes.Equal(p[:6], bytes.Repeat([]byte{0xFF}, 6)) {
		t.Error("Kopf falsch")
	}
	for i := 0; i < 16; i++ {
		if !bytes.Equal(p[6+i*6:12+i*6], hw) {
			t.Fatalf("Wiederholung %d falsch", i)
		}
	}
}

func TestSendDeliversTwice(t *testing.T) {
	l, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	hw, _ := ParseMAC("01:23:45:67:89:ab")
	if err := Send(hw, l.LocalAddr().String(), ""); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 200)
	for i := range 2 {
		l.SetReadDeadline(time.Now().Add(time.Second))
		n, _, err := l.ReadFrom(buf)
		if err != nil || !bytes.Equal(buf[:n], Packet(hw)) {
			t.Fatalf("Paket %d: %v (%d Byte)", i+1, err, n)
		}
	}
}

// Ein Unicast-Ziel ohne Empfänger antwortet mit "Port unreachable". Das darf das zweite Paket
// nicht scheitern lassen (früher: ECONNREFUSED beim verbundenen Socket).
func TestSendIgnoresPortUnreachable(t *testing.T) {
	l, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	target := l.LocalAddr().String()
	l.Close() // Port ist jetzt geschlossen
	hw, _ := ParseMAC("01:23:45:67:89:ab")
	if err := Send(hw, target, ""); err != nil {
		t.Fatalf("Senden an geschlossenen Port: %v", err)
	}
}

func TestSendViaInterface(t *testing.T) {
	var lo string
	ifs, _ := net.Interfaces()
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagLoopback != 0 {
			lo = ifc.Name
		}
	}
	if lo == "" {
		t.Skip("keine Loopback-Schnittstelle")
	}
	l, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	hw, _ := ParseMAC("01:23:45:67:89:ab")
	if err := Send(hw, l.LocalAddr().String(), lo); err != nil {
		t.Fatal(err)
	}
	l.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := l.ReadFrom(make([]byte, 200)); err != nil {
		t.Fatal(err)
	}
}
