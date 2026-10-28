// Package wol baut und verschickt Magic Packets.
package wol

import (
	"context"
	"log"
	"net"
	"strings"
	"time"

	"wolweb/internal/i18n"
)

// Ein Magic Packet ist ein einzelnes UDP-Paket ohne Bestätigung. Geht es verloren – WLAN-Brücke,
// Switch, der den Port gerade erst hochfährt, voller Puffer –, merkt das niemand. Deshalb geht
// es in Runden raus (Rounds, im Abstand Spacing, zusammen knapp eine Sekunde) und an Port 9
// (discard) und 7 (echo), auf die Netzwerkkarten je nach Hersteller hören.
var (
	Rounds  = 3
	Spacing = 400 * time.Millisecond
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
//
// Die erste Runde wird abgewartet (Fehler kommen zurück), die weiteren laufen im Hintergrund –
// sonst warteten Weckseite und Gruppen bei jedem Gerät fast eine Sekunde.
func Send(hw net.HardwareAddr, target, iface string, secureOn []byte) error {
	raddr, err := net.ResolveUDPAddr("udp4", target)
	if err != nil {
		return i18n.M("wol.target", target, err)
	}
	dests := Destinations(raddr)
	conn, err := listen(iface, target)
	if err != nil {
		return err
	}
	pkt := Packet(hw, secureOn)
	if err := sendRound(conn, pkt, dests); err != nil {
		conn.Close()
		return i18n.M("wol.send", target, err)
	}
	if Rounds <= 1 {
		return conn.Close()
	}
	go func() {
		defer conn.Close()
		for range Rounds - 1 {
			time.Sleep(Spacing)
			if err := sendRound(conn, pkt, dests); err != nil {
				log.Printf("Repeating magic packet to %s: %v", target, err)
				return
			}
		}
	}()
	return nil
}

// listen öffnet den Socket zum Senden. Ist iface gesetzt, geht es über diese Schnittstelle hinaus
// (unter Linux per SO_BINDTODEVICE) und mit ihrer IPv4-Adresse als Absender.
//
// Unverbundener Socket: Bei einem verbundenen ließe eine ICMP-Fehlermeldung auf das erste Paket
// (z. B. "Port unreachable" von einem Unicast-Ziel) das nächste mit ECONNREFUSED scheitern –
// obwohl das erste längst unterwegs ist.
func listen(iface, target string) (net.PacketConn, error) {
	laddr := &net.UDPAddr{}
	var lc net.ListenConfig
	if iface != "" {
		ip, err := interfaceIPv4(iface)
		if err != nil {
			return nil, err
		}
		laddr.IP = ip
		// Nur die Absender-IP reicht nicht: Für 255.255.255.255 wählt Linux die Schnittstelle
		// nach der Routingtabelle, nicht nach der Absenderadresse.
		lc.Control = bindToDevice(iface)
	}
	conn, err := lc.ListenPacket(context.Background(), "udp4", laddr.String())
	if err != nil {
		return nil, i18n.M("wol.socket", target, err)
	}
	return conn, nil
}

// sendRound schickt das Paket einmal an jedes Ziel.
func sendRound(conn net.PacketConn, pkt []byte, dests []*net.UDPAddr) error {
	for _, d := range dests {
		if n, err := conn.WriteTo(pkt, d); err != nil {
			return err
		} else if n != len(pkt) {
			return i18n.M("wol.short")
		}
	}
	return nil
}

// Destinations liefert die Ziele einer Runde: zum üblichen Port 9 auch Port 7 und umgekehrt.
// Ein anderer Port (etwa eine Weiterleitung im Router) bleibt allein – dort könnte unter Port 7
// oder 9 etwas ganz anderes lauschen.
func Destinations(a *net.UDPAddr) []*net.UDPAddr {
	out := []*net.UDPAddr{a}
	if a.Port == 9 || a.Port == 7 {
		other := *a
		other.Port = 16 - a.Port
		out = append(out, &other)
	}
	return out
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
