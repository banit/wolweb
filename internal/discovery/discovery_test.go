package discovery

import (
	"context"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"wolweb/internal/i18n"
	"wolweb/internal/oui"
	"wolweb/internal/store"
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

// Der Start muss vor dem Ende gemeldet werden – auch wenn die Suche sofort scheitert.
func TestStartReportedBeforeFinish(t *testing.T) {
	st, _, err := store.Open(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	sc := New(st, []string{"203.0.113.0/30"}, 16) // kein lokales Netz: scheitert sofort
	var mu sync.Mutex
	var seen []bool
	done := make(chan struct{})
	sc.OnUpdate = func(j Job) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, j.Running)
		if !j.Running {
			close(done)
		}
	}
	if err := sc.Start(false); err != nil {
		t.Fatal(err)
	}
	<-done
	mu.Lock()
	defer mu.Unlock()
	if len(seen) == 0 || !seen[0] || seen[len(seen)-1] {
		t.Errorf("Reihenfolge der Meldungen: %v", seen)
	}
}

// Die automatische Suche folgt einem neuen Abstand sofort und ruht bei 0.
func TestRunAutoFollowsInterval(t *testing.T) {
	old := autoStartDelay
	autoStartDelay = 20 * time.Millisecond
	defer func() { autoStartDelay = old }()
	st, _, _ := store.Open(t.TempDir(), "")
	// 198.51.100.0/24 (Dokumentationsnetz) hängt nicht am Rechner: jede Suche endet sofort.
	sc := New(st, []string{"198.51.100.0/24"}, 1024)
	var mu sync.Mutex
	runs := 0
	sc.OnUpdate = func(j Job) {
		if j.Running && j.Auto {
			mu.Lock()
			runs++
			mu.Unlock()
		}
	}
	count := func() int { mu.Lock(); defer mu.Unlock(); return runs }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sc.RunAuto(ctx)
	time.Sleep(100 * time.Millisecond)
	if n := count(); n != 0 {
		t.Fatalf("ohne Abstand gesucht: %d", n)
	}
	sc.SetInterval(40 * time.Millisecond) // eingeschaltet: nach autoStartDelay, dann alle 40 ms
	for deadline := time.Now().Add(3 * time.Second); count() < 2 && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
	}
	if n := count(); n < 2 {
		t.Fatalf("nur %d Suchen", n)
	}
	sc.SetInterval(0)
	time.Sleep(60 * time.Millisecond)
	before := count()
	time.Sleep(150 * time.Millisecond)
	if n := count(); n != before {
		t.Errorf("nach dem Ausschalten weiter gesucht: %d → %d", before, n)
	}
	if err := sc.SetNetworks([]string{"198.51.100.7/24", "203.0.113.0/24"}); err != nil || len(sc.Networks()) != 2 || sc.Networks()[0] != "198.51.100.0/24" {
		t.Errorf("Segmente: %v %v", sc.Networks(), err)
	}
}

// Container-Brücken, VMs und VPNs sind kein Heimnetz; Proxmox-Bridge und VLAN-Interface schon.
func TestVirtualIface(t *testing.T) {
	for name, virtual := range map[string]bool{
		"docker0": true, "br-0123456789ab": true, "veth12ab": true, "virbr0": true, "tailscale0": true, "wg0": true,
		"cni0": true, "podman0": true, "vEthernet (WSL)": true, "tun0": true,
		"eth0": false, "enp3s0": false, "wlan0": false, "vmbr0": false, "eth0.20": false, "br-lan": false, "Ethernet": false,
	} {
		if got := virtualIface(name); got != virtual {
			t.Errorf("%s: %v", name, got)
		}
	}
}

// Suchfehler bleiben Schlüssel und Parameter, bis jemand sie ansieht – dann in dessen Sprache.
func TestJobErrorsLocalized(t *testing.T) {
	j := Job{errs: []error{i18n.M("scan.cancelled")}}
	de, en := j.Localized(i18n.DE).Error, j.Localized(i18n.EN).Error
	if de == "" || en == "" || de == en || de != i18n.T(i18n.DE, "scan.cancelled") || en != i18n.T(i18n.EN, "scan.cancelled") {
		t.Errorf("de %q, en %q", de, en)
	}
}
