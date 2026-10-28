package wol

import (
	"bytes"
	"fmt"
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
	p := Packet(hw, nil)
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

// Send kehrt nach der ersten Runde zurück; die weiteren folgen im Abstand Spacing.
func TestSendRepeatsInRounds(t *testing.T) {
	l, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	hw, _ := ParseMAC("01:23:45:67:89:ab")
	start := time.Now()
	if err := Send(hw, l.LocalAddr().String(), "", nil); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > Spacing/2 {
		t.Errorf("Send hat %v gewartet – die Wiederholungen sollen im Hintergrund laufen", d)
	}
	buf := make([]byte, 200)
	var got []time.Duration
	for i := range Rounds {
		l.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, _, err := l.ReadFrom(buf)
		if err != nil || !bytes.Equal(buf[:n], Packet(hw, nil)) {
			t.Fatalf("Paket %d: %v (%d Byte)", i+1, err, n)
		}
		got = append(got, time.Since(start))
	}
	if last := got[len(got)-1]; last < time.Duration(Rounds-1)*Spacing*3/4 {
		t.Errorf("Runden zu dicht: %v", got)
	}
}

// SendAll kehrt erst zurück, wenn alle Runden draußen sind.
func TestSendAllWaitsForRounds(t *testing.T) {
	l, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	hw, _ := ParseMAC("01:23:45:67:89:ab")
	if err := SendAll(hw, l.LocalAddr().String(), "", nil); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 200)
	for i := range Rounds { // alle schon angekommen: kurze Frist genügt
		l.SetReadDeadline(time.Now().Add(Spacing / 4))
		if _, _, err := l.ReadFrom(buf); err != nil {
			t.Fatalf("Paket %d fehlt nach SendAll: %v", i+1, err)
		}
	}
}

func TestDestinations(t *testing.T) {
	for _, tc := range []struct {
		port int
		want []int
	}{{9, []int{9, 7}}, {7, []int{7, 9}}, {40000, []int{40000}}} {
		a := &net.UDPAddr{IP: net.IPv4(192, 168, 1, 255), Port: tc.port}
		var got []int
		for _, d := range Destinations(a) {
			if !d.IP.Equal(a.IP) {
				t.Errorf("Ziel %v statt %v", d.IP, a.IP)
			}
			got = append(got, d.Port)
		}
		if fmt.Sprint(got) != fmt.Sprint(tc.want) {
			t.Errorf("Port %d → %v, erwartet %v", tc.port, got, tc.want)
		}
		if a.Port != tc.port {
			t.Error("Destinations hat die Adresse verändert")
		}
	}
}

// Ein Unicast-Ziel ohne Empfänger antwortet mit "Port unreachable". Das darf die folgenden
// Runden nicht scheitern lassen (früher: ECONNREFUSED beim verbundenen Socket).
func TestSendIgnoresPortUnreachable(t *testing.T) {
	l, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	target := l.LocalAddr().(*net.UDPAddr)
	l.Close() // Port ist jetzt geschlossen
	hw, _ := ParseMAC("01:23:45:67:89:ab")
	if err := Send(hw, target.String(), "", nil); err != nil {
		t.Fatalf("Senden an geschlossenen Port: %v", err)
	}
	conn, err := listen("", target.String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for i := range 3 {
		if err := sendRound(conn, Packet(hw, nil), []*net.UDPAddr{target}); err != nil {
			t.Fatalf("Runde %d: %v", i+1, err)
		}
		time.Sleep(20 * time.Millisecond) // Zeit für die ICMP-Antwort
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
	if err := Send(hw, l.LocalAddr().String(), lo, nil); err != nil {
		t.Fatal(err)
	}
	l.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := l.ReadFrom(make([]byte, 200)); err != nil {
		t.Fatal(err)
	}
}

func TestSecureOn(t *testing.T) {
	hw, _ := ParseMAC("01:23:45:67:89:ab")
	pass, err := ParseSecureOn("a1-b2-c3-d4-e5-f6")
	if err != nil {
		t.Fatal(err)
	}
	p := Packet(hw, pass)
	if len(p) != 108 || !bytes.Equal(p[102:], []byte{0xa1, 0xb2, 0xc3, 0xd4, 0xe5, 0xf6}) {
		t.Errorf("Paket mit Passwort falsch: % x", p[96:])
	}
	if pass, err := ParseSecureOn(" "); pass != nil || err != nil {
		t.Error("leeres Passwort")
	}
	if _, err := ParseSecureOn("geheim"); err == nil {
		t.Error("ungültiges Passwort angenommen")
	}
}
