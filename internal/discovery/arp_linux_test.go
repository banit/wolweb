//go:build linux

package discovery

import (
	"context"
	"net/netip"
	"testing"
	"time"
)

// Ein abgebrochener ARP-Scan muss zurückkehren. Früher lief die Leseschleife nach dem Schließen
// des Sockets endlos weiter, und die Suche blieb für immer "läuft".
func TestARPScanReturnsAfterCancel(t *testing.T) {
	nets := LocalNets()
	if len(nets) == 0 {
		t.Skip("keine Netzwerkschnittstelle")
	}
	ln := nets[0]
	var targets []netip.Addr
	for a := ln.Prefix.Addr().Next(); ln.Prefix.Contains(a) && len(targets) < 300; a = a.Next() {
		targets = append(targets, a)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := arpScan(ctx, ln, targets, func(int) {})
		done <- err
	}()
	select {
	case err := <-done: // ohne CAP_NET_RAW scheitert schon das Öffnen
		cancel()
		t.Skipf("ARP-Scan nicht möglich: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Errorf("Fehler %v statt context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ARP-Scan kehrt nach dem Abbruch nicht zurück")
	}
}
