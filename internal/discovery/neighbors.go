package discovery

import (
	"context"
	"net"
	"net/netip"
	"sync"
	"time"
)

// neighborSettle ist die Wartezeit nach dem letzten Paket. Linux prüft einen veralteten Eintrag
// erst nach delay_first_probe_time (5 s) und dann mit drei Unicast-Anfragen im Sekundenabstand;
// bis dahin steht er weiter als vollständig in der Tabelle. Nach dieser Zeit sind die Einträge
// ausgeschalteter Geräte als fehlgeschlagen markiert und werden nicht mehr mitgezählt.
const neighborSettle = 10 * time.Second

// neighborScan schickt jeder Adresse ein UDP-Paket (Port 9, "discard"), damit das Betriebssystem
// die MAC per ARP auflöst, und liest danach die Nachbartabelle.
func neighborScan(ctx context.Context, ln LocalNet, targets []netip.Addr, progress func(int)) ([]Hit, error) {
	sem := make(chan struct{}, 64)
	var wg sync.WaitGroup
	for _, ip := range targets {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if c, err := net.DialUDP("udp4", nil, net.UDPAddrFromAddrPort(netip.AddrPortFrom(ip, 9))); err == nil {
				_, _ = c.Write([]byte{0})
				c.Close()
			}
			progress(1)
		}()
	}
	wg.Wait()
	select {
	case <-ctx.Done():
		// Vor Ablauf der Wartezeit enthält die Tabelle noch veraltete Einträge – nichts übernehmen.
		return nil, ctx.Err()
	case <-time.After(neighborSettle):
	}
	return readNeighbors(ln)
}
