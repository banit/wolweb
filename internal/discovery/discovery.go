// Package discovery sucht Geräte in den freigegebenen IPv4-Segmenten.
//
// Bevorzugt wird ein aktiver ARP-Scan (Linux, braucht CAP_NET_RAW). Geht das nicht, bekommt jede
// Adresse ein kleines UDP-Paket, damit der Kernel ARP auflöst, und danach wird die Nachbartabelle
// gelesen. Die Suche weckt keine Geräte und findet nur Geräte, die gerade eingeschaltet sind.
package discovery

import (
	"context"
	"errors"
	"log"
	"net"
	"net/netip"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"wolweb/internal/i18n"
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
	VLAN      int          `json:"vlan,omitempty"` // nur wenn der Rechner selbst taggt (eth0.20)
}

var localCache struct {
	sync.Mutex
	at   time.Time
	nets []LocalNet
}

// LocalNets listet die IPv4-Netze der eigenen Schnittstellen (ohne Loopback). Das Ergebnis wird
// 10 s zwischengespeichert – es wird je Gerät und Anfrage gebraucht (Broadcast, Online-Prüfung).
func LocalNets() []LocalNet {
	localCache.Lock()
	defer localCache.Unlock()
	if localCache.nets == nil || time.Since(localCache.at) > 10*time.Second {
		localCache.nets, localCache.at = readLocalNets(), time.Now()
	}
	return slices.Clone(localCache.nets)
}

func readLocalNets() []LocalNet {
	out := []LocalNet{}
	vlans := readVLANs()
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
			out = append(out, LocalNet{Interface: ifc.Name, Addr: ip, Prefix: p, Broadcast: broadcastOf(p) + ":9", VLAN: vlans[ifc.Name]})
		}
	}
	return out
}

// BroadcastOf liefert die Broadcast-Adresse (mit Port 9) eines Netzes, auch wenn der Rechner
// selbst nicht darin hängt (gerichteter Broadcast, den ein Router weiterleiten kann).
func BroadcastOf(p netip.Prefix) string { return broadcastOf(p) + ":9" }

func broadcastOf(p netip.Prefix) string {
	b := p.Addr().As4()
	host := 32 - p.Bits()
	v := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	if host > 0 {
		v |= (1 << host) - 1
	}
	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}).String()
}

// OnLink sagt, ob ip in einem direkt angeschlossenen Netz liegt.
func OnLink(ip netip.Addr) bool {
	for _, ln := range LocalNets() {
		if ln.Prefix.Contains(ip) {
			return true
		}
	}
	return false
}

// HomeNets liefert die direkt angeschlossenen Netze, die als Heimnetz in Frage kommen: ohne
// Container-Brücken (docker0, br-<id>, cni, podman …), virtuelle Netze (virbr, vboxnet, Hyper-V) und
// VPNs (tailscale, wg, zt, tun), ohne Link-Local (169.254/16) und nur so groß, dass sich das Netz
// durchsuchen lässt (maxHosts).
func HomeNets(maxHosts int) []LocalNet {
	var out []LocalNet
	for _, ln := range LocalNets() {
		if virtualIface(ln.Interface) || ln.Prefix.Addr().IsLinkLocalUnicast() || ln.Prefix.Bits() > 30 {
			continue
		}
		if size := 1 << (32 - ln.Prefix.Bits()); size-2 > maxHosts {
			continue
		}
		out = append(out, ln)
	}
	return out
}

var dockerBridge = regexp.MustCompile(`^br-[0-9a-f]{12}$`)

// virtualIface erkennt Schnittstellen von Containern, VMs und VPNs am Namen. vmbr0 (Proxmox) und
// eth0.20 (VLAN) sind echte Netze und bleiben.
func virtualIface(name string) bool {
	n := strings.ToLower(name)
	if dockerBridge.MatchString(n) {
		return true
	}
	for _, p := range []string{"docker", "veth", "virbr", "lxcbr", "lxdbr", "cni", "flannel", "cali", "vxlan", "podman",
		"tailscale", "wg", "zt", "tun", "tap", "utun", "vboxnet", "vmnet", "ham", "vethernet", "virtualbox", "zerotier"} {
		if strings.HasPrefix(n, p) {
			return true
		}
	}
	return false
}

