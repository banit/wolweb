// Package oui ordnet MAC-Adressen dem Hersteller zu (Liste aus Wireshark "manuf", eingebettet).
package oui

import (
	"bufio"
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/hex"
	"net"
	"strings"
	"sync"
)

//go:embed oui.txt.gz
var data []byte

var (
	once  sync.Once
	table map[string]string // Präfix in Hex-Ziffern (6, 7 oder 9 Stellen) -> Hersteller
)

func load() {
	table = make(map[string]string, 60000)
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return
	}
	defer zr.Close()
	sc := bufio.NewScanner(zr)
	for sc.Scan() {
		prefix, name, ok := strings.Cut(sc.Text(), "\t")
		if ok {
			table[prefix] = name
		}
	}
}

// Lookup liefert den registrierten Hersteller oder "" wenn unbekannt.
// Lokal verwaltete (z. B. zufällige) Adressen werden als solche gekennzeichnet.
func Lookup(mac net.HardwareAddr) string {
	if len(mac) < 6 {
		return ""
	}
	if mac[0]&0x02 != 0 {
		return "Private/zufällige MAC"
	}
	once.Do(load)
	h := strings.ToUpper(hex.EncodeToString(mac))
	for _, n := range []int{9, 7, 6} {
		if name, ok := table[h[:n]]; ok {
			return name
		}
	}
	return ""
}
