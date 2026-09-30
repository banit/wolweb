//go:build !linux

package discovery

import (
	"net"
	"net/netip"
	"os/exec"
	"strings"
)

// readNeighbors wertet "arp -a" aus (Windows/macOS, v. a. zum Entwickeln).
func readNeighbors(ln LocalNet) ([]Hit, error) {
	out, err := exec.Command("arp", "-a").Output()
	if err != nil {
		return nil, err
	}
	var hits []Hit
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(strings.NewReplacer("(", " ", ")", " ").Replace(line))
		var ip netip.Addr
		var mac net.HardwareAddr
		for _, f := range fields {
			if a, err := netip.ParseAddr(f); err == nil && a.Is4() && !ip.IsValid() {
				ip = a
			} else if m, err := net.ParseMAC(f); err == nil && len(m) == 6 {
				mac = m
			}
		}
		if ip.IsValid() && mac != nil && ln.Prefix.Contains(ip) {
			hits = append(hits, Hit{IP: ip, MAC: mac})
		}
	}
	return hits, nil
}
