// Package probe prüft, ob ein Gerät erreichbar ist: per ICMP-Ping (wenn das Betriebssystem es
// erlaubt) und parallel per TCP – auch "Verbindung abgelehnt" beweist, dass das Gerät läuft.
package probe

import (
	"context"
	"errors"
	"math/rand/v2"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

type Result struct {
	Online bool          `json:"online"`
	Method string        `json:"method,omitempty"` // "icmp", "tcp/22", "tcp/445 (abgelehnt)"
	RTT    time.Duration `json:"rtt_ms"`
}

type Prober struct {
	Ports   []int
	Timeout time.Duration
	pinger  *pinger
}

func New(ports []int, timeout time.Duration) *Prober {
	p := &Prober{Ports: ports, Timeout: timeout}
	p.pinger = newPinger()
	return p
}

// ICMPAvailable sagt, ob Ping-Pakete verschickt werden können.
func (p *Prober) ICMPAvailable() bool { return p.pinger != nil }

// Check liefert, sobald eine Methode Erfolg meldet, spätestens nach Timeout.
func (p *Prober) Check(ctx context.Context, ip netip.Addr) Result {
	ctx, cancel := context.WithTimeout(ctx, p.Timeout)
	defer cancel()
	res := make(chan Result, len(p.Ports)+1)
	var wg sync.WaitGroup
	start := time.Now()
	if p.pinger != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if p.pinger.ping(ctx, ip) {
				res <- Result{Online: true, Method: "icmp", RTT: time.Since(start)}
			}
		}()
	}
	for _, port := range p.Ports {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, refused := tcpProbe(ctx, ip, port); ok {
				m := "tcp/" + strconv.Itoa(port)
				if refused {
					m += " (abgelehnt)"
				}
				res <- Result{Online: true, Method: m, RTT: time.Since(start)}
			}
		}()
	}
	go func() { wg.Wait(); close(res) }()
	select {
	case r, ok := <-res:
		if ok {
			return r
		}
	case <-ctx.Done():
	}
	return Result{}
}

func tcpProbe(ctx context.Context, ip netip.Addr, port int) (ok, refused bool) {
	var d net.Dialer
	c, err := d.DialContext(ctx, "tcp4", netip.AddrPortFrom(ip, uint16(port)).String())
	if err == nil {
		c.Close()
		return true, false
	}
	if errors.Is(err, syscall.ECONNREFUSED) || strings.Contains(strings.ToLower(err.Error()), "refused") {
		return true, true
	}
	return false, false
}

// --- ICMP ---

type pinger struct {
	conn    *icmp.PacketConn
	raw     bool
	id      int
	mu      sync.Mutex
	seq     uint16
	waiting map[string]chan struct{}
}

func newPinger() *pinger {
	// Zuerst Raw-Socket (root oder CAP_NET_RAW), dann unprivilegierter Ping-Socket (Linux: ping_group_range).
	for _, network := range []string{"ip4:icmp", "udp4"} {
		c, err := icmp.ListenPacket(network, "0.0.0.0")
		if err != nil {
			continue
		}
		p := &pinger{conn: c, raw: network == "ip4:icmp", id: os.Getpid() & 0xffff,
			seq: uint16(rand.IntN(65536)), waiting: map[string]chan struct{}{}}
		go p.read()
		return p
	}
	return nil
}

func key(ip netip.Addr, seq int) string { return ip.String() + "#" + strconv.Itoa(seq) }

func (p *pinger) read() {
	buf := make([]byte, 1500)
	for {
		n, from, err := p.conn.ReadFrom(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		msg, err := icmp.ParseMessage(1, buf[:n])
		if err != nil || msg.Type != ipv4.ICMPTypeEchoReply {
			continue
		}
		echo, ok := msg.Body.(*icmp.Echo)
		if !ok || (p.raw && echo.ID != p.id) {
			continue
		}
		var addr netip.Addr
		switch a := from.(type) {
		case *net.IPAddr:
			addr, _ = netip.AddrFromSlice(a.IP.To4())
		case *net.UDPAddr:
			addr, _ = netip.AddrFromSlice(a.IP.To4())
		}
		p.mu.Lock()
		if ch, ok := p.waiting[key(addr, echo.Seq)]; ok {
			close(ch)
			delete(p.waiting, key(addr, echo.Seq))
		}
		p.mu.Unlock()
	}
}

// ping schickt bis zu drei Echo-Anfragen und wartet auf eine Antwort.
func (p *pinger) ping(ctx context.Context, ip netip.Addr) bool {
	var dst net.Addr = &net.IPAddr{IP: ip.AsSlice()}
	if !p.raw {
		dst = &net.UDPAddr{IP: ip.AsSlice()}
	}
	got := make(chan struct{}, 1)
	var keys []string
	defer func() {
		p.mu.Lock()
		for _, k := range keys {
			delete(p.waiting, k)
		}
		p.mu.Unlock()
	}()
	for attempt := 0; attempt < 3; attempt++ {
		p.mu.Lock()
		p.seq++
		seq := int(p.seq)
		k := key(ip, seq)
		ch := make(chan struct{})
		p.waiting[k] = ch
		keys = append(keys, k)
		p.mu.Unlock()
		go func() {
			select {
			case <-ch:
				select {
				case got <- struct{}{}:
				default:
				}
			case <-ctx.Done():
			}
		}()
		b, _ := (&icmp.Message{Type: ipv4.ICMPTypeEcho, Body: &icmp.Echo{ID: p.id, Seq: seq, Data: []byte("wolweb")}}).Marshal(nil)
		_, _ = p.conn.WriteTo(b, dst)
		select {
		case <-got:
			return true
		case <-ctx.Done():
			return false
		case <-time.After(700 * time.Millisecond):
		}
	}
	select {
	case <-got:
		return true
	case <-ctx.Done():
		return false
	}
}
