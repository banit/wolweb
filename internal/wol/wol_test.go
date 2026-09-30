package wol

import (
	"bytes"
	"testing"
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