// readVLANs liest unter Linux die VLAN-Schnittstellen des Rechners (Name -> VLAN-ID) aus
// /proc/net/vlan/config. In einem Proxmox-Container ist das meist leer: Dort taggt der Host, und
// der Container sieht ein gewöhnliches eth1.
func readVLANs() map[string]int {
	out := map[string]int{}
	b, err := os.ReadFile("/proc/net/vlan/config")
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(b), "\n") {
		parts := strings.Split(line, "|")
		if len(parts) != 3 {
			continue
		}
		if id, err := strconv.Atoi(strings.TrimSpace(parts[1])); err == nil && id > 0 {
			out[strings.TrimSpace(parts[0])] = id
		}
	}
	return out
}

// LocalNetFor sucht die Schnittstelle, deren Netz das Segment enthält – ohne sie kann wolweb dort
// weder suchen noch per Broadcast wecken.
func LocalNetFor(target netip.Prefix) (LocalNet, bool) { return localNetFor(target) }

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
		return nil, i18n.M("scan.too_large", p.String(), size, max)
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
	Error      string    `json:"error,omitempty"` // auf Deutsch; übersetzt liefert es Localized
	Auto       bool      `json:"auto"`

	errs []error
}

// Localized liefert den Auftrag mit der Fehlermeldung in Sprache l.
func (j Job) Localized(l i18n.Lang) Job {
	if len(j.errs) > 0 {
		parts := make([]string, len(j.errs))
		for i, e := range j.errs {
			parts[i] = i18n.Text(l, e)
		}
		j.Error = strings.Join(parts, "\n")
	}
	return j
}

type Scanner struct {
	store    *store.Store
	maxHosts int

	mu       sync.Mutex // schützt networks, manual, interval, job und cancel
	networks []netip.Prefix
	manual   bool          // Netze sind eingetragen (auch wenn keins durchsucht wird): keine automatische Erkennung
	interval time.Duration // automatische Suche; 0 = aus
	reset    chan struct{}
	job      Job
	cancel   context.CancelFunc

	// OnUpdate bekommt nach jeder Änderung (Start, Fortschritt, Ende) eine Kopie des Auftrags.
	// Vor dem ersten Start setzen.
	OnUpdate func(Job)
}

func New(s *store.Store, networks []string, maxHosts int) *Scanner {
	sc := &Scanner{store: s, maxHosts: maxHosts, reset: make(chan struct{}, 1)}
	for _, n := range networks {
		sc.networks = append(sc.networks, netip.MustParsePrefix(n))
	}
	sc.manual = len(sc.networks) > 0
	return sc
}

// SetNetworks ersetzt die Suchsegmente; ohne Segmente gilt wieder die automatische Erkennung.
func (s *Scanner) SetNetworks(networks []string) error {
	return s.Configure(networks, len(networks) > 0)
}

// Configure ersetzt die Suchsegmente (Einstellungsseite). manual sagt, ob überhaupt Netze eingetragen
// sind: Stehen welche in der Liste, aber bei keinem ist „durchsuchen“ an, wird nicht gesucht – auch
// nicht im automatisch erkannten Heimnetz, das man ja eben ausgeschlossen haben kann. Eine laufende
// Suche bleibt bei den alten Segmenten.
func (s *Scanner) Configure(networks []string, manual bool) error {
	var ps []netip.Prefix
	for _, n := range networks {
		p, err := netip.ParsePrefix(n)
		if err != nil {
			return err
		}
		ps = append(ps, p.Masked())
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.networks, s.manual = ps, manual || len(ps) > 0
	return nil
}

// Networks liefert die Netze, die durchsucht werden – eingestellt oder automatisch erkannt.
func (s *Scanner) Networks() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.networksLocked()
}

