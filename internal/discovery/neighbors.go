package discovery

import (
	"context"
	"net"
	"net/netip"
	"sync"
	"time"
)

// neighborScan schickt jeder Adresse ein UDP-Paket (Port 9, "discard"), damit das Betriebssystem
// die MAC per ARP auflöst, und liest danach die Nachbartabelle. Einträge können veraltet sein.
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
	case <-time.After(2 * time.Second): // ARP-Antworten abwarten
	}
	return readNeighbors(ln)
}
