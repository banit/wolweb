package discovery

import (
	"context"
	"encoding/binary"
	"math/rand/v2"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/net/dns/dnsmessage"
)

type hostName struct{ name, source string }

// resolveNames fragt für jede Adresse parallel Reverse-DNS, mDNS und NetBIOS ab.
// Reihenfolge der Vorlieben: DNS, mDNS, NetBIOS.
func resolveNames(ctx context.Context, hits []Hit) map[netip.Addr]hostName {
	out := map[netip.Addr]hostName{}
	var mu sync.Mutex
	sem := make(chan struct{}, 32)
	var wg sync.WaitGroup
	for _, h := range hits {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if n := lookupName(ctx, h.IP); n.name != "" {
				mu.Lock()
				out[h.IP] = n
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return out
}

func lookupName(ctx context.Context, ip netip.Addr) hostName {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	type res struct {
		prio int
		n    hostName
	}
	ch := make(chan res, 3)
	go func() { ch <- res{0, hostName{reverseDNS(ctx, ip), "dns"}} }()
	go func() { ch <- res{1, hostName{mdnsName(ctx, ip), "mdns"}} }()
	go func() { ch <- res{2, hostName{netbiosName(ctx, ip), "netbios"}} }()
	var names [3]hostName
	for range 3 {
		r := <-ch
		names[r.prio] = r.n
	}
	for _, n := range names {
		if n.name = clean(n.name); n.name != "" {
			return n
		}
	}
	return hostName{}
}

// clean entfernt Steuerzeichen und kürzt – Namen kommen aus dem Netz und sind nicht vertrauenswürdig.
func clean(s string) string {
	s = strings.TrimSuffix(strings.TrimSpace(s), ".")
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	if len([]rune(s)) > 80 {
		s = string([]rune(s)[:80])
	}
	return s
}

func reverseDNS(ctx context.Context, ip netip.Addr) string {
	names, err := net.DefaultResolver.LookupAddr(ctx, ip.String())
	if err != nil || len(names) == 0 {
		return ""
	}
	return names[0]
}

// mdnsName stellt eine PTR-Anfrage direkt per Unicast an Port 5353 des Geräts (RFC 6762 §5.1,
// "legacy unicast"): Apple-Geräte, Linux mit Avahi, Drucker und viele IoT-Geräte antworten so.
func mdnsName(ctx context.Context, ip netip.Addr) string {
	b := ip.As4()
	qname := dnsmessage.MustNewName(
		strings.Join([]string{itoa(b[3]), itoa(b[2]), itoa(b[1]), itoa(b[0]), "in-addr", "arpa", ""}, "."))
	msg := dnsmessage.Message{
		Header:    dnsmessage.Header{ID: uint16(rand.IntN(65536))},
		Questions: []dnsmessage.Question{{Name: qname, Type: dnsmessage.TypePTR, Class: dnsmessage.ClassINET}},
	}
	pkt, err := msg.Pack()
	if err != nil {
		return ""
	}
	resp := exchange(ctx, ip, 5353, pkt)
	if resp == nil {
		return ""
	}
	var p dnsmessage.Parser
	if _, err := p.Start(resp); err != nil {
		return ""
	}
	_ = p.SkipAllQuestions()
	for {
		h, err := p.AnswerHeader()
		if err != nil {
			return ""
		}
		if h.Type != dnsmessage.TypePTR {
			_ = p.SkipAnswer()
			continue
		}
		r, err := p.PTRResource()
		if err != nil {
			return ""
		}
		return strings.TrimSuffix(strings.TrimSuffix(r.PTR.String(), "."), ".local")
	}
}

func itoa(b byte) string { return strconv.Itoa(int(b)) }

// netbiosName stellt eine NetBIOS-Node-Status-Anfrage (RFC 1002, UDP 137) – Windows-Rechner und Samba.
func netbiosName(ctx context.Context, ip netip.Addr) string {
	q := make([]byte, 0, 50)
	q = binary.BigEndian.AppendUint16(q, uint16(rand.IntN(65536)))
	q = append(q, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0) // Flags, QDCOUNT=1
	q = append(q, 0x20, 'C', 'K')               // Name "*" kodiert, mit Nullen aufgefüllt
	for range 15 {
		q = append(q, 'A', 'A')
	}
	q = append(q, 0, 0, 0x21, 0, 1) // Ende, Typ NBSTAT, Klasse IN
	resp := exchange(ctx, ip, 137, q)
	if len(resp) < 57 {
		return ""
	}
	i := 12
	// Name im Antwortteil: entweder vollständig (34 Byte) oder als Zeiger (2 Byte).
	if resp[i]&0xC0 == 0xC0 {
		i += 2
	} else {
		for i < len(resp) && resp[i] != 0 {
			i += int(resp[i]) + 1
		}
		i++
	}
	i += 2 + 2 + 4 + 2 // Typ, Klasse, TTL, Länge
	if i >= len(resp) {
		return ""
	}
	count := int(resp[i])
	i++
	for n := 0; n < count && i+18 <= len(resp); n, i = n+1, i+18 {
		name := strings.TrimRight(string(resp[i:i+15]), " \x00")
		suffix, flags := resp[i+15], binary.BigEndian.Uint16(resp[i+16:i+18])
		if suffix == 0x00 && flags&0x8000 == 0 && name != "" { // Arbeitsstation, keine Gruppe
			return name
		}
	}
	return ""
}

func exchange(ctx context.Context, ip netip.Addr, port uint16, pkt []byte) []byte {
	c, err := net.DialUDP("udp4", nil, net.UDPAddrFromAddrPort(netip.AddrPortFrom(ip, port)))
	if err != nil {
		return nil
	}
	defer c.Close()
	deadline, _ := ctx.Deadline()
	_ = c.SetDeadline(deadline)
	buf := make([]byte, 1500)
	for range 2 {
		if _, err := c.Write(pkt); err != nil {
			return nil
		}
		_ = c.SetReadDeadline(minTime(deadline, time.Now().Add(700*time.Millisecond)))
		n, err := c.Read(buf)
		if err == nil {
			return buf[:n]
		}
		if ctx.Err() != nil {
			return nil
		}
	}
	return nil
}

func minTime(a, b time.Time) time.Time {
	if a.IsZero() || b.Before(a) {
		return b
	}
	return a
}