func (s *Scanner) networksLocked() []string {
	nets := s.effectiveLocked()
	out := make([]string, len(nets))
	for i, n := range nets {
		out[i] = n.String()
	}
	return out
}

// effectiveLocked: die eingestellten Netze; ist keins eingestellt, das eine Heimnetz, falls es
// genau eins gibt. Bei mehreren wählt man es in der Oberfläche (gespeichert in den Einstellungen).
// Ausgewertet bei jedem Aufruf – nach dem Start kann DHCP die Adresse erst später liefern.
func (s *Scanner) effectiveLocked() []netip.Prefix {
	if s.manual {
		return s.networks
	}
	if home := HomeNets(s.maxHosts); len(home) == 1 {
		return []netip.Prefix{home[0].Prefix}
	}
	return nil
}

// Auto sagt, ob das Netz automatisch erkannt wurde (keins eingestellt, genau ein Heimnetz).
func (s *Scanner) Auto() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.manual && len(s.effectiveLocked()) == 1
}

// Configured sagt, ob Netze eingestellt sind (sonst gilt die Erkennung).
func (s *Scanner) Configured() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.manual
}

// ScanOff sagt, ob Netze eingetragen sind, aber bei keinem die Suche eingeschaltet ist.
func (s *Scanner) ScanOff() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.manual && len(s.networks) == 0
}

// Broadcast liefert die Broadcast-Adresse (mit Port 9) des Suchsegments, wenn es genau eins
// gibt (eingestellt oder erkannt) – sonst "".
func (s *Scanner) Broadcast() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	nets := s.effectiveLocked()
	if len(nets) != 1 || nets[0].Bits() > 30 {
		return ""
	}
	return broadcastOf(nets[0]) + ":9"
}

// SetInterval ändert den Abstand der automatischen Suche; 0 schaltet sie ab.
func (s *Scanner) SetInterval(d time.Duration) {
	s.mu.Lock()
	changed := s.interval != d
	s.interval = d
	s.mu.Unlock()
	if changed {
		select {
		case s.reset <- struct{}{}:
		default:
		}
	}
}

// autoStartDelay: kurz nach dem Start einmal suchen, damit die Liste nicht bis zum ersten
// Intervall leer bleibt.
var autoStartDelay = 15 * time.Second

// RunAuto sucht im eingestellten Abstand, bis ctx endet. Ein neuer Abstand gilt ab sofort (die
// nächste Suche folgt nach dem neuen Abstand), ohne Segmente wird nichts gesucht.
func (s *Scanner) RunAuto(ctx context.Context) {
	select { // SetInterval vor dem Start ist keine Änderung
	case <-s.reset:
	default:
	}
	wait := autoStartDelay
	for {
		s.mu.Lock()
		iv, nets := s.interval, s.networksLocked()
		s.mu.Unlock()
		var due <-chan time.Time
		var t *time.Timer
		if iv > 0 {
			if wait <= 0 {
				wait = iv
			}
			t = time.NewTimer(wait)
			due = t.C
		}
		select {
		case <-ctx.Done():
			if t != nil {
				t.Stop()
			}
			s.Cancel()
			return
		case <-s.reset:
			if t != nil {
				t.Stop()
			}
			if s.autoInterval() > 0 {
				log.Printf("Automatic scan now every %s", s.autoInterval())
			} else {
				log.Print("Automatic scan off")
			}
			wait = 0 // ab jetzt im neuen Abstand – gerade eingeschaltet: bald einmal suchen
			if iv == 0 {
				wait = autoStartDelay
			}
			continue
		case <-due:
		}
		wait = 0
		if len(nets) == 0 {
			continue
		}
		if err := s.Start(true); err != nil && !errors.Is(err, ErrBusy) {
			log.Printf("Automatic scan: %v", err)
		}
	}
}

