//go:build linux

package discovery

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"os"
	"sync"
	"time"

	"github.com/mdlayher/arp"
)

// arpScan fragt jede Adresse per ARP an (zwei Durchläufe) und sammelt die Antworten.
func arpScan(ctx context.Context, ln LocalNet, targets []netip.Addr, progress func(int)) ([]Hit, error) {
	ifc, err := net.InterfaceByName(ln.Interface)
	if err != nil {
		return nil, err
	}
	c, err := arp.Dial(ifc)
	if err != nil {
		return nil, err // meist fehlendes CAP_NET_RAW
	}
	defer c.Close()

	var mu sync.Mutex
	found := map[netip.Addr]net.HardwareAddr{}
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			p, _, err := c.Read()
			if err != nil {
				var ne net.Error
				if errors.As(err, &ne) && ne.Timeout() || errors.Is(err, os.ErrDeadlineExceeded) || errors.Is(err, net.ErrClosed) {
					return
				}
				continue
			}
			if p.Operation != arp.OperationReply || !ln.Prefix.Contains(p.SenderIP) {
				continue
			}
			mu.Lock()
			found[p.SenderIP] = p.SenderHardwareAddr
			mu.Unlock()
		}
	}()

	// Etwa 500 Anfragen pro Sekunde; der zweite Durchlauf fragt nur, wer noch nicht geantwortet hat.
	tick := time.NewTicker(2 * time.Millisecond)
	defer tick.Stop()
	for round := 0; round < 2; round++ {
		for _, ip := range targets {
			mu.Lock()
			_, have := found[ip]
			mu.Unlock()
			if !have {
				select {
				case <-ctx.Done():
				case <-tick.C:
					_ = c.Request(ip)
				}
			}
			if round == 0 {
				progress(1)
			}
		}
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
	}
	_ = c.SetReadDeadline(time.Now())
	<-readDone

	out := make([]Hit, 0, len(found))
	for ip, mac := range found {
		out = append(out, Hit{IP: ip, MAC: mac})
	}
	return out, ctx.Err()
}
