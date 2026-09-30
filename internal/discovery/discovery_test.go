package discovery

import (
	"net"
	"net/netip"
	"testing"

	"wolweb/internal/oui"
)

func TestHosts(t *testing.T) {
	h, err := hosts(netip.MustParsePrefix("192.168.1.0/24"), 1024)
	if err != nil || len(h) != 254 || h[0].String() != "192.168.1.1" || h[253].String() != "192.168.1.254" {
		t.Fatalf("%v %d", err, len(h))
	}
	if _, err := hosts(netip.MustParsePrefix("10.0.0.0/16"), 1024); err == nil {
		t.Error("/16 sollte die Grenze überschreiten")
	}
	if h, _ := hosts(netip.MustParsePrefix("10.0.0.5/32"), 10); len(h) != 1 {
		t.Error("/32")
	}
}

func TestBroadcastOf(t *testing.T) {
	for in, want := range map[string]string{"192.168.1.0/24": "192.168.1.255", "10.0.0.0/8": "10.255.255.255", "172.16.4.0/22": "172.16.7.255"} {
		if got := broadcastOf(netip.MustParsePrefix(in)); got != want {
			t.Errorf("%s: %s statt %s", in, got, want)
		}
	}
}

func TestOUI(t *testing.T) {
	mac, _ := net.ParseMAC("00:11:32:01:02:03") // Synology
	if v := oui.Lookup(mac); v == "" {
		t.Error("Synology nicht erkannt")
	}
	mac, _ = net.ParseMAC("02:11:32:01:02:03")
	if v := oui.Lookup(mac); v != "Private/zufällige MAC" {
		t.Errorf("lokale MAC: %q", v)
	}
}
