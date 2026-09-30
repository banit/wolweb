// Package discovery sucht Geräte in den freigegebenen IPv4-Segmenten.
//
// Bevorzugt wird ein aktiver ARP-Scan (Linux, braucht CAP_NET_RAW). Geht das nicht, bekommt jede
// Adresse ein kleines UDP-Paket, damit der Kernel ARP auflöst, und danach wird die Nachbartabelle
// gelesen. Die Suche weckt keine Geräte und findet nur Geräte, die gerade eingeschaltet sind.
package discovery

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/netip"
	"sync"
	"time"

	"wolweb/internal/oui"
	"wolweb/internal/store"
	"wolweb/internal/wol"
)

// Hit ist eine Rohbeobachtung IP ↔ MAC.
type Hit struct {
	IP  netip.Addr
	MAC net.HardwareAddr
}

// LocalNet ist ein direkt angeschlossenes IPv4-Netz.
type LocalNet struct {
	Interface string       `json:"interface"`
	Addr      netip.Addr   `json:"addr"`
	Prefix    netip.Prefix `json:"prefix"`
	Broadcast string       `json:"broadcast"`
}

// LocalNets listet die IPv4-Netze der eigenen Schnittstellen (ohne Loopback).
func LocalNets() []LocalNet {
	var out []LocalNet
	ifs, _ := net.Interfaces()
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok || n.IP.To4() == nil {
				continue
			}
			ip, _ := netip.AddrFromSlice(n.IP.To4())
			ones, _ := n.Mask.Size()
			p := netip.PrefixFrom(ip, ones).Masked()
			out = append(out, LocalNet{Interface: ifc.Name, Addr: ip, Prefix: p, Broadcast: broadcastOf(p) + ":9"})
		}
	}
	return out
}

func broadcastOf(p netip.Prefix) string {
	b := p.Addr().As4()
	host := 32 - p.Bits()
	v := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	if host > 0 {
		v |= (1 << host) - 1
	}
	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}).String()
}

// localNetFor sucht die Schnittstelle, deren Netz das zu scannende Segment enthält.
func localNetFor(target netip.Prefix) (LocalNet, bool) {
	for _, ln := range LocalNets() {
		if ln.Prefix.Bits() <= target.Bits() && ln.Prefix.Contains(target.Addr()) {
			return ln, true
		}
	}
	return LocalNet{}, false
}

// hosts liefert alle Host-Adressen des Netzes (ohne Netz- und Broadcast-Adresse bei /30 und größer).
func hosts(p netip.Prefix, max int) ([]netip.Addr, error) {
	size := 1 << (32 - p.Bits())
	if size-2 > max {
		return nil, fmt.Errorf("%s hat %d Adressen – erlaubt sind höchstens %d (discovery.max_hosts)", p, size, max)
	}
	var out []netip.Addr
	a := p.Addr()
	for i := 0; i < size; i++ {
		if !(size >= 4 && (i == 0 || i == size-1)) {
			out = append(out, a)
		}
		a = a.Next()
	}
	return out, nil
}

// Job beschreibt den laufenden oder letzten Scan.
type Job struct {
	Running    bool      `json:"running"`
	Networks   []string  `json:"networks"`
	Method     string    `json:"method,omitempty"`
	StartedAt  time.Time `json:"started_at,omitzero"`
	FinishedAt time.Time `json:"finished_at,omitzero"`
	Total      int       `json:"total"`
	Done       int       `json:"done"`
	Found      int       `json:"found"`
	New        int       `json:"new"`
	Error      string    `json:"error,omitempty"`
	Auto       bool      `json:"auto"`
}

type Scanner struct {
	store    *store.Store
	networks []netip.Prefix
	maxHosts int

	mu     sync.Mutex
	job    Job
	cancel context.CancelFunc
}

func New(s *store.Store, networks []string, maxHosts int) *Scanner {
	sc := &Scanner{store: s, maxHosts: maxHosts}
	for _, n := range networks {
		sc.networks = append(sc.networks, netip.MustParsePrefix(n))
	}
	return sc
}

func (s *Scanner) Networks() []string {
	out := make([]string, len(s.networks))
	for i, n := range s.networks {
		out[i] = n.String()
	}
	return out
}

func (s *Scanner) Job() Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.job
}

