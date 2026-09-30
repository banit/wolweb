//go:build linux

package discovery

import (
	"bufio"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
)

// readNeighbors liest /proc/net/arp (nur vollständige Einträge der passenden Schnittstelle).
func readNeighbors(ln LocalNet) ([]Hit, error) {
	f, err := os.Open("/proc/net/arp")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Hit
	sc := bufio.NewScanner(f)
	sc.Scan() // Kopfzeile
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 6 || fields[5] != ln.Interface {
			continue
		}
		flags, _ := strconv.ParseUint(strings.TrimPrefix(fields[2], "0x"), 16, 32)
		if flags&0x2 == 0 { // ATF_COM: Eintrag vollständig
			continue
		}
		ip, err1 := netip.ParseAddr(fields[0])
		mac, err2 := net.ParseMAC(fields[3])
		if err1 == nil && err2 == nil {
			out = append(out, Hit{IP: ip, MAC: mac})
		}
	}
	return out, sc.Err()
}

// NeighborMAC liefert die MAC, die der Kernel gerade für ip kennt (nil, wenn unbekannt).
func NeighborMAC(ip netip.Addr) net.HardwareAddr {
	f, err := os.Open("/proc/net/arp")
	if err != nil {
		return nil
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Scan()
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 4 || fields[0] != ip.String() {
			continue
		}
		flags, _ := strconv.ParseUint(strings.TrimPrefix(fields[2], "0x"), 16, 32)
		if flags&0x2 == 0 {
			continue
		}
		if mac, err := net.ParseMAC(fields[3]); err == nil {
			return mac
		}
	}
	return nil
}