func (s *Scanner) autoInterval() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.interval
}

func (s *Scanner) Job() Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.job
}

// Vergleichbare Fehler für errors.Is; übersetzt als scan.busy bzw. scan.no_networks.
var ErrBusy = errors.New("a scan is already running")
var ErrNoNetworks = errors.New("no network segments configured (discovery.networks)")

// maxScanTime begrenzt eine Suche; danach wird sie wie ein Abbruch beendet.
const maxScanTime = 5 * time.Minute

// Start beginnt einen Scan im Hintergrund.
func (s *Scanner) Start(auto bool) error {
	s.mu.Lock()
	nets := slices.Clone(s.effectiveLocked())
	if len(nets) == 0 {
		s.mu.Unlock()
		return ErrNoNetworks
	}
	if s.job.Running {
		s.mu.Unlock()
		return ErrBusy
	}
	ctx, cancel := context.WithTimeout(context.Background(), maxScanTime)
	s.cancel = cancel
	s.job = Job{Running: true, Networks: s.networksLocked(), StartedAt: time.Now().UTC(), Auto: auto}
	j := s.job
	s.mu.Unlock()
	// Erst den Start melden, dann suchen – sonst kann "läuft" nach "fertig" ankommen (etwa wenn
	// das Segment sofort scheitert), und die Oberfläche zeigte dauerhaft eine laufende Suche.
	if s.OnUpdate != nil {
		s.OnUpdate(j)
	}
	go s.run(ctx, cancel, nets)
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
	f(&s.job)
	j := s.job
	s.mu.Unlock()
	if s.OnUpdate != nil {
		s.OnUpdate(j)
	}
}

func (s *Scanner) run(ctx context.Context, cancel context.CancelFunc, nets []netip.Prefix) {
	defer cancel()
	var all []store.Found
	var errs []error
	methods := map[string]bool{}
	for _, p := range nets {
		if ctx.Err() != nil {
			break
		}
		found, method, err := s.scanNetwork(ctx, p)
		// Abbruch und Zeitlimit werden unten einmal gemeldet, nicht je Segment.
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			errs = append(errs, i18n.M("scan.net_error", p.String(), err))
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
	switch ctx.Err() {
	case context.Canceled:
		errs = append(errs, i18n.M("scan.cancelled"))
	case context.DeadlineExceeded:
		errs = append(errs, i18n.M("scan.timeout", maxScanTime.String()))
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
		j.errs = errs
		if err := errors.Join(errs...); err != nil {
			j.Error = err.Error()
		}
	})
	log.Printf("Scan finished: %d devices, %d new, method %s", len(all), newCount, method)
}

func (s *Scanner) scanNetwork(ctx context.Context, p netip.Prefix) ([]store.Found, string, error) {
	targets, err := hosts(p, s.maxHosts)
	if err != nil {
		return nil, "", err
	}
	s.update(func(j *Job) { j.Total += len(targets) })
	ln, ok := localNetFor(p)
	if !ok {
		return nil, "", i18n.M("scan.not_local")
	}
	progress := func(n int) { s.update(func(j *Job) { j.Done += n }) }

	hits, err := arpScan(ctx, ln, targets, progress)
	method := "arp" // Schlüssel; die Oberfläche übersetzt ihn
	switch {
	case err != nil && ctx.Err() != nil:
		// Abgebrochen: Die bis dahin eingegangenen ARP-Antworten sind echt und werden übernommen.
		// Die Nachbartabelle als Ersatz wäre hier falsch – ARP funktioniert ja.
	case err != nil:
		log.Printf("ARP scan on %s not possible (%v) – using the neighbor table", ln.Interface, err)
		method = "neighbors"
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

	var names map[netip.Addr]hostName
	if ctx.Err() == nil { // nach einem Abbruch keine Namen mehr abfragen
		names = resolveNames(ctx, clean)
	}
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
