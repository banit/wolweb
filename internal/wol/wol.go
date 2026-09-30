// Package wol baut und verschickt Magic Packets.
package wol

import (
	"errors"
	"fmt"
	"net"
	"strings"
)

// ParseMAC akzeptiert 00:11:22:33:44:55, 00-11-22-33-44-55, 0011.2233.4455 und 001122334455
// und liefert die Schreibweise AA:BB:CC:DD:EE:FF.
func ParseMAC(s string) (net.HardwareAddr, error) {
	s = strings.TrimSpace(s)
	if len(s) == 12 && !strings.ContainsAny(s, ":-.") {
		var b strings.Builder
		for i := 0; i < 12; i += 2 {
			if i > 0 {
				b.WriteByte(':')
			}
			b.WriteString(s[i : i+2])
		}
		s = b.String()
	}
	hw, err := net.ParseMAC(s)
	if err != nil || len(hw) != 6 {
		return nil, fmt.Errorf("%q ist keine gültige MAC-Adresse", s)
	}
	return hw, nil
}

func FormatMAC(hw net.HardwareAddr) string { return strings.ToUpper(hw.String()) }

// Packet liefert die 102 Bytes: 6 × 0xFF, dann 16 × MAC.
func Packet(hw net.HardwareAddr) []byte {
	p := make([]byte, 0, 102)
	for range 6 {
		p = append(p, 0xFF)
	}
	for range 16 {
		p = append(p, hw...)
	}
	return p
}

// Send schickt das Paket an target ("ip:port"). Ist iface gesetzt, wird dessen IPv4-Adresse als
// Absender benutzt.
func Send(hw net.HardwareAddr, target, iface string) error {
	raddr, err := net.ResolveUDPAddr("udp4", target)
	if err != nil {
		return fmt.Errorf("Zieladresse %q: %w", target, err)
	}
	var laddr *net.UDPAddr
	if iface != "" {
		ip, err := interfaceIPv4(iface)
		if err != nil {
			return err
		}
		laddr = &net.UDPAddr{IP: ip}
	}
	conn, err := net.DialUDP("udp4", laddr, raddr)
	if err != nil {
		return fmt.Errorf("UDP-Verbindung zu %s: %w", target, err)
	}
	defer conn.Close()
	pkt := Packet(hw)
	// Zweimal senden – UDP geht gelegentlich verloren, doppelt schadet nicht.
	for range 2 {
		if n, err := conn.Write(pkt); err != nil {
			return fmt.Errorf("Senden an %s: %w", target, err)
		} else if n != len(pkt) {
			return errors.New("Paket unvollständig gesendet")
		}
	}
	return nil
}

func interfaceIPv4(name string) (net.IP, error) {
	ifc, err := net.InterfaceByName(name)
	if err != nil {
		return nil, fmt.Errorf("Netzwerkschnittstelle %q: %w", name, err)
	}
	addrs, err := ifc.Addrs()
	if err != nil {
		return nil, err
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil {
			return n.IP.To4(), nil
		}
	}
	return nil, fmt.Errorf("Schnittstelle %q hat keine IPv4-Adresse", name)
}