var ErrBusy = errors.New("es läuft bereits eine Suche")
var ErrNoNetworks = errors.New("keine Netzsegmente konfiguriert (discovery.networks)")

// Start beginnt einen Scan im Hintergrund.
func (s *Scanner) Start(auto bool) error {
	if len(s.networks) == 0 {
		return ErrNoNetworks
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.job.Running {
		return ErrBusy
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	s.cancel = cancel
	s.job = Job{Running: true, Networks: s.Networks(), StartedAt: time.Now().UTC(), Auto: auto}
	go s.run(ctx, cancel)
	return nil
}

func (s *Scanner) Cancel() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
}

func (s *Scanner) update(f func(j *Job)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(&s.job)
}

func (s *Scanner) run(ctx context.Context, cancel context.CancelFunc) {
	defer cancel()
	var all []store.Found
	var errs []error
	methods := map[string]bool{}
	for _, p := range s.networks {
		if ctx.Err() != nil {
			break
		}
		found, method, err := s.scanNetwork(ctx, p)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", p, err))
		}
		if method != "" {
			methods[method] = true
		}
		all = append(all, found...)
	}
	newCount, err := s.store.MergeFound(all)
	if err != nil {
		errs = append(errs, err)
	}
	if ctx.Err() == context.Canceled {
		errs = append(errs, errors.New("abgebrochen"))
	}
	var method string
	for m := range methods {
		if method != "" {
			method += ", "
		}
		method += m
	}
	s.update(func(j *Job) {
		j.Running = false
		j.FinishedAt = time.Now().UTC()
		j.Found = len(all)
		j.New = newCount
		j.Method = method
		if err := errors.Join(errs...); err != nil {
			j.Error = err.Error()
		}
	})
	log.Printf("Suche beendet: %d Geräte, %d neu, Methode %s", len(all), newCount, method)
}

func (s *Scanner) scanNetwork(ctx context.Context, p netip.Prefix) ([]store.Found, string, error) {
	targets, err := hosts(p, s.maxHosts)
	if err != nil {
		return nil, "", err
	}
	s.update(func(j *Job) { j.Total += len(targets) })
	ln, ok := localNetFor(p)
	if !ok {
		return nil, "", errors.New("kein direkt angeschlossenes Netz – ARP funktioniert nur im eigenen Segment")
	}
	progress := func(n int) { s.update(func(j *Job) { j.Done += n }) }

	hits, err := arpScan(ctx, ln, targets, progress)
	method := "ARP-Scan"
	if err != nil {
		log.Printf("ARP-Scan auf %s nicht möglich (%v) – nutze Nachbartabelle", ln.Interface, err)
		method = "Nachbartabelle"
		hits, err = neighborScan(ctx, ln, targets, progress)
		if err != nil {
			return nil, method, err
		}
	}

	// Eigene Adresse, Broadcast- und Multicast-MACs aussortieren, doppelte IPs zusammenfassen.
	seen := map[string]bool{}
	var clean []Hit
	for _, h := range hits {
		if h.IP == ln.Addr || !p.Contains(h.IP) || len(h.MAC) != 6 || h.MAC[0]&1 == 1 || isZero(h.MAC) {
			continue
		}
		k := h.IP.String()
		if seen[k] {
			continue
		}
		seen[k] = true
		clean = append(clean, h)
	}

	names := resolveNames(ctx, clean)
	now := time.Now().UTC()
	out := make([]store.Found, 0, len(clean))
	for _, h := range clean {
		f := store.Found{
			MAC: wol.FormatMAC(h.MAC), IP: h.IP.String(), Vendor: oui.Lookup(h.MAC),
			Interface: ln.Interface, Network: p.String(), LastSeen: now,
		}
		if n, ok := names[h.IP]; ok {
			f.Hostname, f.HostnameSource = n.name, n.source
		}
		out = append(out, f)
	}
	return out, method, nil
}

func isZero(mac net.HardwareAddr) bool {
	for _, b := range mac {
		if b != 0 {
			return false
		}
	}
	return true
}

// BroadcastFor liefert die Broadcast-Adresse (mit Port 9) des lokalen Netzes, in dem ip liegt.
func BroadcastFor(ip string) string {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return ""
	}
	for _, ln := range LocalNets() {
		if ln.Prefix.Contains(a) {
			return ln.Broadcast
		}
	}
	return ""
}
