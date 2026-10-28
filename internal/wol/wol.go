// Package wol baut und verschickt Magic Packets.
package wol

import (
	"context"
	"net"
	"strings"

	"wolweb/internal/i18n"
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
		return nil, i18n.M("wol.mac", s)
	}
	return hw, nil
}

func FormatMAC(hw net.HardwareAddr) string { return strings.ToUpper(hw.String()) }

// Packet liefert die 102 Bytes: 6 × 0xFF, dann 16 × MAC – mit SecureOn-Passwort (6 Byte, von
// manchen Netzwerkkarten verlangt) 108 Byte. secureOn darf nil sein.
func Packet(hw net.HardwareAddr, secureOn []byte) []byte {
	p := make([]byte, 0, 108)
	for range 6 {
		p = append(p, 0xFF)
	}
	for range 16 {
		p = append(p, hw...)
	}
	return append(p, secureOn...)
}

// ParseSecureOn prüft ein SecureOn-Passwort (6 Byte, Schreibweise wie eine MAC); leer = keins.
func ParseSecureOn(s string) ([]byte, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	hw, err := ParseMAC(s)
	if err != nil {
		return nil, i18n.M("val.secureon", s)
	}
	return hw, nil
}

// Send schickt das Paket an target ("ip:port"). Ist iface gesetzt, geht es über diese
// Schnittstelle hinaus (unter Linux per SO_BINDTODEVICE) und mit ihrer IPv4-Adresse als Absender.
// secureOn ist das optionale SecureOn-Passwort (nil = keins).
func Send(hw net.HardwareAddr, target, iface string, secureOn []byte) error {
	raddr, err := net.ResolveUDPAddr("udp4", target)
	if err != nil {
		return i18n.M("wol.target", target, err)
	}
	laddr := &net.UDPAddr{}
	var lc net.ListenConfig
	if iface != "" {
		ip, err := interfaceIPv4(iface)
		if err != nil {
			return err
		}
		laddr.IP = ip
		// Nur die Absender-IP reicht nicht: Für 255.255.255.255 wählt Linux die Schnittstelle
		// nach der Routingtabelle, nicht nach der Absenderadresse.
		lc.Control = bindToDevice(iface)
	}
	// Unverbundener Socket: Bei einem verbundenen ließe eine ICMP-Fehlermeldung auf das erste
	// Paket (z. B. "Port unreachable" von einem Unicast-Ziel) das zweite mit ECONNREFUSED
	// scheitern – obwohl das erste längst unterwegs ist.
	conn, err := lc.ListenPacket(context.Background(), "udp4", laddr.String())
	if err != nil {
		return i18n.M("wol.socket", target, err)
	}
	defer conn.Close()
	pkt := Packet(hw, secureOn)
	// Zweimal senden – UDP geht gelegentlich verloren, doppelt schadet nicht.
	for range 2 {
		if n, err := conn.WriteTo(pkt, raddr); err != nil {
			return i18n.M("wol.send", target, err)
		} else if n != len(pkt) {
			return i18n.M("wol.short")
		}
	}
	return nil
}

func interfaceIPv4(name string) (net.IP, error) {
	ifc, err := net.InterfaceByName(name)
	if err != nil {
		return nil, i18n.M("wol.iface", name, err)
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
	return nil, i18n.M("wol.iface_no_ipv4", name)
}
